# Agent Stop and Go

A generic API for building async autonomous agents that can orchestrate tools (MCP) and sub-agents (A2A) with a human-in-the-loop approval workflow for destructive operations.

## Features

- **Multi-LLM**: Supports Gemini and Claude models (`claude-*` → Anthropic, others → Gemini)
- **MCP Tool Support**: Agents use tools from external MCP (Model Context Protocol) servers
- **A2A Client**: Delegate tasks to other agents using the A2A protocol
- **A2A Server**: Expose the agent as an A2A-compliant server for discovery and task execution by other agents
- **Approval Workflow**: Destructive operations require explicit approval before execution
- **Authorization Forwarding**: Bearer tokens are propagated to sub-agents (Zero Trust)
- **Agent Orchestration**: Compose agents with sequential, parallel, and loop patterns (inspired by Google ADK)
- **Generic Architecture**: Swap the MCP server, LLM model, and prompt to create different agents
- **Conversation Persistence**: All conversations are saved and can be resumed
- **Docker Support**: Deployable as a Docker container

## Quick Start

```bash
# Set your LLM API key (one of these, depending on the model)
export GEMINI_API_KEY=your-api-key       # For Gemini models (default)
export ANTHROPIC_API_KEY=your-api-key    # For Claude models

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

### Docker

```bash
# Build and run with Docker (single agent)
export GEMINI_API_KEY=your-api-key
make docker-run
```

### Docker Compose (Multi-Agent)

Run the full multi-agent chain (orchestrator + resource agent + web frontend):

```bash
export GEMINI_API_KEY=your-api-key
docker-compose up --build
```

This starts:
- **agent-b** (port 8082): Resource agent with MCP tools
- **agent-a** (port 8080): Orchestrator delegating to agent-b via A2A
- **web** (port 3000): Browser-based chat UI

Open http://localhost:3000 to interact.

```bash
# Stop all services
docker-compose down
```

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

Approve or reject the action (multiple formats supported):

```bash
# Approve (any of these)
curl -X POST http://localhost:8080/approvals/abc-123 \
  -d '{"approved": true}'
curl -X POST http://localhost:8080/approvals/abc-123 \
  -d '{"action": "approve"}'
curl -X POST http://localhost:8080/approvals/abc-123 \
  -d '{"answer": "yes"}'

# Reject (any of these)
curl -X POST http://localhost:8080/approvals/abc-123 \
  -d '{"approved": false}'
curl -X POST http://localhost:8080/approvals/abc-123 \
  -d '{"action": "reject"}'
curl -X POST http://localhost:8080/approvals/abc-123 \
  -d '{"answer": "no"}'
```

### Authorization Forwarding

Pass a Bearer token to forward it to sub-agents:

```bash
curl -X POST http://localhost:8080/conversations/{id}/messages \
  -H "Authorization: Bearer your-token" \
  -d '{"message": "delegate task to summarizer"}'
```

### Request Tracing

Every conversation gets an 8-char hex session ID for cross-agent log correlation:

- **Entry point (user request)**: A new session ID is auto-generated and stored in the conversation
- **A2A inbound (from another agent)**: The `X-Session-ID` header is extracted and reused
- **A2A outbound**: Session ID is forwarded as `X-Session-ID` header to downstream agents

All request logs include `sid=<session-id>`, making it easy to correlate logs across agents in a multi-agent Docker Compose deployment.

You can also pass a custom session ID:

```bash
curl -X POST http://localhost:8080/conversations \
  -H "X-Session-ID: abcd1234" \
  -d '{"message": "list resources"}'
```

## Configuration

Edit `config/agent.yaml` to customize the agent:

```yaml
# Agent identity (used in A2A Agent Card)
name: resource-manager     # Default: "agent"
description: "A resource management agent"

# Required: defines the agent's behavior and available operations
prompt: |
  Your agent's system prompt here...

# Required: MCP servers providing tools (one or more)
mcp_servers:
  - name: resources
    url: http://localhost:8090/mcp    # Streamable HTTP (preferred)
  # OR legacy stdio transport:
  # - name: resources
  #   command: ./bin/mcp-resources
  #   args: [--db, ./data/resources.db]

