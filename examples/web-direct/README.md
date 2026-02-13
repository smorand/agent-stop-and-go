# Web Direct Example

Web chat → A2A → Agent → destructive MCP tool.

## Architecture

```
Browser (port 3000) → Web App → A2A → Agent (port 8080) → MCP Tools
```

## Setup

```bash
# Terminal 1: Start the agent
export GEMINI_API_KEY=your-key
make build
./bin/agent-$(go env GOOS)-$(go env GOARCH) --config examples/web-direct/agent.yaml

# Terminal 2: Start the web chat
./bin/web-$(go env GOOS)-$(go env GOARCH) --config examples/web-direct/web.yaml

# Or copy configs for default paths:
# cp examples/web-direct/agent.yaml config/agent.yaml
# cp examples/web-direct/web.yaml config/web.yaml
```

## Test

Open http://localhost:3000 in your browser.

### Non-destructive (no approval)

Type: `list all resources`

The agent calls `resources_list` and returns the result immediately.

### Destructive (approval flow)

Type: `add resource my-server with value 42`

1. The agent calls `resources_add` (destructive) → returns "working"
2. Web shows an approval box with Approve/Reject buttons
3. Click **Approve** → Web calls `tasks/approve` → agent executes the tool
4. Result is displayed in the chat

### Test with curl

```bash
# List (no approval)
curl -X POST http://localhost:3000/api/send \
  -H 'Content-Type: application/json' \
  -d '{"message": "list resources"}'

# Add (triggers approval)
curl -X POST http://localhost:3000/api/send \
  -H 'Content-Type: application/json' \
  -d '{"message": "add resource test-1 with value 100"}'
# Note the task ID from the response

# Approve
curl -X POST http://localhost:3000/api/approve \
  -H 'Content-Type: application/json' \
  -d '{"task_id": "TASK_ID_HERE", "approved": true}'
```
