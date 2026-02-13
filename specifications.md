# Agent Stop and Go - Specifications

> **Version**: 1.0.0
> **Last Updated**: 2026-01-18
> **Status**: Draft
> **Owner**: Sebastien MORAND

---

## 1. Overview

**Summary**: A generic API for building async autonomous agents that can orchestrate tools (MCP) and sub-agents (A2A) with a human-in-the-loop approval workflow for destructive operations.

**Purpose**: Platform teams need a reusable foundation to deploy autonomous agents that can interact with external tools and other agents, while maintaining control over destructive actions through an approval gate. The agent is fully configurable via a single YAML file (prompt, LLM model, tool servers, sub-agents).

### Target Users

| User Type | Description | Primary Needs |
|-----------|-------------|---------------|
| Platform Engineers | Teams building agent orchestration systems | Reusable agent framework with configurable tools and approval workflows |
| Agent Developers | Developers creating MCP servers or A2A agents | Standard protocol compliance (MCP, A2A) to plug into the agent |
| Operations Teams | Teams managing deployed agents | Visibility into pending approvals, conversation state, and agent activity |

---

## 2. Problem & Scope

### 2.1 Pain Points

1. **No approval gate for autonomous agents**: Agents execute destructive actions without human review, leading to unintended consequences
2. **Agent orchestration fragmentation**: Each agent implementation reinvents tool integration, conversation management, and approval workflows
3. **No standard for sub-agent delegation**: Agents cannot delegate tasks to other agents using an open protocol while preserving authorization context

### 2.2 Goals

1. **Configurable agent**: A single `agent.yaml` file fully defines the agent behavior (prompt, LLM, tools, sub-agents)
2. **Approval workflow**: Destructive tool calls are gated by a human approval step, with the agent pausing until resolution
3. **Multi-protocol orchestration**: Support both MCP (tool servers) and A2A (sub-agents) as configurable backends
4. **Zero Trust authorization**: Forward `Authorization: Bearer` tokens to all sub-components (MCP servers, A2A agents)
5. **Container-ready**: Deployable as a Docker container with no external dependencies beyond the LLM API

### 2.3 Non-Goals

1. **Web UI / Dashboard**: No frontend — API only, consumers build their own UI
2. **Multi-tenancy**: Single agent instance per deployment, no user isolation
3. ~~**LLM provider abstraction**: Only Gemini (generativelanguage API) is supported; no multi-provider LLM layer~~ **Done** (multi-provider: Gemini + Claude via `LLMClient` interface)
4. **Tool server hosting**: The API does not host or manage MCP/A2A servers; it connects to them

### 2.4 Future Considerations

- ~~Agent chaining (one agent's output feeds another agent's input)~~ **Done** (sequential + output_key)
- Webhook notifications for approval events
- Conversation expiration and automatic cleanup
- ~~Multi-LLM routing (different models for different tasks)~~ **Done** (per-node model in agent tree)

---

## 3. Functional Requirements

### 3.1 Configuration

#### FR-001: Single-file agent configuration

- **Description**: The agent must be fully configured from a single YAML file defining prompt, LLM model, host/port, data directory, tool servers, and sub-agents. Configuration files are centralized in the `config/` directory.
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Agent loads `config/agent.yaml` at startup by default
  - [ ] All configurable fields have sensible defaults
  - [ ] Missing required fields produce clear error messages at startup
  - [ ] Changing the YAML and restarting produces a different agent behavior

#### FR-002: Environment-based secrets

- **Description**: Sensitive values (API keys) must be provided via environment variables, never in the configuration file
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] `GEMINI_API_KEY` is read from environment for Gemini models
  - [ ] `ANTHROPIC_API_KEY` is read from environment for Claude models
  - [ ] Missing API key produces a clear error at startup
  - [ ] API keys are never logged or returned in API responses

### 3.2 Conversations

#### FR-003: Create conversation

- **Description**: Users can create a new conversation, optionally with an initial message
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] POST creates a new conversation with a unique UUID
  - [ ] System prompt from config is added as the first message
  - [ ] Optional initial message is processed immediately
  - [ ] Conversation is persisted to storage

#### FR-004: Send message

- **Description**: Users can send messages within an existing conversation, triggering the LLM to determine the next action
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Message is added to conversation history
  - [ ] Full conversation history is sent to the LLM with available tool schemas
  - [ ] LLM response is either text or a tool/agent call
  - [ ] Sending a message to a conversation in `waiting_approval` status returns an error with the pending approval details

#### FR-005: List and retrieve conversations

- **Description**: Users can list all conversations and retrieve a specific conversation by ID
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] List returns all conversations with status summary (active, waiting_approval, completed)
  - [ ] Get by ID returns the full conversation including all messages and pending approval

#### FR-006: Conversation persistence

- **Description**: Conversations must be persisted to survive server restarts
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Conversations are saved after every state change
  - [ ] Conversations are loadable after server restart
  - [ ] Pending approvals survive server restart with tool arguments intact

### 3.3 LLM Integration

#### FR-007: LLM-based intent parsing with function calling

- **Description**: The agent must use an LLM to interpret user messages and determine which tool or sub-agent to invoke, including parameter extraction
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] LLM receives the system prompt, conversation history, and available tool schemas
  - [ ] LLM can return either a text response or a function call with arguments
  - [ ] Natural language variations (e.g., "create", "add", "make") are correctly mapped to tools
  - [ ] LLM errors are handled gracefully and reported in the conversation

#### FR-008: Configurable LLM model (multi-provider)

- **Description**: The LLM model name must be configurable in `agent.yaml`, with automatic provider detection based on model name prefix
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] `llm.model` field in YAML selects the LLM model
  - [ ] Models starting with `claude-` use the Anthropic Messages API with `ANTHROPIC_API_KEY`
  - [ ] All other models use the Gemini generativelanguage API with `GEMINI_API_KEY`
  - [ ] Default model is `gemini-2.5-flash` if not specified

### 3.4 MCP Tool Execution

#### FR-009: MCP server lifecycle

- **Description**: The agent must start and manage an MCP server as a subprocess, communicating via JSON-RPC 2.0 over stdio
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] MCP server is started on agent startup using configured command and args
  - [ ] MCP server is stopped on agent shutdown
  - [ ] Tool list is retrieved via `tools/list` after initialization
  - [ ] Tools are callable via `tools/call`

#### FR-010: Tool discovery and exposure

- **Description**: Available tools from the MCP server must be discoverable via the API
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] GET endpoint returns all available tools with their schemas and destructiveHint
  - [ ] Tool schemas are converted to LLM function declarations for function calling

#### FR-011: Approval workflow for destructive tools

