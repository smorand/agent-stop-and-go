package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"agent-stop-and-go/internal/a2a"
	"agent-stop-and-go/internal/auth"
	"agent-stop-and-go/internal/config"
	"agent-stop-and-go/internal/conversation"
	"agent-stop-and-go/internal/llm"
	"agent-stop-and-go/internal/mcp"
	"agent-stop-and-go/internal/storage"
)

const a2aToolPrefix = "a2a_"

// Agent handles the processing of conversations using MCP tools and LLM.
type Agent struct {
	config     *config.Config
	storage    *storage.Storage
	mcpClient  mcp.Client
	llmClient  llm.Client            // primary client (backward compat)
	llmClients map[string]llm.Client // model -> client (for orchestrated agents)
	llmMu      sync.Mutex            // protects llmClients map
	a2aClients map[string]*a2a.Client
}

// New creates a new agent instance.
func New(cfg *config.Config, store *storage.Storage) *Agent {
	return &Agent{
		config:     cfg,
		storage:    store,
		a2aClients: make(map[string]*a2a.Client),
		llmClients: make(map[string]llm.Client),
	}
}

// Start initializes the agent, starts MCP servers and LLM client.
func (a *Agent) Start() error {
	// Create MCP clients from config (one per server entry)
	var namedClients []mcp.NamedClient
	for _, serverCfg := range a.config.MCPServers {
		client, err := mcp.NewClient(mcp.ClientConfig{
			URL:     serverCfg.URL,
			Command: serverCfg.Command,
			Args:    serverCfg.Args,
		})
		if err != nil {
			return fmt.Errorf("failed to create MCP client %q: %w", serverCfg.Name, err)
		}
		namedClients = append(namedClients, mcp.NamedClient{Name: serverCfg.Name, Client: client})
	}

	compositeClient := mcp.NewCompositeClient(namedClients)
	if err := compositeClient.Start(); err != nil {
		return fmt.Errorf("failed to start MCP clients: %w", err)
	}
	a.mcpClient = compositeClient

	// Initialize primary LLM client
	llmClient, err := llm.NewClient(a.config.LLM.Model)
	if err != nil {
		a.mcpClient.Stop()
		return fmt.Errorf("failed to initialize LLM client: %w", err)
	}
	a.llmClient = llmClient
	a.llmClients[a.config.LLM.Model] = llmClient

	// Initialize A2A clients from top-level config
	for _, agentCfg := range a.config.A2A {
		client := a2a.NewClient(agentCfg.Name, agentCfg.URL, agentCfg.Description, agentCfg.DestructiveHint)
		a.a2aClients[agentCfg.Name] = client
	}

	// Initialize A2A clients from agent tree
	a.initA2AFromTree(a.config.Agent)

	return nil
}

// initA2AFromTree walks the agent tree and creates A2A clients for all nodes.
func (a *Agent) initA2AFromTree(node *config.AgentNode) {
	if node == nil {
		return
	}
	// Node-level A2A tools (for LLM nodes)
	for _, agentCfg := range node.A2A {
		if _, exists := a.a2aClients[agentCfg.Name]; !exists {
			client := a2a.NewClient(agentCfg.Name, agentCfg.URL, agentCfg.Description, agentCfg.DestructiveHint)
			a.a2aClients[agentCfg.Name] = client
		}
	}
	// A2A workflow nodes
	if node.Type == "a2a" && node.URL != "" {
		if _, exists := a.a2aClients[node.Name]; !exists {
			client := a2a.NewClient(node.Name, node.URL, node.Description, node.DestructiveHint)
			a.a2aClients[node.Name] = client
		}
	}
	for i := range node.Agents {
		a.initA2AFromTree(&node.Agents[i])
	}
}

// Stop terminates the MCP server.
func (a *Agent) Stop() error {
	if a.mcpClient != nil {
		return a.mcpClient.Stop()
	}
	return nil
}

