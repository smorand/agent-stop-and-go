package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-stop-and-go/internal/mcp"
)

// testTools returns a set of MCP tools for testing.
func testTools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name:        "resources_add",
			Description: "Add a resource",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"name":  {Type: "string", Description: "Resource name"},
					"value": {Type: "string"},
				},
				Required: []string{"name", "value"},
			},
		},
	}
}

// textResponse returns an OpenAI text completion response body.
func textResponse(text string) string {
	resp := openaiResponse{
		Choices: []openaiChoice{
			{Message: openaiChoiceMessage{Role: "assistant", Content: text}},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// toolCallResponse returns an OpenAI tool call response body.
func toolCallResponse(name, argsJSON string) string {
	resp := openaiResponse{
		Choices: []openaiChoice{
			{Message: openaiChoiceMessage{
				Role: "assistant",
				ToolCalls: []openaiToolCall{
					{ID: "call_1", Type: "function", Function: openaiToolCallFunc{Name: name, Arguments: argsJSON}},
				},
			}},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// newTestClient creates an OpenAICompatibleClient pointing to a mock server.
func newTestClient(cfg providerConfig, model string, serverURL string) *OpenAICompatibleClient {
	cfg.baseURL = serverURL
	return NewOpenAICompatibleClient(cfg, model)
}

// --- E2E-001: OpenAI Text Response ---

func TestOpenAITextResponse(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(textResponse("Hello from OpenAI")))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: "OPENAI_API_KEY"}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	t.Setenv("OPENAI_API_KEY", "test-key-123")

	resp, err := client.GenerateWithTools(context.Background(), "You are helpful", []Message{{Role: "user", Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from OpenAI" {
		t.Errorf("got text %q, want %q", resp.Text, "Hello from OpenAI")
	}
	if resp.ToolCall != nil {
		t.Errorf("expected nil ToolCall, got %+v", resp.ToolCall)
	}

	// Verify request
	if capturedReq.Method != "POST" {
		t.Errorf("got method %s, want POST", capturedReq.Method)
	}
	if !strings.HasSuffix(capturedReq.URL.Path, "/chat/completions") {
		t.Errorf("got path %s, want /chat/completions", capturedReq.URL.Path)
	}
	if got := capturedReq.Header.Get("Authorization"); got != "Bearer test-key-123" {
		t.Errorf("got Authorization %q, want %q", got, "Bearer test-key-123")
	}
	if got := capturedReq.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("got Content-Type %q, want %q", got, "application/json")
	}

	var reqBody openaiRequest
	if err := json.Unmarshal(capturedBody, &reqBody); err != nil {
		t.Fatalf("failed to parse request body: %v", err)
	}
	if reqBody.Model != "gpt-4o" {
		t.Errorf("got model %q, want %q", reqBody.Model, "gpt-4o")
	}
	if len(reqBody.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(reqBody.Messages))
	}
	if reqBody.Messages[0].Role != "system" || reqBody.Messages[0].Content != "You are helpful" {
		t.Errorf("messages[0] = %+v, want system/You are helpful", reqBody.Messages[0])
	}
	if reqBody.Messages[1].Role != "user" || reqBody.Messages[1].Content != "Hi" {
		t.Errorf("messages[1] = %+v, want user/Hi", reqBody.Messages[1])
	}
}

// --- E2E-002: Mistral Text Response ---

func TestMistralTextResponse(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(textResponse("Hello from Mistral")))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "mistral", baseURL: srv.URL, apiKeyEnv: "MISTRAL_API_KEY"}
	client := NewOpenAICompatibleClient(cfg, "mistral-large-latest")

	t.Setenv("MISTRAL_API_KEY", "mistral-test-key")

	resp, err := client.GenerateWithTools(context.Background(), "Be concise", []Message{{Role: "user", Content: "Hello"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from Mistral" {
		t.Errorf("got text %q, want %q", resp.Text, "Hello from Mistral")
	}
	if got := capturedReq.Header.Get("Authorization"); got != "Bearer mistral-test-key" {
		t.Errorf("got Authorization %q, want %q", got, "Bearer mistral-test-key")
	}

	var reqBody openaiRequest
	json.Unmarshal(capturedBody, &reqBody)
	if reqBody.Model != "mistral-large-latest" {
		t.Errorf("got model %q, want %q", reqBody.Model, "mistral-large-latest")
	}
}

// --- E2E-003: Ollama Text Response (No API Key, Custom Base URL) ---

func TestOllamaTextResponse(t *testing.T) {
	var capturedReq *http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(textResponse("Hello from Ollama")))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "ollama", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "llama3")

	resp, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "Hello"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from Ollama" {
		t.Errorf("got text %q, want %q", resp.Text, "Hello from Ollama")
	}
	if got := capturedReq.Header.Get("Authorization"); got != "" {
		t.Errorf("expected no Authorization header, got %q", got)
	}
}

// --- E2E-004: OpenRouter Text Response (Custom Headers) ---

func TestOpenRouterTextResponse(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(textResponse("Hello from OpenRouter")))
	}))
	defer srv.Close()

	cfg := providerConfig{
		name: "openrouter", baseURL: srv.URL, apiKeyEnv: "OPENROUTER_API_KEY",
		headers: map[string]string{"HTTP-Referer": "https://github.com/agentic-platform", "X-Title": "Agent Stop and Go"},
	}
	client := NewOpenAICompatibleClient(cfg, "anthropic/claude-3-opus")

	t.Setenv("OPENROUTER_API_KEY", "openrouter-test-key")

	resp, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "Hello"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from OpenRouter" {
		t.Errorf("got text %q, want %q", resp.Text, "Hello from OpenRouter")
	}
	if got := capturedReq.Header.Get("HTTP-Referer"); got != "https://github.com/agentic-platform" {
		t.Errorf("got HTTP-Referer %q, want %q", got, "https://github.com/agentic-platform")
	}
	if got := capturedReq.Header.Get("X-Title"); got != "Agent Stop and Go" {
		t.Errorf("got X-Title %q, want %q", got, "Agent Stop and Go")
	}
	if got := capturedReq.Header.Get("Authorization"); got != "Bearer openrouter-test-key" {
		t.Errorf("got Authorization %q, want %q", got, "Bearer openrouter-test-key")
	}

	var reqBody openaiRequest
	json.Unmarshal(capturedBody, &reqBody)
	if reqBody.Model != "anthropic/claude-3-opus" {
		t.Errorf("got model %q, want %q", reqBody.Model, "anthropic/claude-3-opus")
	}
}

