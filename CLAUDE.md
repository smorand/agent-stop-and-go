# Agent Stop and Go

## Overview

Generic API for async autonomous agents with MCP tool support, A2A sub-agent delegation, and approval workflows. Agents can pause execution and wait for external approval before proceeding with destructive actions.

## Tech Stack

- **Language**: Go 1.24
- **Web Framework**: Fiber
- **LLM**: Gemini / Claude (multi-provider: `claude-*` → Anthropic, others → Gemini)
- **MCP Protocol**: Streamable HTTP (primary) or stdio (legacy) via `github.com/mark3labs/mcp-go`
- **A2A Protocol**: JSON-RPC 2.0 over HTTPS
- **Config**: YAML (gopkg.in/yaml.v3)
- **Storage**: JSON files (conversations), SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **Build**: Make
- **Container**: Docker

## Environment Variables

- `GEMINI_API_KEY`: Required for Gemini models (default). API key for Gemini LLM.
- `ANTHROPIC_API_KEY`: Required for Claude models. API key for Anthropic Claude.

## Key Commands

```bash
make build           # Build all binaries for current platform (incremental)
make run CMD=agent   # Build and run agent on port 8080
make test            # Run unit tests
make check           # Run all checks (fmt, vet, lint, test)
make e2e             # Run E2E tests (requires GEMINI_API_KEY)
make docker-build    # Build Docker images for all commands
make docker-run      # Run single agent Docker container
make run-up          # Build Docker images and start docker compose
make run-down        # Stop docker compose services
```

## Project Structure

```
config/
├── agent.yaml                    # Default single-agent config
├── web.yaml                      # Web frontend config (local dev)
├── mcp-resources.yaml            # MCP resources server config (local dev)
├── mcp-filesystem.yaml           # MCP filesystem server config (local dev)
├── mcp-resources-compose.yaml    # MCP resources server config (Docker Compose)
├── agent-a.yaml                  # Docker Compose: orchestrator
├── agent-b.yaml                  # Docker Compose: resource agent
└── web-compose.yaml              # Docker Compose: web frontend
cmd/
├── agent/main.go                 # API entry point
├── web/main.go                   # Web chat (A2A-only frontend)
├── mcp-resources/main.go         # MCP Streamable HTTP server (SQLite resources)
└── mcp-filesystem/main.go        # MCP Streamable HTTP server (sandboxed filesystem)
internal/
├── api/                          # HTTP handlers (Fiber)
├── agent/                        # Agent logic with LLM + MCP + A2A + orchestration
├── llm/                          # Multi-provider LLM clients (60s timeout)
├── mcp/                          # MCP client (dual transport: HTTP + stdio)
│   ├── client.go                 # Client interface, StdioClient, factory
│   ├── client_http.go            # HTTPClient (Streamable HTTP via mcp-go)
│   ├── client_composite.go       # CompositeClient wrapping multiple sub-clients
│   └── protocol.go               # Domain types + stdio transport types
├── filesystem/                   # MCP filesystem server (sandboxed file operations)
│   ├── config.go                 # Config, roots, allowlists, YAML loader
│   ├── security.go               # Path validation, symlink-aware chroot enforcement
│   ├── patch.go                  # Unified diff parser and atomic patcher
│   └── tools.go                  # 15 tool handlers (list, read, write, grep, glob, etc.)
├── a2a/                          # A2A client (JSON-RPC over HTTPS)
├── auth/                         # Bearer token context propagation
├── config/                       # YAML config loader
├── conversation/                 # Data models with tool calls
└── storage/                      # JSON file persistence
testdata/                             # E2E orchestration test configs
```

## API Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | /docs | Interactive HTML documentation |
| GET | /tools | List available MCP tools + A2A agents |
| GET | /health | Health check |
| POST | /conversations | Start new conversation |
| GET | /conversations | List all conversations |
| GET | /conversations/:id | Get conversation |
| POST | /conversations/:id/messages | Send message (may trigger tool) |
| POST | /approvals/:uuid | Approve or reject pending action |
| GET | /.well-known/agent.json | A2A Agent Card (discovery) |
| POST | /a2a | A2A JSON-RPC endpoint (message/send, tasks/get) |

## Key Concepts

