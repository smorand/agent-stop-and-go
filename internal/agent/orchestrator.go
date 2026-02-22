package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"agent-stop-and-go/internal/a2a"
	"agent-stop-and-go/internal/config"
	"agent-stop-and-go/internal/conversation"
	"agent-stop-and-go/internal/llm"
	"agent-stop-and-go/internal/mcp"
)

// extractTaskText extracts text from an A2A task artifact.
func extractTaskText(task *a2a.Task) string {
	if task.Artifact != nil {
		var parts []string
		for _, part := range task.Artifact.Parts {
			if part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "")
		}
	}
	return fmt.Sprintf("Task %s: %s", task.ID, task.Status.State)
}

// SessionState holds output_key values for data flow between agents in the tree.
type SessionState struct {
	mu     sync.RWMutex
	values map[string]string
}

// NewSessionState creates a new empty session state.
func NewSessionState() *SessionState {
	return &SessionState{values: make(map[string]string)}
}

// Get retrieves a value by key.
func (s *SessionState) Get(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[key]
}

// Set stores a value by key.
func (s *SessionState) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
}

// Snapshot returns a copy of all values.
func (s *SessionState) Snapshot() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := make(map[string]string, len(s.values))
	for k, v := range s.values {
		snap[k] = v
	}
	return snap
}

// Load populates the state from a map.
func (s *SessionState) Load(data map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range data {
		s.values[k] = v
	}
}

// ResumeInfo carries context when resuming after approval.
type ResumeInfo struct {
	Path                []int
	ToolResult          string
	PausedNodeOutputKey string
}

// NodeResult is the result of executing an agent node.
type NodeResult struct {
	Response        string
	WaitingApproval bool
	Approval        *conversation.PendingApproval
	ExitLoop        bool
	AuthRequired    bool
}

const defaultLoopMaxIterations = 10

var templateRegex = regexp.MustCompile(`\{(\w+)\}`)

// resolveTemplate replaces {key} placeholders with values from session state.
func resolveTemplate(tmpl string, state *SessionState) string {
	return templateRegex.ReplaceAllStringFunc(tmpl, func(match string) string {
		key := match[1 : len(match)-1]
		if val := state.Get(key); val != "" {
			return val
		}
		return match // leave unresolved placeholders as-is
	})
}

// executeNode dispatches to the appropriate executor based on node type.
func (a *Agent) executeNode(ctx context.Context, node *config.AgentNode, state *SessionState, userMessage string, conv *conversation.Conversation, resume *ResumeInfo, path []int, allowDestructive bool) (*NodeResult, error) {
	switch node.Type {
	case "sequential":
		return a.executeSequential(ctx, node, state, userMessage, conv, resume, path, allowDestructive)
	case "parallel":
		return a.executeParallel(ctx, node, state, userMessage, conv, path)
	case "loop":
		return a.executeLoop(ctx, node, state, userMessage, conv, path)
	case "a2a":
		return a.executeA2ANode(ctx, node, state, userMessage, conv, resume, path, allowDestructive)
	default: // "llm"
		return a.executeLLMNode(ctx, node, state, userMessage, conv, resume, path, allowDestructive)
	}
}

// executeSequential runs sub-agents in order. Supports pause/resume for approval.
func (a *Agent) executeSequential(ctx context.Context, node *config.AgentNode, state *SessionState, userMessage string, conv *conversation.Conversation, resume *ResumeInfo, path []int, allowDestructive bool) (*NodeResult, error) {
	startIndex := 0

	// Resume: fast-forward to the paused child
	if resume != nil && len(resume.Path) > 0 {
		startIndex = resume.Path[0]
		childResume := &ResumeInfo{
			Path:                resume.Path[1:],
			ToolResult:          resume.ToolResult,
			PausedNodeOutputKey: resume.PausedNodeOutputKey,
		}

		child := &node.Agents[startIndex]
		childPath := appendPath(path, startIndex)
		result, err := a.executeNode(ctx, child, state, userMessage, conv, childResume, childPath, allowDestructive)
		if err != nil {
			return nil, err
		}
		if result.WaitingApproval || result.ExitLoop || result.AuthRequired {
			return result, nil
		}
		startIndex++
	}

	// Execute remaining children
	var lastResult *NodeResult
	for i := startIndex; i < len(node.Agents); i++ {
		child := &node.Agents[i]
		childPath := appendPath(path, i)
		result, err := a.executeNode(ctx, child, state, userMessage, conv, nil, childPath, allowDestructive)
		if err != nil {
			return nil, err
		}
		lastResult = result
		if result.WaitingApproval || result.ExitLoop || result.AuthRequired {
			return result, nil
		}
	}

	if lastResult == nil {
		return &NodeResult{Response: ""}, nil
	}
	return lastResult, nil
}

