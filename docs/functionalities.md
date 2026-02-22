# Functionalities

This document covers every feature of Agent Stop and Go in detail, including configuration options, behavior, and usage examples.

## Multi-LLM Support

The system supports 6 LLM providers through 3 client implementations, with automatic routing based on model name prefix.

### Provider Routing

| Provider | Model Prefix | Example Models | Env Variable |
|----------|-------------|----------------|-------------|
| Google Gemini | *(default)* | `gemini-2.5-flash`, `gemini-2.5-pro` | `GEMINI_API_KEY` |
| Anthropic Claude | `claude-*` | `claude-sonnet-4-5-20250929` | `ANTHROPIC_API_KEY` |
| OpenAI | `openai-*` | `openai-gpt-4o`, `openai-gpt-4o-mini` | `OPENAI_API_KEY` |
| Mistral | `mistral-*` | `mistral-large-latest`, `mistral-small-latest` | `MISTRAL_API_KEY` |
| Ollama | `ollama-*` | `ollama-llama3`, `ollama-mistral` | *(none)* |
| OpenRouter | `openrouter-*` | `openrouter-anthropic/claude-3-opus` | `OPENROUTER_API_KEY` |

The prefix (including trailing hyphen) is stripped before sending to the API. Example: `openai-gpt-4o` sends `gpt-4o` to the OpenAI API.

### Client Implementations

| Client | Providers | File |
|--------|-----------|------|
| `GeminiClient` | Google Gemini (default fallback) | `internal/llm/gemini.go` |
| `ClaudeClient` | Anthropic Claude | `internal/llm/claude.go` |
| `OpenAICompatibleClient` | OpenAI, Mistral, Ollama, OpenRouter | `internal/llm/openai.go` |

All providers implement the same interface:

```go
type Client interface {
    GenerateWithTools(ctx context.Context, systemPrompt string, messages []Message, tools []mcp.Tool) (*Response, error)
}
```

The `OpenAICompatibleClient` is parameterized by a `providerConfig` containing base URL, API key env var, prefix, and optional custom headers. Adding a new OpenAI-compatible provider requires only adding a new entry to the `providers` registry map.

### Model Configuration

The default model is set at the top level. Individual orchestration nodes can override it:

```yaml
llm:
  model: gemini-2.5-flash  # default for all nodes

agent:
  type: sequential
  agents:
    - name: analyzer
      type: llm
      model: openai-gpt-4o  # overrides default (uses OpenAI)
    - name: local-check
      type: llm
      model: ollama-llama3   # uses local Ollama
    - name: executor
      type: llm
      # inherits gemini-2.5-flash from llm.model
```

### Provider-Specific Behavior

- **Ollama**: No API key required. `Authorization` header is omitted. Base URL configurable via `OLLAMA_BASE_URL` env var (default: `http://localhost:11434/v1`).
- **OpenRouter**: Includes hardcoded `HTTP-Referer: https://github.com/agentic-platform` and `X-Title: Agent Stop and Go` headers.
- **Lazy validation**: Missing API keys don't cause errors at startup. The error occurs on first API call (HTTP 401).

### Timeout

All LLM HTTP clients use a **60-second timeout**.

### Max Tokens

- **Gemini**: No explicit max tokens (uses API default)
- **Claude**: Fixed at 4096 tokens per request
- **OpenAI-compatible**: No explicit max tokens (uses API default)

## MCP Tool Execution

### Multi-Server Architecture

The agent supports connecting to **multiple MCP servers** simultaneously via the `mcp_servers` configuration list. Each server entry has a required `name` field and uses either Streamable HTTP (preferred) or stdio transport.

A `CompositeClient` aggregates tools from all configured servers, checks for duplicate tool names at startup, and routes `CallTool` requests to the correct sub-client based on the tool name.

```yaml
mcp_servers:
  - name: resources
    url: http://localhost:8090/mcp           # Streamable HTTP
  - name: custom-tools
    command: ./bin/my-mcp-server             # stdio subprocess
    args: [--db, ./data/custom.db]
```

### Protocol

MCP tools communicate via JSON-RPC 2.0. Two transports are supported:

- **Streamable HTTP** (preferred): The agent connects to an MCP server running as a standalone HTTP service via the `mcp-go` library
- **stdio** (legacy): The agent launches an MCP server binary as a subprocess and communicates through stdin/stdout pipes

### Lifecycle

**Streamable HTTP transport:**