- **Description**: Tools marked with `destructiveHint: true` must require explicit human approval before execution
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Destructive tool calls create a PendingApproval with a UUID
  - [ ] Conversation status changes to `waiting_approval`
  - [ ] Tool arguments are preserved in the PendingApproval
  - [ ] Approval executes the tool with the original arguments (no re-generation)
  - [ ] Rejection cancels the tool call and resumes the conversation
  - [ ] Multiple approval formats are supported: `{"approved": true}`, `{"action": "approve"}`, `{"answer": "yes"}`

#### FR-012: Non-destructive tool execution

- **Description**: Tools without `destructiveHint` (or set to false) must execute immediately without approval
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Tool is called via MCP immediately after LLM selects it
  - [ ] Result is returned in the same response

### 3.5 A2A Server

#### FR-018: A2A Agent Card endpoint

- **Description**: The agent must expose an Agent Card at `/.well-known/agent.json` so other A2A agents can discover it
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] GET `/.well-known/agent.json` returns a valid Agent Card
  - [ ] Card contains agent name and description from configuration
  - [ ] Card contains skills dynamically built from available MCP tools
  - [ ] Card contains the agent's URL

#### FR-019: A2A message/send server

- **Description**: The agent must accept `message/send` JSON-RPC 2.0 requests via POST `/a2a`, creating a conversation and processing the message
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Accepts JSON-RPC 2.0 requests with method `message/send`
  - [ ] Extracts text from message parts
  - [ ] Creates a conversation, processes the message, and returns an A2A Task
  - [ ] Non-destructive requests return a task with state `completed`
  - [ ] Destructive requests return a task with state `input-required` and approval info in the status message

#### FR-020: A2A tasks/get server

- **Description**: The agent must accept `tasks/get` JSON-RPC 2.0 requests to retrieve task state by conversation ID
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Accepts JSON-RPC 2.0 requests with method `tasks/get`
  - [ ] Maps conversation ID to task ID
  - [ ] Returns current task state based on conversation status
  - [ ] Unknown method returns JSON-RPC error `-32601`

### 3.6 Agent Orchestration

#### FR-021: Agent tree configuration

- **Description**: The agent must support a tree-based orchestration model defined in `agent.yaml` under the `agent` key, with node types: `llm`, `sequential`, `parallel`, `loop`, `a2a`
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] `agent` key in YAML defines a tree of agent nodes
  - [ ] When `agent` key is absent, a default single LLM node is synthesized from top-level fields (backward compatible)
  - [ ] Each node has a `type`, `name`, and type-specific fields

#### FR-022: Sequential execution

- **Description**: Sequential nodes must execute sub-agents in order, passing data via session state
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Sub-agents execute one at a time in order
  - [ ] `output_key` stores each node's result in session state
  - [ ] `{placeholder}` in prompts resolves to session state values
  - [ ] Approval pauses propagate up and resume correctly after approval

#### FR-023: Parallel execution

- **Description**: Parallel nodes must execute sub-agents concurrently
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Sub-agents execute concurrently via goroutines
  - [ ] Results from all branches are collected and combined
  - [ ] MCP calls are serialized for subprocess safety
  - [ ] Destructive tools execute immediately without approval (no pipeline pause)

#### FR-024: Loop execution

- **Description**: Loop nodes must repeatedly execute sub-agents until an exit condition or max iterations
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Sub-agents execute repeatedly up to `max_iterations` (default: 10 safety cap)
  - [ ] LLM nodes with `can_exit_loop: true` receive an `exit_loop` tool
  - [ ] Calling `exit_loop` immediately stops the loop
  - [ ] Destructive tools execute immediately without approval inside loops

#### FR-025: Session state and data flow

- **Description**: Agent nodes must be able to pass data between each other via a shared session state
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] `output_key` on a node stores its response text in session state
  - [ ] `{key}` placeholders in `prompt` fields are resolved from session state
  - [ ] Session state is thread-safe for parallel execution
  - [ ] Session state is saved in `PipelineState` when approval pauses the pipeline

#### FR-026: Pipeline pause and resume

- **Description**: When a destructive tool is encountered in a sequential pipeline, the pipeline must pause and resume after approval
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Pipeline state (node path, session state, user message) is persisted to the conversation
  - [ ] After approval, the pipeline resumes from the paused node
  - [ ] Remaining sequential steps execute after resume
  - [ ] Rejection cancels the pipeline

### 3.7 Web Chat Frontend

#### FR-027: Web chat application

- **Description**: A web chat application that connects to an agent via the REST API, providing a browser-based interface for conversing with agents and handling approvals. The A2A protocol is reserved for agent-to-agent communication only.
- **Priority**: Should Have
- **Acceptance Criteria**:
  - [ ] `cmd/web` binary serves an HTML chat UI on a configurable port
  - [ ] Configurable via `web.yaml` with `agent_url` (REST API base URL), `host`, and `port`
  - [ ] Messages are sent via REST `POST /conversations` and `POST /conversations/:id/messages`
  - [ ] Approval actions are sent via REST `POST /approvals/:uuid`
  - [ ] Conversation status is retrieved via REST `GET /conversations/:id`
  - [ ] UI shows approval buttons when conversation has `waiting_approval` status
  - [ ] Multi-turn conversations are supported within the same session

#### FR-028: A2A task continuation via message/send with taskId

- **Description**: The A2A server must support continuing existing tasks via `message/send` with a `taskId` parameter, following the standard A2A protocol for `input-required` state resolution
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] `message/send` with `taskId` continues an existing task (conversation)
  - [ ] When conversation is in `waiting_approval`, message text is interpreted as approval ("approved", "yes") or rejection ("rejected", "no")
  - [ ] When conversation is active, message is processed normally
  - [ ] Returns the updated task with current state

#### FR-029: Proxy approval chain

- **Description**: When an A2A sub-agent returns a "working" state (needs approval), the parent agent must create a proxy approval and propagate the approval chain
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] When `message/send` to a sub-agent returns `state: "input-required"`, parent creates a proxy `PendingApproval`
  - [ ] Proxy approval stores `remote_task_id` and `remote_agent_name`
  - [ ] When the proxy approval is resolved, the parent forwards `message/send` with `taskId` to the sub-agent
  - [ ] Result from the sub-agent flows back through the chain
  - [ ] Works in both simple agent mode and orchestrated pipeline mode

### 3.10 Observability

#### FR-030: Session ID propagation

- **Description**: Each conversation must have an 8-char hex session ID generated at the entry point agent, stored in the conversation, and forwarded to downstream A2A agents via the `X-Session-ID` header for cross-agent request tracing
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Session ID is generated from `crypto/rand` (4 bytes → 8 hex chars) when no `X-Session-ID` header is present
  - [ ] Incoming `X-Session-ID` header is extracted and reused (A2A inbound)
  - [ ] Session ID is stored in the `Conversation.SessionID` field and persisted to JSON
  - [ ] Session ID is forwarded as `X-Session-ID` header on all A2A outbound calls (next to Bearer token forwarding)
  - [ ] Fiber request logger includes `sid=` for every request