// --- E2E-005: MCP Tool Schema to OpenAI Function Format Conversion ---

func TestToolSchemaConversion(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(textResponse("ok")))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")
	tools := testTools()

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var reqBody map[string]any
	json.Unmarshal(capturedBody, &reqBody)

	toolsArr, ok := reqBody["tools"].([]any)
	if !ok || len(toolsArr) != 1 {
		t.Fatalf("expected 1 tool, got %v", reqBody["tools"])
	}

	tool := toolsArr[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("got type %q, want %q", tool["type"], "function")
	}

	fn := tool["function"].(map[string]any)
	if fn["name"] != "resources_add" {
		t.Errorf("got name %q, want %q", fn["name"], "resources_add")
	}
	if fn["description"] != "Add a resource" {
		t.Errorf("got description %q, want %q", fn["description"], "Add a resource")
	}

	params := fn["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Errorf("got params type %q, want %q", params["type"], "object")
	}

	props := params["properties"].(map[string]any)
	nameProp := props["name"].(map[string]any)
	if nameProp["type"] != "string" {
		t.Errorf("got name.type %q, want %q", nameProp["type"], "string")
	}
	if nameProp["description"] != "Resource name" {
		t.Errorf("got name.description %q, want %q", nameProp["description"], "Resource name")
	}

	valueProp := props["value"].(map[string]any)
	if valueProp["type"] != "string" {
		t.Errorf("got value.type %q, want %q", valueProp["type"], "string")
	}

	required := params["required"].([]any)
	if len(required) != 2 {
		t.Errorf("got %d required fields, want 2", len(required))
	}
}

// --- E2E-006: Tool Call Response Parsing ---

func TestToolCallResponseParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(toolCallResponse("resources_add", `{"name":"test","value":"123"}`)))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	resp, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "add test"}}, testTools())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ToolCall == nil {
		t.Fatal("expected ToolCall, got nil")
	}
	if resp.ToolCall.Name != "resources_add" {
		t.Errorf("got name %q, want %q", resp.ToolCall.Name, "resources_add")
	}
	if resp.ToolCall.Arguments["name"] != "test" {
		t.Errorf("got name arg %v, want %q", resp.ToolCall.Arguments["name"], "test")
	}
	if resp.ToolCall.Arguments["value"] != "123" {
		t.Errorf("got value arg %v, want %q", resp.ToolCall.Arguments["value"], "123")
	}
	if resp.Text != "" {
		t.Errorf("expected empty Text, got %q", resp.Text)
	}
}

// --- E2E-007: CoerceToolCallArgs with OpenAI-Parsed Arguments ---

func TestCoerceToolCallArgsWithOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(toolCallResponse("resources_add", `{"name":"server","value":42}`)))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	resp, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "add"}}, testTools())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ToolCall == nil {
		t.Fatal("expected ToolCall, got nil")
	}
	// value should be coerced from float64(42) to "42"
	if got, ok := resp.ToolCall.Arguments["value"].(string); !ok || got != "42" {
		t.Errorf("got value %v (%T), want string %q", resp.ToolCall.Arguments["value"], resp.ToolCall.Arguments["value"], "42")
	}
}

// --- E2E-008: NewClient Provider Routing for All Providers ---

func TestNewClientProviderRouting(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "fake")
	t.Setenv("ANTHROPIC_API_KEY", "fake")

	tests := []struct {
		model    string
		wantType string
	}{
		{"openai:gpt-4o", "*llm.OpenAICompatibleClient"},
		{"mistral:mistral-large-latest", "*llm.OpenAICompatibleClient"},
		{"ollama:llama3", "*llm.OpenAICompatibleClient"},
		{"openrouter:anthropic/claude-3-opus", "*llm.OpenAICompatibleClient"},
		{"anthropic:claude-sonnet-4-20250514", "*llm.ClaudeClient"},
		{"google:gemini-2.5-flash", "*llm.GeminiClient"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			client, err := NewClient(tt.model)
			if err != nil {
				t.Fatalf("NewClient(%q) error: %v", tt.model, err)
			}
			gotType := typeString(client)
			if gotType != tt.wantType {
				t.Errorf("NewClient(%q) = %s, want %s", tt.model, gotType, tt.wantType)
			}
		})
	}
}

func typeString(v any) string {
	return strings.Replace(strings.Replace(
		strings.TrimPrefix(strings.TrimPrefix(
			typeOf(v), ""), ""), "llm.", "llm.", 1), "llm.", "llm.", 1)
}

func typeOf(v any) string {
	if v == nil {
		return "<nil>"
	}
	return "*" + strings.TrimPrefix(strings.TrimPrefix(
		strings.Replace(
			strings.Replace(typeNameOf(v), "agent-stop-and-go/internal/", "", 1),
			"agent_stop_and_go/internal/", "", 1),
		"*"), "*")
}

func typeNameOf(v any) string {
	t := strings.TrimPrefix(
		strings.Replace(
			strings.Replace(
				typeReflect(v), "agent-stop-and-go/internal/", "", 1),
			"agent_stop_and_go/internal/", "", 1),
		"*")
	return t
}

func typeReflect(v any) string {
	switch v.(type) {
	case *OpenAICompatibleClient:
		return "llm.OpenAICompatibleClient"
	case *ClaudeClient:
		return "llm.ClaudeClient"
	case *GeminiClient:
		return "llm.GeminiClient"
	default:
		return "unknown"
	}
}

// --- E2E-009: Google and Anthropic Explicit Provider Routing ---