// ProcessResult contains the result of processing a message.
type ProcessResult struct {
	Response        string                        `json:"response"`
	WaitingApproval bool                          `json:"waiting_approval"`
	Approval        *conversation.PendingApproval `json:"approval,omitempty"`
}

// isSimpleAgent returns true if the agent is a single LLM node (backward compat mode).
func (a *Agent) isSimpleAgent() bool {
	if a.config.Agent == nil {
		return true
	}
	return a.config.Agent.Type == "llm" && len(a.config.Agent.Agents) == 0
}

// StartConversation creates a new conversation with the system prompt.
func (a *Agent) StartConversation(ctx context.Context) (*conversation.Conversation, error) {
	sessionID := auth.SessionID(ctx)
	conv := conversation.New(a.config.Prompt, sessionID)
	if err := a.storage.SaveConversation(conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// ProcessMessage handles a user message using the LLM.
func (a *Agent) ProcessMessage(ctx context.Context, conv *conversation.Conversation, userMessage string) (*ProcessResult, error) {
	// Enrich context with conversation's session ID for downstream calls
	if conv.SessionID != "" && auth.SessionID(ctx) == "" {
		ctx = auth.WithSessionID(ctx, conv.SessionID)
	}

	if conv.Status == conversation.StatusWaitingApproval {
		return &ProcessResult{
			Response:        "Conversation is waiting for approval. Please respond to the pending approval first.",
			WaitingApproval: true,
			Approval:        conv.PendingApproval,
		}, nil
	}

	conv.AddMessage(conversation.RoleUser, userMessage)

	if a.isSimpleAgent() {
		return a.processSimpleMessage(ctx, conv)
	}

	return a.processOrchestrated(ctx, conv, userMessage)
}

// processOrchestrated runs the agent tree for a user message.
func (a *Agent) processOrchestrated(ctx context.Context, conv *conversation.Conversation, userMessage string) (*ProcessResult, error) {
	state := NewSessionState()
	result, err := a.executeNode(ctx, a.config.Agent, state, userMessage, conv, nil, nil, false)
	if err != nil {
		errorMsg := fmt.Sprintf("Pipeline error: %v", err)
		conv.AddMessage(conversation.RoleAssistant, errorMsg)
		if saveErr := a.storage.SaveConversation(conv); saveErr != nil {
			return nil, saveErr
		}
		return &ProcessResult{Response: errorMsg}, nil
	}

	if err := a.storage.SaveConversation(conv); err != nil {
		return nil, err
	}

	return &ProcessResult{
		Response:        result.Response,
		WaitingApproval: result.WaitingApproval,
		Approval:        result.Approval,
	}, nil
}

// processSimpleMessage runs a multi-turn loop: LLM → tool → LLM → ... until a text response.
func (a *Agent) processSimpleMessage(ctx context.Context, conv *conversation.Conversation) (*ProcessResult, error) {
	const maxToolIterations = 10
	tools := a.getAllTools()

	for range maxToolIterations {
		llmMessages := a.convertToLLMMessages(conv)

		response, err := a.llmClient.GenerateWithTools(ctx, a.config.Prompt, llmMessages, tools)
		if err != nil {
			errorMsg := fmt.Sprintf("LLM error: %v", err)
			conv.AddMessage(conversation.RoleAssistant, errorMsg)
			_ = a.storage.SaveConversation(conv)
			return &ProcessResult{Response: errorMsg}, nil
		}

		// Text response → done
		if response.ToolCall == nil {
			conv.AddMessage(conversation.RoleAssistant, response.Text)
			if err := a.storage.SaveConversation(conv); err != nil {
				return nil, err
			}
			return &ProcessResult{Response: response.Text}, nil
		}

		toolName := response.ToolCall.Name
		toolArgs := response.ToolCall.Arguments

		// --- A2A tool call ---
		if strings.HasPrefix(toolName, a2aToolPrefix) {
			agentName := strings.TrimPrefix(toolName, a2aToolPrefix)
			client, ok := a.a2aClients[agentName]
			if !ok {
				errorMsg := "A2A agent not found: " + agentName
				conv.AddMessage(conversation.RoleAssistant, errorMsg)
				_ = a.storage.SaveConversation(conv)
				return &ProcessResult{Response: errorMsg}, nil
			}

			// Destructive A2A → approval, break loop
			if client.DestructiveHint() {
				description := fmt.Sprintf("**DELEGATE to A2A Agent: %s**\n\nMessage: %v", agentName, toolArgs["message"])
				approval := conv.SetWaitingApproval(toolName, toolArgs, description)
				conv.AddToolCall(toolName, toolArgs)
				responseText := fmt.Sprintf("This action requires approval:\n\n%s\n\nPlease approve or reject using the approval UUID: %s", description, approval.UUID)
				conv.AddMessage(conversation.RoleAssistant, responseText)
				_ = a.storage.SaveConversation(conv)
				return &ProcessResult{Response: responseText, WaitingApproval: true, Approval: approval}, nil
			}

			// Non-destructive A2A → execute, continue loop
			message, _ := toolArgs["message"].(string)
			conv.AddToolCall(toolName, toolArgs)
			task, err := client.SendMessage(ctx, message)
			if err != nil {
				conv.AddToolResult(toolName, fmt.Sprintf("A2A error: %v", err), true)
				continue
			}

			// Sub-agent needs approval → proxy approval, break loop
			if task.Status.State == "input-required" {
				description := fmt.Sprintf("**PROXY APPROVAL — A2A Agent: %s**\n\n", client.Name())
				if task.Status.Message != nil {
					description += *task.Status.Message
				}
				approval := conv.SetWaitingApproval(toolName, toolArgs, description)
				approval.RemoteTaskID = task.ID
				approval.RemoteAgentName = client.Name()
				responseText := fmt.Sprintf("This action requires approval:\n\n%s\n\nPlease approve or reject using the approval UUID: %s", description, approval.UUID)
				conv.AddMessage(conversation.RoleAssistant, responseText)
				_ = a.storage.SaveConversation(conv)
				return &ProcessResult{Response: responseText, WaitingApproval: true, Approval: approval}, nil
			}

			resultText := extractTaskText(task)
			conv.AddToolResult(toolName, resultText, task.Status.State == "failed")
			continue
		}

		// --- MCP tool call ---
		tool := a.mcpClient.GetTool(toolName)
		if tool == nil {
			errorMsg := "Tool not found: " + toolName
			conv.AddMessage(conversation.RoleAssistant, errorMsg)
			_ = a.storage.SaveConversation(conv)
			return &ProcessResult{Response: errorMsg}, nil
		}

		// Destructive MCP tool → approval, break loop
		if tool.DestructiveHint {
			description := a.formatApprovalDescription(tool.Name, toolArgs)
			approval := conv.SetWaitingApproval(tool.Name, toolArgs, description)
			conv.AddToolCall(tool.Name, toolArgs)
			responseText := fmt.Sprintf("This action requires approval:\n\n%s\n\nPlease approve or reject using the approval UUID: %s", description, approval.UUID)
			conv.AddMessage(conversation.RoleAssistant, responseText)
			_ = a.storage.SaveConversation(conv)
			return &ProcessResult{Response: responseText, WaitingApproval: true, Approval: approval}, nil
		}

		// Non-destructive MCP tool → execute, continue loop
		conv.AddToolCall(toolName, toolArgs)
		result, err := a.mcpClient.CallTool(ctx, toolName, toolArgs)
		if err != nil {
			conv.AddToolResult(toolName, fmt.Sprintf("Tool execution failed: %v", err), true)
			continue
		}
		var resultText string
		if len(result.Content) > 0 {
			resultText = result.Content[0].Text
		}
		conv.AddToolResult(toolName, resultText, result.IsError)
		continue
	}

	// Safety cap reached
	response := "Maximum tool call iterations reached."
	conv.AddMessage(conversation.RoleAssistant, response)
	_ = a.storage.SaveConversation(conv)
	return &ProcessResult{Response: response}, nil
}

// getAllTools returns MCP tools + synthetic A2A tools.
func (a *Agent) getAllTools() []mcp.Tool {
	tools := a.mcpClient.Tools()

	// Add A2A agents as synthetic tools
	for _, client := range a.a2aClients {
		tool := mcp.Tool{
			Name:        a2aToolPrefix + client.Name(),
			Description: fmt.Sprintf("Delegate task to A2A agent '%s'. %s", client.Name(), client.Description()),
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"message": {
						Type:        "string",
						Description: "The message/task to send to the agent",
					},
				},
				Required: []string{"message"},
			},
			DestructiveHint: client.DestructiveHint(),
		}
		tools = append(tools, tool)
	}

	return tools
}

