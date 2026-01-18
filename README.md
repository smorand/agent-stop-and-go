# Agent Stop and Go

A generic API for building async autonomous agents that can pause execution and wait for external approval before performing destructive actions.

## Features

- **MCP Tool Support**: Agents use tools from external MCP (Model Context Protocol) servers
- **Approval Workflow**: Destructive operations require explicit approval before execution
- **Generic Architecture**: Swap the MCP server and prompt to create different agents
- **Conversation Persistence**: All conversations are saved and can be resumed
- **Real-time Status**: Track which conversations are waiting for approval

## Quick Start

```bash
# Build both binaries
make build

# Create symlink for MCP server
ln -sf mcp-resources-darwin-arm64 bin/mcp-resources  # macOS ARM
# or
ln -sf mcp-resources-linux-amd64 bin/mcp-resources   # Linux

# Run the API
make run
```

The API will start at http://localhost:8080

## Usage

### List Available Tools

```bash
curl http://localhost:8080/tools
```

### Start a Conversation

```bash
curl -X POST http://localhost:8080/conversations
```

### Send a Message

```bash
# List resources (automatic - no approval needed)
curl -X POST http://localhost:8080/conversations/{id}/messages \
  -d '{"message": "list resources"}'

# Add a resource (requires approval)
curl -X POST http://localhost:8080/conversations/{id}/messages \
  -d '{"message": "add resource server-1 with value 100"}'
```

### Handle Approvals

When a destructive action is requested, you'll receive an approval UUID:

```json
{
  "waiting_approval": true,
  "approval": {
    "uuid": "abc-123",
    "tool_name": "resources_add",
    "tool_args": {"name": "server-1", "value": 100}
  }
}
```

Approve or reject the action:

```bash
# Approve
curl -X POST http://localhost:8080/approvals/abc-123 \
  -d '{"approved": true}'

# Reject
curl -X POST http://localhost:8080/approvals/abc-123 \
  -d '{"action": "reject"}'
```

## Configuration

Edit `agent.yaml` to customize the agent:

```yaml
prompt: |
  Your agent's system prompt here...

host: 0.0.0.0
port: 8080
data_dir: ./data

mcp:
  command: ./bin/mcp-resources
  args:
    - --db
    - ./data/resources.db
```

## Creating Custom Agents

To create a different agent:

1. **Create a new MCP server** that provides your tools
2. **Mark destructive tools** with `destructiveHint: true`
3. **Update agent.yaml** with your MCP server and a new prompt
4. **Run the agent** - approval workflow works automatically

## MCP Server Protocol

MCP servers communicate via JSON-RPC 2.0 over stdin/stdout:

```json
// Request
{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}

// Response
{"jsonrpc": "2.0", "id": 1, "result": {"tools": [...]}}
```

Tools can have a `destructiveHint` property to trigger the approval workflow.

## API Reference

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /docs | Interactive API documentation |
| GET | /tools | List available MCP tools |
| GET | /health | Health check |
| POST | /conversations | Create conversation |
| GET | /conversations | List all conversations |
| GET | /conversations/:id | Get conversation details |
| POST | /conversations/:id/messages | Send message |
| POST | /approvals/:uuid | Resolve approval |

## License

MIT