# Optional (shown with defaults)
host: 0.0.0.0        # Listen address
port: 8080            # Listen port
data_dir: ./data      # Directory for conversation persistence
llm:
  model: gemini-2.5-flash  # Or claude-sonnet-4-5-20250929 for Claude

# Optional: A2A sub-agents for delegation
a2a:
  - name: summarizer
    url: https://summarizer.example.com
    description: "Summarizes texts"
    destructiveHint: false
  - name: deployer
    url: https://deployer.example.com
    description: "Deploys applications"
    destructiveHint: true  # Will require approval
```

**Environment Variables:**
- `GEMINI_API_KEY`: Required for Gemini models (default). Your Google AI API key.
- `ANTHROPIC_API_KEY`: Required for Claude models. Your Anthropic API key.

## Agent Orchestration

Beyond single LLM agents, you can compose complex pipelines using an agent tree. Define the `agent` key in `config/agent.yaml` to use orchestration (when absent, the agent runs in simple single-LLM mode for backward compatibility).

### Node Types

| Type | Description | Key Fields |
|------|-------------|------------|
| `llm` | Calls the LLM with MCP tools and optional A2A tools | `model`, `prompt`, `output_key`, `can_exit_loop`, `a2a` |
| `sequential` | Runs sub-agents in order | `agents` |
| `parallel` | Runs sub-agents concurrently | `agents` |
| `loop` | Repeats sub-agents until exit or max iterations | `agents`, `max_iterations` |
| `a2a` | Delegates to a remote A2A agent as a workflow step | `url`, `prompt`, `destructiveHint` |

### Data Flow

Nodes communicate via **session state**:
- `output_key`: Stores a node's response under this key
- `{placeholder}`: In `prompt`, replaced with the value from session state

### Example: Sequential Pipeline

```yaml
agent:
  name: analyze-then-execute
  type: sequential
  agents:
    - name: analyzer
      type: llm
      output_key: analysis
      prompt: "Analyze the user's request. Output: intent, target, details."
    - name: executor
      type: llm
      prompt: "Based on this analysis: {analysis}, execute the appropriate tool."
```

### Example: Loop with Exit Condition

```yaml
agent:
  name: resource-monitor
  type: loop
  max_iterations: 5
  agents:
    - name: checker
      type: llm
      can_exit_loop: true
      prompt: "List resources. If 3+ exist, call exit_loop. Otherwise add one."
```

### Approval Behavior in Orchestration

| Context | Destructive Tool Behavior |
|---------|--------------------------|
| Sequential | Pipeline pauses, saves state, resumes after approval |
| Parallel | Executes immediately (no pause) |
| Loop | Executes immediately (no pause) |

See [`examples/`](examples/) for complete working configurations with test prompts.

## Creating Custom Agents

To create a different agent:

1. **Create a new MCP server** that provides your tools
2. **Mark destructive tools** with `destructiveHint: true`
3. **Optionally add A2A sub-agents** in the `a2a` config section
4. **Update `config/agent.yaml`** with your servers and a new prompt
5. **Run the agent** - approval workflow works automatically

## MCP Server Protocol

MCP servers communicate via JSON-RPC 2.0 over stdin/stdout:

```json
// Request
{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}

// Response
{"jsonrpc": "2.0", "id": 1, "result": {"tools": [...]}}
```

Tools can have a `destructiveHint` property to trigger the approval workflow.

## A2A Protocol

### A2A Client (outbound)

A2A sub-agents communicate via JSON-RPC 2.0 over HTTPS. They appear as synthetic tools to the LLM (prefixed with `a2a_`). The agent forwards Bearer tokens to A2A agents for Zero Trust authorization.

### A2A Server (inbound)

The agent also exposes itself as an A2A server, allowing other A2A-compliant agents to discover and call it.

**Agent Card** — discover the agent:
```bash
curl http://localhost:8080/.well-known/agent.json
```

**message/send** — send a task via JSON-RPC 2.0:
```bash
curl -X POST http://localhost:8080/a2a \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "message/send",
    "params": {
      "message": {
        "role": "user",
        "parts": [{"type": "text", "text": "list resources"}]
      }
    }
  }'