// convertToLLMMessages converts conversation messages to LLM format.
// Tool call records are skipped, tool results are included as user messages.
// Consecutive same-role messages are merged (Gemini requires alternating user/model).
func (a *Agent) convertToLLMMessages(conv *conversation.Conversation) []llm.Message {
	var messages []llm.Message

	for _, msg := range conv.Messages {
		// Skip system messages (handled separately as system instruction)
		if msg.Role == conversation.RoleSystem {
			continue
		}

		var role, content string

		// Tool call records: skip (exposing them causes LLMs to mimic the format)
		if msg.Role == conversation.RoleAssistant && msg.Content == "" && msg.ToolCall != nil {
			continue
		}

		// Tool results → user message
		if msg.Role == conversation.RoleTool && msg.ToolCall != nil {
			role = "user"
			content = fmt.Sprintf("Tool %q returned:\n%s", msg.ToolCall.Name, msg.ToolCall.Result)
		} else if msg.Content != "" {
			role = string(msg.Role)
			if msg.Role == conversation.RoleAssistant {
				role = "model"
			}
			content = msg.Content
		} else {
			continue
		}

		// Merge consecutive same-role messages (Gemini requires alternating user/model)
		if len(messages) > 0 && messages[len(messages)-1].Role == role {
			messages[len(messages)-1].Content += "\n\n" + content
		} else {
			messages = append(messages, llm.Message{Role: role, Content: content})
		}
	}

	return messages
}