func TestExplicitProviderRouting(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "fake")
	t.Setenv("ANTHROPIC_API_KEY", "fake")

	gemini, err := NewClient("google:gemini-2.5-flash")
	if err != nil {
		t.Fatalf("NewClient(google:gemini) error: %v", err)
	}
	if _, ok := gemini.(*GeminiClient); !ok {
		t.Errorf("google:gemini-2.5-flash returned %T, want *GeminiClient", gemini)
	}

	claude, err := NewClient("anthropic:claude-sonnet-4-20250514")
	if err != nil {
		t.Fatalf("NewClient(anthropic:claude) error: %v", err)
	}
	if _, ok := claude.(*ClaudeClient); !ok {
		t.Errorf("anthropic:claude-sonnet-4-20250514 returned %T, want *ClaudeClient", claude)
	}
}

// --- E2E-010: Ollama Base URL from Environment Variable ---

func TestOllamaBaseURLFromEnv(t *testing.T) {
	t.Setenv("OLLAMA_BASE_URL", "http://custom-host:9999/v1")

	client, err := NewClient("ollama:llama3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	oai, ok := client.(*OpenAICompatibleClient)
	if !ok {
		t.Fatalf("expected *OpenAICompatibleClient, got %T", client)
	}
	if oai.config.baseURL != "http://custom-host:9999/v1" {
		t.Errorf("got baseURL %q, want %q", oai.config.baseURL, "http://custom-host:9999/v1")
	}

	// Default when env var is not set
	t.Setenv("OLLAMA_BASE_URL", "")
	client2, err := NewClient("ollama:llama3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	oai2 := client2.(*OpenAICompatibleClient)
	if oai2.config.baseURL != "http://localhost:11434/v1" {
		t.Errorf("got baseURL %q, want %q", oai2.config.baseURL, "http://localhost:11434/v1")
	}
}

// --- E2E-011: OpenRouter Custom Headers Present in Request ---

func TestOpenRouterCustomHeaders(t *testing.T) {
	var capturedReq *http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(textResponse("ok")))
	}))
	defer srv.Close()

	cfg := providers["openrouter"]
	cfg.baseURL = srv.URL

	client := NewOpenAICompatibleClient(cfg, "anthropic/claude-3-opus")
	t.Setenv("OPENROUTER_API_KEY", "test-key")

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := capturedReq.Header.Get("HTTP-Referer"); got != "https://github.com/agentic-platform" {
		t.Errorf("got HTTP-Referer %q, want %q", got, "https://github.com/agentic-platform")
	}
	if got := capturedReq.Header.Get("X-Title"); got != "Agent Stop and Go" {
		t.Errorf("got X-Title %q, want %q", got, "Agent Stop and Go")
	}
}

// --- E2E-012: Authorization Header Omitted When API Key Is Empty ---

func TestAuthorizationOmittedWhenEmpty(t *testing.T) {
	var capturedReq *http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(textResponse("ok")))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "ollama", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "llama3")

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := capturedReq.Header.Get("Authorization"); got != "" {
		t.Errorf("expected no Authorization header, got %q", got)
	}
}

// --- E2E-013: Mixed Providers -- Different Client Configs ---

func TestMixedProviderConfigs(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")

	ollamaClient, err := NewClient("ollama:llama3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	openaiClient, err := NewClient("openai:gpt-4o")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	oaiOllama, ok := ollamaClient.(*OpenAICompatibleClient)
	if !ok {
		t.Fatalf("ollama: expected *OpenAICompatibleClient, got %T", ollamaClient)
	}
	oaiOpenai, ok := openaiClient.(*OpenAICompatibleClient)
	if !ok {
		t.Fatalf("openai: expected *OpenAICompatibleClient, got %T", openaiClient)
	}

	if oaiOllama.config.name != "ollama" {
		t.Errorf("ollama config name = %q, want %q", oaiOllama.config.name, "ollama")
	}
	if oaiOpenai.config.name != "openai" {
		t.Errorf("openai config name = %q, want %q", oaiOpenai.config.name, "openai")
	}
	if oaiOllama.config.apiKeyEnv != "" {
		t.Errorf("ollama apiKeyEnv = %q, want empty", oaiOllama.config.apiKeyEnv)
	}
	if oaiOpenai.config.apiKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("openai apiKeyEnv = %q, want %q", oaiOpenai.config.apiKeyEnv, "OPENAI_API_KEY")
	}
}