```

**tasks/get** — check task status:
```bash
curl -X POST http://localhost:8080/a2a \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc": "2.0", "id": 1, "method": "tasks/get", "params": {"id": "task-uuid"}}'
```

**message/send with taskId** — continue an existing task (e.g., approve or reject):
```bash
curl -X POST http://localhost:8080/a2a \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "message/send",
    "params": {
      "taskId": "task-uuid",
      "message": {
        "role": "user",
        "parts": [{"type": "text", "text": "approved"}]
      }
    }
  }'
```

## Web Chat

A browser-based chat interface that connects to an agent via the REST API. The A2A protocol is reserved for agent-to-agent communication only.

### Setup

```bash
# Start the agent (terminal 1)
export GEMINI_API_KEY=your-api-key
make build
./bin/agent-$(go env GOOS)-$(go env GOARCH)

# Start the web chat (terminal 2)
./bin/web-$(go env GOOS)-$(go env GOARCH)
```

### Configuration (config/web.yaml)

```yaml
agent_url: http://localhost:8080
host: 0.0.0.0
port: 3000
```

Open http://localhost:3000 in your browser.

### Proxy Approval Chain

When Agent A delegates to Agent B via A2A and Agent B encounters a destructive tool:

1. Agent B returns `state: "input-required"` to Agent A via A2A
2. Agent A creates a **proxy approval** and returns `waiting_approval` to the web client via REST
3. When the user approves via REST `POST /approvals/:uuid`, Agent A forwards the approval to Agent B via A2A `message/send` with `taskId`
4. Agent B executes the tool and the result flows back

```
Web → REST API → Agent A → A2A → Agent B → MCP Tool (destructive)
                                     ↓
                          A2A: input-required → Agent A
                                     ↓
                          REST: waiting_approval → Web (approve/reject)
                                     ↓
                          REST: POST /approvals/:uuid → Agent A
                                     ↓
                          A2A: message/send with taskId → Agent B → execute
```

See [`examples/web-chain/`](examples/web-chain/) for a complete working example.

## API Reference

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /docs | Interactive API documentation |
| GET | /tools | List available MCP tools and A2A agents |
| GET | /health | Health check |
| POST | /conversations | Create conversation |
| GET | /conversations | List all conversations |
| GET | /conversations/:id | Get conversation details |
| POST | /conversations/:id/messages | Send message |
| POST | /approvals/:uuid | Resolve approval |
| GET | /.well-known/agent.json | A2A Agent Card (discovery) |
| POST | /a2a | A2A JSON-RPC endpoint (message/send, tasks/get) |

## Examples

The [`examples/`](examples/) directory contains ready-to-use configurations:

| Example | Pattern | Description |
|---------|---------|-------------|
| [simple](examples/simple/) | Single LLM | Basic agent with MCP tools |
| [sequential](examples/sequential/) | Sequential | Analyze then execute pipeline |
| [parallel](examples/parallel/) | Parallel | Gather data concurrently then summarize |
| [loop](examples/loop/) | Loop | Auto-populate resources until threshold |
| [full-pipeline](examples/full-pipeline/) | Combined | Sequential + parallel + multiple LLM stages |
| [web-direct](examples/web-direct/) | Web + REST | Web chat → REST API → agent → destructive MCP tool |
| [web-chain](examples/web-chain/) | Proxy chain | Web → REST → Agent A → A2A → Agent B → destructive MCP tool |

```bash
cp examples/sequential/agent.yaml config/agent.yaml
make run
```

See [`examples/README.md`](examples/README.md) for test prompts and curl commands.

## Testing

```bash
# Unit tests
make test

# E2E tests (requires GEMINI_API_KEY and built binaries)
make e2e
```

## License

MIT