- **MCP Server**: One or more standalone services providing tools. Configured as a list under `mcp_servers`, each with a required `name` field. Tools are aggregated by a `CompositeClient` that routes calls to the correct sub-client
- **A2A Agents**: Remote agents accessible via JSON-RPC over HTTPS
- **destructiveHint**: Tool/agent property indicating approval requirement
- **Conversation Status**: `active`, `waiting_approval`, `completed`
- **Approval Flow**: Tools/agents with `destructiveHint=true` require explicit approval
- **Auth Forwarding**: `Authorization: Bearer` tokens are forwarded to A2A agents
- **Session ID**: 8-char hex ID generated per conversation at the entry point, propagated via `X-Session-ID` header to A2A agents, stored in `Conversation.SessionID`, and logged as `sid=` in request logs for cross-agent tracing
- **Agent Tree**: Orchestration tree with `sequential`, `parallel`, `loop`, `llm`, `a2a` node types
- **Session State**: `output_key` stores node output, `{placeholder}` resolves in prompts
- **Pipeline Pause/Resume**: Sequential pipelines pause on approval and resume from the paused node

## Configuration (config/agent.yaml)

### Simple mode (single LLM, backward compatible)

```yaml
name: resource-manager        # Agent name for A2A Agent Card (default: "agent")
description: "Agent desc"     # Agent description for A2A Agent Card

prompt: |
  System prompt for the agent...

host: 0.0.0.0
port: 8080
data_dir: ./data

llm:
  model: gemini-2.5-flash    # or claude-sonnet-4-5-20250929 for Claude

mcp_servers:
  - name: resources
    url: http://localhost:8090/mcp    # Streamable HTTP (preferred)
  # OR legacy stdio transport:
  # - name: resources
  #   command: ./bin/mcp-resources
  #   args: [--db, ./data/resources.db]

a2a:
  - name: summarizer
    url: https://summarizer.example.com
    description: "Summarizes texts"
    destructiveHint: false
```

### Orchestrated mode (agent tree)

When `agent` key is present, the tree-based orchestrator is used:

```yaml
mcp_servers:
  - name: resources
    url: http://localhost:8090/mcp

agent:
  name: pipeline
  type: sequential          # sequential | parallel | loop | llm | a2a
  agents:
    - name: analyzer
      type: llm
      model: gemini-2.5-flash
      output_key: analysis    # stores output in session state
      prompt: "Analyze: ..."
    - name: executor
      type: llm
      model: gemini-2.5-flash
      prompt: "Execute based on {analysis}"  # {placeholder} resolves from session state
```

### Agent node fields

| Field | Types | Description |
|-------|-------|-------------|
| `name` | all | Node identifier |
| `type` | all | `llm`, `sequential`, `parallel`, `loop`, `a2a` |
| `agents` | sequential, parallel, loop | Sub-agent list |
| `model` | llm | LLM model — `claude-*` for Anthropic, others for Gemini (defaults to `llm.model`) |
| `prompt` | llm, a2a | System prompt / message template with `{placeholders}` |
| `output_key` | llm, a2a | Key to store output in session state |
| `can_exit_loop` | llm | Gives the node an `exit_loop` tool |
| `max_iterations` | loop | Max iterations (default: 10 safety cap) |
| `url` | a2a | Remote agent URL |
| `destructiveHint` | a2a | Requires approval |
| `a2a` | llm | Per-node A2A tools for LLM decision |

## MCP Tools (mcp-resources)

| Tool | Description | destructiveHint |
|------|-------------|-----------------|
| resources_add | Add a new resource | true |
| resources_remove | Remove resources | true |
| resources_list | List/search resources | false |

## MCP Tools (mcp-filesystem)

Sandboxed filesystem server with chroot-like security per root. Configured via `config/mcp-filesystem.yaml`. Each root has an `allowed_tools` allowlist (`"*"` for all).

| Tool | Description | Read-only |
|------|-------------|-----------|
| list_roots | List configured roots and their allowed tools | yes |
| list_folder | List directory contents (name, type, size, mtime) | yes |
| read_file | Read file fully or partially (byte/line offsets) | yes |
| write_file | Write file (overwrite/append/create_only modes) | no |
| remove_file | Delete a single file | no |
| patch_file | Apply unified diff patch atomically | no |
| create_folder | Create directory (with parents, idempotent) | no |
| remove_folder | Remove directory recursively | no |
| stat_file | File/directory metadata (size, mtime, type) | yes |
| hash_file | Compute hash (md5, sha1, sha256) | yes |
| permissions_file | Owner, group, permission bits | yes |
| copy | Copy file/directory, potentially cross-root | no |
| move | Move/rename file/directory, potentially cross-root | no |
| grep | Regex search in file contents with context lines | yes |
| glob | File name search by glob or regex pattern | yes |