// executeParallel runs sub-agents concurrently. Destructive tools execute immediately.
func (a *Agent) executeParallel(ctx context.Context, node *config.AgentNode, state *SessionState, userMessage string, conv *conversation.Conversation, path []int) (*NodeResult, error) {
	type indexedResult struct {
		index  int
		result *NodeResult
		err    error
	}

	results := make(chan indexedResult, len(node.Agents))
	var wg sync.WaitGroup

	for i := range node.Agents {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			child := &node.Agents[idx]
			childPath := appendPath(path, idx)
			// Parallel: destructive tools execute immediately (allowDestructive=true)
			nr, err := a.executeNode(ctx, child, state, userMessage, conv, nil, childPath, true)
			results <- indexedResult{index: idx, result: nr, err: err}
		}(i)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results in order
	ordered := make([]*NodeResult, len(node.Agents))
	for r := range results {
		if r.err != nil {
			return nil, r.err
		}
		ordered[r.index] = r.result
	}

	// Build combined response
	var responses []string
	for _, r := range ordered {
		if r != nil && r.Response != "" {
			responses = append(responses, r.Response)
		}
	}

	return &NodeResult{Response: strings.Join(responses, "\n")}, nil
}

// executeLoop runs sub-agents repeatedly until max_iterations or exit_loop.
func (a *Agent) executeLoop(ctx context.Context, node *config.AgentNode, state *SessionState, userMessage string, conv *conversation.Conversation, path []int) (*NodeResult, error) {
	maxIter := node.MaxIterations
	if maxIter == 0 {
		maxIter = defaultLoopMaxIterations
	}

	var lastResult *NodeResult
	for iter := 0; iter < maxIter; iter++ {
		for i := range node.Agents {
			child := &node.Agents[i]
			childPath := appendPath(path, i)
			// Loop: destructive tools execute immediately (allowDestructive=true)
			result, err := a.executeNode(ctx, child, state, userMessage, conv, nil, childPath, true)
			if err != nil {
				return nil, err
			}
			lastResult = result
			if result.ExitLoop {
				return &NodeResult{Response: result.Response}, nil
			}
		}
	}

	if lastResult == nil {
		return &NodeResult{Response: ""}, nil
	}
	return lastResult, nil
}

