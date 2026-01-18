package conversation

import (
	"time"

	"github.com/google/uuid"
)

// Status represents the current state of a conversation.
type Status string

const (
	StatusActive          Status = "active"
	StatusWaitingApproval Status = "waiting_approval"
	StatusCompleted       Status = "completed"
)

// Role represents who sent a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single message in a conversation.
type Message struct {
	ID        string    `json:"id"`
	Role      Role      `json:"role"`
	Content   string    `json:"content"`
	ToolCall  *ToolCall `json:"tool_call,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ToolCall represents a tool invocation.
type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Result    string         `json:"result,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

// PendingApproval represents a tool call waiting for external approval.
type PendingApproval struct {
	UUID           string         `json:"uuid"`
	ConversationID string         `json:"conversation_id"`
	ToolName       string         `json:"tool_name"`
	ToolArgs       map[string]any `json:"tool_args"`
	Description    string         `json:"description"`
	CreatedAt      time.Time      `json:"created_at"`
}

// Conversation represents a chat session with the agent.
type Conversation struct {
	ID              string           `json:"id"`
	Status          Status           `json:"status"`
	Messages        []Message        `json:"messages"`
	PendingApproval *PendingApproval `json:"pending_approval,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// New creates a new conversation with a system prompt.
func New(systemPrompt string) *Conversation {
	now := time.Now()
	conv := &Conversation{
		ID:        uuid.New().String(),
		Status:    StatusActive,
		Messages:  []Message{},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if systemPrompt != "" {
		conv.Messages = append(conv.Messages, Message{
			ID:        uuid.New().String(),
			Role:      RoleSystem,
			Content:   systemPrompt,
			CreatedAt: now,
		})
	}

	return conv
}

// AddMessage appends a new message to the conversation.
func (c *Conversation) AddMessage(role Role, content string) Message {
	msg := Message{
		ID:        uuid.New().String(),
		Role:      role,
		Content:   content,
		CreatedAt: time.Now(),
	}
	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = time.Now()
	return msg
}

// AddToolCall appends a tool call message to the conversation.
func (c *Conversation) AddToolCall(name string, args map[string]any) Message {
	msg := Message{
		ID:   uuid.New().String(),
		Role: RoleAssistant,
		ToolCall: &ToolCall{
			Name:      name,
			Arguments: args,
		},
		CreatedAt: time.Now(),
	}
	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = time.Now()
	return msg
}

// AddToolResult appends a tool result message to the conversation.
func (c *Conversation) AddToolResult(name string, result string, isError bool) Message {
	msg := Message{
		ID:   uuid.New().String(),
		Role: RoleTool,
		ToolCall: &ToolCall{
			Name:    name,
			Result:  result,
			IsError: isError,
		},
		CreatedAt: time.Now(),
	}
	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = time.Now()
	return msg
}

// SetWaitingApproval marks the conversation as waiting for tool approval.
func (c *Conversation) SetWaitingApproval(toolName string, toolArgs map[string]any, description string) *PendingApproval {
	approval := &PendingApproval{
		UUID:           uuid.New().String(),
		ConversationID: c.ID,
		ToolName:       toolName,
		ToolArgs:       toolArgs,
		Description:    description,
		CreatedAt:      time.Now(),
	}
	c.Status = StatusWaitingApproval
	c.PendingApproval = approval
	c.UpdatedAt = time.Now()
	return approval
}

// ResolveApproval clears the pending approval and resumes the conversation.
func (c *Conversation) ResolveApproval() {
	if c.PendingApproval != nil {
		c.PendingApproval = nil
		c.Status = StatusActive
		c.UpdatedAt = time.Now()
	}
}

// Complete marks the conversation as completed.
func (c *Conversation) Complete() {
	c.Status = StatusCompleted
	c.UpdatedAt = time.Now()
}