1. **Connect**: The HTTP client connects to the MCP server URL (with retry: up to 20 attempts, 500ms delay)
2. **Initialize**: The client sends an `initialize` request via the `mcp-go` library
3. **Tool Discovery**: The client calls `ListTools` to load available tools
4. **Tool Execution**: During message processing, the client calls `CallTool` as needed (30s timeout per call)
5. **Stop**: On shutdown, the client closes the connection

**stdio transport:**

1. **Start**: The MCP binary is launched with configured command and args
2. **Initialize**: The agent sends an `initialize` request and `notifications/initialized`
3. **Tool Discovery**: The agent calls `tools/list` to load available tools
4. **Tool Execution**: During message processing, the agent calls `tools/call` as needed
5. **Stop**: On shutdown, the agent closes stdin and kills the process

### Tool Discovery

Each tool returned from `tools/list` has:

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique tool identifier |
| `description` | string | What the tool does (shown to the LLM) |
| `inputSchema` | object | JSON Schema for the tool's parameters |
| `destructiveHint` | boolean | If `true`, triggers the approval workflow |
| `server` | string | Name of the MCP server that provides this tool (set by CompositeClient) |

### Built-in MCP Server: mcp-resources

The included `mcp-resources` binary provides a SQLite-backed resource management tool:

| Tool | Description | destructiveHint | Parameters |
|------|-------------|-----------------|------------|
| `resources_add` | Add a new resource | `true` | `name` (string, required), `value` (integer, required) |
| `resources_remove` | Remove resources by ID or name pattern | `true` | `id` (string), `pattern` (string, regex) |
| `resources_list` | List resources, optionally filtered | `false` | `pattern` (string, regex, optional) |

### Built-in MCP Server: mcp-filesystem

The included `mcp-filesystem` binary provides a sandboxed filesystem server with chroot-like security. Each configured root directory is isolated — symlink-aware path validation prevents escape. Per-root tool allowlists control which operations are permitted.

**Configuration** (`config/mcp-filesystem.yaml`):

```yaml
host: 0.0.0.0
port: 8091
max_full_read_size: 1048576  # 1MB threshold for full reads

roots:
  - name: workspace
    path: ./data/workspace
    allowed_tools:
      - "*"              # all tools allowed
  - name: readonly
    path: ./data/docs
    allowed_tools:
      - read_file
      - list_folder
      - stat_file
      - grep
      - glob
```

**Available tools (15):**

| Tool | Description | Read-only |
|------|-------------|-----------|
| `list_roots` | List configured roots and their allowed tools | yes |
| `list_folder` | List directory contents (name, type, size, mtime) | yes |
| `read_file` | Read file fully or partially (byte/line offsets) | yes |
| `write_file` | Write file (overwrite/append/create_only modes) | no |
| `remove_file` | Delete a single file | no |
| `patch_file` | Apply unified diff patch atomically | no |
| `create_folder` | Create directory with parents (idempotent) | no |
| `remove_folder` | Remove directory recursively | no |
| `stat_file` | File/directory metadata (size, mtime, type) | yes |
| `hash_file` | Compute hash (md5, sha1, sha256) | yes |
| `permissions_file` | Owner, group, permission bits | yes |
| `copy` | Copy file/directory, potentially cross-root | no |
| `move` | Move/rename, potentially cross-root | no |
| `grep` | Regex search in file contents with context lines | yes |
| `glob` | File name search by glob or regex pattern | yes |

**Security features:**
- Symlink-aware path validation using `filepath.EvalSymlinks` + `filepath.Abs`
- Null byte rejection in paths
- Non-existent path resolution walks up to nearest existing ancestor
- Root directory itself cannot be removed
- Binary file detection (null bytes in first 8KB) for read operations

### Custom MCP Servers

Any server that speaks the MCP protocol can be used. Two transport options are available:

**Streamable HTTP (preferred):**

```yaml
mcp_servers:
  - name: my-tools
    url: http://my-mcp-server:8090/mcp
```

The server must support MCP Streamable HTTP transport and implement the standard MCP endpoints (`initialize`, `tools/list`, `tools/call`).

**stdio (legacy):**

```yaml
mcp_servers:
  - name: my-tools
    command: /path/to/my-mcp-server
    args:
      - --flag1
      - value1
```

The binary must implement:

- `initialize` -- return server info and capabilities
- `notifications/initialized` -- accept the notification (no response)
- `tools/list` -- return available tools with schemas and destructiveHint
- `tools/call` -- execute a tool and return the result