### 3.8 A2A Sub-Agent Delegation

#### FR-013: A2A agent configuration (client)

- **Description**: Sub-agents using the A2A protocol must be configurable in `agent.yaml` alongside MCP tools
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] `a2a` section in YAML defines sub-agent endpoints and capabilities
  - [ ] Each sub-agent has a name, URL, and optional description
  - [ ] Sub-agents are discoverable alongside MCP tools
  - [ ] LLM can choose between MCP tools and A2A agents based on the task

#### FR-014: A2A task delegation

- **Description**: The agent must be able to delegate tasks to sub-agents using the A2A protocol (JSON-RPC 2.0 over HTTPS)
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Agent can send tasks to A2A sub-agents
  - [ ] Agent can receive task results from A2A sub-agents
  - [ ] A2A agent discovery uses Agent Cards
  - [ ] Sub-agent tasks are tracked in the conversation history

#### FR-015: A2A destructive hint support

- **Description**: A2A sub-agent calls can be marked as destructive and require approval, similar to MCP tools
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] A2A sub-agents can be marked as destructive in configuration
  - [ ] Destructive A2A calls follow the same approval workflow as MCP tools

### 3.8 Zero Trust Authorization

#### FR-016: Authorization header forwarding

- **Description**: When an `Authorization: Bearer` header is present on incoming requests, it must be forwarded to all sub-components (MCP servers, A2A agents)
- **Priority**: Must Have
- **Acceptance Criteria**:
  - [ ] Bearer token from incoming request is extracted and stored in conversation context
  - [ ] Token is forwarded as `Authorization: Bearer` header to A2A sub-agent HTTP calls
  - [ ] Token is available to MCP tools that support HTTP-based communication
  - [ ] Absence of token does not block requests (token is optional but forwarded when present)

### 3.9 API Documentation

#### FR-017: Interactive API documentation

- **Description**: The API must provide a self-documenting HTML page listing all routes, methods, and payload formats
- **Priority**: Should Have
- **Acceptance Criteria**:
  - [ ] GET /docs returns an interactive HTML page
  - [ ] All routes are documented with request/response examples

---

## 4. Non-Functional Requirements

### 4.1 Performance

#### NFR-001: API response time

- **Description**: API responses (excluding LLM latency) must be fast
- **Target**: < 50ms for non-LLM operations (health, list, get conversation)
- **Measurement**: Response time measured at the API handler level
- **Priority**: Must Have

#### NFR-002: LLM call timeout

- **Description**: LLM calls must have a reasonable timeout to avoid hanging conversations
- **Target**: 60 seconds maximum per LLM call
- **Measurement**: HTTP client timeout on Gemini API calls
- **Priority**: Must Have

### 4.2 Scalability

#### NFR-003: Single-instance design

- **Description**: The system operates as a single instance; horizontal scaling is not required
- **Target**: Support up to 100 concurrent conversations per instance
- **Measurement**: Load test with 100 concurrent conversation operations
- **Priority**: Should Have

### 4.3 Security

#### NFR-004: Zero Trust token propagation

- **Description**: Authorization tokens must be propagated to all downstream calls without modification
- **Target**: 100% token forwarding compliance for A2A calls
- **Measurement**: E2E test verifying token presence in sub-agent requests
- **Priority**: Must Have

#### NFR-005: Secret protection

- **Description**: API keys and tokens must never appear in logs, responses, or persisted data
- **Target**: Zero exposure of secrets
- **Measurement**: Grep for key patterns in logs and stored files
- **Priority**: Must Have

### 4.4 Reliability

#### NFR-006: Approval persistence

- **Description**: Pending approvals must survive server restarts
- **Target**: 100% approval recovery after restart
- **Measurement**: E2E test: create approval, restart server, resolve approval
- **Priority**: Must Have

### 4.5 Observability

#### NFR-007: Structured logging

- **Description**: The API must produce structured logs for request tracing
- **Target**: Log level, timestamp, request path, conversation ID, and session ID (`sid=`) for each request
- **Measurement**: Log output inspection
- **Priority**: Should Have

### 4.6 Debugging

#### NFR-008: Conversation traceability

- **Description**: Every conversation must contain a complete trace of messages, tool calls, results, and approvals
- **Target**: Full history available via GET /conversations/:id
- **Measurement**: E2E test verifying all events are recorded
- **Priority**: Must Have

### 4.7 Deployment

#### NFR-009: Docker containerization and Docker Compose

- **Description**: The application must be deployable as a Docker container (single agent) or via Docker Compose (multi-agent chain with orchestrator, resource agent, and web frontend)
- **Target**: Single Dockerfile producing a working container; Docker Compose for multi-agent deployment
- **Measurement**: `docker build && docker run` succeeds; `docker-compose up --build` starts all 3 services
- **Priority**: Must Have

#### NFR-010: Multi-platform binary

- **Description**: Binaries must be buildable for linux/amd64, darwin/amd64, darwin/arm64
- **Target**: `make build-all` produces all platform binaries
- **Measurement**: Build output verification
- **Priority**: Must Have

### 4.8 Resilience

#### NFR-011: MCP server crash recovery

- **Description**: If the MCP server subprocess crashes, the agent must report the error gracefully
- **Target**: Error is reported in the conversation, no panic or hang
- **Measurement**: E2E test with a crashing MCP server
- **Priority**: Should Have

### 4.9 Maintainability

#### NFR-012: Modular architecture

- **Description**: The codebase must separate concerns into distinct packages (API, agent, LLM, MCP, A2A, config, storage)
- **Target**: Each package has a single responsibility
- **Measurement**: Code review and dependency analysis
- **Priority**: Must Have

### 4.10 Compatibility

#### NFR-013: MCP protocol compliance

- **Description**: MCP communication must follow JSON-RPC 2.0 over stdio
- **Target**: Compatible with any MCP server implementing the standard
- **Measurement**: E2E test with a different MCP server
- **Priority**: Must Have

#### NFR-014: A2A protocol compliance

- **Description**: A2A communication must follow the Agent2Agent protocol specification
- **Target**: Compatible with any A2A-compliant agent
- **Measurement**: E2E test with a standard A2A agent
- **Priority**: Must Have

### 4.11 Cross-Agent Observability

#### NFR-015: Session ID in logs for cross-agent correlation

- **Description**: All agents in a multi-agent setup must log the same session ID for requests belonging to the same user interaction, enabling log correlation across `agent-a.log` and `agent-b.log`
- **Target**: Same `sid=` value visible in both agent logs for a single user request chain
- **Measurement**: Docker Compose logs inspection after cross-agent request
- **Priority**: Should Have

---

## 5. Tests

### 5.1 Coverage Summary

