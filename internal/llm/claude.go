package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"agent-stop-and-go/internal/mcp"
)

const (
	claudeBaseURL   = "https://api.anthropic.com/v1/messages"
	claudeMaxTokens = 4096
)

// ClaudeClient handles communication with the Anthropic Messages API.
type ClaudeClient struct {
	model  string
	apiKey string
	client *http.Client
}

// NewClaudeClient creates a new Claude client.
func NewClaudeClient(model string) (*ClaudeClient, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable not set")
	}

	return &ClaudeClient{
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: httpClientTimeout},
	}, nil
}

// Claude API request/response types

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []claudeMessage `json:"messages"`
	Tools     []claudeTool    `json:"tools,omitempty"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type claudeInputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]claudeProperty `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

type claudeProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type claudeResponse struct {
	ID         string               `json:"id"`
	Type       string               `json:"type"`
	Role       string               `json:"role"`
	Content    []claudeContentBlock `json:"content"`
	StopReason string               `json:"stop_reason"`
	Error      *claudeError         `json:"error,omitempty"`
}

type claudeContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type claudeError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// GenerateWithTools sends a request to Claude with tool use support.
func (c *ClaudeClient) GenerateWithTools(ctx context.Context, systemPrompt string, messages []Message, tools []mcp.Tool) (*Response, error) {
	// Convert MCP tools to Claude tool format
	claudeTools := make([]claudeTool, 0, len(tools))
	for _, tool := range tools {
		schema := claudeInputSchema{
			Type:       "object",
			Properties: make(map[string]claudeProperty),
			Required:   tool.InputSchema.Required,
		}

		for propName, propSchema := range tool.InputSchema.Properties {
			prop := claudeProperty{
				Type: propSchema.Type,
			}
			if propSchema.Description != "" {
				prop.Description = propSchema.Description
			}
			schema.Properties[propName] = prop
		}

		schemaJSON, err := json.Marshal(schema)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal tool schema: %w", err)
		}

		claudeTools = append(claudeTools, claudeTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schemaJSON,
		})
	}

	// Build request
	req := claudeRequest{
		Model:     c.model,
		MaxTokens: claudeMaxTokens,
		System:    systemPrompt,
		Messages:  make([]claudeMessage, 0, len(messages)),
	}

	// Add tools if any
	if len(claudeTools) > 0 {
		req.Tools = claudeTools
	}

	// Convert messages
	for _, msg := range messages {
		role := ToClaudeRole(msg.Role)
		req.Messages = append(req.Messages, claudeMessage{
			Role:    role,
			Content: msg.Content,
		})
	}

	// Make API request
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", claudeBaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if claudeResp.Error != nil {
		return nil, fmt.Errorf("Claude API error: %s", claudeResp.Error.Message)
	}

	// Parse response
	response := &Response{}

	for _, block := range claudeResp.Content {
		if block.Type == "tool_use" {
			var args map[string]any
			if err := json.Unmarshal(block.Input, &args); err != nil {
				return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
			}
			response.ToolCall = &ToolCall{
				Name:      block.Name,
				Arguments: args,
			}
			break
		}
		if block.Type == "text" && block.Text != "" {
			response.Text += block.Text
		}
	}

	return response, nil
}