// formatApprovalDescription creates a human-readable description of the pending tool call.
func (a *Agent) formatApprovalDescription(toolName string, args map[string]any) string {
	argsJSON, err := json.MarshalIndent(args, "", "  ")
	if err != nil {
		argsJSON = []byte(fmt.Sprintf("%v", args))
	}

	switch toolName {
	case "resources_add":
		return fmt.Sprintf("**ADD Resource**\n\nName: %v\nValue: %v", args["name"], args["value"])
	case "resources_remove":
		if id, ok := args["id"]; ok && id != "" {
			return fmt.Sprintf("**REMOVE Resource**\n\nID: %v", id)
		}
		if pattern, ok := args["pattern"]; ok && pattern != "" {
			return fmt.Sprintf("**REMOVE Resources**\n\nPattern: %v\n\n⚠️ This will remove ALL resources matching this pattern!", pattern)
		}
		return fmt.Sprintf("**REMOVE Resource**\n\nArgs: %s", argsJSON)
	default:
		return fmt.Sprintf("**%s**\n\nArgs: %s", strings.ToUpper(toolName), argsJSON)
	}
}

// executeToolAndRespond executes an MCP tool and creates a response.
func (a *Agent) executeToolAndRespond(ctx context.Context, conv *conversation.Conversation, toolName string, args map[string]any) (*ProcessResult, error) {
	conv.AddToolCall(toolName, args)

	result, err := a.mcpClient.CallTool(ctx, toolName, args)
	if err != nil {
		errorMsg := fmt.Sprintf("Tool execution failed: %v", err)
		conv.AddToolResult(toolName, errorMsg, true)
		conv.AddMessage(conversation.RoleAssistant, errorMsg)
		if saveErr := a.storage.SaveConversation(conv); saveErr != nil {
			return nil, saveErr
		}
		return &ProcessResult{Response: errorMsg, WaitingApproval: false}, nil
	}

	// Extract result text
	var resultText string
	if len(result.Content) > 0 {
		resultText = result.Content[0].Text
	}

	conv.AddToolResult(toolName, resultText, result.IsError)

	// Create response
	var response string
	if result.IsError {
		response = fmt.Sprintf("Operation failed:\n\n```\n%s\n```", resultText)
	} else {
		response = fmt.Sprintf("Operation completed successfully:\n\n```json\n%s\n```", resultText)
	}

	conv.AddMessage(conversation.RoleAssistant, response)

	if err := a.storage.SaveConversation(conv); err != nil {
		return nil, err
	}

	return &ProcessResult{Response: response, WaitingApproval: false}, nil
}