| Requirement | Test Scenario(s) | Status |
|-------------|-------------------|--------|
| FR-001 | TS-001 | Covered |
| FR-003 | TS-002 | Covered |
| FR-004 | TS-003, TS-004 | Covered |
| FR-006 | TS-005 | Covered |
| FR-007 | TS-003, TS-006 | Covered |
| FR-011 | TS-004, TS-007, TS-008 | Covered |
| FR-012 | TS-003 | Covered |
| FR-013, FR-014 | TS-009 | Covered |
| FR-016 | TS-010 | Covered |
| FR-018 | TS-011 | Covered |
| FR-019 | TS-012, TS-013 | Covered |
| FR-020 | TS-014 | Covered |
| FR-021 | TS-015 | Covered |
| FR-022, FR-025 | TS-016, TS-022, TS-023 | Covered |
| FR-023 | TS-017, TS-024 | Covered |
| FR-024 | TS-018, TS-025 | Covered |
| FR-026 | TS-019, TS-023, TS-027 | Covered |
| FR-027 | TS-020 | Manual |
| FR-028 | TS-020 | Covered |
| FR-029 | TS-021, TS-027 | Covered |
| FR-014, FR-015 | TS-026, TS-027 | Covered |
| FR-030 | TS-026, TS-027 | Covered |
| NFR-006 | TS-005 | Covered |

### 5.2 Core Workflows

#### TS-001: Server startup with configuration

- **Description**: Verify the server starts correctly with a valid agent.yaml
- **Type**: Automated
- **Preconditions**: Valid agent.yaml, GEMINI_API_KEY set, MCP binary built
- **Steps**:
  1. Start the server
  2. Call GET /health
  3. Call GET /tools
- **Expected Results**:
  - [ ] Server starts without errors
  - [ ] Health returns `{"status": "ok"}`
  - [ ] Tools endpoint returns the MCP tool list with destructiveHint properties
- **Validates**: FR-001, FR-002, FR-009, FR-010
- **Priority**: Critical

#### TS-002: Create conversation

- **Description**: Verify conversation creation with and without initial message
- **Type**: Automated
- **Preconditions**: Server running
- **Steps**:
  1. POST /conversations with no body
  2. POST /conversations with `{"message": "list resources"}`
- **Expected Results**:
  - [ ] First call returns a conversation with system prompt and status "active"
  - [ ] Second call returns a conversation with system prompt, user message, and tool result
- **Validates**: FR-003
- **Priority**: Critical

#### TS-003: Non-destructive tool execution (list)

- **Description**: Verify that safe tools execute immediately without approval
- **Type**: Automated
- **Preconditions**: Server running, conversation created
- **Steps**:
  1. Send message "list all resources" to conversation
- **Expected Results**:
  - [ ] Response contains tool result with resource data
  - [ ] `waiting_approval` is false
  - [ ] Conversation status remains "active"
  - [ ] Tool call and result are recorded in conversation messages
- **Validates**: FR-004, FR-007, FR-012
- **Priority**: Critical

#### TS-004: Destructive tool with approval (add)

- **Description**: Verify that destructive tools require approval and execute on approval
- **Type**: Automated
- **Preconditions**: Server running, conversation created
- **Steps**:
  1. Send message "create a server my-test with value 42"
  2. Note the approval UUID
  3. POST /approvals/{uuid} with `{"answer": "yes"}`
- **Expected Results**:
  - [ ] Step 1 returns `waiting_approval: true` with approval UUID
  - [ ] Conversation status is "waiting_approval"
  - [ ] Step 3 executes the tool and returns the created resource
  - [ ] Conversation status returns to "active"
- **Validates**: FR-004, FR-007, FR-011
- **Priority**: Critical

### 5.3 Error & Edge Case Scenarios

#### TS-005: Approval persistence across restart

- **Description**: Verify pending approvals survive server restart
- **Type**: Automated
- **Preconditions**: Server running
- **Steps**:
  1. Create conversation and trigger a destructive tool
  2. Note the approval UUID
  3. Stop the server
  4. Restart the server
  5. POST /approvals/{uuid} with `{"approved": true}`
- **Expected Results**:
  - [ ] Approval is found after restart
  - [ ] Tool executes successfully with original arguments
- **Validates**: FR-006, NFR-006
- **Priority**: High

#### TS-006: Natural language variations

- **Description**: Verify the LLM handles various phrasings for the same intent
- **Type**: Automated
- **Preconditions**: Server running
- **Steps**:
  1. Send "list"
  2. Send "show me all resources"
  3. Send "create a server test-1 with value 10"
  4. Send "add resource test-2 value 20"
  5. Send "remove test-1"
  6. Send "delete resource test-2"
- **Expected Results**:
  - [ ] Steps 1-2 trigger `resources_list`
  - [ ] Steps 3-4 trigger `resources_add` with correct name and value
  - [ ] Steps 5-6 trigger `resources_remove`
- **Validates**: FR-007
- **Priority**: High

#### TS-007: Approval rejection

- **Description**: Verify that rejecting an approval cancels the operation
- **Type**: Automated
- **Preconditions**: Server running, conversation with pending approval
- **Steps**:
  1. Send a destructive tool request
  2. POST /approvals/{uuid} with `{"answer": "no"}`
- **Expected Results**:
  - [ ] Response indicates operation cancelled
  - [ ] Conversation status returns to "active"
  - [ ] Resource is NOT created/deleted
- **Validates**: FR-011
- **Priority**: High

#### TS-008: Multiple approval formats

- **Description**: Verify all approval request formats work
- **Type**: Automated
- **Preconditions**: Server running
- **Steps**:
  1. Create 3 pending approvals
  2. Approve first with `{"approved": true}`
  3. Approve second with `{"action": "approve"}`
  4. Approve third with `{"answer": "yes"}`
- **Expected Results**:
  - [ ] All three approvals succeed and execute the tool
- **Validates**: FR-011
- **Priority**: Medium

#### TS-009: A2A sub-agent delegation

- **Description**: Verify the agent can delegate tasks to A2A sub-agents
- **Type**: Automated
- **Preconditions**: Server running with A2A sub-agent configured
- **Steps**:
  1. Send a message that should be delegated to the A2A sub-agent
  2. Verify the task is sent to the sub-agent
  3. Verify the result is returned in the conversation
- **Expected Results**:
  - [ ] A2A sub-agent receives the task
  - [ ] Result is included in the conversation history
  - [ ] Destructive A2A calls require approval
- **Validates**: FR-013, FR-014, FR-015
- **Priority**: High

#### TS-010: Authorization token forwarding

- **Description**: Verify Bearer tokens are forwarded to sub-components
- **Type**: Automated
- **Preconditions**: Server running with A2A sub-agent configured
- **Steps**:
  1. Send a message with `Authorization: Bearer test-token-123` header
  2. Trigger an A2A sub-agent call
  3. Verify the sub-agent receives the token
- **Expected Results**:
  - [ ] A2A sub-agent receives `Authorization: Bearer test-token-123` in the request
  - [ ] Without token, requests still work but no token is forwarded
