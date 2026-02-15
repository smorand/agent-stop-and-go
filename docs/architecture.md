# Architecture

## System Overview

Agent Stop and Go is a modular Go application that connects an LLM to external tools (MCP) and remote agents (A2A), with an approval gateway for destructive operations. The system supports two operating modes: **simple mode** (single LLM with tools) and **orchestrated mode** (tree-based agent pipelines).

## High-Level Architecture

```mermaid
graph TB
    subgraph "Clients"
        WebUI["Web Chat UI<br/>(cmd/web)"]
        REST["REST API Client<br/>(curl, etc.)"]
        RemoteAgent["Remote A2A Agent"]
    end

    subgraph "Agent API (cmd/agent)"
        API["HTTP Handlers<br/>(internal/api)"]
        SID["Session ID<br/>Middleware"]
        Auth["Auth<br/>Middleware"]
    end

    subgraph "Agent Core (internal/agent)"
        Simple["Simple Agent<br/>(single LLM)"]
        Orch["Orchestrator<br/>(agent tree)"]
        State["Session State<br/>(output_key / placeholder)"]
    end

    subgraph "External Services"
        LLM["LLM Providers<br/>Gemini / Claude"]
        MCP["MCP Servers<br/>(Streamable HTTP / stdio)"]
        A2ARemote["A2A Sub-Agents<br/>(HTTPS)"]
    end

    subgraph "Storage"
        JSONFiles["Conversation Files<br/>(JSON on disk)"]
        SQLite["SQLite DB<br/>(MCP resources)"]
    end

    WebUI -->|"REST API"| API
    REST -->|"REST API"| API
    RemoteAgent -->|"A2A JSON-RPC"| API

    API --> SID --> Auth
    Auth --> Simple
    Auth --> Orch

    Simple -->|"GenerateWithTools"| LLM
    Orch -->|"GenerateWithTools"| LLM
    Orch --> State

    Simple -->|"MCP (HTTP/stdio)"| MCP
    Orch -->|"MCP (HTTP/stdio)"| MCP

    Simple -->|"JSON-RPC HTTPS"| A2ARemote
    Orch -->|"JSON-RPC HTTPS"| A2ARemote

    Simple --> JSONFiles
    Orch --> JSONFiles
    MCP --> SQLite
```

## Package Responsibilities

| Package | Path | Responsibility |
|---------|------|----------------|
| `api` | `internal/api/` | Fiber HTTP handlers, route setup, A2A server (JSON-RPC dispatch), interactive HTML docs |
| `agent` | `internal/agent/` | Core agent logic: LLM interaction, MCP tool calls, A2A delegation, orchestration engine with sequential/parallel/loop execution |
| `llm` | `internal/llm/` | Multi-provider LLM interface. `GeminiClient` calls the Google Generative Language API. `ClaudeClient` calls the Anthropic Messages API. Both use a 60-second HTTP timeout. |
| `mcp` | `internal/mcp/` | MCP client with multi-server support. `CompositeClient` aggregates tools from multiple MCP servers (HTTP or stdio) and routes `CallTool` to the correct sub-client. Handles `initialize`, `tools/list`, and `tools/call`. |
| `filesystem` | `internal/filesystem/` | MCP filesystem server implementation. Provides 15 sandboxed filesystem tools with chroot-like security (symlink-aware path validation), per-root tool allowlists, unified diff patching, regex content search (grep), and glob file search. |
| `a2a` | `internal/a2a/` | A2A client: JSON-RPC 2.0 over HTTPS to remote agents. Supports `message/send`, `tasks/get`, and `ContinueTask` (approval forwarding). |
| `auth` | `internal/auth/` | Context-based propagation of Bearer tokens and session IDs. Provides `WithBearerToken()`, `BearerToken()`, `WithSessionID()`, `SessionID()`, and `GenerateSessionID()`. |
| `config` | `internal/config/` | YAML config loader. Parses agent configuration including MCP, LLM, A2A, and orchestration tree settings. Applies defaults for missing fields. |
| `conversation` | `internal/conversation/` | Data models: `Conversation`, `Message`, `ToolCall`, `PendingApproval`, `PipelineState`. Thread-safe message operations via `sync.Mutex`. |
| `storage` | `internal/storage/` | JSON file persistence. Saves/loads conversations, searches by approval UUID. Thread-safe via `sync.RWMutex`. |

## Component Interaction Diagram