**Validation rules:**

- Each server entry must have a unique, non-empty `name`
- Duplicate tool names across servers cause startup failure
- If no servers are configured, the agent runs with no MCP tools (A2A-only mode)

### Thread Safety

The `CompositeClient` uses a `sync.Mutex` to serialize tool map lookups and protect against concurrent access to the tool routing table. Once the target sub-client is identified, the actual `CallTool` invocation is released from the lock and executed on the sub-client directly. Each sub-client (HTTP or stdio) has its own internal mutex for thread safety.

## A2A Protocol

### A2A Client (Outbound)

A2A agents are configured in `config/agent.yaml`. They appear as **synthetic tools** to the LLM with the prefix `a2a_` (e.g., `a2a_summarizer`). The LLM naturally picks between MCP tools and A2A agents based on the task description.

```yaml
a2a:
  - name: summarizer
    url: https://summarizer.example.com
    description: "Summarizes texts"
    destructiveHint: false
  - name: deployer
    url: https://deployer.example.com
    description: "Deploys applications"
    destructiveHint: true  # will require approval
```

**Key behaviors:**

- Bearer tokens from incoming requests are forwarded in the `Authorization` header
- Session IDs are forwarded in the `X-Session-ID` header
- A2A HTTP client uses a 60-second timeout
- The A2A endpoint URL is used directly for `POST` (JSON-RPC) requests

### A2A Server (Inbound)

The agent exposes itself as an A2A-compliant server:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `GET /.well-known/agent.json` | HTTP GET | Returns the Agent Card (name, description, skills) |
| `POST /a2a` | JSON-RPC 2.0 | Accepts `message/send` and `tasks/get` methods |

**Agent Card** -- Skills are derived from the agent's MCP tools and A2A agents:

```json
{
  "name": "resource-manager",
  "description": "A resource management agent...",
  "url": "http://0.0.0.0:8080",
  "skills": [
    {"id": "resources_add", "name": "resources_add", "description": "..."},
    {"id": "resources_list", "name": "resources_list", "description": "..."}
  ]
}
```

**Task ID mapping**: Task ID = Conversation ID. Each A2A task corresponds to exactly one conversation.

### Task States

| State | Meaning |
|-------|---------|
| `completed` | Task finished successfully |
| `input-required` | Task waiting for approval (destructive operation pending) |

### A2A Approval via Message

When a task is in `input-required` state, approval can be sent via `message/send` with `taskId`:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "message/send",
  "params": {
    "taskId": "conversation-uuid",
    "message": {
      "role": "user",
      "parts": [{"type": "text", "text": "approved"}]
    }
  }
}
```

**Recognized approval words**: `yes`, `y`, `true`, `approve`, `approved`, `ok`, `confirm`

## Approval Workflow

### How It Works

1. The LLM calls a tool or A2A agent with `destructiveHint=true`
2. Instead of executing, the system creates a `PendingApproval` with a UUID
3. The conversation status changes to `waiting_approval`
4. The response includes the approval UUID and a description of the pending action
5. The client submits approval or rejection via `POST /approvals/:uuid`
6. On approval: the tool executes and the result is returned
7. On rejection: the operation is cancelled and the conversation returns to `active`

### PendingApproval Structure

| Field | Description |
|-------|-------------|
| `uuid` | Unique approval identifier |
| `conversation_id` | Parent conversation |
| `tool_name` | The tool that was called |
| `tool_args` | Arguments passed to the tool |
| `description` | Human-readable description of the action |
| `remote_task_id` | For proxy approvals: the downstream agent's task ID |
| `remote_agent_name` | For proxy approvals: the downstream agent's name |

### Approval Request Formats

The REST API accepts multiple formats for flexibility:

```json
{"approved": true}
{"action": "approve"}
{"answer": "yes"}
```

Rejection:

```json
{"approved": false}
{"action": "reject"}
{"answer": "no"}
```

### Proxy Approval Chain

When Agent A delegates to Agent B via A2A and Agent B returns `input-required`:

1. Agent A creates a **proxy** `PendingApproval` with `remote_task_id` and `remote_agent_name`
2. When the proxy is approved, Agent A forwards the approval to Agent B via `message/send` with `taskId`
3. Agent B executes the tool and returns the result through the chain

This enables multi-hop approval chains where the human only interacts with the top-level agent.

## Agent Orchestration

The orchestration engine enables complex multi-step agent workflows defined as a tree in YAML configuration.

### When Orchestration Activates

- If the `agent` key is present in config, the tree-based orchestrator is used
- If absent, the agent runs in simple single-LLM mode (backward compatible)
- If `agent` is a single `llm` node with no children, it behaves like simple mode

### Node Types

#### Sequential

Executes children in order. If a child triggers an approval, the **entire pipeline pauses**. After the approval is resolved, execution resumes from the exact child that was paused.

```yaml
agent:
  type: sequential
  agents:
    - name: step1
      type: llm
      output_key: result1
      prompt: "Analyze the request"
    - name: step2
      type: llm
      prompt: "Execute based on {result1}"
