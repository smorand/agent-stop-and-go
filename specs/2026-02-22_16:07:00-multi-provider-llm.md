# Multi-Provider LLM Support -- Specification Document

> Generated on: 2026-02-22
> Version: 1.0
> Status: Draft

## 1. Executive Summary

Extend the agentic-platform's LLM provider system from 2 providers (Gemini + Claude) to 6 providers by adding OpenAI, Mistral, Ollama, and OpenRouter. All four new providers use the OpenAI-compatible Chat Completions format and share a single `OpenAICompatibleClient` implementation. Each provider differs only in base URL, API key environment variable, and optional custom headers. Existing `GeminiClient` and `ClaudeClient` remain unchanged.

## 2. Scope

### 2.1 In Scope

- New `OpenAICompatibleClient` struct implementing `llm.Client` interface, shared by 4 providers
- Provider configuration registry (table of provider configs: base URL, API key env var, prefix, custom headers)
- Prefix-based model routing in `NewClient()` factory
- MCP tool schema conversion to OpenAI function calling format
- OpenAI Chat Completions request building and response parsing
- HTTP error handling with provider name, status code, and error message
- `OLLAMA_BASE_URL` environment variable for configurable Ollama endpoint
- OpenRouter hardcoded `HTTP-Referer` and `X-Title` headers
- Conditional `Authorization` header (omitted when API key is empty)
- Lazy API key validation (fail on first use, not at creation)
- Unit tests with mocked HTTP servers
- Documentation updates (CLAUDE.md, .agent_docs/, docs/)

### 2.2 Out of Scope (Non-Goals)

- Streaming responses (current clients do not stream)
- Changes to the `llm.Client` interface
- New config YAML fields (existing `model` field is sufficient)
- Provider-specific features beyond Chat Completions + tool use (vision, embeddings, etc.)
- Refactoring the message conversion layer to use native OpenAI `tool` role messages
- Retry logic or exponential backoff for rate-limited responses
- E2E tests against real provider APIs
- Configurable base URLs for providers other than Ollama
- Ollama server lifecycle management (starting/stopping the Ollama process)

## 3. User Personas & Actors

**Platform Operator:** Configures agent YAML files, sets environment variables, deploys agents. Chooses which LLM provider and model to use per agent or per node in an orchestrated pipeline.

**End User:** Sends messages to the agent via REST API or web UI. Unaware of which LLM provider is used behind the scenes. Expects consistent behavior regardless of provider.

## 4. Authentication & Environment Variables

Each LLM provider requires its own authentication method. All API keys are sourced exclusively from environment variables — never from config files.

| Provider | Env Variable | Required | Description |
|----------|-------------|----------|-------------|
| Gemini | `GEMINI_API_KEY` | When using Gemini models (default provider) | API key from Google AI Studio |
| Claude/Anthropic | `ANTHROPIC_API_KEY` | When using `claude-*` models | API key from Anthropic Console |
| OpenAI | `OPENAI_API_KEY` | When using `openai-*` models | API key from OpenAI Platform |
| Mistral | `MISTRAL_API_KEY` | When using `mistral-*` models | API key from Mistral AI Console |
| OpenRouter | `OPENROUTER_API_KEY` | When using `openrouter-*` models | API key from OpenRouter |
| Ollama | *(none)* | — | Local inference, no API key needed. Runs on `localhost:11434` by default |

**Additional configuration:**

| Env Variable | Default | Description |
|-------------|---------|-------------|
| `OLLAMA_BASE_URL` | `http://localhost:11434/v1` | Override Ollama endpoint when running on a different host/port |

**Key behaviors:**
- Validation is **lazy**: missing keys do not cause errors at startup. The error occurs on first API call to that provider (HTTP 401).
- Only providers actually used in the agent config need their env var set.
- Ollama skips the `Authorization` header entirely (no dummy key needed).

## 5. Usage Scenarios

### SC-001: OpenAI Model Configuration and Usage

**Actor:** Platform Operator / End User
**Preconditions:** `OPENAI_API_KEY` env var is set. Agent YAML config has `model: openai-gpt-4o`.
**Flow:**
1. Operator starts the agent.
2. Agent loads config, sees `openai-gpt-4o` model.
3. On first message, `NewClient("openai-gpt-4o")` is called. Matches `openai-*` prefix, creates `OpenAICompatibleClient` with base URL `https://api.openai.com/v1`, strips prefix to get model `gpt-4o`.
4. User sends a message via REST API.
5. Client builds OpenAI Chat Completions request: system prompt as `system` role message, conversation messages with `user`/`assistant` roles, MCP tools converted to OpenAI function calling format.
6. Client sends POST to `https://api.openai.com/v1/chat/completions` with `Authorization: Bearer <key>` and `Content-Type: application/json` headers.
7. Client parses response: extracts text content or tool call. For tool calls, parses `function.arguments` JSON string into `map[string]any`.
8. Client runs `CoerceToolCallArgs` on any tool call.
9. Client returns `llm.Response` to the agent.
**Postconditions:** Agent has an `llm.Response` (text or tool call) identical in structure to Gemini/Claude responses.
**Exceptions:**
- [EXC-001a]: `OPENAI_API_KEY` not set -- client creation succeeds (lazy validation), first API call returns 401 error.
- [EXC-001b]: API returns rate limit (429) -- error returned with provider name, status code, and message. No retry.
- [EXC-001c]: Invalid model name (404) -- error returned with "openai API error (404): ..." message.
- [EXC-001d]: Malformed JSON in `function.arguments` -- error returned: "failed to parse tool arguments: ...".

### SC-002: Mistral Model Configuration and Usage

