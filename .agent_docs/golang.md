# Go Coding Standards

Project-specific Go conventions for Agent Stop and Go.

## Naming Conventions

- **Interfaces**: Use concise names without package-name stuttering (e.g., `llm.Client` not `llm.LLMClient`)
- **Constants**: Group related constants with `const ()` blocks (see `conversation/conversation.go`)
- **Unexported context keys**: Use empty struct types (`type bearerKey struct{}`)

## Error Handling

- Always wrap errors with `%w` and context: `fmt.Errorf("load config %s: %w", path, err)`
- Return early on errors, keep happy path unindented
- Use `fmt.Errorf` for domain errors, `%w` for wrapped chains

## Package Organization

- `internal/agent/`: Core agent logic (split across `agent.go` for simple mode, `orchestrator.go` for tree-based)
- `internal/llm/`: Multi-provider LLM interface (`Client` interface, `GeminiClient`, `ClaudeClient`)
- `internal/mcp/`: MCP JSON-RPC client (`client.go`) and protocol types (`protocol.go`)
- `internal/a2a/`: A2A JSON-RPC client and protocol types
- `internal/auth/`: Context-based auth propagation (Bearer tokens, session IDs)
- `internal/config/`: YAML config loader
- `internal/conversation/`: Data models with mutex-protected methods
- `internal/storage/`: JSON file persistence

## Concurrency

- `sync.Mutex` on `Agent.llmMu` to protect lazy LLM client creation
- `sync.Mutex` on `Agent.mcpMu` to serialize MCP tool calls in parallel pipelines
- `sync.RWMutex` on `Conversation.mu` for message append safety
- `sync.RWMutex` on `Storage.mu` for file read/write safety
- `sync.RWMutex` on `SessionState.mu` for state access in parallel nodes

## Context Usage

- Context is always the first parameter
- Never stored in structs
- `context.Background()` only in `main()` and HTTP handler entry points
- Auth values propagated via context: `auth.WithBearerToken()`, `auth.WithSessionID()`

## Testing

- E2E tests use build tags: `//go:build e2e`
- Test configs in `testdata/` directory
- Tests start real HTTP servers on dedicated ports (9090-9092)

## Linting

- `.golangci.yml` configured with recommended linters
- Run `make check` for full validation (fmt + vet + lint + test)