```

#### Parallel

Executes all children concurrently. Results are collected in order. **Destructive tools execute immediately** within parallel nodes (no approval pause).

```yaml
agent:
  type: parallel
  agents:
    - name: fetch-data
      type: llm
      output_key: data
    - name: fetch-metadata
      type: llm
      output_key: metadata
```

#### Loop

Repeats children until `exit_loop` is called or `max_iterations` is reached. **Destructive tools execute immediately** within loop nodes. Default safety cap: 10 iterations.

```yaml
agent:
  type: loop
  max_iterations: 5
  agents:
    - name: checker
      type: llm
      can_exit_loop: true
      prompt: "List resources. If 3+ exist, call exit_loop."
```

#### LLM

Sends a prompt to an LLM with MCP tools and optional node-level A2A tools. Supports `output_key`, `can_exit_loop`, and per-node `a2a` tools.

```yaml
- name: analyzer
  type: llm
  model: gemini-2.5-flash
  output_key: analysis
  can_exit_loop: true
  a2a:
    - name: validator
      url: http://validator:8082/a2a
      description: "Validates results"
  prompt: "Analyze: {user_message}"
```

#### A2A

Delegates to a remote A2A agent as a workflow step. The prompt is used as the message template.

```yaml
- name: resource-agent
  type: a2a
  url: http://agent-b:8082/a2a
  prompt: "{analysis}"
  output_key: result
  destructiveHint: false
```

### Node Configuration Reference

| Field | Applicable Types | Description |
|-------|-----------------|-------------|
| `name` | all | Node identifier (required) |
| `type` | all | `llm`, `sequential`, `parallel`, `loop`, `a2a` |
| `agents` | sequential, parallel, loop | Sub-agent list |
| `model` | llm | LLM model name. Defaults to top-level `llm.model` |
| `prompt` | llm, a2a | System prompt or message template with `{placeholders}` |
| `output_key` | llm, a2a | Key to store output in session state |
| `can_exit_loop` | llm | Gives the node an `exit_loop` tool |
| `max_iterations` | loop | Max iterations (default: 10 safety cap) |
| `url` | a2a | Remote agent URL |
| `description` | a2a | Agent description |
| `destructiveHint` | a2a | Requires approval before delegation |
| `a2a` | llm | Per-node A2A tools for LLM decision |

### Session State

Nodes communicate via a shared `SessionState` map:

- **`output_key`**: A node stores its response text under this key
- **`{placeholder}`**: In `prompt`, `{key}` is replaced with the stored value at runtime
- **Thread safety**: `SessionState` uses `sync.RWMutex` for safe parallel access

### Pipeline Pause/Resume

When a sequential pipeline pauses for approval:

1. The current session state is serialized into `PipelineState`
2. The paused node's path (array of child indices) is recorded
3. On resume, the orchestrator fast-forwards to the paused node using the path
4. Execution continues from where it left off

## Conversation Lifecycle

### States

```mermaid
stateDiagram-v2
    [*] --> active: Create conversation
    active --> waiting_approval: Destructive tool called
    waiting_approval --> active: Approval resolved
    active --> completed: Conversation finalized
    active --> active: Non-destructive tool / text response
```

### Persistence

Conversations are stored as individual JSON files:

```
data/
├── conversation_abc123-def456.json
├── conversation_789ghi-jkl012.json
└── ...
```

File naming: `conversation_{uuid}.json`

Each file contains the full conversation state: messages, pending approval, pipeline state, and metadata. Files are written atomically via `os.WriteFile`.

### Message Roles

| Role | Description |
|------|-------------|
| `system` | System prompt (set on conversation creation, not sent to LLM as a message) |
| `user` | User messages and approval decisions |
| `assistant` | Agent responses, tool call descriptions, error messages |
| `tool` | Tool execution results (not sent to LLM) |

## Web Chat Frontend

The web chat (`cmd/web`) provides a browser-based interface for interacting with the agent.

### Architecture

The web frontend is a separate Go binary that proxies requests to the agent's REST API. It does **not** use the A2A protocol (which is reserved for agent-to-agent communication).

```mermaid
flowchart LR
    Browser -->|"HTML/JS"| Web["Web Server<br/>(:3000)"]
    Web -->|"REST API"| Agent["Agent API<br/>(:8080)"]