**Actor:** Platform Operator / End User
**Preconditions:** `MISTRAL_API_KEY` env var is set. Agent YAML config has `model: mistral-large-latest`.
**Flow:**
1. Same as SC-001 steps 1-2, with `mistral-large-latest` model.
2. `NewClient("mistral-large-latest")` matches `mistral-*` prefix, creates `OpenAICompatibleClient` with base URL `https://api.mistral.ai/v1`, strips prefix to get model `large-latest`.
3. Steps 4-9 identical to SC-001, targeting Mistral API.
**Postconditions:** Same as SC-001.
**Exceptions:** Same pattern as SC-001 (replace "openai" with "mistral" in error messages).

### SC-003: Ollama Model Configuration and Usage

**Actor:** Platform Operator / End User
**Preconditions:** Ollama server running locally (or at custom URL). Agent YAML config has `model: ollama-llama3`. Optionally, `OLLAMA_BASE_URL` env var is set.
**Flow:**
1. Same as SC-001 steps 1-2, with `ollama-llama3` model.
2. `NewClient("ollama-llama3")` matches `ollama-*` prefix, creates `OpenAICompatibleClient` with base URL from `OLLAMA_BASE_URL` env var (default `http://localhost:11434/v1`), strips prefix to get model `llama3`. No API key required.
3. Steps 4-9 identical to SC-001, but the `Authorization` header is omitted (no API key).
4. If the Ollama model does not support tool calling, tools are sent in the request but the model returns text only. The agent handles this gracefully (no tool call triggered).
**Postconditions:** Same as SC-001.
**Exceptions:**
- [EXC-003a]: Ollama server unreachable -- `http.Client.Do()` returns connection error: "failed to send request: ...".
- [EXC-003b]: Model not available in Ollama -- API returns 404 error.
**Cross-scenario notes:** Ollama is commonly used for local development and testing, then swapped for a cloud provider in production. The same config YAML structure works for both.

### SC-004: OpenRouter Model Configuration and Usage

**Actor:** Platform Operator / End User
**Preconditions:** `OPENROUTER_API_KEY` env var is set. Agent YAML config has `model: openrouter-anthropic/claude-3-opus`.
**Flow:**
1. Same as SC-001 steps 1-2, with `openrouter-anthropic/claude-3-opus` model.
2. `NewClient("openrouter-anthropic/claude-3-opus")` matches `openrouter-*` prefix, creates `OpenAICompatibleClient` with base URL `https://openrouter.ai/api/v1`, strips prefix to get model `anthropic/claude-3-opus`.
3. Steps 4-9 identical to SC-001, but the HTTP request includes additional headers: `HTTP-Referer: https://github.com/agentic-platform` and `X-Title: Agent Stop and Go`.
**Postconditions:** Same as SC-001.
**Exceptions:** Same pattern as SC-001 (replace "openai" with "openrouter" in error messages). OpenRouter may return errors from downstream providers; the error body still follows the OpenAI format.

### SC-005: Orchestrated Pipeline with Mixed Providers

**Actor:** Platform Operator / End User
**Preconditions:** Multiple provider env vars set (e.g., `OLLAMA_BASE_URL` and `OPENAI_API_KEY`). Agent tree config uses different models per node.
**Flow:**
1. Operator configures an agent tree with multiple models:
   ```yaml
   agent:
     type: sequential
     agents:
       - name: analyzer
         type: llm
         model: ollama-llama3
         output_key: analysis
         prompt: "Analyze: ..."
       - name: decider
         type: llm
         model: openai-gpt-4o
         prompt: "Based on {analysis}, decide..."
   ```
2. Agent starts, loads config.
3. User sends a message. Pipeline executes sequentially.
4. Node "analyzer" triggers `getLLMClient("ollama-llama3")`, which calls `NewClient()`, creates and caches an `OpenAICompatibleClient` with Ollama config.
5. Ollama returns analysis text, stored in session state under `analysis` key.
6. Node "decider" triggers `getLLMClient("openai-gpt-4o")`, creates and caches an `OpenAICompatibleClient` with OpenAI config.
7. OpenAI returns decision text.
**Postconditions:** Pipeline completes with both providers used. Each client is cached and reused for subsequent calls with the same model.
**Exceptions:**
- [EXC-005a]: First node succeeds but second node fails (missing API key, API error) -- pipeline fails at the second node with a clear error message identifying the provider and issue.

### SC-006: Backward Compatibility (Existing Gemini/Claude)

**Actor:** Existing Platform Users
**Preconditions:** Existing agent configs with `model: gemini-2.5-flash` or `model: claude-sonnet-4-20250514`.
**Flow:**
1. Operator starts agent with existing config (no changes).
2. `NewClient("gemini-2.5-flash")` does not match any new prefix, falls to default, returns `GeminiClient` (unchanged behavior).
3. `NewClient("claude-sonnet-4-20250514")` matches `claude-*` prefix, returns `ClaudeClient` (unchanged behavior).
4. All existing functionality works identically.
**Postconditions:** Zero behavioral changes for existing users.
**Exceptions:** None -- identical to current behavior.

### SC-007: Tool Call Argument Coercion Across Providers

**Actor:** System
**Preconditions:** Any provider returns a tool call where argument types do not match the schema (e.g., LLM returns `42` as float64 for a string-typed parameter).
**Flow:**
1. `OpenAICompatibleClient` receives a tool call response from the API.
2. Client parses `function.arguments` JSON string into `map[string]any`. Go's `json.Unmarshal` produces `float64` for JSON numbers, `bool` for booleans, `string` for strings.
3. Client calls `CoerceToolCallArgs(response.ToolCall, tools)`.
4. `CoerceToolCallArgs` iterates the tool's schema properties and coerces mismatched types (e.g., `float64` to `string`, `bool` to `string`).
5. Client returns the coerced `llm.Response`.
**Postconditions:** Tool call arguments match the schema types, consistent across all 6 providers.
**Exceptions:** None -- `CoerceToolCallArgs` handles unknown tools and nil tool calls gracefully (existing behavior).

## 6. Functional Requirements

### FR-001: OpenAICompatibleClient Struct

