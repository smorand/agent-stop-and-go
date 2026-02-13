package llm

import (
	"strings"

	"agent-stop-and-go/internal/mcp"
)

// Client is the interface for LLM providers.
type Client interface {
	GenerateWithTools(systemPrompt string, messages []Message, tools []mcp.Tool) (*Response, error)
}

// Message represents a conversation message.
type Message struct {
	Role    string `json:"role"` // "user" or "model"/"assistant"
	Content string `json:"content"`
}

// ToolCall represents a function call from the LLM.
type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Response represents the LLM response.
type Response struct {
	Text     string    `json:"text,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
}

// NewClient creates an LLM client based on the model name.
// Models starting with "claude-" use Anthropic, everything else uses Gemini.
func NewClient(model string) (Client, error) {
	if strings.HasPrefix(model, "claude-") {
		return NewClaudeClient(model)
	}
	return NewGeminiClient(model)
}

// ToClaudeRole converts a role to Claude's format ("model" → "assistant").
func ToClaudeRole(role string) string {
	if role == "model" {
		return "assistant"
	}
	return role
}