// --- E2E-014: API Returns 401 Unauthorized ---

func TestAPIError401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "openai") {
		t.Errorf("error should contain 'openai': %v", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should contain '401': %v", err)
	}
	if !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("error should contain 'Invalid API key': %v", err)
	}
}

// --- E2E-015: API Returns 429 Rate Limited ---

func TestAPIError429(t *testing.T) {
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should contain '429': %v", err)
	}
	if !strings.Contains(err.Error(), "Rate limit exceeded") {
		t.Errorf("error should contain 'Rate limit exceeded': %v", err)
	}
	if requestCount != 1 {
		t.Errorf("expected exactly 1 request (no retry), got %d", requestCount)
	}
}

// --- E2E-016: API Returns 404 Model Not Found ---

func TestAPIError404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		w.Write([]byte(`{"error":{"message":"Model not found","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should contain '404': %v", err)
	}
	if !strings.Contains(err.Error(), "Model not found") {
		t.Errorf("error should contain 'Model not found': %v", err)
	}
}

// --- E2E-017: API Returns 500 Server Error ---

func TestAPIError500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		w.Write([]byte(`{"error":{"message":"Internal server error","type":"server_error"}}`))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain '500': %v", err)
	}
	if !strings.Contains(err.Error(), "Internal server error") {
		t.Errorf("error should contain 'Internal server error': %v", err)
	}
}

// --- E2E-018: Ollama Server Unreachable ---

func TestOllamaServerUnreachable(t *testing.T) {
	cfg := providerConfig{name: "ollama", baseURL: "http://localhost:1/v1", apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "llama3")

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to send request") {
		t.Errorf("error should contain 'failed to send request': %v", err)
	}
}

// --- E2E-019: Malformed JSON in Tool Call Arguments ---

func TestMalformedToolCallArguments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(toolCallResponse("resources_add", `not valid json{`)))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, testTools())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse tool arguments") {
		t.Errorf("error should contain 'failed to parse tool arguments': %v", err)
	}
}

// --- E2E-020: Empty Choices Array in Response ---

func TestEmptyChoicesArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[],"model":"gpt-4o"}`))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no choices in response") {
		t.Errorf("error should contain 'no choices in response': %v", err)
	}
}

// --- E2E-021: Response with Both Text and Tool Call ---

func TestResponseWithBothTextAndToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := openaiResponse{
			Choices: []openaiChoice{
				{Message: openaiChoiceMessage{
					Role:    "assistant",
					Content: "Some text",
					ToolCalls: []openaiToolCall{
						{ID: "call_1", Type: "function", Function: openaiToolCallFunc{Name: "resources_add", Arguments: `{"name":"test","value":"x"}`}},
					},
				}},
			},
		}
		b, _ := json.Marshal(resp)
		w.Write(b)
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	resp, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, testTools())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Tool call takes precedence
	if resp.ToolCall == nil {
		t.Fatal("expected ToolCall, got nil")
	}
	if resp.ToolCall.Name != "resources_add" {
		t.Errorf("got name %q, want %q", resp.ToolCall.Name, "resources_add")
	}
}

// --- E2E-022: System Prompt Handling ---

func TestSystemPromptHandling(t *testing.T) {
	var capturedBodies [][]byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBodies = append(capturedBodies, body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(textResponse("ok")))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	// With system prompt
	client.GenerateWithTools(context.Background(), "You are helpful", []Message{{Role: "user", Content: "Hi"}}, nil)

	var req1 openaiRequest
	json.Unmarshal(capturedBodies[0], &req1)
	if len(req1.Messages) != 2 {
		t.Fatalf("with system prompt: got %d messages, want 2", len(req1.Messages))
	}
	if req1.Messages[0].Role != "system" || req1.Messages[0].Content != "You are helpful" {
		t.Errorf("messages[0] = %+v, want system/You are helpful", req1.Messages[0])
	}
	if req1.Messages[1].Role != "user" {
		t.Errorf("messages[1].role = %q, want %q", req1.Messages[1].Role, "user")
	}

	// Without system prompt
	client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "Hi"}}, nil)

	var req2 openaiRequest
	json.Unmarshal(capturedBodies[1], &req2)
	if len(req2.Messages) != 1 {
		t.Fatalf("without system prompt: got %d messages, want 1", len(req2.Messages))
	}
	if req2.Messages[0].Role != "user" {
		t.Errorf("messages[0].role = %q, want %q", req2.Messages[0].Role, "user")
	}
}