- **Description:** The system must provide a single `OpenAICompatibleClient` struct that implements the `llm.Client` interface. The struct must be parameterized by a `providerConfig` containing: base URL, API key env var name, model name prefix, display name for errors, and optional custom HTTP headers.
- **Inputs:** `providerConfig` (base URL, API key env var, prefix, display name, headers), model name (with prefix already stripped)
- **Outputs:** A valid `llm.Client` implementation
- **Business Rules:** One struct serves all 4 new providers. No provider-specific subclasses or interfaces.
- **Priority:** Must-have

### FR-002: MCP Tool Schema to OpenAI Function Format Conversion

- **Description:** The system must convert MCP `Tool` objects (with `InputSchema` containing `Properties` and `Required` fields) to OpenAI function calling format. Each tool must be represented as `{"type": "function", "function": {"name": "...", "description": "...", "parameters": {...}}}` where `parameters` is a JSON Schema object.
- **Inputs:** `[]mcp.Tool` from the agent's MCP client
- **Outputs:** OpenAI-format `tools` array in the request body
- **Business Rules:** Tools with no input schema properties must produce an empty parameters object or omit the field. The `parameters` field must include `type: "object"`, `properties`, and `required` matching the MCP schema.
- **Priority:** Must-have

### FR-003: Chat Completions Request Building

- **Description:** The system must build OpenAI Chat Completions API requests with: `model` field (prefix stripped), `messages` array (`system`, `user`, `assistant` roles), and `tools` array (if tools are provided).
- **Inputs:** System prompt (string), messages (`[]llm.Message` with `user`/`model` roles), tools (`[]mcp.Tool`)
- **Outputs:** HTTP POST request to `<base_url>/chat/completions` with JSON body
- **Business Rules:** System prompt must be sent as a `system` role message prepended to the messages array. If system prompt is empty, no system message is prepended. `model` role in incoming messages must be converted to `assistant` role. The request must use the 60-second `httpClientTimeout` constant.
- **Priority:** Must-have

### FR-004: Chat Completions Response Parsing

- **Description:** The system must parse OpenAI Chat Completions responses, extracting either text content or tool calls from `choices[0].message`.
- **Inputs:** HTTP response body (JSON)
- **Outputs:** `llm.Response` with `Text` and/or `ToolCall` populated
- **Business Rules:** Tool call arguments (`function.arguments`) are returned as a JSON string by the API and must be parsed via `json.Unmarshal` into `map[string]any`. If both text and tool calls are present, the tool call takes precedence (consistent with Gemini/Claude behavior). Empty `choices` array must return an error: `"no choices in response"`. Malformed JSON in `function.arguments` must return an error: `"failed to parse tool arguments: ..."`.
- **Priority:** Must-have

### FR-005: Provider Configuration Registry

- **Description:** The system must define a package-level registry (map or table) of provider configurations. Each entry contains: display name, base URL, API key env var name, model prefix, and optional custom headers.
- **Inputs:** N/A (compile-time data)
- **Outputs:** Provider config lookup by prefix
- **Business Rules:** The registry must contain entries for: OpenAI (`openai`, `https://api.openai.com/v1`, `OPENAI_API_KEY`), Mistral (`mistral`, `https://api.mistral.ai/v1`, `MISTRAL_API_KEY`), Ollama (`ollama`, default `http://localhost:11434/v1`, no required key), OpenRouter (`openrouter`, `https://openrouter.ai/api/v1`, `OPENROUTER_API_KEY`, custom headers). Adding a new OpenAI-compatible provider must require only adding a new entry to this registry.
- **Priority:** Must-have

### FR-006: Extended NewClient() Factory with Prefix Routing

- **Description:** The `NewClient()` factory function must be extended to route model names to the correct client based on prefix matching.
- **Inputs:** Model name string (e.g., `openai-gpt-4o`)
- **Outputs:** Appropriate `llm.Client` implementation
- **Business Rules:** Routing order: `claude-*` returns `ClaudeClient`, `openai-*` returns `OpenAICompatibleClient` (OpenAI config), `mistral-*` returns `OpenAICompatibleClient` (Mistral config), `ollama-*` returns `OpenAICompatibleClient` (Ollama config), `openrouter-*` returns `OpenAICompatibleClient` (OpenRouter config), default (no match) returns `GeminiClient`. Each prefix is checked with `strings.HasPrefix`.
- **Priority:** Must-have

### FR-007: Model Name Prefix Stripping

- **Description:** When creating an `OpenAICompatibleClient`, the provider prefix must be stripped from the model name before storing it. The stripped model name is sent to the API.
- **Inputs:** Full model name (e.g., `openai-gpt-4o`, `openrouter-anthropic/claude-3-opus`)
- **Outputs:** Stripped model name (e.g., `gpt-4o`, `anthropic/claude-3-opus`)
- **Business Rules:** The prefix includes the trailing hyphen (e.g., `openai-` is stripped, not just `openai`). The stripping must handle model names containing slashes (OpenRouter uses `provider/model` format).
- **Priority:** Must-have

### FR-008: HTTP Error Handling

- **Description:** The system must parse non-2xx HTTP responses and return descriptive Go errors.
- **Inputs:** HTTP response with non-2xx status code and JSON error body
- **Outputs:** Go error with format: `"<provider> API error (<status>): <message>"`
- **Business Rules:** The error body follows OpenAI format: `{"error": {"message": "...", "type": "...", "code": "..."}}`. The provider display name (e.g., "openai", "mistral", "ollama", "openrouter") must be included in the error message. If the error body cannot be parsed, fall back to the HTTP status text. No retry logic.
- **Priority:** Must-have

### FR-009: CoerceToolCallArgs Compatibility

