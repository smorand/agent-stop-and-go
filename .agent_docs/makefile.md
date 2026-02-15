# Makefile Documentation

Standard Go project Makefile using `define`/`eval` templates for incremental builds with proper dependency tracking.

## Incremental Build System

The Makefile uses `define`/`eval` to generate per-command, per-platform build rules:
- Each binary (`bin/<cmd>-<os>-<arch>`) has its own Make rule
- Dependencies: `go.sum` + all `.go` source files (excluding `_test.go`)
- Only rebuilds when sources change
- `build` depends on `$(CURRENT_BINARIES)`, `build-all` on `$(ALL_BINARIES)` + `$(ALL_LAUNCHERS)`
- On macOS, binaries are automatically signed with `codesign`

## Key Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `COMMANDS` | Auto-detected from `cmd/` subdirectories | - |
| `MODULE_NAME` | Go module name | first command name |
| `BUILD_DIR` | Output directory for binaries | `bin` |
| `MAKE_DOCKER_PREFIX` | Docker registry prefix | empty |
| `DOCKER_TAG` | Docker image tag | `latest` |
| `PROJECT_NAME` | Project name (from directory) | `agent-stop-and-go` |
| `HAS_INTERNAL` | Detected: `internal/` exists | `yes` |
| `HAS_DATA` | Detected: `data/` exists | `no` |

## Key Targets

| Target | Description |
|--------|-------------|
| `build` | Build all commands for current platform (incremental) → `bin/` |
| `build-all` | Build for all platforms + create launcher scripts |
| `run CMD=x` | Build and run a command (`CMD` is required) |
| `test` | Run `go test -v ./...` |
| `test-unit` | Run Go unit tests (alias for `test`) |
| `test-functional` | Run functional tests (shell scripts in `tests/`, if present) |
| `test-all` | Run unit + E2E tests |
| `e2e` | Build all binaries + run E2E tests with `-tags=e2e` (mcp-resources started as subprocess by tests) |
| `fmt` | Run `go fmt ./...` |
| `vet` | Run `go vet ./...` |
| `lint` | Run `golangci-lint` (falls back to `go vet`) |
| `check` | Run fmt + vet + lint + test |
| `clean` | Remove `bin/` directory |
| `clean-all` | Remove `bin/`, `go.mod`, `go.sum` |
| `docker-build` | Build Docker images for all commands |
| `docker-push` | Push Docker images to registry |
| `docker` | Build + push Docker images |
| `docker-run` | Run single agent Docker container |
| `run-up` | Build Docker images + start docker compose |
| `run-down` | Stop docker compose |
| `compose-up` | Alias for `run-up` |
| `compose-down` | Alias for `run-down` |

## Build Outputs

Binaries are placed in `bin/` with platform suffixes:
- `bin/agent-darwin-arm64` (macOS Apple Silicon)
- `bin/agent-darwin-amd64` (macOS Intel)
- `bin/agent-linux-amd64` (Linux)
- `bin/agent-windows-amd64.exe` (Windows)
- Same pattern for `mcp-resources` and `web`

## Platform Support

- `-linux-amd64`, `-darwin-amd64`, `-darwin-arm64`, `-windows-amd64.exe`
- On macOS, binaries are automatically signed with `codesign`
- Launcher scripts (`.sh`) auto-detect platform and execute the right binary

## Docker Usage

```bash
make docker-build                                         # Build per-command images
make docker-push                                          # Push to registry
MAKE_DOCKER_PREFIX=gcr.io/my-project/ DOCKER_TAG=v1.0.0 make docker  # Custom registry
make run-up                                               # Build + docker compose up
make run-down                                             # docker compose down
```

Each command (`agent`, `web`, `mcp-resources`, `mcp-filesystem`) gets its own Docker image with a single binary. No CGO required — all builds use `CGO_ENABLED=0`. Docker Compose runs 4 services: `mcp-resources`, `agent-a`, `agent-b`, `web`.

## Multi-Command Detection

The Makefile auto-detects `cmd/` subdirectories. For this project:
- `cmd/agent/` → agent binary
- `cmd/web/` → web chat frontend binary
- `cmd/mcp-resources/` → MCP resource server binary (SQLite)
- `cmd/mcp-filesystem/` → MCP filesystem server binary (sandboxed file operations)

Use `CMD=<name>` with `make run` to specify which binary to run:
```bash
make run CMD=agent ARGS="--config config/agent.yaml"
```
