# Web Chain Example

Web chat → A2A → Agent A → A2A → Agent B → destructive MCP tool.

This demonstrates proxy approval chain: when Agent B needs approval, the "working" state propagates back through Agent A to the Web UI.

## Architecture

```
Browser (port 3000) → Web App → A2A → Agent A (port 8080) → A2A → Agent B (port 8082) → MCP Tools
```

- **Agent B** (port 8082): Has MCP tools with `destructiveHint`. Handles actual resource operations.
- **Agent A** (port 8080): Orchestrator that delegates to Agent B via A2A. `destructiveHint: false` on the A2A config — Agent A doesn't know B's tools are destructive.
- **Web App** (port 3000): Chat UI connecting to Agent A via A2A.

## Setup

```bash
export GEMINI_API_KEY=your-key
make build

# Terminal 1: Start Agent B (backend with tools)
./bin/agent-$(go env GOOS)-$(go env GOARCH) --config examples/web-chain/agent-b.yaml

# Terminal 2: Start Agent A (orchestrator)
./bin/agent-$(go env GOOS)-$(go env GOARCH) --config examples/web-chain/agent-a.yaml

# Terminal 3: Start the web chat
./bin/web-$(go env GOOS)-$(go env GOARCH) --config examples/web-chain/web.yaml

# Or use Docker Compose (uses config/ directory):
# docker-compose up --build
```

## Test

Open http://localhost:3000 in your browser.

### Proxy approval chain

Type: `add resource chain-test with value 99`

1. Web sends message to Agent A
2. Agent A delegates to Agent B via A2A
3. Agent B encounters `resources_add` (destructive) → returns "working" to Agent A
4. Agent A sees "working" → creates proxy approval → returns "working" to Web
5. Web shows approval box
6. User clicks **Approve**
7. Web calls `tasks/approve` on Agent A
8. Agent A forwards `tasks/approve` to Agent B
9. Agent B executes the tool → returns "completed" to Agent A
10. Agent A returns result to Web
11. Chat shows the created resource

### Test with curl

```bash
# Start the chain
curl -X POST http://localhost:3000/api/send \
  -H 'Content-Type: application/json' \
  -d '{"message": "add resource chain-test with value 99"}'
# Note the task ID

# Approve (forwarded through the chain)
curl -X POST http://localhost:3000/api/approve \
  -H 'Content-Type: application/json' \
  -d '{"task_id": "TASK_ID_HERE", "approved": true}'
```