- **Description:** The existing `CoerceToolCallArgs` function must work unchanged with tool calls returned by the `OpenAICompatibleClient`.
- **Inputs:** `ToolCall` with arguments parsed from OpenAI-format JSON string
- **Outputs:** Coerced `ToolCall` arguments matching schema types
- **Business Rules:** After `json.Unmarshal` of the `function.arguments` JSON string, the resulting `map[string]any` uses the same Go types as Gemini/Claude responses (`float64` for numbers, `bool` for booleans, `string` for strings). No changes to `CoerceToolCallArgs` are required.
- **Priority:** Must-have

### FR-010: Lazy API Key Validation

- **Description:** Client creation must succeed even when the required API key env var is not set. The error must occur only when the client is first used (first `GenerateWithTools` call).
- **Inputs:** Model name with provider prefix
- **Outputs:** `OpenAICompatibleClient` instance (no error at creation)
- **Business Rules:** `NewClient()` must not check whether the API key env var is set. The API key is read from the env var at request time. If the key is empty and the provider requires one, the API will return 401, which is handled by FR-008.
- **Priority:** Must-have

### FR-011: Ollama Base URL Configuration

- **Description:** The Ollama provider must support a configurable base URL via the `OLLAMA_BASE_URL` environment variable.
- **Inputs:** `OLLAMA_BASE_URL` env var (optional)
- **Outputs:** Base URL used for Ollama API requests
- **Business Rules:** If `OLLAMA_BASE_URL` is set, use its value as the base URL. If not set, default to `http://localhost:11434/v1`. The env var is read at client creation time.
- **Priority:** Must-have

### FR-012: OpenRouter Custom Headers

- **Description:** The OpenRouter provider must include hardcoded custom HTTP headers in every request.
- **Inputs:** N/A (compile-time constants)
- **Outputs:** HTTP request headers
- **Business Rules:** Every request to OpenRouter must include: `HTTP-Referer: https://github.com/agentic-platform` and `X-Title: Agent Stop and Go`. These values are hardcoded constants, not configurable.
- **Priority:** Must-have

### FR-013: Conditional Authorization Header

- **Description:** The `Authorization` header must be omitted when the API key is empty.
- **Inputs:** API key value (from env var, may be empty string)
- **Outputs:** HTTP request with or without `Authorization` header
- **Business Rules:** If the API key is a non-empty string, include `Authorization: Bearer <key>` header. If the API key is empty (env var not set or empty), do not include the `Authorization` header at all. This enables Ollama usage without an API key.
- **Priority:** Must-have

### FR-014: Documentation Updates

- **Description:** Project documentation must be updated to reflect the new providers.
- **Inputs:** N/A
- **Outputs:** Updated documentation files
- **Business Rules:** The following must be updated:
  - `CLAUDE.md`: Add new env vars (`OPENAI_API_KEY`, `MISTRAL_API_KEY`, `OLLAMA_BASE_URL`, `OPENROUTER_API_KEY`) to the Environment Variables section. Update the LLM section to list all 6 providers and their prefixes. Update the Development Notes or Key Concepts with the routing table.
  - `.agent_docs/`: Create or update a file documenting the LLM provider architecture, provider config registry, and how to add new providers.
  - `docs/`: Update relevant docs (overview.md, functionalities.md) with multi-provider information.
- **Priority:** Must-have

## 7. Non-Functional Requirements

### 7.1 Performance

- All provider clients must use the existing `httpClientTimeout` constant (60 seconds) for HTTP requests.
- No additional latency introduced by provider routing (prefix matching is O(n) with n=5 prefixes, negligible).

### 7.2 Security

- API keys must be sourced exclusively from environment variables, never from config files.
- API keys must not appear in log output or error messages.
- The `Authorization` header must use `Bearer` scheme for all providers that require authentication.

### 7.3 Maintainability

- Adding a new OpenAI-compatible provider must require only adding a new entry to the provider config registry (no new Go file, no new struct).
- The `OpenAICompatibleClient` must contain zero provider-specific branching logic (all differences are in the config data).

### 7.4 Testability

- All new code must have unit tests using `net/http/httptest` mock servers.
- Tests must not require real API keys or network access.
- Tests must cover: prefix routing, request building, response parsing (text and tool calls), error handling (all HTTP error codes), header behavior (Authorization, custom headers), and edge cases (empty schemas, nested arguments, system prompt handling).

### 7.5 Backward Compatibility

- Existing `GeminiClient` and `ClaudeClient` Go source files must not be modified.
- Existing configs with `gemini-*` (default) or `claude-*` models must produce identical behavior.
- The `llm.Client` interface must not change.
- The `CoerceToolCallArgs` function must not change.

## 8. Data Model

No new data entities or persistent storage. The change is limited to the `internal/llm/` package.

**New types (in `internal/llm/`):**

| Type | Description |
|------|-------------|
| `providerConfig` | Struct: `name` (string), `baseURL` (string), `apiKeyEnv` (string), `prefix` (string), `headers` (map[string]string) |
| `OpenAICompatibleClient` | Struct: `model` (string, prefix-stripped), `config` (providerConfig), `apiKey` (string), `client` (*http.Client) |
| `openaiRequest` | OpenAI Chat Completions request body |
| `openaiMessage` | Message in the request/response (role + content) |
| `openaiTool` | Tool definition (type: "function", function: {name, description, parameters}) |
| `openaiResponse` | Chat Completions response body |
| `openaiChoice` | Single choice in the response |
| `openaiToolCall` | Tool call in the response (id, type, function: {name, arguments}) |
| `openaiError` | Error response body |

**Package-level registry:**

```go
var providers = map[string]providerConfig{
    "openai":     {name: "openai",     baseURL: "https://api.openai.com/v1",      apiKeyEnv: "OPENAI_API_KEY",     prefix: "openai-"},
    "mistral":    {name: "mistral",    baseURL: "https://api.mistral.ai/v1",      apiKeyEnv: "MISTRAL_API_KEY",    prefix: "mistral-"},
    "ollama":     {name: "ollama",     baseURL: "http://localhost:11434/v1",       apiKeyEnv: "",                   prefix: "ollama-"},
    "openrouter": {name: "openrouter", baseURL: "https://openrouter.ai/api/v1",   apiKeyEnv: "OPENROUTER_API_KEY", prefix: "openrouter-", headers: ...},
}
```

