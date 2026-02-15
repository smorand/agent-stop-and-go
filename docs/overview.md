# Project Overview

Agent Stop and Go is a generic API for building async autonomous agents with human-in-the-loop approval workflows. Agents orchestrate external tools via the Model Context Protocol (MCP) and delegate tasks to other agents via the Agent-to-Agent (A2A) protocol. Destructive operations pause execution until explicitly approved by a human or upstream agent.

## Business Context

Modern AI agent systems need a safety mechanism before executing irreversible operations. Agent Stop and Go solves this by providing a framework where:

- An LLM decides **what** action to take based on user input
- Non-destructive actions execute immediately (e.g., listing data)
- Destructive actions pause and wait for explicit approval (e.g., deleting records)
- Multiple agents can be chained together, with approval propagating across the chain

This enables building production-ready agent pipelines where humans remain in control of consequential decisions.

## Key Features

| Feature | Description |
|---------|-------------|
| **Multi-LLM Support** | Supports Gemini and Claude models with automatic routing based on model name |
| **MCP Tool Execution** | Agents call external tools via MCP Streamable HTTP (primary) or stdio (legacy), with multi-server aggregation |
| **A2A Protocol** | Agents delegate tasks to other agents via JSON-RPC 2.0 over HTTPS |
| **Approval Workflow** | Destructive operations require explicit human approval before execution |
| **Agent Orchestration** | Compose agents into sequential, parallel, and loop pipelines |
| **Auth Forwarding** | Bearer tokens propagate through agent chains (Zero Trust) |
| **Session Tracing** | 8-char hex session IDs enable cross-agent log correlation |
| **Conversation Persistence** | All conversations are saved as JSON files and can be resumed |
| **Web Chat UI** | Browser-based frontend for interacting with agents |
| **MCP Filesystem Server** | Sandboxed filesystem operations with chroot-like security, per-root allowlists, diff patching, grep, glob |
| **Docker Support** | Single-agent and multi-agent Docker Compose deployments |

## Tech Stack

| Component | Technology |
|-----------|------------|
| Language | Go 1.24 |
| Web Framework | [Fiber](https://gofiber.io/) v2 |
| LLM Providers | Google Gemini API, Anthropic Claude API |
| MCP Protocol | Streamable HTTP (primary) or stdio (legacy) via `github.com/mark3labs/mcp-go` |
| A2A Protocol | JSON-RPC 2.0 over HTTPS |
| Configuration | YAML (`gopkg.in/yaml.v3`) |
| Conversation Storage | JSON files on disk |
| MCP Resource Storage | SQLite (`modernc.org/sqlite`, pure Go, no CGO) |
| Unique IDs | `github.com/google/uuid` |
| Build System | Make |
| Container | Docker, Docker Compose |

## Quick Start

### Prerequisites

- Go 1.24 or later
- Make
- A Gemini API key (`GEMINI_API_KEY`) or Anthropic API key (`ANTHROPIC_API_KEY`)

### Steps

```bash
# 1. Clone the repository
git clone https://github.com/smorand/agent-stop-and-go.git
cd agent-stop-and-go

# 2. Set your LLM API key
export GEMINI_API_KEY=your-api-key       # For Gemini models (default)
# export ANTHROPIC_API_KEY=your-api-key  # For Claude models

# 3. Build all binaries
make build

# 4. Run the agent API (starts on port 8080)
make run CMD=agent
```

### Verify it works

```bash
# Check health
curl http://localhost:8080/health
# {"status":"ok"}

# List available tools
curl http://localhost:8080/tools

# Start a conversation
curl -X POST http://localhost:8080/conversations \
  -H "Content-Type: application/json" \
  -d '{"message": "list resources"}'
```

### Docker Quick Start

```bash
# Single agent
export GEMINI_API_KEY=your-api-key
make docker-run

# Multi-agent stack (orchestrator + resource agent + web UI)
docker-compose up --build
# Open http://localhost:3000
```

## Project Structure

```
agent-stop-and-go/
├── cmd/
│   ├── agent/main.go              # Agent API entry point
│   ├── web/main.go                # Web chat frontend entry point
│   ├── mcp-resources/main.go      # MCP resource server (SQLite)
│   └── mcp-filesystem/main.go     # MCP filesystem server (sandboxed)
├── internal/
│   ├── api/                       # HTTP handlers, routes, A2A server, docs
│   ├── agent/                     # Core agent logic, orchestration engine
│   ├── llm/                       # Multi-provider LLM clients (Gemini, Claude)
│   ├── mcp/                       # MCP client (multi-server via CompositeClient, HTTP + stdio)
│   ├── filesystem/                # MCP filesystem server (sandboxed file operations)
│   ├── a2a/                       # A2A JSON-RPC client (HTTPS)
│   ├── auth/                      # Bearer token and session ID propagation
│   ├── config/                    # YAML configuration loader
│   ├── conversation/              # Data models (messages, approvals, pipelines)
│   └── storage/                   # JSON file persistence
├── config/                        # YAML configuration files
│   ├── agent.yaml                 # Default single-agent config
│   ├── agent-a.yaml               # Docker Compose: orchestrator
│   ├── agent-b.yaml               # Docker Compose: resource agent
│   ├── mcp-resources.yaml         # MCP resources server config (local dev)
│   ├── mcp-filesystem.yaml        # MCP filesystem server config (local dev)
│   ├── mcp-resources-compose.yaml # MCP resources server config (Docker Compose)
│   ├── web.yaml                   # Web frontend config (local dev)
│   └── web-compose.yaml           # Web frontend config (Docker Compose)
├── examples/                      # Ready-to-use configuration examples
├── testdata/                      # E2E test configurations
├── docs/                          # Project documentation
├── .agent_docs/                   # Agent-oriented documentation
├── Makefile                       # Build, test, deploy targets
├── Dockerfile                     # Multi-stage Docker build
├── docker-compose.yaml            # Multi-agent deployment
├── e2e_test.go                    # Single-agent E2E tests
├── e2e_orchestration_test.go      # Multi-agent orchestration E2E tests
└── e2e_filesystem_test.go         # MCP filesystem server E2E tests
```

## Related Documentation

- [Architecture](architecture.md) -- System architecture, component diagrams, data flow
- [Functionalities](functionalities.md) -- Detailed feature documentation
- [Authentication](authentication.md) -- Auth forwarding and session tracing
- [DevOps](devops.md) -- Build tools, testing strategy, code quality
- [Deployment](deployment.md) -- Local, Docker, and Docker Compose deployment