```

### Configuration

```yaml
# config/web.yaml
agent_url: http://localhost:8080
host: 0.0.0.0
port: 3000
```

### Routes

| Route | Description |
|-------|-------------|
| `GET /` | Chat UI (embedded HTML/CSS/JS) |
| `POST /api/send` | Send message (proxies to agent REST API) |
| `POST /api/approve` | Approve/reject (proxies to `POST /approvals/:uuid`) |
| `GET /api/conversation/:id` | Get conversation state |

## REST API Reference

### Health Check

```
GET /health
```

Response: `{"status": "ok"}`

### List Tools

```
GET /tools
```

Returns all MCP tools and A2A agents available to the agent.

### Create Conversation

```
POST /conversations
Content-Type: application/json

{"message": "optional initial message"}
```

If a message is provided, it is processed immediately. Otherwise, an empty conversation is created.

### List Conversations

```
GET /conversations
```

Returns all conversations with a summary of status counts (active, waiting_approval, completed).

### Get Conversation

```
GET /conversations/:id
```

Returns a single conversation by ID.

### Send Message

```
POST /conversations/:id/messages
Content-Type: application/json

{"message": "list resources"}
```

Processes the user message. May return a direct response or an approval request.

### Resolve Approval

```
POST /approvals/:uuid
Content-Type: application/json

{"approved": true}
```

Approves or rejects a pending destructive action.

### Interactive Documentation

```
GET /docs
```

Returns an interactive HTML page documenting all API endpoints with examples.

## Configuration Reference

### Full Configuration Schema

```yaml
# Agent identity (used in A2A Agent Card)
name: resource-manager          # Default: "agent"
description: "Agent description" # Default: ""

# System prompt (simple mode only; ignored in orchestrated mode)
prompt: |
  Your agent's system prompt here...

# Server settings
host: 0.0.0.0                  # Default: "0.0.0.0"
port: 8080                      # Default: 8080
data_dir: ./data                # Default: "./data"

# LLM settings
llm:
  model: gemini-2.5-flash       # Default: "gemini-2.5-flash"

# MCP servers (optional, one or more)
mcp_servers:
  - name: resources              # Required: unique server name
    url: http://localhost:8090/mcp  # Streamable HTTP (preferred)
  # OR legacy stdio transport:
  # - name: resources
  #   command: ./bin/mcp-resources
  #   args: [--db, ./data/resources.db]

# A2A sub-agents (optional, simple mode)
a2a:
  - name: summarizer
    url: https://summarizer.example.com
    description: "Summarizes texts"
    destructiveHint: false

# Agent tree (optional; presence activates orchestrated mode)
agent:
  name: pipeline
  type: sequential
  agents:
    - name: step1
      type: llm
      output_key: result
      prompt: "Analyze..."
    - name: step2
      type: llm
      prompt: "Execute based on {result}"
```

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `GEMINI_API_KEY` | When using Gemini models (default) | Google Gemini API key |
| `ANTHROPIC_API_KEY` | When using `claude-*` models | Anthropic Claude API key |
| `OPENAI_API_KEY` | When using `openai-*` models | OpenAI API key |
| `MISTRAL_API_KEY` | When using `mistral-*` models | Mistral AI API key |
| `OPENROUTER_API_KEY` | When using `openrouter-*` models | OpenRouter API key |
| `OLLAMA_BASE_URL` | Optional | Ollama endpoint (default: `http://localhost:11434/v1`) |

### Command-Line Flags

| Binary | Flag | Default | Description |
|--------|------|---------|-------------|
| `agent` | `--config` | `config/agent.yaml` | Path to agent configuration file |
| `web` | `--config` | `config/web.yaml` | Path to web configuration file |
| `mcp-resources` | `--db` | `./resources.db` | Path to SQLite database |
| `mcp-filesystem` | `--config` | (required) | Path to YAML config file |
| `mcp-filesystem` | `--port` | (from config) | Override listen port |
