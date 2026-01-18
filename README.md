# Agent Stop and Go

An API for async autonomous agents with approval workflows. Agents process requests autonomously but can pause and wait for external approval before proceeding with sensitive actions.

## Concept

The API allows you to:
1. Start conversations with an autonomous agent
2. Send messages like a normal chat
3. When the agent needs approval, it stops and returns a UUID
4. The conversation stays stuck until you provide an answer via the approval endpoint
5. All conversations are persisted to JSON files

## Installation

### Prerequisites

- Go 1.23 or later

### Build

```bash
make build
```

### Run

```bash
make run
# or
./bin/agent-stop-and-go-darwin-arm64 --config agent.yaml
```

## Configuration

Edit `agent.yaml`:

```yaml
prompt: |
  You are an autonomous agent that processes user requests.
  Sometimes you need external approval before proceeding.
  When you need approval, respond with a message starting with [APPROVAL_NEEDED]:
  followed by your question or the action that needs approval.

port: 8080
data_dir: ./data
```

## API Endpoints

### Health Check

```bash
GET /health
```

### Conversations

#### Start a new conversation

```bash
POST /conversations
Content-Type: application/json

{
  "message": "Hello!"  # Optional initial message
}
```

#### List all conversations

```bash
GET /conversations
```

Returns conversations grouped by status (active, waiting_approval, completed).

#### Get a specific conversation

```bash
GET /conversations/:id
```

#### Send a message

```bash
POST /conversations/:id/messages
Content-Type: application/json

{
  "message": "Please delete the old files"
}
```

If the agent needs approval, the response includes:
```json
{
  "result": {
    "response": "[APPROVAL_NEEDED]: This action requires approval...",
    "waiting_approval": true,
    "approval": {
      "uuid": "abc-123-def",
      "conversation_id": "...",
      "question": "This action requires approval..."
    }
  }
}
```

### Approvals

#### Respond to a pending approval

```bash
POST /approvals/:uuid
Content-Type: application/json

{
  "answer": "Yes, proceed"
}
```

## Usage Example

```bash
# Start the API
make run &

# Create a conversation
curl -X POST http://localhost:8080/conversations \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello!"}'

# Send a message that triggers approval
curl -X POST http://localhost:8080/conversations/{id}/messages \
  -H "Content-Type: application/json" \
  -d '{"message": "Please delete the cache"}'

# Response shows waiting_approval: true with a UUID

# Resolve the approval
curl -X POST http://localhost:8080/approvals/{uuid} \
  -H "Content-Type: application/json" \
  -d '{"answer": "Yes, approved"}'

# Check conversation status
curl http://localhost:8080/conversations/{id}
```

## Project Structure

```
agent-stop-and-go/
├── agent.yaml                # Agent configuration
├── data/                     # JSON storage for conversations
├── cmd/agent-stop-and-go/
│   └── main.go               # Entry point
└── internal/
    ├── api/                  # Fiber HTTP handlers
    ├── agent/                # Agent logic with approval workflow
    ├── config/               # YAML config loader
    ├── conversation/         # Conversation data model
    └── storage/              # JSON file persistence
```

## Development

```bash
make build      # Build for current platform
make run        # Build and run
make test       # Run tests
make fmt        # Format code
make check      # Run all checks
```

## License

MIT
