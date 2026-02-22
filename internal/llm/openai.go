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

// providerConfig holds the configuration for an OpenAI-compatible provider.
type providerConfig struct {
	name      string
	baseURL   string
	apiKeyEnv string
	headers   map[string]string
}

// providers is the registry of OpenAI-compatible provider configurations.
// Adding a new provider requires only a new entry here.
var providers = map[string]providerConfig{
	"openai": {
		name:      "openai",
		baseURL:   "https://api.openai.com/v1",
		apiKeyEnv: "OPENAI_API_KEY",
	},
	"mistral": {
		name:      "mistral",
		baseURL:   "https://api.mistral.ai/v1",
		apiKeyEnv: "MISTRAL_API_KEY",
	},
	"ollama": {
		name:      "ollama",
		baseURL:   "http://localhost:11434/v1",
		apiKeyEnv: "",
	},
	"openrouter": {
		name:      "openrouter",
		baseURL:   "https://openrouter.ai/api/v1",
		apiKeyEnv: "OPENROUTER_API_KEY",
		headers: map[string]string{
			"HTTP-Referer": "https://github.com/agentic-platform",
			"X-Title":      "Agent Stop and Go",
		},
	},
}

// OpenAICompatibleClient handles communication with OpenAI-compatible APIs.
type OpenAICompatibleClient struct {
	model  string
	config providerConfig
	client *http.Client
}

// NewOpenAICompatibleClient creates a new client for the given provider config and model name.
// Validation is lazy: missing API keys do not cause errors at creation time.
func NewOpenAICompatibleClient(cfg providerConfig, model string) *OpenAICompatibleClient {
	// Ollama: override base URL from env var
	if cfg.name == "ollama" {
		if envURL := os.Getenv("OLLAMA_BASE_URL"); envURL != "" {
			cfg.baseURL = envURL
		}
	}
	return &OpenAICompatibleClient{
		model:  model,
		config: cfg,
		client: &http.Client{Timeout: httpClientTimeout},
	}
}

// OpenAI Chat Completions request/response types

type openaiRequest struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
	Tools    []openaiTool    `json:"tools,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
}

type openaiChoice struct {
	Message openaiChoiceMessage `json:"message"`
}

type openaiChoiceMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiToolCallFunc `json:"function"`
}

type openaiToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiErrorResponse struct {
	Error *openaiError `json:"error"`
}

type openaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

// GenerateWithTools sends a request to an OpenAI-compatible API with function calling support.
func (c *OpenAICompatibleClient) GenerateWithTools(ctx context.Context, systemPrompt string, messages []Message, tools []mcp.Tool) (*Response, error) {
	// Build messages array
	msgs := make([]openaiMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: systemPrompt})
	}
	for _, msg := range messages {
		role := msg.Role
		if role == "model" {
			role = "assistant"
		}
		msgs = append(msgs, openaiMessage{Role: role, Content: msg.Content})
	}

	// Build request
	req := openaiRequest{
		Model:    c.model,
		Messages: msgs,
	}

	// Convert MCP tools to OpenAI function calling format
	if len(tools) > 0 {
		oaiTools := make([]openaiTool, 0, len(tools))
		for _, tool := range tools {
			params := map[string]any{"type": "object"}
			if tool.InputSchema.Properties != nil {
				props := make(map[string]any, len(tool.InputSchema.Properties))
				for propName, propSchema := range tool.InputSchema.Properties {
					p := map[string]any{"type": propSchema.Type}
					if propSchema.Description != "" {
						p["description"] = propSchema.Description
					}
					props[propName] = p
				}
				params["properties"] = props
			}
			if tool.InputSchema.Required != nil {
				params["required"] = tool.InputSchema.Required
			}
			oaiTools = append(oaiTools, openaiTool{
				Type: "function",
				Function: openaiFunction{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  params,
				},
			})
		}
		req.Tools = oaiTools
	}

	// Marshal request body
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build HTTP request
	url := c.config.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Conditional Authorization header
	if c.config.apiKeyEnv != "" {
		apiKey := os.Getenv(c.config.apiKeyEnv)
		if apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}

	// Custom headers
	for k, v := range c.config.headers {
		httpReq.Header.Set(k, v)
	}

	// Send request
	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle non-2xx responses
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		var errResp openaiErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error != nil {
			return nil, fmt.Errorf("%s API error (%d): %s", c.config.name, httpResp.StatusCode, errResp.Error.Message)
		}
		return nil, fmt.Errorf("%s API error (%d): %s", c.config.name, httpResp.StatusCode, http.StatusText(httpResp.StatusCode))
	}

	// Parse response
	var oaiResp openaiResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := oaiResp.Choices[0]
	response := &Response{}

	// Tool call takes precedence over text
	if len(choice.Message.ToolCalls) > 0 {
		tc := choice.Message.ToolCalls[0]
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
		}
		response.ToolCall = &ToolCall{
			Name:      tc.Function.Name,
			Arguments: args,
		}
	} else if choice.Message.Content != "" {
		response.Text = choice.Message.Content
	}

	// Coerce tool call arguments to match schema types
	CoerceToolCallArgs(response.ToolCall, tools)

	return response, nil
}