- **Validates**: FR-016
- **Priority**: High

### 5.3b A2A Server Scenarios

#### TS-011: Agent Card discovery

- **Description**: Verify the agent card returns valid skills from MCP tools
- **Type**: Automated
- **Preconditions**: Server running
- **Steps**:
  1. GET `/.well-known/agent.json`
- **Expected Results**:
  - [ ] Returns a valid Agent Card with name, URL, and skills
  - [ ] Skills include `resources_list` from the MCP server
- **Validates**: FR-018
- **Priority**: High

#### TS-012: A2A message/send (non-destructive)

- **Description**: Verify message/send with a list request returns a completed task with artifact
- **Type**: Automated
- **Preconditions**: Server running
- **Steps**:
  1. Send JSON-RPC `message/send` with text "list all resources"
- **Expected Results**:
  - [ ] Response contains a task with state `completed`
  - [ ] Task has an artifact with text content
- **Validates**: FR-019
- **Priority**: High

#### TS-013: A2A message/send (destructive)

- **Description**: Verify message/send with an add request returns a working task, approve via REST, then tasks/get returns completed
- **Type**: Automated
- **Preconditions**: Server running
- **Steps**:
  1. Send JSON-RPC `message/send` with text "add resource a2a-test-item with value 77"
  2. Verify task state is `input-required`
  3. Approve via REST POST `/approvals/{uuid}`
  4. Send JSON-RPC `tasks/get` with the task ID
- **Expected Results**:
  - [ ] Step 2: task state is `input-required` with approval info in status message
  - [ ] Step 3: approval succeeds
  - [ ] Step 4: task state is `completed`
- **Validates**: FR-019, FR-020
- **Priority**: High

#### TS-014: A2A tasks/get

- **Description**: Verify tasks/get retrieves a task by conversation ID
- **Type**: Automated
- **Preconditions**: Server running, conversation created via REST
- **Steps**:
  1. Create a conversation via REST with initial message
  2. Send JSON-RPC `tasks/get` with the conversation ID
- **Expected Results**:
  - [ ] Task ID matches conversation ID
  - [ ] Task state is `completed`
  - [ ] Task has an artifact
- **Validates**: FR-020
- **Priority**: High

### 5.4 Orchestration Scenarios

#### TS-015: Backward compatibility (no agent tree)

- **Description**: Verify that an agent without the `agent` key in YAML works as a simple single LLM agent
- **Type**: Automated
- **Preconditions**: Server running with simple config (no `agent` key)
- **Steps**:
  1. Send a non-destructive message
  2. Verify response uses simple agent path
- **Expected Results**:
  - [ ] Agent processes messages without orchestration
  - [ ] Tool calls work normally
- **Validates**: FR-021
- **Priority**: High

#### TS-016: Sequential pipeline with data flow

- **Description**: Verify sequential execution with output_key and {placeholder} resolution
- **Type**: Automated (E2E, see also TS-022)
- **Preconditions**: Server running with sequential example config
- **Steps**:
  1. Send message "list all resources"
  2. Inspect conversation messages for both analyzer and executor steps
- **Expected Results**:
  - [ ] Analyzer node produces analysis stored in session state
  - [ ] Executor node receives resolved prompt with analysis
  - [ ] Final response contains tool results
- **Validates**: FR-022, FR-025
- **Priority**: High

#### TS-017: Parallel execution

- **Description**: Verify parallel nodes execute concurrently and combine results
- **Type**: Automated (E2E, see also TS-024)
- **Preconditions**: Server running with parallel example config
- **Steps**:
  1. Send message "give me a full overview of all resources"
  2. Inspect conversation for parallel node results
- **Expected Results**:
  - [ ] Both parallel nodes produce output
  - [ ] Summarizer receives combined results
- **Validates**: FR-023
- **Priority**: High

#### TS-018: Loop with exit condition

- **Description**: Verify loop execution with exit_loop tool
- **Type**: Automated (E2E, see also TS-025)
- **Preconditions**: Server running with loop example config, clean database
- **Steps**:
  1. Send message "ensure we have at least 3 resources"
  2. Verify resources were created
- **Expected Results**:
  - [ ] Loop iterates, adding resources each time
  - [ ] Loop exits when 3+ resources exist
  - [ ] Does not exceed max_iterations
- **Validates**: FR-024
- **Priority**: High

#### TS-019: Pipeline pause/resume on approval

- **Description**: Verify sequential pipeline pauses for destructive tools and resumes after approval
- **Type**: Automated (E2E, see also TS-023)
- **Preconditions**: Server running with sequential example config
- **Steps**:
  1. Send message "add resource test-seq with value 55"
  2. Note the approval UUID
  3. Approve the operation
  4. Verify pipeline completed
- **Expected Results**:
  - [ ] Pipeline pauses at executor node with approval request
  - [ ] Pipeline state is saved (node path, session state)
  - [ ] After approval, pipeline resumes and completes
- **Validates**: FR-026
- **Priority**: High

### 5.5 Orchestration E2E Scenarios

#### TS-022: Sequential pipeline with non-destructive tool

- **Description**: Verify sequential pipeline executes analyzer → executor with a non-destructive tool (resources_list), completing without approval
- **Type**: Automated (E2E)
- **Preconditions**: Server running with sequential orchestration config, GEMINI_API_KEY set, MCP binary built
- **Steps**:
  1. Start server with `testdata/e2e-sequential.yaml`
  2. Create conversation via REST
  3. Send message "list all resources"
- **Expected Results**:
  - [ ] Pipeline executes both nodes (analyzer + executor) sequentially
  - [ ] No approval required (resources_list is non-destructive)
  - [ ] Conversation status remains `active`
- **Validates**: FR-022, FR-025, FR-012
- **Priority**: High

#### TS-023: Sequential pipeline with destructive tool and approval

- **Description**: Verify sequential pipeline pauses for approval on destructive tool (resources_add) and resumes correctly after approval
- **Type**: Automated (E2E)
- **Preconditions**: Server running with sequential orchestration config
- **Steps**:
  1. Start server with `testdata/e2e-sequential.yaml`
  2. Create conversation and send "add resource seq-test with value 42"
  3. Verify pipeline pauses with `waiting_approval` and `pipeline_state` is saved
  4. Approve via REST `POST /approvals/{uuid}`
  5. Verify pipeline resumes and completes
- **Expected Results**:
  - [ ] Pipeline pauses at executor node with approval request
  - [ ] `pipeline_state` is saved with node path and session state
  - [ ] After approval, pipeline resumes and conversation returns to `active`
- **Validates**: FR-022, FR-026, FR-011
- **Priority**: High

#### TS-024: Parallel execution with non-destructive tools

