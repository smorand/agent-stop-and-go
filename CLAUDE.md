# Agent Stop and Go

## Overview

API for async autonomous agents with approval workflows. Agents can pause execution and wait for external approval before proceeding with sensitive actions.

## Tech Stack

- **Language**: Go 1.23
- **Web Framework**: Fiber
- **Config**: YAML (gopkg.in/yaml.v3)
- **Storage**: JSON files
- **Build**: Make

## Key Commands

```bash
make build      # Build for current platform
make run        # Build and run API on port 8080
make test       # Run tests
make fmt        # Format code
make check      # Run all checks
```

## Project Structure

```
cmd/agent-stop-and-go/main.go     # Entry point
internal/
├── api/                          # HTTP handlers (Fiber)
│   ├── api.go                    # Server setup
│   ├── routes.go                 # Route definitions
│   └── handlers.go               # Request handlers
├── agent/agent.go                # Agent logic with approval workflow
├── config/config.go              # YAML config loader
├── conversation/conversation.go  # Data models
└── storage/storage.go            # JSON file persistence
```

## API Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | /docs | Interactive HTML documentation |
| GET | /docs/json | API spec as JSON |
| GET | /health | Health check |
| POST | /conversations | Start new conversation |
| GET | /conversations | List all conversations |
| GET | /conversations/:id | Get conversation |
| POST | /conversations/:id/messages | Send message |
| POST | /approvals/:uuid | Resolve pending approval |

## Key Concepts

- **Conversation Status**: `active`, `waiting_approval`, `completed`
- **Approval Flow**: Agent returns `[APPROVAL_NEEDED]:` prefix to trigger approval
- **UUID**: Each pending approval has a unique UUID for resolution
- **Persistence**: Conversations stored as JSON in `data/` directory

## Configuration (agent.yaml)

```yaml
prompt: |
  System prompt for the agent...
host: 0.0.0.0
port: 8080
data_dir: ./data
```

## Documentation Index

- `.agent_docs/` - Detailed documentation (load on demand)