// executeLLMNode calls the LLM with tools and handles the response.
func (a *Agent) executeLLMNode(ctx context.Context, node *config.AgentNode, state *SessionState, userMessage string, conv *conversation.Conversation, resume *ResumeInfo, path []int, allowDestructive bool) (*NodeResult, error) {
	// Resume: we are the paused node, store tool result and return
	if resume != nil && len(resume.Path) == 0 {
		if resume.PausedNodeOutputKey != "" {
			state.Set(resume.PausedNodeOutputKey, resume.ToolResult)
		}
		response := fmt.Sprintf("Operation completed: %s", resume.ToolResult)
		conv.AddMessage(conversation.RoleAssistant, fmt.Sprintf("[%s] %s", node.Name, response))
		return &NodeResult{Response: resume.ToolResult}, nil
	}

	// Resolve prompt template
	prompt := resolveTemplate(node.Prompt, state)

	// Get LLM client for this node's model
	llmClient, err := a.getLLMClient(node.Model)
	if err != nil {
		return nil, fmt.Errorf("LLM client error for node %s: %w", node.Name, err)
	}

	// Build tools: MCP + node's A2A + exit_loop
	tools := a.getNodeTools(node)

	// Single-turn LLM call with the user message
	messages := []llm.Message{
		{Role: "user", Content: userMessage},
	}

	response, err := llmClient.GenerateWithTools(ctx, prompt, messages, tools)
	if err != nil {
		errorMsg := fmt.Sprintf("[%s] LLM error: %v", node.Name, err)
		conv.AddMessage(conversation.RoleAssistant, errorMsg)
		return &NodeResult{Response: errorMsg}, nil
	}

	// Handle text response
	if response.ToolCall == nil {
		conv.AddMessage(conversation.RoleAssistant, fmt.Sprintf("[%s] %s", node.Name, response.Text))
		if node.OutputKey != "" {
			state.Set(node.OutputKey, response.Text)
		}
		return &NodeResult{Response: response.Text}, nil
	}

	// Handle tool call
	toolName := response.ToolCall.Name
	toolArgs := response.ToolCall.Arguments

	// exit_loop
	if toolName == "exit_loop" {
		conv.AddMessage(conversation.RoleAssistant, fmt.Sprintf("[%s] exit_loop called", node.Name))
		return &NodeResult{ExitLoop: true}, nil
	}

	// A2A tool call (within LLM node's tools)
	if strings.HasPrefix(toolName, a2aToolPrefix) {
		agentName := strings.TrimPrefix(toolName, a2aToolPrefix)
		client, ok := a.a2aClients[agentName]
		if !ok {
			errorMsg := fmt.Sprintf("[%s] A2A agent not found: %s", node.Name, agentName)
			conv.AddMessage(conversation.RoleAssistant, errorMsg)
			return &NodeResult{Response: errorMsg}, nil
		}

		if client.DestructiveHint() && !allowDestructive {
			return a.pauseForApproval(conv, state, node, path, userMessage, toolName, toolArgs,
				fmt.Sprintf("[%s] Delegate to A2A agent: %s", node.Name, agentName))
		}

		message, _ := toolArgs["message"].(string)
		conv.AddToolCall(toolName, toolArgs)
		task, err := client.SendMessage(ctx, message)
		if err != nil {
			errorMsg := fmt.Sprintf("[%s] A2A error: %v", node.Name, err)
			conv.AddToolResult(toolName, errorMsg, true)
			return &NodeResult{Response: errorMsg}, nil
		}

		// Sub-agent returned "input-required" — create proxy approval
		if task.Status.State == "input-required" {
			result, err := a.pauseForApproval(conv, state, node, path, userMessage, toolName, toolArgs,
				fmt.Sprintf("[%s] Proxy approval for A2A agent: %s", node.Name, agentName))
			if err != nil {
				return nil, err
			}
			conv.PendingApproval.RemoteTaskID = task.ID
			conv.PendingApproval.RemoteAgentName = client.Name()
			if err := a.storage.SaveConversation(conv); err != nil {
				return nil, err
			}
			return result, nil
		}

		// Sub-agent returned "auth-required" — propagate upstream
		if task.Status.State == "auth-required" {
			response := fmt.Sprintf("[%s] Authentication required by A2A agent %s.", node.Name, agentName)
			if task.Status.Message != nil {
				response = *task.Status.Message
			}
			conv.AddMessage(conversation.RoleAssistant, response)
			return &NodeResult{Response: response, AuthRequired: true}, nil
		}

		resultText := extractTaskText(task)
		conv.AddToolResult(toolName, resultText, task.Status.State == "failed")
		if node.OutputKey != "" {
			state.Set(node.OutputKey, resultText)
		}
		return &NodeResult{Response: resultText}, nil
	}

	// MCP tool call
	tool := a.mcpClient.GetTool(toolName)
	if tool == nil {
		errorMsg := fmt.Sprintf("[%s] Tool not found: %s", node.Name, toolName)
		conv.AddMessage(conversation.RoleAssistant, errorMsg)
		return &NodeResult{Response: errorMsg}, nil
	}

	if tool.DestructiveHint && !allowDestructive {
		description := a.formatApprovalDescription(tool.Name, toolArgs)
		conv.AddToolCall(tool.Name, toolArgs)
		return a.pauseForApproval(conv, state, node, path, userMessage, tool.Name, toolArgs, description)
	}

	// Execute non-destructive MCP tool (CompositeClient handles serialization)
	conv.AddToolCall(toolName, toolArgs)
	result, err := a.mcpClient.CallTool(ctx, toolName, toolArgs)
	if err != nil {
		var authErr *mcp.AuthRequiredError
		if errors.As(err, &authErr) {
			response := fmt.Sprintf("[%s] Authentication required to access the %s server.", node.Name, tool.Server)
			conv.AddMessage(conversation.RoleAssistant, response)
			return &NodeResult{Response: response, AuthRequired: true}, nil
		}
		errorMsg := fmt.Sprintf("[%s] Tool execution failed: %v", node.Name, err)
		conv.AddToolResult(toolName, errorMsg, true)
		return &NodeResult{Response: errorMsg}, nil
	}

	var resultText string
	if len(result.Content) > 0 {
		resultText = result.Content[0].Text
	}
	conv.AddToolResult(toolName, resultText, result.IsError)
	conv.AddMessage(conversation.RoleAssistant, fmt.Sprintf("[%s] %s", node.Name, resultText))

	if node.OutputKey != "" {
		state.Set(node.OutputKey, resultText)
	}
	return &NodeResult{Response: resultText}, nil
}