- **Description**: Verify parallel nodes execute concurrently and combine results, with no approval pause
- **Type**: Automated (E2E)
- **Preconditions**: Server running with parallel orchestration config
- **Steps**:
  1. Start server with `testdata/e2e-parallel.yaml`
  2. Create conversation and send "give me an overview of all resources"
- **Expected Results**:
  - [ ] Both parallel nodes (list-all, count-analysis) produce output
  - [ ] Summarizer receives combined results via session state
  - [ ] No approval required (parallel nodes use allowDestructive)
  - [ ] Conversation status remains `active`
- **Validates**: FR-023, FR-025
- **Priority**: High

#### TS-025: Loop execution with exit condition

- **Description**: Verify loop adds resources until threshold and exits via exit_loop tool, with destructive tools running immediately (no approval)
- **Type**: Automated (E2E)
- **Preconditions**: Server running with loop orchestration config, clean database
- **Steps**:
  1. Start server with `testdata/e2e-loop.yaml`
  2. Create conversation and send "ensure we have at least 2 resources"
- **Expected Results**:
  - [ ] Loop iterates, adding resources each time
  - [ ] Loop exits when 2+ resources exist via `exit_loop` tool
  - [ ] No approval pause (loops use allowDestructive)
  - [ ] Conversation status remains `active`
- **Validates**: FR-024, FR-025
- **Priority**: High

#### TS-026: A2A chain non-destructive (Agent A → A2A → Agent B → resources_list)

- **Description**: Verify an A2A chain where Agent A delegates to Agent B via A2A, and Agent B lists resources (non-destructive), completing without approval
- **Type**: Automated (E2E)
- **Preconditions**: Two servers: Agent B (port 9092) with MCP tools, Agent A (port 9091) with sequential pipeline + A2A delegation
- **Steps**:
  1. Start Agent B with `testdata/e2e-chain-b.yaml` on port 9092
  2. Start Agent A with `testdata/e2e-chain-a.yaml` on port 9091
  3. Create conversation on Agent A and send "list all resources"
- **Expected Results**:
  - [ ] Agent A's analyzer processes the message
  - [ ] Agent A's delegator sends to Agent B via A2A
  - [ ] Agent B calls resources_list and returns result
  - [ ] No approval required
  - [ ] Conversation on Agent A remains `active`
- **Validates**: FR-022, FR-014, FR-021
- **Priority**: High

#### TS-027: A2A chain destructive with proxy approval (Agent A → A2A → Agent B → resources_add)

- **Description**: Verify proxy approval chain: Agent A delegates to Agent B via A2A, Agent B encounters destructive tool, returns input-required, Agent A creates proxy approval, user approves on Agent A, approval forwarded to Agent B
- **Type**: Automated (E2E)
- **Preconditions**: Two servers: Agent B (port 9092), Agent A (port 9091)
- **Steps**:
  1. Start Agent B with `testdata/e2e-chain-b.yaml` on port 9092
  2. Start Agent A with `testdata/e2e-chain-a.yaml` on port 9091
  3. Create conversation on Agent A and send "add resource chain-test with value 77"
  4. Verify Agent A returns `waiting_approval` with proxy approval
  5. Approve on Agent A via REST
  6. Verify Agent A forwards approval to Agent B via A2A message/send with taskId
- **Expected Results**:
  - [ ] Agent B returns `input-required` to Agent A via A2A
  - [ ] Agent A creates proxy `PendingApproval` with `remote_task_id` and `remote_agent_name`
  - [ ] User approves on Agent A, forwarded to Agent B
  - [ ] Conversation on Agent A returns to `active`
- **Validates**: FR-029, FR-026, FR-014, FR-015
- **Priority**: High

#### TS-028: Sequential pipeline accessed via A2A protocol

- **Description**: Verify an orchestrated sequential pipeline can be accessed via the A2A message/send endpoint
- **Type**: Automated (E2E)
- **Preconditions**: Server running with sequential orchestration config
- **Steps**:
  1. Start server with `testdata/e2e-sequential.yaml`
  2. Send A2A JSON-RPC `message/send` with text "list all resources"
- **Expected Results**:
  - [ ] A2A returns a task with state `completed`
  - [ ] Task has an artifact with parts
  - [ ] Pipeline executed both sequential nodes
- **Validates**: FR-019, FR-022
- **Priority**: High

### 5.6 Web Chat & A2A Continuation Scenarios

#### TS-020: A2A message/send continuation (approve and reject)

- **Description**: Verify the message/send method with taskId approves and rejects pending approvals via A2A
- **Type**: Automated (E2E)
- **Preconditions**: Server running
- **Steps**:
  1. Send A2A `message/send` with a destructive request
  2. Verify task state is `input-required`
  3. Send A2A `message/send` with `taskId` and message "approved"
  4. Verify task state is `completed`
  5. Repeat with message "rejected" and verify rejection
- **Expected Results**:
  - [ ] Approval via A2A message/send continuation executes the tool and returns completed task
  - [ ] Rejection via A2A message/send continuation cancels the operation
- **Validates**: FR-028
- **Priority**: High

#### TS-021: Proxy approval chain (Web → REST → Agent A → A2A → Agent B)

- **Description**: Verify the proxy approval chain works end-to-end through multiple agents, with the web client using REST API and agents communicating via A2A
- **Type**: Manual (web UI portion); A2A proxy chain automated via TS-027
- **Preconditions**: Agent B (port 8082), Agent A (port 8080), Web (port 3000) all running
- **Steps**:
  1. From web chat, send "add resource chain-test with value 99"
  2. Web sends REST `POST /conversations` to Agent A
  3. Agent A delegates to Agent B via A2A
  4. Agent B encounters destructive tool → returns A2A `input-required` to Agent A
  5. Agent A creates proxy approval → returns REST `waiting_approval` with approval UUID to web
  6. User clicks Approve in the web UI
  7. Web sends REST `POST /approvals/:uuid` to Agent A
  8. Agent A forwards A2A `message/send` with `taskId` to Agent B
  9. Agent B executes tool → returns result through the chain
- **Expected Results**:
  - [ ] Web shows approval box with UUID when conversation has `waiting_approval` status
  - [ ] REST approval is forwarded through the A2A chain
  - [ ] Final result (created resource) is displayed in web chat
- **Validates**: FR-027, FR-029
- **Priority**: High

### 5.6 Untestable Requirements

| Requirement | Reason | Alternative Validation |
|-------------|--------|----------------------|
| FR-007 (LLM quality) | LLM responses are non-deterministic | Test multiple phrasings (TS-006) and verify tool selection, not exact text |
| NFR-003 (100 concurrent) | Requires load testing infrastructure | Manual validation with a load test script |

---

## 6. Technical Architecture

### 6.1 Architecture Overview

