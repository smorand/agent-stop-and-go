package agent

import (
	"strings"

	"agent-stop-and-go/internal/config"
	"agent-stop-and-go/internal/conversation"
	"agent-stop-and-go/internal/storage"
)

const approvalPrefix = "[APPROVAL_NEEDED]:"

// Agent handles the processing of conversations.
type Agent struct {
	config  *config.Config
	storage *storage.Storage
}

// New creates a new agent instance.
func New(cfg *config.Config, store *storage.Storage) *Agent {
	return &Agent{
		config:  cfg,
		storage: store,
	}
}

// ProcessResult contains the result of processing a message.
type ProcessResult struct {
	Response        string                       `json:"response"`
	WaitingApproval bool                         `json:"waiting_approval"`
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

// ProcessMessage handles a user message and generates a response.
// For now, this is a simple echo/mock implementation.
// In a real scenario, this would call an LLM API.
func (a *Agent) ProcessMessage(conv *conversation.Conversation, userMessage string) (*ProcessResult, error) {
	if conv.Status == conversation.StatusWaitingApproval {
		return &ProcessResult{
			Response:        "Conversation is waiting for approval. Please respond to the pending approval first.",
			WaitingApproval: true,
			Approval:        conv.PendingApproval,
		}, nil
	}

	conv.AddMessage(conversation.RoleUser, userMessage)

	// Mock agent response - in real implementation, this would call an LLM
	response := a.generateResponse(conv, userMessage)

	// Check if the response requires approval
	if strings.HasPrefix(response, approvalPrefix) {
		question := strings.TrimPrefix(response, approvalPrefix)
		question = strings.TrimSpace(question)

		approval := conv.SetWaitingApproval(question)
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

	conv.AddMessage(conversation.RoleAssistant, response)

	if err := a.storage.SaveConversation(conv); err != nil {
		return nil, err
	}

	return &ProcessResult{
		Response:        response,
		WaitingApproval: false,
	}, nil
}

// Keywords that trigger approval requirements.
var approvalKeywords = []string{
	// Deletion
	"delete", "remove", "drop", "truncate", "purge", "clear",
	// Scaling
	"scale", "resize",
	// Production changes
	"deploy", "production", "release", "rollout",
	// Service management
	"restart", "stop", "terminate", "kill",
	// Database operations
	"migrate", "migration", "alter table", "modify schema",
	// Data operations
	"update user", "modify user", "change password", "reset",
	// Cost operations
	"create instance", "spin up", "provision", "new cluster",
	// Security
	"permission", "access", "secret", "credential", "iam", "role",
}

// Keywords for automatic (read-only) operations.
var automaticKeywords = []string{
	"list", "show", "get", "describe", "status", "logs", "metrics",
	"check", "health", "info", "version", "help", "dry-run", "report",
}

// generateResponse creates a mock response.
// Replace this with actual LLM integration.
func (a *Agent) generateResponse(conv *conversation.Conversation, userMessage string) string {
	lowerMsg := strings.ToLower(userMessage)

	// Check for automatic actions first
	for _, keyword := range automaticKeywords {
		if strings.Contains(lowerMsg, keyword) {
			return a.handleAutomaticAction(lowerMsg, userMessage)
		}
	}

	// Check for approval-required actions
	for _, keyword := range approvalKeywords {
		if strings.Contains(lowerMsg, keyword) {
			return a.handleApprovalRequired(keyword, userMessage)
		}
	}

	// Greetings
	if strings.Contains(lowerMsg, "hello") || strings.Contains(lowerMsg, "hi") {
		return "Hello! I'm your autonomous DevOps agent. I can help you manage infrastructure and deployments. Some actions may require your approval before I proceed."
	}

	return "I received your request: \"" + userMessage + "\". Processing complete."
}

// handleAutomaticAction generates responses for read-only operations.
func (a *Agent) handleAutomaticAction(lowerMsg, userMessage string) string {
	if strings.Contains(lowerMsg, "list") || strings.Contains(lowerMsg, "show") {
		return "Listing resources... Found 3 pods, 2 services, 1 deployment. All healthy."
	}
	if strings.Contains(lowerMsg, "status") || strings.Contains(lowerMsg, "health") {
		return "System status: All services operational. CPU: 45%, Memory: 62%, No alerts."
	}
	if strings.Contains(lowerMsg, "logs") {
		return "Fetching logs... Last 100 lines retrieved. No errors detected in the past hour."
	}
	if strings.Contains(lowerMsg, "metrics") {
		return "Metrics report: Avg response time 120ms, Error rate 0.1%, Throughput 1.2k req/s."
	}
	if strings.Contains(lowerMsg, "describe") || strings.Contains(lowerMsg, "info") {
		return "Resource details retrieved. Configuration is valid and up to date."
	}
	if strings.Contains(lowerMsg, "help") {
		return "I'm an autonomous DevOps agent. I can list resources, check status, view logs, deploy applications, scale services, and more. Destructive or sensitive actions will require your approval."
	}
	return "Read-only operation completed for: " + userMessage
}

// handleApprovalRequired generates approval requests for sensitive operations.
func (a *Agent) handleApprovalRequired(matchedKeyword, userMessage string) string {
	var action, impact string

	switch {
	case strings.Contains(matchedKeyword, "delete") || strings.Contains(matchedKeyword, "remove") ||
		strings.Contains(matchedKeyword, "drop") || strings.Contains(matchedKeyword, "purge"):
		action = "DELETE operation"
		impact = "This will permanently remove the specified resource(s). Data may be unrecoverable."

	case strings.Contains(matchedKeyword, "scale") || strings.Contains(matchedKeyword, "resize"):
		action = "SCALING operation"
		impact = "This will change resource allocation and may affect availability during the transition."

	case strings.Contains(matchedKeyword, "deploy") || strings.Contains(matchedKeyword, "release"):
		action = "DEPLOYMENT operation"
		impact = "This will deploy new code to the environment. Ensure tests have passed."

	case strings.Contains(matchedKeyword, "restart") || strings.Contains(matchedKeyword, "stop"):
		action = "SERVICE RESTART operation"
		impact = "This will cause temporary service interruption."

	case strings.Contains(matchedKeyword, "migrate") || strings.Contains(matchedKeyword, "migration"):
		action = "DATABASE MIGRATION"
		impact = "This will modify the database schema. Ensure backups are available."

	case strings.Contains(matchedKeyword, "permission") || strings.Contains(matchedKeyword, "access") ||
		strings.Contains(matchedKeyword, "secret") || strings.Contains(matchedKeyword, "iam"):
		action = "SECURITY CHANGE"
		impact = "This will modify access permissions or credentials."

	case strings.Contains(matchedKeyword, "create instance") || strings.Contains(matchedKeyword, "provision"):
		action = "RESOURCE PROVISIONING"
		impact = "This will create new infrastructure and may incur additional costs."

	default:
		action = "SENSITIVE OPERATION"
		impact = "This action may have significant impact on the system."
	}

	return approvalPrefix + " " + action + "\n\nRequest: " + userMessage + "\n\nImpact: " + impact + "\n\nDo you approve this action?"
}

// ResolveApproval handles an approval response.
func (a *Agent) ResolveApproval(approvalUUID, answer string) (*conversation.Conversation, *ProcessResult, error) {
	conv, err := a.storage.FindConversationByApprovalUUID(approvalUUID)
	if err != nil {
		return nil, nil, err
	}

	conv.ResolveApproval(answer)

	// Process the approval response
	var response string
	lowerAnswer := strings.ToLower(answer)
	if strings.Contains(lowerAnswer, "yes") ||
		strings.Contains(lowerAnswer, "confirm") ||
		strings.Contains(lowerAnswer, "approve") ||
		strings.Contains(lowerAnswer, "ok") {
		response = "Approval received. Proceeding with the requested action."
	} else {
		response = "Action cancelled based on your response."
	}

	conv.AddMessage(conversation.RoleAssistant, response)

	if err := a.storage.SaveConversation(conv); err != nil {
		return nil, nil, err
	}

	return conv, &ProcessResult{
		Response:        response,
		WaitingApproval: false,
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
