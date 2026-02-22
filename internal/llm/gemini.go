package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"agent-stop-and-go/internal/mcp"
)

const (
	baseURL           = "https://generativelanguage.googleapis.com/v1beta/models"
	httpClientTimeout = 60 * time.Second
)

// GeminiClient handles communication with the Gemini API.
type GeminiClient struct {
	model  string
	apiKey string
	client *http.Client
}

// NewGeminiClient creates a new Gemini client.
func NewGeminiClient(model string) (*GeminiClient, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY environment variable not set")
	}

	return &GeminiClient{
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: httpClientTimeout},
	}, nil
}

// Gemini API request/response types

type geminiRequest struct {
	Contents          []geminiContent   `json:"contents"`
	Tools             []geminiTool      `json:"tools,omitempty"`
	SystemInstruction *geminiContent    `json:"systemInstruction,omitempty"`
	ToolConfig        *geminiToolConfig `json:"toolConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text         string              `json:"text,omitempty"`
	FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations"`
}

type geminiFunctionDecl struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  *geminiParameters `json:"parameters,omitempty"`
}

type geminiParameters struct {
	Type       string                    `json:"type"`
	Properties map[string]geminiProperty `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

type geminiProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig *geminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type geminiFunctionCallingConfig struct {
	Mode string `json:"mode"` // "AUTO", "ANY", "NONE"
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	Error      *geminiError      `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// GenerateWithTools sends a request to Gemini with function calling support.
func (c *GeminiClient) GenerateWithTools(ctx context.Context, systemPrompt string, messages []Message, tools []mcp.Tool) (*Response, error) {
	// Convert MCP tools to Gemini function declarations
	funcDecls := make([]geminiFunctionDecl, 0, len(tools))
	for _, tool := range tools {
		decl := geminiFunctionDecl{
			Name:        tool.Name,
			Description: tool.Description,
		}

		// Convert input schema if present
		if tool.InputSchema.Properties != nil {
			params := &geminiParameters{
				Type:       "object",
				Properties: make(map[string]geminiProperty),
				Required:   tool.InputSchema.Required,
			}

			for propName, propSchema := range tool.InputSchema.Properties {
				prop := geminiProperty{
					Type: propSchema.Type,
				}
				if propSchema.Description != "" {
					prop.Description = propSchema.Description
				}
				params.Properties[propName] = prop
			}

			decl.Parameters = params
		}

		funcDecls = append(funcDecls, decl)
	}

	// Build request
	req := geminiRequest{
		Contents: make([]geminiContent, 0, len(messages)),
		ToolConfig: &geminiToolConfig{
			FunctionCallingConfig: &geminiFunctionCallingConfig{
				Mode: "AUTO",
			},
		},
	}

	// Add system instruction
	if systemPrompt != "" {
		req.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		}
	}

	// Add tools if any
	if len(funcDecls) > 0 {
		req.Tools = []geminiTool{{FunctionDeclarations: funcDecls}}
	}

	// Convert messages
	for _, msg := range messages {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		req.Contents = append(req.Contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: msg.Content}},
		})
	}

	// Make API request
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", baseURL, c.model, c.apiKey)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var geminiResp geminiResponse
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if geminiResp.Error != nil {
		return nil, fmt.Errorf("Gemini API error: %s", geminiResp.Error.Message)
	}

	if len(geminiResp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	// Parse response
	candidate := geminiResp.Candidates[0]
	response := &Response{}

	for _, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			response.ToolCall = &ToolCall{
				Name:      part.FunctionCall.Name,
				Arguments: part.FunctionCall.Args,
			}
			break
		}
		if part.Text != "" {
			response.Text = part.Text
		}
	}

	// Coerce tool call arguments to match schema types
	CoerceToolCallArgs(response.ToolCall, tools)

	return response, nil
}