## A2A Integration

### A2A Client (outbound)
A2A agents are configured in `config/agent.yaml` under the `a2a` section. They appear as synthetic tools to the LLM with the prefix `a2a_` (e.g., `a2a_summarizer`). The LLM naturally picks between MCP tools and A2A agents. Bearer tokens from incoming requests are forwarded to A2A agents.

### A2A Server (inbound)
The agent exposes itself as an A2A server via `/.well-known/agent.json` (Agent Card) and `POST /a2a` (JSON-RPC 2.0). Other A2A agents can discover skills and send tasks. Task ID = conversation ID. Destructive tasks return `state: "input-required"` until approved via REST or A2A `message/send` with `taskId`.

### Proxy Approval Chain
When an A2A sub-agent returns `state: "input-required"`, the parent agent creates a proxy `PendingApproval` with `remote_task_id` and `remote_agent_name`. When the proxy is resolved (via REST or `message/send` with `taskId`), the parent forwards the approval to the sub-agent via `message/send` with `taskId`.

## Approval Flow

```
User: "add resource X"
  → LLM determines intent and calls resources_add (or a2a_agent)
  → destructiveHint=true → Create PendingApproval with UUID
  → Return approval request to client
  → Wait...

POST /approvals/{uuid} { "approved": true }
  # or { "action": "approve" }
  # or { "answer": "yes" }
  # or A2A message/send { "taskId": "task-id", "message": {..., "text": "approved"} }
  → Execute tool via MCP (or delegate to A2A agent)
  → If proxy: forward message/send with taskId to remote agent
  → Return result
```

## Web Chat (cmd/web)

Browser-based chat frontend that communicates with the agent via REST API. Configured via `config/web.yaml`:

```yaml
agent_url: http://localhost:8080
host: 0.0.0.0
port: 3000
```

Routes: `GET /` (chat UI), `POST /api/send`, `POST /api/approve`, `GET /api/conversation/:id`

## E2E Tests

Run with `make e2e` or `go test -v -tags=e2e -timeout 300s ./...`. Tests require `GEMINI_API_KEY` and built binaries.

- **Core tests** (`e2e_test.go`): Single-agent scenarios on port 9090 (TS-001 to TS-014, TS-020). Starts `mcp-resources` on port 8090 in `TestMain`.
- **Orchestration tests** (`e2e_orchestration_test.go`): Multi-agent orchestration on ports 9091-9092 (TS-022 to TS-028). Each test starts `mcp-resources` on port 9290.
  - Sequential, Parallel, Loop pipelines with MCP tools
  - A2A chain delegation with proxy approval
  - Orchestrated pipelines accessed via A2A protocol
- **Filesystem tests** (`e2e_filesystem_test.go`): MCP filesystem server tests on ports 9190+. Each test starts its own `mcp-filesystem` with isolated temp dirs.
  - Core journeys, read/write, patch, copy/move, grep/glob, security (symlink escape, path traversal), error handling
- **Test configs**: `testdata/e2e-*.yaml` (isolated configs for orchestration tests, use `mcp_servers`)

## Development Notes

- **No CI/CD pipeline**: This project does not have a CI/CD pipeline. Tests are run locally via `make test` and `make e2e`.
- **Minimal unit tests**: Unit test coverage is intentionally limited to core packages (auth, config, conversation, storage). The primary testing strategy relies on E2E tests that validate the full request flow.

## Configuration (config/mcp-filesystem.yaml)

```yaml
host: 0.0.0.0
port: 8091
max_full_read_size: 1048576  # 1MB threshold for full reads

roots:
  - name: workspace
    path: ./data/workspace
    allowed_tools:
      - "*"              # all tools, or list specific: read_file, write_file, etc.
```

## Documentation Index

- `.agent_docs/golang.md` - Go coding standards and project conventions
- `.agent_docs/makefile.md` - Makefile targets and build documentation
- `docs/README.md` - Documentation index and reading order
- `docs/overview.md` - Project overview, features, tech stack, quick start
- `docs/architecture.md` - System architecture, component diagrams, data models, design decisions
- `docs/functionalities.md` - Comprehensive feature documentation, API reference, configuration
- `docs/authentication.md` - Bearer token forwarding, session ID tracing, security
- `docs/devops.md` - Build system, testing strategy, code quality, Docker build, logging
- `docs/deployment.md` - Local dev, Docker, Docker Compose deployment
