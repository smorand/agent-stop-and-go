# LLM Provider Architecture

## Overview

The platform supports 6 LLM providers through 3 client implementations:

| Client | Providers | File |
|--------|-----------|------|
| `GeminiClient` | Google Gemini (default) | `internal/llm/gemini.go` |
| `ClaudeClient` | Anthropic Claude | `internal/llm/claude.go` |
| `OpenAICompatibleClient` | OpenAI, Mistral, Ollama, OpenRouter | `internal/llm/openai.go` |

All clients implement the `llm.Client` interface:

```go
type Client interface {
    GenerateWithTools(ctx context.Context, systemPrompt string, messages []Message, tools []mcp.Tool) (*Response, error)
}
```

## Provider Config Registry

The `OpenAICompatibleClient` is parameterized by a `providerConfig` struct. A package-level `providers` map holds all configurations:

```go
var providers = map[string]providerConfig{
    "openai":     {name: "openai",     baseURL: "https://api.openai.com/v1",    apiKeyEnv: "OPENAI_API_KEY"},
    "mistral":    {name: "mistral",    baseURL: "https://api.mistral.ai/v1",    apiKeyEnv: "MISTRAL_API_KEY"},
    "ollama":     {name: "ollama",     baseURL: "http://localhost:11434/v1",     apiKeyEnv: ""},
    "openrouter": {name: "openrouter", baseURL: "https://openrouter.ai/api/v1", apiKeyEnv: "OPENROUTER_API_KEY", headers: ...},
}
```

## Provider:Model Routing

`NewClient(model)` uses `strings.Cut(model, ":")` to split the provider from the model name. The colon is mandatory.

```
google:gemini-2.5-flash       → GeminiClient("gemini-2.5-flash")
anthropic:claude-sonnet-4-6   → ClaudeClient("claude-sonnet-4-6")
openai:gpt-4o                 → OpenAICompatibleClient(openai config, "gpt-4o")
mistral:mistral-large-latest  → OpenAICompatibleClient(mistral config, "mistral-large-latest")
ollama:llama3                 → OpenAICompatibleClient(ollama config, "llama3")
openrouter:anthropic/claude-3 → OpenAICompatibleClient(openrouter config, "anthropic/claude-3")
```

Missing colon → error. Unknown provider → error. No silent fallback.

## Environment Variables

| Provider | Env Variable | Required | Default |
|----------|-------------|----------|---------|
| Gemini | `GEMINI_API_KEY` | When using `google:*` models | — |
| Claude | `ANTHROPIC_API_KEY` | When using `anthropic:*` models | — |
| OpenAI | `OPENAI_API_KEY` | When using `openai:*` models | — |
| Mistral | `MISTRAL_API_KEY` | When using `mistral:*` models | — |
| OpenRouter | `OPENROUTER_API_KEY` | When using `openrouter:*` models | — |
| Ollama | *(none)* | — | No API key needed |
| Ollama | `OLLAMA_BASE_URL` | Optional | `http://localhost:11434/v1` |

**Lazy validation**: Missing API keys don't cause errors at startup. The error occurs on first API call (HTTP 401).

## Key Design Decisions

- **Shared client**: All 4 new providers use the same `OpenAICompatibleClient` — zero provider-specific branching
- **Conditional auth**: `Authorization: Bearer` header is omitted when apiKeyEnv is empty (Ollama)
- **OpenRouter headers**: Hardcoded `HTTP-Referer` and `X-Title` headers via the `headers` map
- **60s timeout**: All HTTP clients use `httpClientTimeout` (defined in `gemini.go`)
- **CoerceToolCallArgs**: Called on all providers to fix LLM type mismatches

## Adding a New OpenAI-Compatible Provider

1. Add an entry to the `providers` map in `internal/llm/openai.go`:
   ```go
   "newprovider": {name: "newprovider", baseURL: "https://api.newprovider.com/v1", apiKeyEnv: "NEWPROVIDER_API_KEY"},
   ```
2. If custom headers are needed, add them to the `headers` map
3. Document the new env var in `CLAUDE.md` and this file
4. No new Go file, struct, or interface changes needed
5. Users configure it as `newprovider:model-name` in YAML configs

## Test Coverage

All tests are in `internal/llm/openai_test.go` using `net/http/httptest` mock servers:
- Core journeys: E2E-001 to E2E-004 (OpenAI, Mistral, Ollama, OpenRouter)
- Feature tests: E2E-005 to E2E-013 (schema conversion, routing, headers, base URL)
- Error handling: E2E-014 to E2E-020 (401, 429, 404, 500, unreachable, malformed JSON, empty choices)
- Edge cases: E2E-021 to E2E-026 (both text+tool, system prompt, empty schema, default fallback, nested args, lazy validation)