```mermaid
sequenceDiagram
    participant C as Client
    participant API as API Handler
    participant AG as Agent
    participant LLM as LLM Provider
    participant MCP as MCP Server
    participant Store as Storage

    C->>API: POST /conversations/:id/messages
    API->>API: Extract Bearer token, Session ID
    API->>AG: ProcessMessage(ctx, conv, msg)
    AG->>LLM: GenerateWithTools(ctx, prompt, messages, tools)
    LLM-->>AG: Response (text or tool_call)

    alt Tool Call (non-destructive)
        AG->>MCP: CallTool(name, args)
        MCP-->>AG: Tool result
        AG->>Store: SaveConversation
        AG-->>API: ProcessResult{response}
    else Tool Call (destructive)
        AG->>Store: SaveConversation (waiting_approval)
        AG-->>API: ProcessResult{waiting_approval, approval_uuid}
        C->>API: POST /approvals/:uuid {approved: true}
        API->>AG: ResolveApproval(ctx, uuid, true)
        AG->>MCP: CallTool(name, args)
        MCP-->>AG: Tool result
        AG->>Store: SaveConversation
        AG-->>API: ProcessResult{response}
    end

    API-->>C: JSON response
```

## Data Models

### Conversation

```mermaid
erDiagram
    CONVERSATION {
        string ID PK "UUID v4"
        string SessionID "8-char hex"
        string Status "active | waiting_approval | completed"
        datetime CreatedAt
        datetime UpdatedAt
    }

    MESSAGE {
        string ID PK "UUID v4"
        string Role "system | user | assistant | tool"
        string Content
        datetime CreatedAt
    }

    TOOL_CALL {
        string Name
        json Arguments
        string Result
        boolean IsError
    }

    PENDING_APPROVAL {
        string UUID PK "UUID v4"
        string ConversationID FK
        string ToolName
        json ToolArgs
        string Description
        string RemoteTaskID "for proxy approvals"
        string RemoteAgentName "for proxy approvals"
        datetime CreatedAt
    }

    PIPELINE_STATE {
        json PausedNodePath "int array path"
        string PausedNodeOutputKey
        json SessionState "key-value map"
        string UserMessage
    }

    CONVERSATION ||--o{ MESSAGE : contains
    MESSAGE ||--o| TOOL_CALL : has
    CONVERSATION ||--o| PENDING_APPROVAL : has
    CONVERSATION ||--o| PIPELINE_STATE : has
```

### MCP Resource (SQLite)

```mermaid
erDiagram
    RESOURCE {
        string id PK "Unix nanosecond timestamp"
        string name "NOT NULL"
        integer value "NOT NULL"
        string created_at "RFC3339"
        string updated_at "RFC3339"
    }
```

## Operating Modes

### Simple Mode (Single LLM)

When the `agent` key is absent from the YAML config (or is a single `llm` node with no children), the agent runs in backward-compatible simple mode:

1. User message is sent to the LLM with all MCP tools and A2A agents as available functions
2. The LLM decides which tool/agent to call (or responds with text)
3. Destructive tools trigger the approval workflow
4. Non-destructive tools execute immediately

```mermaid
flowchart LR
    User -->|message| LLM
    LLM -->|tool_call| Decision{destructive?}
    Decision -->|no| MCP[Execute MCP Tool]
    Decision -->|yes| Approval[Wait for Approval]
    Approval -->|approved| MCP
    Approval -->|rejected| Cancel[Cancel Operation]
    MCP --> Response[Return Result]
    LLM -->|text| Response
```

### Orchestrated Mode (Agent Tree)

When the `agent` key is present in config, the tree-based orchestrator is used. The tree defines a directed acyclic graph of execution nodes.

```mermaid
graph TD
    Root["sequential"]
    A["llm: analyzer<br/>output_key: analysis"]
    B["parallel"]
    C["llm: executor<br/>prompt: {analysis}"]
    D["a2a: validator"]

    Root --> A
    Root --> B
    Root --> C
    B --> D
    B --> E["llm: enricher"]
```

**Node Types:**

| Type | Behavior | Approval Handling |
|------|----------|-------------------|
| `sequential` | Runs children in order | Pauses pipeline, resumes after approval |
| `parallel` | Runs children concurrently | Destructive tools execute immediately |
| `loop` | Repeats children until exit or max iterations | Destructive tools execute immediately |
| `llm` | Calls LLM with MCP + optional A2A tools | Depends on parent context |
| `a2a` | Delegates to remote A2A agent | Depends on parent context |

## Session State and Data Flow

Nodes communicate through a shared `SessionState` map:

1. A node sets `output_key: analysis` to store its output under the key `analysis`
2. A downstream node uses `prompt: "Based on {analysis}"` to read that value
3. Placeholders are resolved at execution time via regex replacement