## 9. Documentation Requirements

All documentation listed below must be created and maintained as part of this project.

### 9.1 CLAUDE.md

Update the existing `CLAUDE.md` with:
- New environment variables in the Environment Variables section: `OPENAI_API_KEY`, `MISTRAL_API_KEY`, `OLLAMA_BASE_URL`, `OPENROUTER_API_KEY`
- Model prefix routing table in Key Concepts or a new LLM Providers section
- Updated project structure if new files are added to `internal/llm/`

### 9.2 .agent_docs/

Create or update `.agent_docs/llm-providers.md` with:
- Provider architecture overview (3 client implementations)
- Provider config registry documentation
- How to add a new OpenAI-compatible provider (step-by-step)
- Environment variable reference for all 6 providers
- Model prefix routing rules

### 9.3 docs/

Update `docs/overview.md` and `docs/functionalities.md` with:
- List of supported LLM providers
- Configuration examples for each provider
- Mixed-provider pipeline examples

## 10. Traceability Matrix

| Scenario | Functional Req | E2E Tests (Happy) | E2E Tests (Failure) | E2E Tests (Edge) |
|----------|---------------|-------------------|---------------------|-------------------|
| SC-001 | FR-001, FR-002, FR-003, FR-004, FR-006, FR-007, FR-008, FR-010 | E2E-001, E2E-005, E2E-006, E2E-008 | E2E-014, E2E-015, E2E-016, E2E-017, E2E-019, E2E-020 | E2E-021, E2E-022, E2E-023, E2E-025, E2E-026 |
| SC-002 | FR-001, FR-005 | E2E-002 | (covered by SC-001 error tests -- same client) | (covered by SC-001 edge tests) |
| SC-003 | FR-001, FR-005, FR-011, FR-013 | E2E-003, E2E-010, E2E-012 | E2E-018 | (covered by SC-001 edge tests) |
| SC-004 | FR-001, FR-005, FR-012 | E2E-004, E2E-011 | (covered by SC-001 error tests -- same client) | (covered by SC-001 edge tests) |
| SC-005 | FR-005, FR-006 | E2E-013 | (EXC-005a covered by E2E-014/E2E-018 patterns) | (covered by SC-001 edge tests) |
| SC-006 | FR-006 | E2E-009 | (no new failure modes) | E2E-024 |
| SC-007 | FR-009 | E2E-007 | (no new failure modes) | E2E-025 |

**Coverage verification:**
- All 7 scenarios have at least one happy-path test.
- All 7 scenarios have failure coverage (SC-002/SC-004/SC-005 share the same `OpenAICompatibleClient` error handling validated in SC-001 tests).
- All 7 scenarios have edge case coverage (SC-002/SC-003/SC-004/SC-005 share the same client logic validated in SC-001 edge tests).
- All 14 functional requirements appear in at least one test (see Section 11.1).
- No orphan tests, requirements, or scenarios.

## 11. End-to-End Test Suite

All tests must be implemented in `internal/llm/` as Go unit tests using `net/http/httptest` mock servers. Tests must not require real API keys or network access.

### 11.1 Test Summary

| Test ID | Category | Scenario | FR refs | Priority |
|---------|----------|----------|---------|----------|
| E2E-001 | Core Journey | SC-001 | FR-001, FR-003, FR-004 | Critical |
| E2E-002 | Core Journey | SC-002 | FR-001, FR-005 | Critical |
| E2E-003 | Core Journey | SC-003 | FR-001, FR-005, FR-011, FR-013 | Critical |
| E2E-004 | Core Journey | SC-004 | FR-001, FR-005, FR-012 | Critical |
| E2E-005 | Feature | SC-001 | FR-002 | Critical |
| E2E-006 | Feature | SC-001 | FR-004 | Critical |
| E2E-007 | Feature | SC-007 | FR-009 | Critical |
| E2E-008 | Feature | SC-001 | FR-006, FR-007 | Critical |
| E2E-009 | Feature | SC-006 | FR-006 | Critical |
| E2E-010 | Feature | SC-003 | FR-011 | High |
| E2E-011 | Feature | SC-004 | FR-012 | High |
| E2E-012 | Feature | SC-003 | FR-013 | High |
| E2E-013 | Feature | SC-005 | FR-005, FR-006 | High |
| E2E-014 | Error | SC-001 | FR-008 | High |
| E2E-015 | Error | SC-001 | FR-008 | High |
| E2E-016 | Error | SC-001 | FR-008 | High |
| E2E-017 | Error | SC-001 | FR-008 | High |
| E2E-018 | Error | SC-003 | FR-008 | High |
| E2E-019 | Error | SC-001 | FR-004 | High |
| E2E-020 | Error | SC-001 | FR-004 | Medium |
| E2E-021 | Edge | SC-001 | FR-004 | Medium |
| E2E-022 | Edge | SC-001 | FR-003 | Medium |
| E2E-023 | Edge | SC-001 | FR-002 | Medium |
| E2E-024 | Edge | SC-006 | FR-006 | Medium |
| E2E-025 | Edge | SC-007 | FR-009 | Medium |
| E2E-026 | Edge | SC-001 | FR-010 | Medium |

### 11.2 Test Specifications