// executeA2AAndRespond executes an A2A call and creates a response.
func (a *Agent) executeA2AAndRespond(ctx context.Context, conv *conversation.Conversation, client *a2a.Client, args map[string]any) (*ProcessResult, error) {
	toolName := a2aToolPrefix + client.Name()
	message, _ := args["message"].(string)

	conv.AddToolCall(toolName, args)

	task, err := client.SendMessage(ctx, message)
	if err != nil {
		errorMsg := fmt.Sprintf("A2A agent '%s' error: %v", client.Name(), err)
		conv.AddToolResult(toolName, errorMsg, true)
		conv.AddMessage(conversation.RoleAssistant, errorMsg)
		if saveErr := a.storage.SaveConversation(conv); saveErr != nil {
			return nil, saveErr
		}
		return &ProcessResult{Response: errorMsg, WaitingApproval: false}, nil
	}

	// Check if the sub-agent returned "input-required" (needs approval)
	if task.Status.State == "input-required" {
		description := fmt.Sprintf("**PROXY APPROVAL — A2A Agent: %s**\n\n", client.Name())
		if task.Status.Message != nil {
			description += *task.Status.Message
		}
		approval := conv.SetWaitingApproval(toolName, args, description)
		approval.RemoteTaskID = task.ID
		approval.RemoteAgentName = client.Name()

		responseText := fmt.Sprintf("This action requires approval:\n\n%s\n\nPlease approve or reject using the approval UUID: %s", description, approval.UUID)
		conv.AddMessage(conversation.RoleAssistant, responseText)

		if err := a.storage.SaveConversation(conv); err != nil {
			return nil, err
		}

		return &ProcessResult{
			Response:        responseText,
			WaitingApproval: true,
			Approval:        approval,
		}, nil
	}

	// Extract result text from task artifact
	var resultText string
	if task.Artifact != nil {
		for _, part := range task.Artifact.Parts {
			if part.Text != "" {
				resultText += part.Text
			}
		}
	}
	if resultText == "" {
		resultText = fmt.Sprintf("Task %s completed with status: %s", task.ID, task.Status.State)
	}

	isError := task.Status.State == "failed"
	conv.AddToolResult(toolName, resultText, isError)

	var response string
	if isError {
		response = fmt.Sprintf("A2A agent '%s' failed:\n\n```\n%s\n```", client.Name(), resultText)
	} else {
		response = fmt.Sprintf("A2A agent '%s' responded:\n\n%s", client.Name(), resultText)
	}

	conv.AddMessage(conversation.RoleAssistant, response)

	if err := a.storage.SaveConversation(conv); err != nil {
		return nil, err
	}

	return &ProcessResult{Response: response, WaitingApproval: false}, nil
}