```
                          ┌─────────────────────────┐
                          │       agent.yaml         │
                          │  prompt, llm, mcp, a2a   │
                          └──────────┬──────────────┘
                                     │
    ┌────────────┐          ┌────────▼─────────┐         ┌──────────────┐
    │   Client   │──HTTP───▶│   API (Fiber)    │◀──A2A──▶│ Other A2A    │
    │  (curl/..) │◀─────────│   port: 8080     │         │   Agents     │
    └────────────┘          └────────┬─────────┘         └──────────────┘
         Authorization: Bearer ───────┤
                                     │
                          ┌──────────▼──────────┐
                          │      Agent          │
                          │  (orchestrator)     │
                          └──┬──────┬───────┬───┘
                             │      │       │
               ┌─────────────▼┐  ┌──▼────┐ ┌▼───────────────┐
               │ LLM Client   │  │ MCP   │ │ A2A Sub-Agents │
               │ Gemini/Claude│  │Client │ │ (HTTP/JSON-RPC)│
               │ (func call)  │  │(stdio)│ │  + Bearer fwd  │
               └──────────────┘  └───┬───┘ └────────────────┘
                                     │
                              ┌──────▼──────┐
                              │ MCP Server  │
                              │ (subprocess)│
                              │   SQLite    │
                              └─────────────┘
```

**A2A Bidirectional**: The agent acts as both an A2A **client** (delegating to sub-agents) and an A2A **server** (exposing `/.well-known/agent.json` for discovery and `POST /a2a` for task execution by other agents).

### 6.2 Technology Stack

| Layer | Technology | Justification |
|-------|------------|---------------|
| Language | Go 1.23 | Fast compilation, single binary, strong concurrency |
| Web Framework | Fiber | Lightweight, Express-like API, high performance |
| LLM | Gemini (generativelanguage API) / Claude (Anthropic Messages API) | Multi-provider with function/tool calling support, configurable model |
| MCP Protocol | JSON-RPC 2.0 over stdio | MCP standard for tool servers |
| A2A Protocol | JSON-RPC 2.0 over HTTPS | Google A2A standard for agent interop |
| Config | YAML (gopkg.in/yaml.v3) | Human-readable, widely adopted |
| Conversation Storage | JSON files | Simple, no external DB, portable |
| MCP Resource Storage | SQLite | Embedded, zero-config, ACID |
| Build | Make | Standard build automation |
| Container | Docker | Standard containerization |

### 6.3 Key Design Decisions

| Decision | Choice | Alternatives Considered | Rationale | Addresses |
|----------|--------|------------------------|-----------|-----------|
| LLM for intent | Multi-provider (Gemini/Claude) function calling | Regex/keyword matching | Natural language flexibility, schema-based tool selection, provider choice | FR-007, FR-008 |
| MCP over stdio | Subprocess with stdin/stdout | HTTP-based MCP | Simpler deployment, no port management, standard MCP | FR-009 |
| JSON file storage | One file per conversation | SQLite, PostgreSQL | Simple, no external dependency, portable, human-readable | FR-006 |
| Approval via UUID | UUID in response + POST endpoint | WebSocket, callback URL | Stateless, works with any HTTP client, async-friendly | FR-011 |
| Authorization forwarding | Extract and forward Bearer token | OAuth proxy, service mesh | Zero Trust compliant, simple, no middleware dependency | FR-016 |

### 6.4 Module Descriptions

#### 6.4.1 API Module (`internal/api/`)

##### Routes

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| GET | /health | Health check | No |
| GET | /docs | Interactive HTML documentation | No |
| GET | /tools | List available tools and sub-agents | No |
| POST | /conversations | Create conversation (optional initial message) | Optional Bearer |
| GET | /conversations | List all conversations with status summary | No |
| GET | /conversations/:id | Get conversation by ID | No |
| POST | /conversations/:id/messages | Send message to conversation | Optional Bearer |
| POST | /approvals/:uuid | Approve or reject pending action | Optional Bearer |
| GET | /.well-known/agent.json | A2A Agent Card (discovery) | No |
| POST | /a2a | A2A JSON-RPC endpoint (message/send, tasks/get). Use message/send with taskId to continue tasks. | Optional Bearer |

##### Approval Request Formats

```json
{"approved": true}
{"action": "approve"}
{"answer": "yes"}
```

#### 6.4.2 Agent Module (`internal/agent/`)

Orchestrator that connects LLM, MCP, and A2A. Supports two modes:

**Simple mode** (backward compatible): Single LLM node with tools.
1. Receives user message
2. Builds LLM request with conversation history + tool schemas
3. Handles LLM response (text or function call)
4. Routes function calls to MCP or A2A
5. Manages approval workflow for destructive operations

**Orchestrated mode** (agent tree): Tree-based execution with multiple node types.
- `sequential`: Executes sub-agents in order, supports pause/resume for approvals
- `parallel`: Executes sub-agents concurrently via goroutines
- `loop`: Repeats sub-agents until `exit_loop` or `max_iterations`
- `llm`: Calls the LLM with MCP tools, node-level A2A tools, and optional `exit_loop`
- `a2a`: Delegates directly to a remote A2A agent as a workflow step

**Data flow**: `output_key` stores node results in `SessionState`, `{placeholder}` in prompts resolves from session state. Pipeline state is saved to the conversation on approval pause and restored on resume.

#### 6.4.3 LLM Module (`internal/llm/`)

Multi-provider LLM clients behind a shared `LLMClient` interface:
- `client.go`: `LLMClient` interface, shared types (`Message`, `ToolCall`, `Response`), `NewClient()` factory (provider detection by model prefix)
- `gemini.go`: Gemini client — converts MCP tools to Gemini function declarations, uses `systemInstruction`, API: `POST /v1beta/models/{model}:generateContent`
- `claude.go`: Claude client — converts MCP tools to Claude `input_schema`, uses `system` field, API: `POST https://api.anthropic.com/v1/messages`

#### 6.4.4 MCP Module (`internal/mcp/`)

JSON-RPC 2.0 client over stdin/stdout:
- Manages MCP server subprocess lifecycle
- Methods: `initialize`, `tools/list`, `tools/call`
- Parses tool schemas including `destructiveHint`

#### 6.4.5 A2A Module (`internal/a2a/`)

A2A protocol types and client over HTTPS:
- Fetches Agent Cards for capability discovery
- Sends tasks to sub-agents via JSON-RPC 2.0
- Forwards Authorization Bearer token
- Supports synchronous request/response
- Protocol types shared by both client and server sides

#### 6.4.6 Data Storage (`internal/storage/`, `internal/conversation/`)

##### Conversation Model

| Field | Type | Description |
|-------|------|-------------|
| id | UUID | Unique conversation identifier |
| session_id | string | 8-char hex session ID for cross-agent tracing |
| status | string | `active`, `waiting_approval`, `completed` |
| messages | []Message | Ordered list of messages |
| pending_approval | *PendingApproval | Current pending approval (if any) |
| pipeline_state | *PipelineState | Orchestration pause/resume state (if any) |
| created_at | timestamp | Creation time |
| updated_at | timestamp | Last update time |

