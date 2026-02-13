# Makefile Documentation

Standard Go project Makefile with auto-detection of project structure.

## Key Targets

| Target | Description |
|--------|-------------|
| `build` | Build all binaries for current platform → `bin/` |
| `build-all` | Build for linux-amd64, darwin-amd64, darwin-arm64 |
| `run` | Build and run (use `CMD=agent` for multi-cmd projects) |
| `test` | Run `go test -v ./...` |
| `fmt` | Run `go fmt ./...` |
| `vet` | Run `go vet ./...` |
| `lint` | Run `golangci-lint` (falls back to `go vet`) |
| `check` | Run fmt + vet + lint + test |
| `clean` | Remove `bin/` directory |
| `e2e` | Build + run E2E tests with `-tags=e2e` |
| `docker` | Build Docker image |
| `docker-run` | Run single agent in Docker |
| `compose-up` | Start multi-agent Docker Compose stack |
| `compose-down` | Stop Docker Compose stack |

## Build Outputs

Binaries are placed in `bin/` with platform suffixes:
- `bin/agent-darwin-arm64` (macOS Apple Silicon)
- `bin/agent-darwin-amd64` (macOS Intel)
- `bin/agent-linux-amd64` (Linux)
- `bin/mcp-resources-darwin-arm64` etc.
- `bin/web-darwin-arm64` etc.

## Multi-Command Detection

The Makefile auto-detects `cmd/` subdirectories. For this project:
- `cmd/agent/` → agent binary
- `cmd/web/` → web chat frontend binary
- `cmd/mcp-resources/` → MCP server binary

Use `CMD=<name>` with `make run` to specify which binary to run:
```bash
make run CMD=agent ARGS="--config config/agent.yaml"
```

## Dependencies

- `go.sum` is auto-generated from `go.mod` changes
- `make clean-all` removes both `go.mod` and `go.sum` (use with care)