#### E2E-001: OpenAI Text Response
- **Category:** Core Journey
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-001, FR-003, FR-004
- **Preconditions:** Mock HTTP server returning OpenAI chat completions text response
- **Steps:**
  - Given an `OpenAICompatibleClient` configured with OpenAI provider config pointing to the mock server
  - When `GenerateWithTools` is called with a system prompt, one user message, and no tools
  - Then the response `Text` field contains the expected text from the mock response
  - And the response `ToolCall` is nil
  - And the mock server received a POST to `/chat/completions`
  - And the request body has `model: "gpt-4o"` (prefix stripped)
  - And the request body `messages[0]` has `role: "system"` with the system prompt content
  - And the request body `messages[1]` has `role: "user"` with the user message content
  - And the request has `Authorization: Bearer <test-key>` header
  - And the request has `Content-Type: application/json` header
- **Priority:** Critical

#### E2E-002: Mistral Text Response
- **Category:** Core Journey
- **Scenario:** SC-002 -- Mistral Model Configuration and Usage
- **Requirements:** FR-001, FR-005
- **Preconditions:** Mock HTTP server returning OpenAI-format text response
- **Steps:**
  - Given an `OpenAICompatibleClient` configured with Mistral provider config pointing to the mock server
  - When `GenerateWithTools` is called with a system prompt and user message
  - Then the response contains the expected text
  - And the request has `Authorization: Bearer <mistral-test-key>` header
  - And the request body has `model: "large-latest"` (prefix `mistral-` stripped from `mistral-large-latest`)
- **Priority:** Critical

#### E2E-003: Ollama Text Response (No API Key, Custom Base URL)
- **Category:** Core Journey
- **Scenario:** SC-003 -- Ollama Model Configuration and Usage
- **Requirements:** FR-001, FR-005, FR-011, FR-013
- **Preconditions:** Mock HTTP server acting as Ollama endpoint
- **Steps:**
  - Given an `OpenAICompatibleClient` configured with Ollama provider config, base URL pointing to the mock server
  - When `GenerateWithTools` is called with a user message
  - Then the response contains the expected text
  - And the request does NOT contain an `Authorization` header
  - And the request body has `model: "llama3"` (prefix `ollama-` stripped from `ollama-llama3`)
- **Priority:** Critical

#### E2E-004: OpenRouter Text Response (Custom Headers)
- **Category:** Core Journey
- **Scenario:** SC-004 -- OpenRouter Model Configuration and Usage
- **Requirements:** FR-001, FR-005, FR-012
- **Preconditions:** Mock HTTP server returning OpenAI-format text response
- **Steps:**
  - Given an `OpenAICompatibleClient` configured with OpenRouter provider config pointing to the mock server
  - When `GenerateWithTools` is called with a user message
  - Then the response contains the expected text
  - And the request has `HTTP-Referer: https://github.com/agentic-platform` header
  - And the request has `X-Title: Agent Stop and Go` header
  - And the request has `Authorization: Bearer <openrouter-test-key>` header
  - And the request body has `model: "anthropic/claude-3-opus"` (prefix `openrouter-` stripped)
- **Priority:** Critical

#### E2E-005: MCP Tool Schema to OpenAI Function Format Conversion
- **Category:** Feature
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-002
- **Preconditions:** Mock HTTP server that captures the request body and returns a text response
- **Steps:**
  - Given an `OpenAICompatibleClient` with OpenAI config
  - And MCP tools: `[{Name: "resources_add", Description: "Add a resource", InputSchema: {Type: "object", Properties: {"name": {Type: "string", Description: "Resource name"}, "value": {Type: "string"}}, Required: ["name", "value"]}}]`
  - When `GenerateWithTools` is called with these tools
  - Then the request body `tools[0].type` is `"function"`
  - And `tools[0].function.name` is `"resources_add"`
  - And `tools[0].function.description` is `"Add a resource"`
  - And `tools[0].function.parameters.type` is `"object"`
  - And `tools[0].function.parameters.properties.name.type` is `"string"`
  - And `tools[0].function.parameters.properties.name.description` is `"Resource name"`
  - And `tools[0].function.parameters.properties.value.type` is `"string"`
  - And `tools[0].function.parameters.required` is `["name", "value"]`
- **Priority:** Critical

#### E2E-006: Tool Call Response Parsing (JSON String Arguments)
- **Category:** Feature
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-004
- **Preconditions:** Mock HTTP server returning response with tool call: `choices[0].message.tool_calls[0] = {id: "call_1", type: "function", function: {name: "resources_add", arguments: "{\"name\":\"test\",\"value\":\"123\"}"}}`
- **Steps:**
  - Given an `OpenAICompatibleClient` with OpenAI config and matching MCP tools
  - When `GenerateWithTools` is called
  - Then the response `ToolCall.Name` is `"resources_add"`
  - And `ToolCall.Arguments` is `{"name": "test", "value": "123"}` (parsed from JSON string)
  - And the response `Text` is empty
- **Priority:** Critical

#### E2E-007: CoerceToolCallArgs with OpenAI-Parsed Arguments
- **Category:** Feature
- **Scenario:** SC-007 -- Tool Call Argument Coercion Across Providers
- **Requirements:** FR-009
- **Preconditions:** Mock HTTP server returning tool call with `function.arguments: "{\"name\":\"server\",\"value\":42}"` where the MCP tool schema declares `value` as type `string`
- **Steps:**
  - Given an `OpenAICompatibleClient` with tools where `value` is type `string`
  - When `GenerateWithTools` is called and the response contains a tool call with `value: 42` (float64 after JSON parsing)
  - Then `CoerceToolCallArgs` converts `value` from float64 `42` to string `"42"`
  - And `ToolCall.Arguments["value"]` is the string `"42"`
- **Priority:** Critical

