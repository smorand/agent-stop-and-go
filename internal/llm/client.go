package llm

import (
	"context"
	"fmt"
	"math"
	"strings"

	"agent-stop-and-go/internal/mcp"
)

// Client is the interface for LLM providers.
type Client interface {
	GenerateWithTools(ctx context.Context, systemPrompt string, messages []Message, tools []mcp.Tool) (*Response, error)
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
//
// Format: "provider:model" (colon is mandatory).
//
//	"google:gemini-2.5-flash"       → GeminiClient
//	"anthropic:claude-sonnet-4-6"   → ClaudeClient
//	"openai:gpt-4o"                 → OpenAICompatibleClient (OpenAI)
//	"mistral:mistral-large-latest"  → OpenAICompatibleClient (Mistral)
//	"ollama:llama3"                 → OpenAICompatibleClient (Ollama)
//	"openrouter:anthropic/claude-3" → OpenAICompatibleClient (OpenRouter)
func NewClient(model string) (Client, error) {
	provider, modelName, hasColon := strings.Cut(model, ":")
	if !hasColon {
		return nil, fmt.Errorf("invalid model format %q: expected \"provider:model\" (e.g. \"google:gemini-2.5-flash\")", model)
	}

	switch provider {
	case "google":
		return NewGeminiClient(modelName)
	case "anthropic":
		return NewClaudeClient(modelName)
	default:
		cfg, ok := providers[provider]
		if !ok {
			return nil, fmt.Errorf("unknown LLM provider: %q", provider)
		}
		return NewOpenAICompatibleClient(cfg, modelName), nil
	}
}

// ToClaudeRole converts a role to Claude's format ("model" → "assistant").
func ToClaudeRole(role string) string {
	if role == "model" {
		return "assistant"
	}
	return role
}

// CoerceToolCallArgs coerces tool call arguments to match the schema types.
// LLMs sometimes return numbers for string fields (e.g., IP "192.168.1.100"
// returned as float64 3232235876). This function converts values to the
// declared schema type.
func CoerceToolCallArgs(tc *ToolCall, tools []mcp.Tool) {
	if tc == nil {
		return
	}
	for _, tool := range tools {
		if tool.Name != tc.Name {
			continue
		}
		for propName, propSchema := range tool.InputSchema.Properties {
			val, ok := tc.Arguments[propName]
			if !ok {
				continue
			}
			switch propSchema.Type {
			case "string":
				switch v := val.(type) {
				case float64:
					if v == math.Trunc(v) && !math.IsInf(v, 0) {
						tc.Arguments[propName] = fmt.Sprintf("%.0f", v)
					} else {
						tc.Arguments[propName] = fmt.Sprintf("%g", v)
					}
				case bool:
					tc.Arguments[propName] = fmt.Sprintf("%t", v)
				}
			}
		}
		break
	}
}
