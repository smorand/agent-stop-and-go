# Agent Stop and Go

## Overview

Generic API for async autonomous agents with MCP tool support and approval workflows. Agents can pause execution and wait for external approval before proceeding with destructive actions.

## Tech Stack

- **Language**: Go 1.23
- **Web Framework**: Fiber
- **LLM**: Gemini 2.5 Flash (generativelanguage API)
- **MCP Protocol**: JSON-RPC 2.0 over stdio
- **Config**: YAML (gopkg.in/yaml.v3)
- **Storage**: JSON files (conversations), SQLite (MCP resources)
- **Build**: Make

## Environment Variables

- `GEMINI_API_KEY`: Required. API key for Gemini LLM.

## Key Commands

```bash
make build      # Build both binaries for current platform
make run        # Build and run API on port 8080
make test       # Run tests
make check      # Run all checks (fmt, vet, lint, test)
```

## Project Structure

```
cmd/
├── agent-stop-and-go/main.go     # API entry point
└── mcp-resources/main.go         # MCP server (SQLite resources)
internal/
├── api/                          # HTTP handlers (Fiber)
├── agent/                        # Agent logic with LLM + MCP
├── llm/                          # Gemini LLM client
├── mcp/                          # MCP client (JSON-RPC)
├── config/                       # YAML config loader
├── conversation/                 # Data models with tool calls
└── storage/                      # JSON file persistence
```

## API Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | /docs | Interactive HTML documentation |
| GET | /tools | List available MCP tools |
| GET | /health | Health check |
| POST | /conversations | Start new conversation |
| GET | /conversations | List all conversations |
| GET | /conversations/:id | Get conversation |
| POST | /conversations/:id/messages | Send message (may trigger tool) |
| POST | /approvals/:uuid | Approve or reject pending action |

## Key Concepts

- **MCP Server**: External binary providing tools via JSON-RPC
- **destructiveHint**: Tool property indicating approval requirement
- **Conversation Status**: `active`, `waiting_approval`, `completed`
- **Approval Flow**: Tools with `destructiveHint=true` require explicit approval

## Configuration (agent.yaml)

```yaml
prompt: |
  System prompt for the agent...

host: 0.0.0.0
port: 8080
data_dir: ./data

llm:
  model: gemini-2.5-flash

mcp:
  command: ./bin/mcp-resources
  args:
    - --db
    - ./data/resources.db
```

## MCP Tools (mcp-resources)

| Tool | Description | destructiveHint |
|------|-------------|-----------------|
| resources_add | Add a new resource | true |
| resources_remove | Remove resources | true |
| resources_list | List/search resources | false |

## Approval Flow

```
User: "add resource X"
  → LLM (Gemini) determines intent and calls resources_add
  → Tool has destructiveHint=true → Create PendingApproval with UUID
  → Return approval request to client
  → Wait...

POST /approvals/{uuid} { "approved": true }
  # or { "action": "approve" }
  # or { "answer": "yes" }
  → Execute tool via MCP
  → Return result
```

## Documentation Index

- `.agent_docs/` - Detailed documentation (load on demand)