// executeA2ANode delegates to a remote A2A agent as a workflow step.
func (a *Agent) executeA2ANode(ctx context.Context, node *config.AgentNode, state *SessionState, userMessage string, conv *conversation.Conversation, resume *ResumeInfo, path []int, allowDestructive bool) (*NodeResult, error) {
	// Resume: we are the paused node, store tool result and return
	if resume != nil && len(resume.Path) == 0 {
		if resume.PausedNodeOutputKey != "" {
			state.Set(resume.PausedNodeOutputKey, resume.ToolResult)
		}
		response := fmt.Sprintf("Operation completed: %s", resume.ToolResult)
		conv.AddMessage(conversation.RoleAssistant, fmt.Sprintf("[%s] %s", node.Name, response))
		return &NodeResult{Response: resume.ToolResult}, nil
	}

	// Build message from prompt template or user message
	message := userMessage
	if node.Prompt != "" {
		message = resolveTemplate(node.Prompt, state)
	}

	client, ok := a.a2aClients[node.Name]
	if !ok {
		return nil, fmt.Errorf("A2A agent not found: %s", node.Name)
	}

	toolName := a2aToolPrefix + node.Name
	toolArgs := map[string]any{"message": message}

	if node.DestructiveHint && !allowDestructive {
		conv.AddToolCall(toolName, toolArgs)
		description := fmt.Sprintf("[%s] Delegate to A2A agent: %s\n\nMessage: %s", node.Name, node.Name, message)
		return a.pauseForApproval(conv, state, node, path, userMessage, toolName, toolArgs, description)
	}

	conv.AddToolCall(toolName, toolArgs)
	task, err := client.SendMessage(ctx, message)
	if err != nil {
		errorMsg := fmt.Sprintf("[%s] A2A error: %v", node.Name, err)
		conv.AddToolResult(toolName, errorMsg, true)
		return &NodeResult{Response: errorMsg}, nil
	}

	// Sub-agent returned "input-required" — create proxy approval
	if task.Status.State == "input-required" {
		result, err := a.pauseForApproval(conv, state, node, path, userMessage, toolName, toolArgs,
			fmt.Sprintf("[%s] Proxy approval for A2A agent: %s", node.Name, node.Name))
		if err != nil {
			return nil, err
		}
		conv.PendingApproval.RemoteTaskID = task.ID
		conv.PendingApproval.RemoteAgentName = client.Name()
		if err := a.storage.SaveConversation(conv); err != nil {
			return nil, err
		}
		return result, nil
	}

	// Sub-agent returned "auth-required" — propagate upstream
	if task.Status.State == "auth-required" {
		response := fmt.Sprintf("[%s] Authentication required by A2A agent %s.", node.Name, node.Name)
		if task.Status.Message != nil {
			response = *task.Status.Message
		}
		conv.AddMessage(conversation.RoleAssistant, response)
		return &NodeResult{Response: response, AuthRequired: true}, nil
	}

	resultText := extractTaskText(task)
	conv.AddToolResult(toolName, resultText, task.Status.State == "failed")
	conv.AddMessage(conversation.RoleAssistant, fmt.Sprintf("[%s] %s", node.Name, resultText))

	if node.OutputKey != "" {
		state.Set(node.OutputKey, resultText)
	}
	return &NodeResult{Response: resultText}, nil
}

