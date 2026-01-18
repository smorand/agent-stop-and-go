package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-stop-and-go/internal/config"
	"agent-stop-and-go/internal/conversation"
	"agent-stop-and-go/internal/llm"
	"agent-stop-and-go/internal/mcp"
	"agent-stop-and-go/internal/storage"
)

// Agent handles the processing of conversations using MCP tools and LLM.
type Agent struct {
	config    *config.Config
	storage   *storage.Storage
	mcpClient *mcp.Client
	llmClient *llm.GeminiClient
}

// New creates a new agent instance.
func New(cfg *config.Config, store *storage.Storage) *Agent {
	return &Agent{
		config:  cfg,
		storage: store,
	}
}

// Start initializes the agent, starts the MCP server and LLM client.
func (a *Agent) Start() error {
	if a.config.MCP.Command == "" {
		return fmt.Errorf("MCP server command not configured")
	}

	// Start MCP client
	a.mcpClient = mcp.NewClient(a.config.MCP.Command, a.config.MCP.Args)
	if err := a.mcpClient.Start(); err != nil {
		return fmt.Errorf("failed to start MCP server: %w", err)
	}

	// Initialize LLM client
	gemini, err := llm.NewGeminiClient(a.config.LLM.Model)
	if err != nil {
		a.mcpClient.Stop()
		return fmt.Errorf("failed to initialize LLM client: %w", err)
	}
	a.llmClient = gemini

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
	Response        string                        `json:"response"`
	WaitingApproval bool                          `json:"waiting_approval"`
	Approval        *conversation.PendingApproval `json:"approval,omitempty"`
}

// StartConversation creates a new conversation with the system prompt.
func (a *Agent) StartConversation() (*conversation.Conversation, error) {
	conv := conversation.New(a.config.Prompt)
	if err := a.storage.SaveConversation(conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// ProcessMessage handles a user message using the LLM.
func (a *Agent) ProcessMessage(conv *conversation.Conversation, userMessage string) (*ProcessResult, error) {
	if conv.Status == conversation.StatusWaitingApproval {
		return &ProcessResult{
			Response:        "Conversation is waiting for approval. Please respond to the pending approval first.",
			WaitingApproval: true,
			Approval:        conv.PendingApproval,
		}, nil
	}

	conv.AddMessage(conversation.RoleUser, userMessage)

	// Convert conversation messages to LLM format
	llmMessages := a.convertToLLMMessages(conv)

	// Get available tools from MCP
	tools := a.mcpClient.Tools()

	// Call LLM with tools
	response, err := a.llmClient.GenerateWithTools(a.config.Prompt, llmMessages, tools)
	if err != nil {
		errorMsg := fmt.Sprintf("LLM error: %v", err)
		conv.AddMessage(conversation.RoleAssistant, errorMsg)
		if saveErr := a.storage.SaveConversation(conv); saveErr != nil {
			return nil, saveErr
		}
		return &ProcessResult{Response: errorMsg, WaitingApproval: false}, nil
	}

	// Handle text response (no tool call)
	if response.ToolCall == nil {
		conv.AddMessage(conversation.RoleAssistant, response.Text)
		if err := a.storage.SaveConversation(conv); err != nil {
			return nil, err
		}
		return &ProcessResult{Response: response.Text, WaitingApproval: false}, nil
	}

	// Handle tool call
	toolName := response.ToolCall.Name
	toolArgs := response.ToolCall.Arguments

	// Get tool info
	tool := a.mcpClient.GetTool(toolName)
	if tool == nil {
		errorMsg := "Tool not found: " + toolName
		conv.AddMessage(conversation.RoleAssistant, errorMsg)
		if saveErr := a.storage.SaveConversation(conv); saveErr != nil {
			return nil, saveErr
		}
		return &ProcessResult{Response: errorMsg, WaitingApproval: false}, nil
	}

	// If tool is destructive, require approval
	if tool.DestructiveHint {
		description := a.formatApprovalDescription(tool.Name, toolArgs)
		approval := conv.SetWaitingApproval(tool.Name, toolArgs, description)
		conv.AddToolCall(tool.Name, toolArgs)

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

	// Execute tool directly (non-destructive)
	return a.executeToolAndRespond(conv, tool.Name, toolArgs)
}

// convertToLLMMessages converts conversation messages to LLM format.
func (a *Agent) convertToLLMMessages(conv *conversation.Conversation) []llm.Message {
	messages := make([]llm.Message, 0)

	for _, msg := range conv.Messages {
		// Skip system messages (handled separately as system instruction)
		if msg.Role == conversation.RoleSystem {
			continue
		}

		// Skip tool messages (they're context for the agent, not for the LLM conversation)
		if msg.Role == conversation.RoleTool {
			continue
		}

		role := string(msg.Role)
		if msg.Role == conversation.RoleAssistant {
			role = "model"
		}

		messages = append(messages, llm.Message{
			Role:    role,
			Content: msg.Content,
		})
	}

	return messages
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