#### E2E-008: NewClient Prefix Routing for All Providers
- **Category:** Feature
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-006, FR-007
- **Preconditions:** Required env vars set (or test uses env var stubs)
- **Steps:**
  - Given the `NewClient()` factory function with env vars set for all providers
  - When called with `"openai-gpt-4o"`, it returns an `*OpenAICompatibleClient`
  - And when called with `"mistral-large-latest"`, it returns an `*OpenAICompatibleClient`
  - And when called with `"ollama-llama3"`, it returns an `*OpenAICompatibleClient`
  - And when called with `"openrouter-anthropic/claude-3-opus"`, it returns an `*OpenAICompatibleClient`
  - And when called with `"claude-sonnet-4-20250514"`, it returns a `*ClaudeClient`
  - And when called with `"gemini-2.5-flash"`, it returns a `*GeminiClient`
- **Priority:** Critical

#### E2E-009: Backward Compatibility -- Gemini and Claude Routing Unchanged
- **Category:** Feature
- **Scenario:** SC-006 -- Backward Compatibility
- **Requirements:** FR-006
- **Preconditions:** `GEMINI_API_KEY` and `ANTHROPIC_API_KEY` env vars set
- **Steps:**
  - Given the `NewClient()` factory function
  - When called with `"gemini-2.5-flash"` (no matching new prefix), it returns a `*GeminiClient`
  - And when called with `"claude-sonnet-4-20250514"`, it returns a `*ClaudeClient`
  - And neither returns an `*OpenAICompatibleClient`
- **Priority:** Critical

#### E2E-010: Ollama Base URL from Environment Variable
- **Category:** Feature
- **Scenario:** SC-003 -- Ollama Model Configuration and Usage
- **Requirements:** FR-011
- **Preconditions:** Test sets/unsets `OLLAMA_BASE_URL` env var
- **Steps:**
  - Given `OLLAMA_BASE_URL` set to `http://custom-host:9999/v1`
  - When `NewClient("ollama-llama3")` is called and the client is inspected
  - Then the client's base URL is `http://custom-host:9999/v1`
  - And given `OLLAMA_BASE_URL` is not set (empty)
  - When `NewClient("ollama-llama3")` is called
  - Then the client's base URL is `http://localhost:11434/v1`
- **Priority:** High

#### E2E-011: OpenRouter Custom Headers Present in Request
- **Category:** Feature
- **Scenario:** SC-004 -- OpenRouter Model Configuration and Usage
- **Requirements:** FR-012
- **Preconditions:** Mock HTTP server capturing request headers
- **Steps:**
  - Given an `OpenAICompatibleClient` with OpenRouter config pointing to the mock server
  - When `GenerateWithTools` is called
  - Then the HTTP request includes header `HTTP-Referer` with value `https://github.com/agentic-platform`
  - And includes header `X-Title` with value `Agent Stop and Go`
- **Priority:** High

#### E2E-012: Authorization Header Omitted When API Key Is Empty
- **Category:** Feature
- **Scenario:** SC-003 -- Ollama Model Configuration and Usage
- **Requirements:** FR-013
- **Preconditions:** Mock HTTP server capturing request headers, Ollama config with no API key env var
- **Steps:**
  - Given an `OpenAICompatibleClient` with Ollama config (apiKeyEnv is empty string)
  - When `GenerateWithTools` is called targeting the mock server
  - Then the HTTP request does NOT contain an `Authorization` header
- **Priority:** High

#### E2E-013: Mixed Providers -- Different Client Configs
- **Category:** Feature
- **Scenario:** SC-005 -- Orchestrated Pipeline with Mixed Providers
- **Requirements:** FR-005, FR-006
- **Preconditions:** Env vars set for multiple providers
- **Steps:**
  - Given `NewClient("ollama-llama3")` returns client A
  - And `NewClient("openai-gpt-4o")` returns client B
  - When both clients are inspected
  - Then both are `*OpenAICompatibleClient` instances
  - And client A has Ollama base URL and no API key
  - And client B has OpenAI base URL and OpenAI API key
- **Priority:** High

#### E2E-014: API Returns 401 Unauthorized
- **Category:** Error
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-008
- **Preconditions:** Mock HTTP server returning 401 with `{"error": {"message": "Invalid API key", "type": "invalid_request_error"}}`
- **Steps:**
  - Given an `OpenAICompatibleClient` with OpenAI config
  - When `GenerateWithTools` is called
  - Then it returns a non-nil error
  - And the error message contains "openai"
  - And the error message contains "401"
  - And the error message contains "Invalid API key"
- **Priority:** High

#### E2E-015: API Returns 429 Rate Limited
- **Category:** Error
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-008
- **Preconditions:** Mock HTTP server returning 429 with `{"error": {"message": "Rate limit exceeded", "type": "rate_limit_error"}}`
- **Steps:**
  - Given an `OpenAICompatibleClient` with OpenAI config
  - When `GenerateWithTools` is called
  - Then it returns a non-nil error
  - And the error message contains "429"
  - And the error message contains "Rate limit exceeded"
  - And the mock server received exactly 1 request (no retry)
- **Priority:** High

#### E2E-016: API Returns 404 Model Not Found
- **Category:** Error
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-008
- **Preconditions:** Mock HTTP server returning 404 with `{"error": {"message": "Model not found", "type": "invalid_request_error"}}`
- **Steps:**
  - Given an `OpenAICompatibleClient` with OpenAI config
  - When `GenerateWithTools` is called
  - Then it returns a non-nil error
  - And the error message contains "404"
  - And the error message contains "Model not found"
- **Priority:** High

#### E2E-017: API Returns 500 Server Error
- **Category:** Error
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-008
- **Preconditions:** Mock HTTP server returning 500 with `{"error": {"message": "Internal server error", "type": "server_error"}}`
- **Steps:**
  - Given an `OpenAICompatibleClient` with OpenAI config
  - When `GenerateWithTools` is called
  - Then it returns a non-nil error
  - And the error message contains "500"
  - And the error message contains "Internal server error"
- **Priority:** High

