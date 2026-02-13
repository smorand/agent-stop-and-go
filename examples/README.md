# Agent Stop and Go - Examples

Example configurations demonstrating different orchestration patterns. Each example has its own `agent.yaml` that can replace the root config.

## Prerequisites

```bash
export GEMINI_API_KEY=your-api-key
make build
```

## Running an Example

Copy the example config and start the agent:

```bash
cp examples/sequential/agent.yaml config/agent.yaml
make run
```

## Examples

### 1. Simple (single LLM)

**Pattern**: Single LLM node with MCP tools (backward-compatible mode).

**Config**: [`simple/agent.yaml`](simple/agent.yaml)

**Test prompts**:
```bash
# Create a conversation
CONV=$(curl -s -X POST http://localhost:8080/conversations | jq -r '.conversation.id')

# Non-destructive: list resources
curl -s -X POST http://localhost:8080/conversations/$CONV/messages \
  -H "Content-Type: application/json" \
  -d '{"message": "list all resources"}' | jq .

# Destructive: add resource (will require approval)
curl -s -X POST http://localhost:8080/conversations/$CONV/messages \
  -H "Content-Type: application/json" \
  -d '{"message": "add resource server-1 with value 42"}' | jq .

# Approve (replace UUID with actual value from response)
curl -s -X POST http://localhost:8080/approvals/APPROVAL_UUID \
  -H "Content-Type: application/json" \
  -d '{"approved": true}' | jq .
```

---

### 2. Sequential Pipeline

**Pattern**: Two LLM nodes in sequence - analyze then execute.

**Config**: [`sequential/agent.yaml`](sequential/agent.yaml)

**Data flow**: `user message` -> `analyzer` (output_key: analysis) -> `executor` (reads `{analysis}`)

**Test prompts**:
```bash
CONV=$(curl -s -X POST http://localhost:8080/conversations | jq -r '.conversation.id')

# The analyzer will classify, then the executor will act
curl -s -X POST http://localhost:8080/conversations/$CONV/messages \
  -H "Content-Type: application/json" \
  -d '{"message": "show me all resources"}' | jq .

# Destructive action through sequential pipeline (needs approval)
curl -s -X POST http://localhost:8080/conversations/$CONV/messages \
  -H "Content-Type: application/json" \
  -d '{"message": "add a new resource called test-item with value 99"}' | jq .
```

---

### 3. Parallel Gathering

**Pattern**: Two LLM nodes run in parallel, then a summarizer combines results.

**Config**: [`parallel/agent.yaml`](parallel/agent.yaml)

**Data flow**: `list-all` + `count-analysis` (parallel) -> `summarizer` (reads `{all_resources}` + `{count_info}`)

**Test prompts**:
```bash
CONV=$(curl -s -X POST http://localhost:8080/conversations | jq -r '.conversation.id')

# Both parallel nodes will call resources_list simultaneously
curl -s -X POST http://localhost:8080/conversations/$CONV/messages \
  -H "Content-Type: application/json" \
  -d '{"message": "give me a full overview of all resources"}' | jq .
```

---

### 4. Loop Processing

**Pattern**: LLM node runs in a loop, adding resources until 3+ exist, then exits.

**Config**: [`loop/agent.yaml`](loop/agent.yaml)

**Behavior**: The checker lists resources, adds one if fewer than 3 exist, and calls `exit_loop` when 3+ resources are found. Max 5 iterations as safety cap.

**Test prompts**:
```bash
# Start with a clean database for best results
rm -f data/resources.db

CONV=$(curl -s -X POST http://localhost:8080/conversations | jq -r '.conversation.id')

# The loop will auto-populate resources until threshold is met
curl -s -X POST http://localhost:8080/conversations/$CONV/messages \
  -H "Content-Type: application/json" \
  -d '{"message": "ensure we have at least 3 resources"}' | jq .
```

**Note**: In loop mode, destructive tools (`resources_add`) execute immediately without approval to avoid blocking the loop.

---

### 5. Full Pipeline

**Pattern**: Complex 4-step sequential pipeline with nested parallel gathering.

**Config**: [`full-pipeline/agent.yaml`](full-pipeline/agent.yaml)

**Data flow**:
```
intent-analyzer (output: intent)
    |
    v
parallel-gather:
  ├── current-state (output: current_resources)
  └── request-enricher (reads {intent}, output: enriched_request)
    |
    v
executor (reads {intent}, {current_resources}, {enriched_request}, output: execution_result)
    |
    v
response-formatter (reads {intent}, {execution_result})
```

**Test prompts**:
```bash
CONV=$(curl -s -X POST http://localhost:8080/conversations | jq -r '.conversation.id')

# Query intent - will flow through all pipeline stages
curl -s -X POST http://localhost:8080/conversations/$CONV/messages \
  -H "Content-Type: application/json" \
  -d '{"message": "what resources do we have?"}' | jq .

# Mutate intent - executor stage will trigger approval
CONV2=$(curl -s -X POST http://localhost:8080/conversations | jq -r '.conversation.id')
curl -s -X POST http://localhost:8080/conversations/$CONV2/messages \
  -H "Content-Type: application/json" \
  -d '{"message": "add a new resource called pipeline-test with value 123"}' | jq .
```

## Key Concepts

| Concept | Description |
|---------|-------------|
| `output_key` | Stores a node's output in session state under this key |
| `{placeholder}` | In `prompt`, replaced with value from session state |
| `can_exit_loop` | Gives the LLM node an `exit_loop` tool to break out of loops |
| `max_iterations` | Safety cap for loop execution (default: 10) |
| `allowDestructive` | In parallel/loop, destructive tools run without approval |
| `type: a2a` | Workflow node that always delegates to a remote A2A agent |