##### Message Model

| Field | Type | Description |
|-------|------|-------------|
| id | UUID | Unique message identifier |
| role | string | `system`, `user`, `assistant`, `tool` |
| content | string | Message text |
| tool_call | *ToolCall | Tool invocation details (if applicable) |
| created_at | timestamp | Message time |

##### PendingApproval Model

| Field | Type | Description |
|-------|------|-------------|
| uuid | UUID | Approval identifier |
| conversation_id | string | Parent conversation |
| tool_name | string | Tool or agent to call |
| tool_args | map | Arguments for the call |
| description | string | Human-readable description |
| created_at | timestamp | Approval creation time |

##### PipelineState Model

| Field | Type | Description |
|-------|------|-------------|
| paused_node_path | []int | Path of indices through the agent tree to the paused node |
| paused_node_output_key | string | Output key of the paused node |
| session_state | map[string]string | Snapshot of all output_key values at pause time |
| user_message | string | Original user message for resume |

##### MCP Resource Model (mcp-resources server)

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| id | TEXT | PK | Auto-generated ID |
| name | TEXT | NOT NULL | Resource name |
| value | INTEGER | NOT NULL | Integer value |
| created_at | TEXT | NOT NULL | ISO 8601 timestamp |
| updated_at | TEXT | NOT NULL | ISO 8601 timestamp |

### 6.5 Identity & Permissions

#### Authentication

- **Method**: Bearer token forwarding (Zero Trust)
- **Behavior**: If `Authorization: Bearer <token>` is present, it is stored in request context and forwarded to all downstream calls
- **No auth required**: The API itself does not enforce authentication; it forwards tokens

#### Test Authentication

- **Test tokens**: Any string can be used as a Bearer token for testing
- **Verification**: E2E test uses a mock A2A server that echoes back received headers

### 6.6 Dependencies

#### External Services

| Service | Purpose | Criticality | Fallback |
|---------|---------|-------------|----------|
| Gemini / Anthropic API | LLM for intent parsing | Critical | None — agent cannot process messages without LLM |
| A2A Sub-Agents | Task delegation | Important | Agent responds with "sub-agent unavailable" |

#### Third-Party Libraries

| Library | Version | License | Purpose |
|---------|---------|---------|---------|
| gofiber/fiber/v2 | 2.52.10 | MIT | HTTP framework |
| google/uuid | 1.6.0 | BSD-3 | UUID generation |
| mattn/go-sqlite3 | 1.14.33 | MIT | SQLite driver (MCP server) |
| gopkg.in/yaml.v3 | 3.0.1 | MIT | YAML config parsing |

### 6.7 Observability

#### Log Standards

- **Format**: Plain text (stdout)
- **Fields**: timestamp, request method, path, status code, latency
- **Levels**: Fiber default request logging

### 6.8 DevOps

#### Version Control

- **Repository**: github.com/smorand/agent-stop-and-go
- **Hosting**: GitHub
- **Branching strategy**: Single branch (main)
- **Push policy**: Push after feature complete

#### Build & Automation

| Target | Description | When to Run |
|--------|-------------|-------------|
| `make build` | Build binaries for current platform | Before run/test |
| `make build-all` | Build for all platforms (linux, darwin) | Before release |
| `make run` | Build and run the API | Development |
| `make test` | Run Go tests | Before commit |
| `make check` | Run fmt, vet, lint, test | Before push |
| `make clean` | Remove build artifacts | As needed |
| `make compose-up` | Start multi-agent Docker Compose stack | Multi-agent testing |
| `make compose-down` | Stop Docker Compose stack | After testing |

#### Environments

| Environment | Purpose | Deploy Method | URL |
|-------------|---------|---------------|-----|
| Local | Development | `make run` | localhost:8080 |
| Docker | Container testing | `docker build && docker run` | localhost:8080 |
| Docker Compose | Multi-agent testing | `docker-compose up --build` | localhost:8080 (API), localhost:3000 (Web) |

### 6.9 Code Structure

#### Project Layout

```
agent-stop-and-go/
├── config/
│   ├── agent.yaml                    # Default single-agent config
│   ├── web.yaml                      # Web frontend config (local dev)
│   ├── agent-a.yaml                  # Docker Compose: orchestrator
│   ├── agent-b.yaml                  # Docker Compose: resource agent
│   └── web-compose.yaml              # Docker Compose: web frontend
├── cmd/
│   ├── agent/main.go                 # API entry point
│   ├── web/main.go                   # Web chat entry point
│   └── mcp-resources/main.go         # MCP server (SQLite resources)
├── internal/
│   ├── api/                          # HTTP handlers (Fiber)
│   │   ├── handlers.go               # Route handlers
│   │   └── routes.go                 # Route definitions
│   ├── agent/                        # Agent orchestrator
│   │   ├── agent.go                  # LLM + MCP + A2A coordination
│   │   └── orchestrator.go           # Tree-based agent orchestration engine
│   ├── llm/                          # Multi-provider LLM clients
│   │   ├── client.go                 # LLMClient interface, factory, shared types
│   │   ├── gemini.go                 # Gemini (generativelanguage API)
│   │   └── claude.go                 # Claude (Anthropic Messages API)
│   ├── mcp/                          # MCP client
│   │   ├── client.go                 # Subprocess management
│   │   └── protocol.go              # JSON-RPC types
│   ├── a2a/                          # A2A client (NEW)
│   │   ├── client.go                 # HTTP client with auth forwarding
│   │   └── protocol.go              # A2A types
│   ├── config/                       # YAML config loader
│   │   └── config.go
│   ├── conversation/                 # Data models
│   │   └── conversation.go
│   └── storage/                      # JSON file persistence
│       └── storage.go
├── testdata/                            # E2E orchestration test configs
│   ├── e2e-sequential.yaml              # Sequential pipeline test
│   ├── e2e-parallel.yaml                # Parallel pipeline test
│   ├── e2e-loop.yaml                    # Loop pipeline test
│   ├── e2e-chain-a.yaml                 # A2A chain orchestrator test
│   └── e2e-chain-b.yaml                 # A2A chain resource agent test
├── docker-compose.yaml               # Multi-agent Docker Compose
├── Makefile                          # Build automation
├── Dockerfile                        # Container build
├── go.mod
├── go.sum
├── CLAUDE.md
└── README.md
```

#### Key Conventions

| Convention | Standard | Source |
|------------|----------|--------|
| Naming style | camelCase (Go standard) | Go convention |
| Package structure | By responsibility | Go convention |
| Config format | YAML | Team decision |
| Test location | Co-located `_test.go` | Go convention |
| Binary naming | `{name}-{os}-{arch}` | Makefile convention |

#### Skill Alignment

- **Primary skill**: golang
- **Conventions inherited**: cmd/ layout, internal/ packages, Makefile build
- **Deviations**: None