// ResolveApproval handles an approval response.
func (a *Agent) ResolveApproval(ctx context.Context, approvalUUID string, approved bool) (*conversation.Conversation, *ProcessResult, error) {
	conv, err := a.storage.FindConversationByApprovalUUID(approvalUUID)
	if err != nil {
		return nil, nil, err
	}

	// Enrich context with conversation's session ID for downstream calls
	if conv.SessionID != "" && auth.SessionID(ctx) == "" {
		ctx = auth.WithSessionID(ctx, conv.SessionID)
	}

	if conv.PendingApproval == nil {
		return nil, nil, fmt.Errorf("no pending approval found")
	}

	toolName := conv.PendingApproval.ToolName
	toolArgs := conv.PendingApproval.ToolArgs
	remoteTaskID := conv.PendingApproval.RemoteTaskID
	remoteAgentName := conv.PendingApproval.RemoteAgentName
	pipelineState := conv.PipelineState

	conv.ResolveApproval()
	conv.PipelineState = nil

	if !approved {
		response := "Operation cancelled by user."
		conv.AddMessage(conversation.RoleUser, "[APPROVAL]: Rejected")
		conv.AddMessage(conversation.RoleAssistant, response)
		if err := a.storage.SaveConversation(conv); err != nil {
			return nil, nil, err
		}

		// Forward rejection to remote agent if proxy
		if remoteTaskID != "" && remoteAgentName != "" {
			client, ok := a.a2aClients[remoteAgentName]
			if ok {
				_, _ = client.ContinueTask(ctx, remoteTaskID, "rejected")
			}
		}

		return conv, &ProcessResult{Response: response, WaitingApproval: false}, nil
	}

	conv.AddMessage(conversation.RoleUser, "[APPROVAL]: Approved")

	// Proxy forwarding: forward approval to the remote A2A agent
	if remoteTaskID != "" && remoteAgentName != "" {
		client, ok := a.a2aClients[remoteAgentName]
		if !ok {
			return nil, nil, fmt.Errorf("A2A agent not found for proxy approval: %s", remoteAgentName)
		}

		task, err := client.ContinueTask(ctx, remoteTaskID, "approved")
		if err != nil {
			return nil, nil, fmt.Errorf("proxy approval failed: %w", err)
		}

		// Remote agent needs another approval → create new proxy approval
		if task.Status.State == "input-required" {
			description := fmt.Sprintf("**PROXY APPROVAL — A2A Agent: %s**\n\n", client.Name())
			if task.Status.Message != nil {
				description += *task.Status.Message
			}
			approval := conv.SetWaitingApproval(toolName, toolArgs, description)
			approval.RemoteTaskID = task.ID
			approval.RemoteAgentName = client.Name()
			responseText := fmt.Sprintf("This action requires approval:\n\n%s\n\nPlease approve or reject using the approval UUID: %s", description, approval.UUID)
			conv.AddMessage(conversation.RoleAssistant, responseText)
			if err := a.storage.SaveConversation(conv); err != nil {
				return nil, nil, err
			}
			return conv, &ProcessResult{Response: responseText, WaitingApproval: true, Approval: approval}, nil
		}

		resultText := extractTaskText(task)
		isError := task.Status.State == "failed"
		conv.AddToolResult(toolName, resultText, isError)

		// Pipeline: resume from paused node
		if pipelineState != nil {
			state := NewSessionState()
			state.Load(pipelineState.SessionState)
			if pipelineState.PausedNodeOutputKey != "" {
				state.Set(pipelineState.PausedNodeOutputKey, resultText)
			}

			resume := &ResumeInfo{
				Path:                pipelineState.PausedNodePath,
				ToolResult:          resultText,
				PausedNodeOutputKey: pipelineState.PausedNodeOutputKey,
			}

			nodeResult, err := a.executeNode(ctx, a.config.Agent, state, pipelineState.UserMessage, conv, resume, nil, false)
			if err != nil {
				return nil, nil, err
			}

			if err := a.storage.SaveConversation(conv); err != nil {
				return nil, nil, err
			}

			return conv, &ProcessResult{
				Response:        nodeResult.Response,
				WaitingApproval: nodeResult.WaitingApproval,
				Approval:        nodeResult.Approval,
			}, nil
		}

		// Simple agent: continue multi-turn loop
		loopResult, err := a.processSimpleMessage(ctx, conv)
		if err != nil {
			return nil, nil, err
		}
		return conv, loopResult, nil
	}

	// No pipeline state → simple agent: execute tool and continue multi-turn loop
	if pipelineState == nil {
		if strings.HasPrefix(toolName, a2aToolPrefix) {
			agentName := strings.TrimPrefix(toolName, a2aToolPrefix)
			client, ok := a.a2aClients[agentName]
			if !ok {
				return nil, nil, fmt.Errorf("A2A agent not found: %s", agentName)
			}
			message, _ := toolArgs["message"].(string)
			task, err := client.SendMessage(ctx, message)
			if err != nil {
				conv.AddToolResult(toolName, fmt.Sprintf("A2A error: %v", err), true)
			} else {
				resultText := extractTaskText(task)
				conv.AddToolResult(toolName, resultText, task.Status.State == "failed")
			}
		} else {
			result, err := a.mcpClient.CallTool(ctx, toolName, toolArgs)
			if err != nil {
				conv.AddToolResult(toolName, fmt.Sprintf("Tool execution failed: %v", err), true)
			} else {
				var resultText string
				if len(result.Content) > 0 {
					resultText = result.Content[0].Text
				}
				conv.AddToolResult(toolName, resultText, result.IsError)
			}
		}

		// Continue multi-turn loop
		loopResult, err := a.processSimpleMessage(ctx, conv)
		if err != nil {
			return nil, nil, err
		}
		return conv, loopResult, nil
	}

	// Pipeline resume: execute the approved tool, then continue the pipeline
	var toolResult string
	if strings.HasPrefix(toolName, a2aToolPrefix) {
		agentName := strings.TrimPrefix(toolName, a2aToolPrefix)
		client, ok := a.a2aClients[agentName]
		if !ok {
			return nil, nil, fmt.Errorf("A2A agent not found: %s", agentName)
		}
		message, _ := toolArgs["message"].(string)
		task, err := client.SendMessage(ctx, message)
		if err != nil {
			return nil, nil, fmt.Errorf("A2A execution failed: %w", err)
		}
		toolResult = extractTaskText(task)
		conv.AddToolResult(toolName, toolResult, task.Status.State == "failed")
	} else {
		result, err := a.mcpClient.CallTool(ctx, toolName, toolArgs)
		if err != nil {
			return nil, nil, fmt.Errorf("tool execution failed: %w", err)
		}
		if len(result.Content) > 0 {
			toolResult = result.Content[0].Text
		}
		conv.AddToolResult(toolName, toolResult, result.IsError)
	}

	// Resume the pipeline from the paused node
	state := NewSessionState()
	state.Load(pipelineState.SessionState)

	resume := &ResumeInfo{
		Path:                pipelineState.PausedNodePath,
		ToolResult:          toolResult,
		PausedNodeOutputKey: pipelineState.PausedNodeOutputKey,
	}

	nodeResult, err := a.executeNode(ctx, a.config.Agent, state, pipelineState.UserMessage, conv, resume, nil, false)
	if err != nil {
		return nil, nil, err
	}

	if err := a.storage.SaveConversation(conv); err != nil {
		return nil, nil, err
	}

	return conv, &ProcessResult{
		Response:        nodeResult.Response,
		WaitingApproval: nodeResult.WaitingApproval,
		Approval:        nodeResult.Approval,
	}, nil
}

// GetConversation retrieves a conversation by ID.
func (a *Agent) GetConversation(id string) (*conversation.Conversation, error) {
	return a.storage.LoadConversation(id)
}

// ListConversations returns all conversations.
func (a *Agent) ListConversations() ([]*conversation.Conversation, error) {
	return a.storage.ListConversations()
}

// GetTools returns the available MCP tools and A2A agents.
func (a *Agent) GetTools() []mcp.Tool {
	return a.getAllTools()
}