#### E2E-018: Ollama Server Unreachable
- **Category:** Error
- **Scenario:** SC-003 -- Ollama Model Configuration and Usage
- **Requirements:** FR-008
- **Preconditions:** No server running on the configured port
- **Steps:**
  - Given an `OpenAICompatibleClient` with Ollama config pointing to `http://localhost:1/v1` (unlikely to be listening)
  - When `GenerateWithTools` is called
  - Then it returns a non-nil error
  - And the error message contains "failed to send request" (connection error from net/http)
- **Priority:** High

#### E2E-019: Malformed JSON in Tool Call Arguments
- **Category:** Error
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-004
- **Preconditions:** Mock HTTP server returning tool call with `function.arguments: "not valid json{"`
- **Steps:**
  - Given an `OpenAICompatibleClient` with tools
  - When `GenerateWithTools` is called
  - Then it returns a non-nil error
  - And the error message contains "failed to parse tool arguments"
- **Priority:** High

#### E2E-020: Empty Choices Array in Response
- **Category:** Error
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-004
- **Preconditions:** Mock HTTP server returning `{"choices": [], "model": "gpt-4o"}`
- **Steps:**
  - Given an `OpenAICompatibleClient`
  - When `GenerateWithTools` is called
  - Then it returns a non-nil error
  - And the error message contains "no choices in response"
- **Priority:** Medium

#### E2E-021: Response with Both Text and Tool Call
- **Category:** Edge
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-004
- **Preconditions:** Mock HTTP server returning response with `choices[0].message.content: "Some text"` AND `choices[0].message.tool_calls[0]: {function: {name: "test", arguments: "{}"}}`
- **Steps:**
  - Given an `OpenAICompatibleClient` with tools
  - When `GenerateWithTools` is called
  - Then the response `ToolCall` is populated (tool call takes precedence)
  - And `ToolCall.Name` is `"test"`
- **Priority:** Medium

#### E2E-022: System Prompt Handling
- **Category:** Edge
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-003
- **Preconditions:** Mock HTTP server capturing request body
- **Steps:**
  - Given an `OpenAICompatibleClient`
  - When `GenerateWithTools` is called with system prompt `"You are helpful"`
  - Then the request `messages[0]` is `{role: "system", content: "You are helpful"}`
  - And `messages[1]` is the first user message
  - And when `GenerateWithTools` is called with an empty system prompt
  - Then the request `messages[0]` is the first user message (no system message prepended)
- **Priority:** Medium

#### E2E-023: Tools with Empty Input Schema
- **Category:** Edge
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-002
- **Preconditions:** Mock HTTP server capturing request body
- **Steps:**
  - Given an MCP tool with `Name: "exit_loop"`, `Description: "Exit the loop"`, and `InputSchema: {Type: "object", Properties: nil, Required: nil}`
  - When `GenerateWithTools` is called with this tool
  - Then the request body includes the tool with `function.name: "exit_loop"` and `function.parameters` is either an empty object `{"type": "object"}` or omitted
- **Priority:** Medium

#### E2E-024: Model with No Matching Prefix Falls to Gemini Default
- **Category:** Edge
- **Scenario:** SC-006 -- Backward Compatibility
- **Requirements:** FR-006
- **Preconditions:** `GEMINI_API_KEY` env var set
- **Steps:**
  - Given `NewClient("some-unknown-model-name")`
  - When the client is created
  - Then it returns a `*GeminiClient` (default fallback)
  - And the model name is `"some-unknown-model-name"` (no stripping)
- **Priority:** Medium

#### E2E-025: Tool Call with Nested/Complex Argument Types
- **Category:** Edge
- **Scenario:** SC-007 -- Tool Call Argument Coercion Across Providers
- **Requirements:** FR-009
- **Preconditions:** Mock HTTP server returning tool call with `function.arguments: "{\"filter\":{\"name\":\"test\"},\"tags\":[\"a\",\"b\"],\"count\":5}"`
- **Steps:**
  - Given an `OpenAICompatibleClient` with a tool schema where `count` is type `string`
  - When `GenerateWithTools` is called and the response is parsed
  - Then `ToolCall.Arguments["filter"]` is `map[string]any{"name": "test"}` (nested object preserved)
  - And `ToolCall.Arguments["tags"]` is `[]any{"a", "b"}` (array preserved)
  - And `ToolCall.Arguments["count"]` is `"5"` (coerced from float64 to string by `CoerceToolCallArgs`)
- **Priority:** Medium

#### E2E-026: Lazy Validation -- Client Creation Succeeds Without API Key
- **Category:** Edge
- **Scenario:** SC-001 -- OpenAI Model Configuration and Usage
- **Requirements:** FR-010
- **Preconditions:** `OPENAI_API_KEY` env var is NOT set
- **Steps:**
  - Given `OPENAI_API_KEY` is unset/empty
  - When `NewClient("openai-gpt-4o")` is called
  - Then no error is returned (client creation succeeds)
  - And the returned client is a valid `*OpenAICompatibleClient`
- **Priority:** Medium

## 12. Open Questions & TBDs

None. All questions were resolved during the discovery interview.

## 13. Glossary

| Term | Definition |
|------|-----------|
| Chat Completions | The OpenAI API endpoint format (`/chat/completions`) for generating conversational responses, used by OpenAI, Mistral, Ollama, and OpenRouter |
| Function Calling | The mechanism by which LLMs can request execution of external tools/functions, returning structured arguments |
| MCP | Model Context Protocol -- the tool protocol used by the agentic-platform for agent-to-tool communication |
| Provider Config | A data structure defining how to communicate with a specific LLM provider (base URL, API key, headers) |
| Prefix Routing | The pattern of using a model name prefix (e.g., `openai-`) to determine which LLM client to use |
| CoerceToolCallArgs | An existing function that fixes type mismatches in tool call arguments (e.g., float64 to string) |
| Lazy Validation | Deferring validation (e.g., API key presence) until the resource is actually used, rather than at creation time |
| OpenRouter | A unified API gateway that provides access to hundreds of LLM models from various providers through a single OpenAI-compatible endpoint |
