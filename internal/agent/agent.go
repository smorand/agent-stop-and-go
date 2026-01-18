package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-stop-and-go/internal/config"
	"agent-stop-and-go/internal/conversation"
	"agent-stop-and-go/internal/mcp"
	"agent-stop-and-go/internal/storage"
)

// Agent handles the processing of conversations using MCP tools.
type Agent struct {
	config    *config.Config
	storage   *storage.Storage
	mcpClient *mcp.Client
}

// New creates a new agent instance.
func New(cfg *config.Config, store *storage.Storage) *Agent {
	return &Agent{
		config:  cfg,
		storage: store,
	}
}

// Start initializes the agent and starts the MCP server.
func (a *Agent) Start() error {
	if a.config.MCP.Command == "" {
		return fmt.Errorf("MCP server command not configured")
	}

	a.mcpClient = mcp.NewClient(a.config.MCP.Command, a.config.MCP.Args)
	if err := a.mcpClient.Start(); err != nil {
		return fmt.Errorf("failed to start MCP server: %w", err)
	}

	return nil
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
	Response        string                       `json:"response"`
	WaitingApproval bool                         `json:"waiting_approval"`
	Approval        *conversation.PendingApproval `json:"approval,omitempty"`
}

// StartConversation creates a new conversation with the system prompt.
func (a *Agent) StartConversation() (*conversation.Conversation, error) {
	// Build system prompt with available tools
	prompt := a.buildSystemPrompt()
	conv := conversation.New(prompt)
	if err := a.storage.SaveConversation(conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// buildSystemPrompt creates the system prompt including available tools.
func (a *Agent) buildSystemPrompt() string {
	var sb strings.Builder
	sb.WriteString(a.config.Prompt)
	sb.WriteString("\n\n## Available Tools\n\n")

	tools := a.mcpClient.Tools()
	for _, tool := range tools {
		sb.WriteString(fmt.Sprintf("- **%s**: %s", tool.Name, tool.Description))
		if tool.DestructiveHint {
			sb.WriteString(" (requires approval)")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// ProcessMessage handles a user message and determines which tool to call.
func (a *Agent) ProcessMessage(conv *conversation.Conversation, userMessage string) (*ProcessResult, error) {
	if conv.Status == conversation.StatusWaitingApproval {
		return &ProcessResult{
			Response:        "Conversation is waiting for approval. Please respond to the pending approval first.",
			WaitingApproval: true,
			Approval:        conv.PendingApproval,
		}, nil
	}

	conv.AddMessage(conversation.RoleUser, userMessage)

	// Parse intent and determine tool to call
	toolName, toolArgs, err := a.parseIntent(userMessage)
	if err != nil {
		response := fmt.Sprintf("I couldn't understand your request. %s\n\nAvailable operations:\n- Add a resource: \"add resource <name> with value <number>\"\n- Remove a resource: \"remove resource <id or pattern>\"\n- List resources: \"list resources\" or \"search resources <pattern>\"", err.Error())
		conv.AddMessage(conversation.RoleAssistant, response)
		if saveErr := a.storage.SaveConversation(conv); saveErr != nil {
			return nil, saveErr
		}
		return &ProcessResult{Response: response, WaitingApproval: false}, nil
	}

	// Get tool info
	tool := a.mcpClient.GetTool(toolName)
	if tool == nil {
		response := "Tool not found: " + toolName
		conv.AddMessage(conversation.RoleAssistant, response)
		if saveErr := a.storage.SaveConversation(conv); saveErr != nil {
			return nil, saveErr
		}
		return &ProcessResult{Response: response, WaitingApproval: false}, nil
	}

	// If tool is destructive, require approval
	if tool.DestructiveHint {
		description := a.formatApprovalDescription(tool.Name, toolArgs)
		approval := conv.SetWaitingApproval(tool.Name, toolArgs, description)
		conv.AddToolCall(tool.Name, toolArgs)

		response := fmt.Sprintf("This action requires approval:\n\n%s\n\nPlease approve or reject using the approval UUID: %s", description, approval.UUID)
		conv.AddMessage(conversation.RoleAssistant, response)

		if err := a.storage.SaveConversation(conv); err != nil {
			return nil, err
		}

		return &ProcessResult{
			Response:        response,
			WaitingApproval: true,
			Approval:        approval,
		}, nil
	}

	// Execute tool directly (non-destructive)
	return a.executeToolAndRespond(conv, tool.Name, toolArgs)
}

// parseIntent determines which tool to call based on user message.
func (a *Agent) parseIntent(message string) (string, map[string]any, error) {
	lower := strings.ToLower(message)

	// List/Search resources
	if strings.Contains(lower, "list") || strings.Contains(lower, "show") || strings.Contains(lower, "search") || strings.Contains(lower, "find") {
		args := map[string]any{}
		// Extract pattern if present
		if pattern := extractPattern(lower); pattern != "" {
			args["pattern"] = pattern
		}
		return "resources_list", args, nil
	}

	// Add resource
	if strings.Contains(lower, "add") || strings.Contains(lower, "create") || strings.Contains(lower, "new") {
		name, value, err := extractNameAndValue(message)
		if err != nil {
			return "", nil, fmt.Errorf("to add a resource, specify a name and value (e.g., 'add resource server-1 with value 100')")
		}
		return "resources_add", map[string]any{"name": name, "value": value}, nil
	}

	// Remove resource
	if strings.Contains(lower, "remove") || strings.Contains(lower, "delete") || strings.Contains(lower, "drop") {
		id, pattern := extractIDOrPattern(message)
		if id == "" && pattern == "" {
			return "", nil, fmt.Errorf("to remove a resource, specify an ID or pattern (e.g., 'remove resource abc123' or 'remove resources matching server-.*')")
		}
		args := map[string]any{}
		if id != "" {
			args["id"] = id
		}
		if pattern != "" {
			args["pattern"] = pattern
		}
		return "resources_remove", args, nil
	}

	return "", nil, fmt.Errorf("I can only add, remove, or list resources")
}

// extractPattern extracts a search pattern from the message.
func extractPattern(message string) string {
	// Look for patterns like "matching X" or "pattern X" or "like X"
	patterns := []string{"matching ", "pattern ", "like ", "named ", "called "}
	for _, p := range patterns {
		if idx := strings.Index(message, p); idx != -1 {
			rest := strings.TrimSpace(message[idx+len(p):])
			// Take first word or quoted string
			if strings.HasPrefix(rest, "\"") || strings.HasPrefix(rest, "'") {
				quote := rest[0]
				end := strings.Index(rest[1:], string(quote))
				if end != -1 {
					return rest[1 : end+1]
				}
			}
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

// extractNameAndValue extracts name and value from add command.
func extractNameAndValue(message string) (string, int, error) {
	// Patterns: "add X with value Y", "create X value Y", "add resource named X with value Y"
	lower := strings.ToLower(message)

	// Find value
	var value int
	valuePatterns := []string{"value ", "val ", "= "}
	for _, vp := range valuePatterns {
		if idx := strings.Index(lower, vp); idx != -1 {
			rest := strings.TrimSpace(message[idx+len(vp):])
			if _, err := fmt.Sscanf(rest, "%d", &value); err == nil {
				break
			}
		}
	}

	// Find name - look for quoted string or word after "named", "called", or resource name position
	var name string
	namePatterns := []string{"named ", "called ", "name "}
	for _, np := range namePatterns {
		if idx := strings.Index(lower, np); idx != -1 {
			rest := strings.TrimSpace(message[idx+len(np):])
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				name = strings.Trim(fields[0], "\"'")
				break
			}
		}
	}

	// If no explicit name pattern, try to extract from "add resource X" pattern
	if name == "" {
		words := strings.Fields(message)
		for i, w := range words {
			wl := strings.ToLower(w)
			if (wl == "add" || wl == "create" || wl == "new") && i+1 < len(words) {
				next := strings.ToLower(words[i+1])
				if next == "resource" && i+2 < len(words) {
					name = strings.Trim(words[i+2], "\"'")
					break
				} else if next != "a" && next != "the" {
					name = strings.Trim(words[i+1], "\"'")
					break
				}
			}
		}
	}

	if name == "" {
		return "", 0, fmt.Errorf("could not extract resource name")
	}

	return name, value, nil
}

// extractIDOrPattern extracts ID or pattern from remove command.
func extractIDOrPattern(message string) (string, string) {
	lower := strings.ToLower(message)

	// Check for pattern
	if strings.Contains(lower, "matching") || strings.Contains(lower, "pattern") || strings.Contains(lower, "like") {
		pattern := extractPattern(lower)
		return "", pattern
	}

	// Extract ID - look for word after "remove/delete resource"
	words := strings.Fields(message)
	for i, w := range words {
		wl := strings.ToLower(w)
		if (wl == "remove" || wl == "delete" || wl == "drop") && i+1 < len(words) {
			next := strings.ToLower(words[i+1])
			if next == "resource" || next == "resources" {
				if i+2 < len(words) {
					return strings.Trim(words[i+2], "\"'"), ""
				}
			} else {
				return strings.Trim(words[i+1], "\"'"), ""
			}
		}
	}

	return "", ""
}

// formatApprovalDescription creates a human-readable description of the pending tool call.
func (a *Agent) formatApprovalDescription(toolName string, args map[string]any) string {
	argsJSON, _ := json.MarshalIndent(args, "", "  ")

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

// executeToolAndRespond executes a tool and creates a response.
func (a *Agent) executeToolAndRespond(conv *conversation.Conversation, toolName string, args map[string]any) (*ProcessResult, error) {
	conv.AddToolCall(toolName, args)

	result, err := a.mcpClient.CallTool(toolName, args)
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

// ResolveApproval handles an approval response.
func (a *Agent) ResolveApproval(approvalUUID string, approved bool) (*conversation.Conversation, *ProcessResult, error) {
	conv, err := a.storage.FindConversationByApprovalUUID(approvalUUID)
	if err != nil {
		return nil, nil, err
	}

	if conv.PendingApproval == nil {
		return nil, nil, fmt.Errorf("no pending approval found")
	}

	toolName := conv.PendingApproval.ToolName
	toolArgs := conv.PendingApproval.ToolArgs

	conv.ResolveApproval()

	if !approved {
		response := "Operation cancelled by user."
		conv.AddMessage(conversation.RoleUser, "[APPROVAL]: Rejected")
		conv.AddMessage(conversation.RoleAssistant, response)
		if err := a.storage.SaveConversation(conv); err != nil {
			return nil, nil, err
		}
		return conv, &ProcessResult{Response: response, WaitingApproval: false}, nil
	}

	// Execute the approved tool
	conv.AddMessage(conversation.RoleUser, "[APPROVAL]: Approved")
	result, err := a.executeToolAndRespond(conv, toolName, toolArgs)
	if err != nil {
		return nil, nil, err
	}

	return conv, result, nil
}

// GetConversation retrieves a conversation by ID.
func (a *Agent) GetConversation(id string) (*conversation.Conversation, error) {
	return a.storage.LoadConversation(id)
}

// ListConversations returns all conversations.
func (a *Agent) ListConversations() ([]*conversation.Conversation, error) {
	return a.storage.ListConversations()
}

// GetTools returns the available MCP tools.
func (a *Agent) GetTools() []mcp.Tool {
	if a.mcpClient != nil {
		return a.mcpClient.Tools()
	}
	return nil
}