```mermaid
flowchart LR
    A["Node A<br/>output_key: analysis"] -->|"state.Set('analysis', text)"| State["SessionState<br/>{analysis: '...'}"]
    State -->|"resolveTemplate({analysis})"| B["Node B<br/>prompt: 'Use {analysis}'"]
```

The `SessionState` is thread-safe (protected by `sync.RWMutex`) to support parallel execution.

## A2A Protocol Flow

### Outbound (Client)

A2A agents appear as synthetic tools to the LLM, prefixed with `a2a_`. The LLM naturally selects between MCP tools and A2A agents.

```mermaid
sequenceDiagram
    participant LLM
    participant Agent as Agent Core
    participant A2A as A2A Sub-Agent

    LLM->>Agent: tool_call: a2a_summarizer
    Agent->>A2A: POST /a2a (message/send)
    Note over Agent,A2A: Bearer token + X-Session-ID forwarded
    A2A-->>Agent: Task {state: "completed", artifact: ...}
    Agent-->>LLM: tool result
```

### Inbound (Server)

The agent also exposes itself as an A2A server:

- `GET /.well-known/agent.json` returns the Agent Card (name, skills)
- `POST /a2a` accepts JSON-RPC 2.0 calls (`message/send`, `tasks/get`)
- Task ID = Conversation ID (1:1 mapping)

### Proxy Approval Chain

When Agent A delegates to Agent B, and Agent B encounters a destructive tool:

```mermaid
sequenceDiagram
    participant Client
    participant AgentA as Agent A
    participant AgentB as Agent B
    participant MCP as MCP Server

    Client->>AgentA: POST /conversations/:id/messages
    AgentA->>AgentB: A2A message/send
    AgentB->>AgentB: LLM calls destructive tool
    AgentB-->>AgentA: Task {state: "input-required"}
    AgentA->>AgentA: Create proxy PendingApproval
    AgentA-->>Client: {waiting_approval: true, approval: {uuid: ...}}

    Client->>AgentA: POST /approvals/:uuid {approved: true}
    AgentA->>AgentB: A2A message/send (taskId, "approved")
    AgentB->>MCP: Execute tool
    MCP-->>AgentB: Result
    AgentB-->>AgentA: Task {state: "completed", artifact: ...}
    AgentA-->>Client: Result
```

## Session ID Propagation

Session IDs enable cross-agent request tracing in distributed deployments.

```mermaid
flowchart LR
    A["Incoming Request"] -->|"X-Session-ID header?"| Check{Present?}
    Check -->|yes| Use["Use provided ID"]
    Check -->|no| Gen["Generate 8-char hex<br/>(crypto/rand)"]
    Use --> Store["Store in Conversation.SessionID"]
    Gen --> Store
    Store --> Log["Log as sid= in every request"]
    Store --> Forward["Forward as X-Session-ID<br/>to A2A sub-agents"]
```

## Auth Flow

Bearer tokens flow through the entire agent chain, enabling end-to-end authentication without the agent needing to understand the token content.

```mermaid
flowchart LR
    Client -->|"Authorization: Bearer TOKEN"| API
    API -->|"auth.WithBearerToken(ctx)"| Agent
    Agent -->|"Authorization: Bearer TOKEN"| A2AAgent["A2A Sub-Agent"]
    A2AAgent -->|"Authorization: Bearer TOKEN"| NextAgent["Next Agent..."]
```

## Key Design Decisions

1. **JSON-RPC 2.0 everywhere**: Both MCP (over Streamable HTTP or stdio) and A2A (over HTTPS) use the same protocol format, simplifying the codebase and enabling consistent error handling.

2. **Tools as first-class LLM functions**: A2A agents appear as synthetic tools alongside MCP tools. The LLM makes the routing decision, not the application code.

3. **Approval is a conversation state**: The `PendingApproval` is stored on the `Conversation` object. This keeps the approval workflow stateless from the server's perspective -- the conversation file is the single source of truth.

4. **Pipeline pause/resume via serialized state**: When an orchestrated pipeline pauses for approval, the entire session state and execution path are serialized into `PipelineState`. On resume, the orchestrator fast-forwards to the paused node.

5. **Multi-MCP server support**: Multiple MCP servers (HTTP or stdio) can be configured under `mcp_servers`. A `CompositeClient` aggregates tools from all servers and routes calls to the correct sub-client, keeping tool implementation isolated and language-agnostic.

6. **Lazy LLM client creation**: In orchestrated mode, different nodes can use different LLM models. Clients are created on first use and cached in a thread-safe map, avoiding unnecessary API key validation for unused providers.