// pauseForApproval saves pipeline state and returns a waiting_approval result.
func (a *Agent) pauseForApproval(conv *conversation.Conversation, state *SessionState, node *config.AgentNode, path []int, userMessage string, toolName string, toolArgs map[string]any, description string) (*NodeResult, error) {
	approval := conv.SetWaitingApproval(toolName, toolArgs, description)
	conv.PipelineState = &conversation.PipelineState{
		PausedNodePath:      path,
		PausedNodeOutputKey: node.OutputKey,
		SessionState:        state.Snapshot(),
		UserMessage:         userMessage,
	}

	responseText := fmt.Sprintf("This action requires approval:\n\n%s\n\nApproval UUID: %s", description, approval.UUID)
	conv.AddMessage(conversation.RoleAssistant, responseText)

	if err := a.storage.SaveConversation(conv); err != nil {
		return nil, err
	}
	return &NodeResult{
		Response:        responseText,
		WaitingApproval: true,
		Approval:        approval,
	}, nil
}

// getNodeTools returns MCP tools + node's A2A tools + exit_loop for an LLM node.
func (a *Agent) getNodeTools(node *config.AgentNode) []mcp.Tool {
	var tools []mcp.Tool

	// Add MCP tools (shared by all LLM nodes)
	tools = append(tools, a.mcpClient.Tools()...)

	// Add node-level A2A tools
	for _, agentCfg := range node.A2A {
		tool := mcp.Tool{
			Name:        a2aToolPrefix + agentCfg.Name,
			Description: fmt.Sprintf("Delegate to A2A agent '%s'. %s", agentCfg.Name, agentCfg.Description),
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"message": {
						Type:        "string",
						Description: "The message/task to send",
					},
				},
				Required: []string{"message"},
			},
			DestructiveHint: agentCfg.DestructiveHint,
		}
		tools = append(tools, tool)
	}

	// Add exit_loop tool if node can exit loops
	if node.CanExitLoop {
		tools = append(tools, mcp.Tool{
			Name:        "exit_loop",
			Description: "Exit the current loop. Call this only when instructed to do so.",
			InputSchema: mcp.InputSchema{
				Type:       "object",
				Properties: map[string]mcp.Property{},
			},
		})
	}

	return tools
}

// getLLMClient returns the LLM client for the given model, creating it if needed.
func (a *Agent) getLLMClient(model string) (llm.Client, error) {
	if model == "" {
		model = a.config.LLM.Model
	}

	a.llmMu.Lock()
	defer a.llmMu.Unlock()

	if client, ok := a.llmClients[model]; ok {
		return client, nil
	}

	client, err := llm.NewClient(model)
	if err != nil {
		return nil, err
	}
	a.llmClients[model] = client
	return client, nil
}

// appendPath creates a new path by appending an index (avoids slice aliasing).
func appendPath(path []int, index int) []int {
	newPath := make([]int, len(path)+1)
	copy(newPath, path)
	newPath[len(path)] = index
	return newPath
}