// --- E2E-023: Tools with Empty Input Schema ---

func TestEmptyToolSchema(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(textResponse("ok")))
	}))
	defer srv.Close()

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	tools := []mcp.Tool{
		{
			Name:        "exit_loop",
			Description: "Exit the loop",
			InputSchema: mcp.InputSchema{Type: "object"},
		},
	}

	_, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var reqBody map[string]any
	json.Unmarshal(capturedBody, &reqBody)

	toolsArr := reqBody["tools"].([]any)
	fn := toolsArr[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "exit_loop" {
		t.Errorf("got name %q, want %q", fn["name"], "exit_loop")
	}
	params := fn["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Errorf("got params type %q, want %q", params["type"], "object")
	}
}

// --- E2E-024: Unknown Provider Returns Error ---

func TestUnknownProviderReturnsError(t *testing.T) {
	_, err := NewClient("foobar:some-model")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), `unknown LLM provider: "foobar"`) {
		t.Errorf("error = %q, want unknown LLM provider message", err.Error())
	}
}

// --- Missing Colon Returns Error ---

func TestMissingColonReturnsError(t *testing.T) {
	_, err := NewClient("gemini-2.5-flash")
	if err == nil {
		t.Fatal("expected error for missing colon, got nil")
	}
	if !strings.Contains(err.Error(), "expected \"provider:model\"") {
		t.Errorf("error = %q, want provider:model format message", err.Error())
	}
}

// --- E2E-025: Tool Call with Nested/Complex Argument Types ---

func TestNestedToolCallArguments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(toolCallResponse("resources_add", `{"filter":{"name":"test"},"tags":["a","b"],"count":5}`)))
	}))
	defer srv.Close()

	// Tool schema: count is declared as string to test coercion
	tools := []mcp.Tool{
		{
			Name:        "resources_add",
			Description: "Add a resource",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"filter": {Type: "object"},
					"tags":   {Type: "array"},
					"count":  {Type: "string"},
				},
			},
		},
	}

	cfg := providerConfig{name: "openai", baseURL: srv.URL, apiKeyEnv: ""}
	client := NewOpenAICompatibleClient(cfg, "gpt-4o")

	resp, err := client.GenerateWithTools(context.Background(), "", []Message{{Role: "user", Content: "test"}}, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nested object preserved
	filter, ok := resp.ToolCall.Arguments["filter"].(map[string]any)
	if !ok {
		t.Fatalf("filter: expected map[string]any, got %T", resp.ToolCall.Arguments["filter"])
	}
	if filter["name"] != "test" {
		t.Errorf("filter.name = %v, want %q", filter["name"], "test")
	}

	// Array preserved
	tags, ok := resp.ToolCall.Arguments["tags"].([]any)
	if !ok {
		t.Fatalf("tags: expected []any, got %T", resp.ToolCall.Arguments["tags"])
	}
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tags = %v, want [a b]", tags)
	}

	// count coerced from float64 to string
	if got, ok := resp.ToolCall.Arguments["count"].(string); !ok || got != "5" {
		t.Errorf("count = %v (%T), want string %q", resp.ToolCall.Arguments["count"], resp.ToolCall.Arguments["count"], "5")
	}
}

// --- E2E-026: Lazy Validation -- Client Creation Succeeds Without API Key ---

func TestLazyValidation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	client, err := NewClient("openai:gpt-4o")
	if err != nil {
		t.Fatalf("NewClient should succeed without API key, got error: %v", err)
	}
	if _, ok := client.(*OpenAICompatibleClient); !ok {
		t.Errorf("expected *OpenAICompatibleClient, got %T", client)
	}
}
