# Backlog

## Ideas

### Kafka Communication Channel Between Agent API and Clients

**Added:** 2026-02-15

**Description:** Replace or complement the current synchronous REST API communication between the main agent and clients (web application, other consumers) with a Kafka-based asynchronous messaging channel. This would decouple the agent's processing from client polling, enable real-time event streaming (tool calls, approvals, LLM responses), and allow multiple clients to subscribe to conversation updates independently.

**Current State:** The web chat frontend communicates with the agent via REST API (`POST /api/send`, `POST /api/approve`, `GET /api/conversation/:id`). Clients must poll for updates. A2A agents also use synchronous JSON-RPC over HTTPS.

**Potential Benefits:**
- Real-time streaming of agent responses and tool execution events to clients
- Decoupled architecture: multiple clients can subscribe to the same conversation
- Better scalability for long-running agent tasks
- Event sourcing capability for conversation history
- Reduced polling overhead on the web frontend

### Google Chat Client via Kafka

**Added:** 2026-02-15

**Description:** Add a Google Chat integration as a client front-end that communicates with the agent through the Kafka messaging channel. Users would interact with the agent directly from Google Chat (sending messages, receiving responses, approving actions), with Kafka acting as the intermediary transport layer between Google Chat and the agent API.

**Dependencies:** Requires the Kafka communication channel (see above) to be implemented first.

**Potential Benefits:**
- Interact with agents directly from Google Chat without opening a separate web UI
- Leverage Google Chat for team collaboration around agent tasks
- Approval workflows directly within chat (approve/reject destructive actions)
- Notifications and updates pushed to Google Chat spaces in real-time via Kafka
- Familiar interface for enterprise users already using Google Workspace

### Gmail Client via Kafka with Permanent Authorization

**Added:** 2026-02-15

**Description:** Add a Gmail integration as a client front-end that communicates with the agent through the Kafka messaging channel. Users would interact with the agent by sending emails to a dedicated Gmail address; incoming emails are consumed via Kafka and routed to the agent, and agent responses are sent back as email replies. The integration must use a permanent OAuth authorization (offline access with refresh tokens) so the Gmail connection never expires and requires no manual re-authentication.

**Dependencies:** Requires the Kafka communication channel (see above) to be implemented first.

**Key Requirements:**
- OAuth 2.0 with offline access (`access_type=offline`) and persistent refresh token storage
- Automatic token refresh without user intervention
- Gmail API (push notifications via Pub/Sub or periodic polling) to detect incoming emails
- Email threading: agent replies maintain the same email thread/conversation
- Support for approval workflows via email replies (e.g., reply "approved" or "rejected")

**Potential Benefits:**
- Interact with agents via email — no dedicated UI required
- Asynchronous by nature: send a request, get a reply when ready
- Works from any email client (mobile, desktop, web)
- Approval workflows via simple email replies
- Permanent authorization ensures unattended, always-on operation

### Microsoft Teams Client via Kafka

**Added:** 2026-02-15

**Description:** Add a Microsoft Teams integration as a client front-end that communicates with the agent through the Kafka messaging channel. Users would interact with the agent from a Teams channel or direct message via a Teams Bot; incoming messages are consumed via Kafka and routed to the agent, and agent responses are posted back as Teams replies. This enables enterprise users on Microsoft 365 to leverage the agent platform without leaving their primary collaboration tool.

**Dependencies:** Requires the Kafka communication channel (see above) to be implemented first.

**Key Requirements:**
- Teams Bot registration via Azure Bot Framework / Bot Service
- Adaptive Cards for rich agent responses and approval prompts
- Support for both channel conversations and 1:1 direct messages
- Approval workflows via interactive card buttons (Approve / Reject)
- OAuth / Azure AD app registration with appropriate Graph API permissions

**Potential Benefits:**
- Interact with agents directly from Microsoft Teams
- Rich UI via Adaptive Cards for structured responses and approval actions
- Works across Teams desktop, mobile, and web clients
- Familiar interface for enterprise users on Microsoft 365
- Team collaboration around agent tasks within Teams channels

### Slack Client via Kafka

**Added:** 2026-02-15

**Description:** Add a Slack integration as a client front-end that communicates with the agent through the Kafka messaging channel. Users would interact with the agent from a Slack channel or direct message via a Slack App/Bot; incoming messages are consumed via Kafka and routed to the agent, and agent responses are posted back as Slack replies (threaded). This enables teams using Slack as their primary workspace to leverage the agent platform natively.

**Dependencies:** Requires the Kafka communication channel (see above) to be implemented first.

**Key Requirements:**
- Slack App with Bot Token (Events API for incoming messages, Web API for posting replies)
- Socket Mode or HTTP endpoint for receiving Slack events
- Threaded replies to maintain conversation context within Slack threads
- Block Kit for rich agent responses and interactive approval prompts
- Approval workflows via interactive buttons (Approve / Reject) with action handlers
- OAuth 2.0 for Slack workspace installation and token management

**Potential Benefits:**
- Interact with agents directly from Slack channels or DMs
- Rich UI via Block Kit for structured responses and approval actions
- Threaded conversations keep agent interactions organized
- Works across Slack desktop, mobile, and web clients
- Widely adopted in tech teams — low friction for adoption

### File Integration Workload (inspired by agentic-data-platform)

**Added:** 2026-02-15

**Description:** Add a file integration workload similar to the agentic-data-platform's file ingestion pipeline. When a file is deposited in a designated storage area (e.g., a local directory, S3/MinIO bucket, or GCS bucket), the system automatically detects it, triggers an AI agent to analyze the file (format, schema, content), and proposes an integration action. The agent interacts with the user (via any connected client channel) for human-in-the-loop validation before executing the integration.

**Inspiration:** The `agentic-data-platform` spec defines automatic file arrival detection (FR-001), batch file detection with sentinel (FR-002), AI-assisted pipeline creation, and human-in-the-loop approval — all event-driven with zero manual intervention.

**Key Requirements:**
- File arrival detection: event-driven monitoring of designated storage (MCP filesystem roots, object storage buckets)
- File analysis agent: AI agent analyzes file metadata, format, schema, and sample content
- Human-in-the-loop: agent proposes integration action and waits for user approval via the approval workflow
- Pipeline execution: upon approval, execute the integration (parse, transform, load into target)
- Batch support: optional sentinel-based batch detection for multi-file arrivals
- Kafka integration: file events published to Kafka for consumption by the agent and status updates back to clients

**Dependencies:** Benefits from the Kafka communication channel for event-driven architecture.

**Potential Benefits:**
- Zero-touch file ingestion for known file types
- AI-assisted onboarding of unknown file formats
- Leverages existing approval workflow for destructive actions
- Event-driven architecture scales to high file volumes
- Reuses the multi-channel client front-ends (Slack, Teams, Gmail, Google Chat) for user interaction

### Gmail Client (Direct, No Kafka)

**Added:** 2026-02-22

**Description:** Implement a Gmail-based client as a new `cmd/gmail` service that communicates with the agent via its REST API — the same way `cmd/web` does today. The service monitors a dedicated Gmail account for incoming emails, forwards them as messages to the agent, and sends agent responses back as email replies. This is a standalone client mode that coexists with the web UI; no Kafka dependency required.

**Current State:** The platform has one client frontend: `cmd/web` (browser chat UI) that proxies to the agent REST API (`POST /api/send`, `POST /api/approve`, `GET /api/conversation/:id`). The existing "Gmail Client via Kafka" backlog entry depends on the Kafka channel being built first. This entry is a simpler, direct approach that can be implemented immediately using the existing REST API.

**Relation to Existing Backlog:** The "Gmail Client via Kafka with Permanent Authorization" entry describes a Kafka-based approach. This entry is a direct alternative that does not require Kafka and can be built now. If Kafka is implemented later, this client could optionally migrate to Kafka as transport, but the Gmail ↔ Agent interface would remain the same.

**Key Requirements:**
- New `cmd/gmail` service with its own YAML config (Gmail account, agent URL, polling interval)
- Gmail API integration: OAuth 2.0 with offline access and persistent refresh token storage for unattended operation
- Incoming email detection: Gmail API push notifications (Pub/Sub) or periodic polling for new messages
- Email → Agent: extract text from incoming email, call `POST /api/send` (or `POST /conversations/:id/messages` for existing threads)
- Agent → Email: send agent response as Gmail reply, maintaining the email thread
- Conversation mapping: map email thread IDs to agent conversation IDs for multi-turn conversations
- Approval workflows: agent approval requests formatted in the email body with reply instructions (e.g., reply "approved" or "rejected"); parse reply text via `POST /api/approve`
- Coexistence: web UI and Gmail client can operate on the same agent simultaneously

**Potential Benefits:**
- No Kafka dependency — can be built and deployed immediately
- Interact with agents via any email client (mobile, desktop, web Gmail)
- Asynchronous by nature: send a request, get a reply when the agent is done
- Approval workflows via simple email replies
- Same REST API interface as the web UI — minimal agent-side changes

### Interactive CLI Client

**Added:** 2026-02-22

**Description:** Implement a terminal-based interactive CLI client as a new `cmd/cli` service, equivalent to the web UI but for the terminal. The CLI communicates with the agent via its REST API (same as `cmd/web`). It provides a rich interactive experience inspired by modern CLI tools like Claude Code and Gemini CLI — with readline-style input, conversation history, session management, tool approval prompts, and ANSI-styled output. Everything is stored locally in `$HOME/.cache/agentic-platform-cli/`.

**Current State:** The platform has one client frontend: `cmd/web` (browser chat UI). There is no terminal-based client. Users must use the web UI or craft raw curl/API calls.

**Key Requirements:**

*Input & Readline:*
- Arrow key navigation (up/down for history, left/right for cursor movement)
- Line editing with standard keybindings (Ctrl+A/E, Ctrl+W, Ctrl+K, etc.)
- Multi-line input support (e.g., Shift+Enter or `\` continuation)
- Go library: `github.com/chzyer/readline` or `github.com/peterh/liner`

*Session Management:*
- Conversations persisted locally in `$HOME/.cache/agentic-platform-cli/sessions/`
- Each session stored as a JSON file with conversation ID, agent URL, messages, and metadata
- `/history` command: interactive list of past sessions with timestamps, first message preview, and conversation status
- Resume any previous conversation by selecting from history or by ID
- `/new` command: start a fresh conversation
- `/clear` command: clear the terminal screen

*Tool Approval & OAuth:*
- When the agent returns `waiting_approval`, display the approval description with clear formatting and prompt `[A]pprove / [R]eject`
- Single-keystroke approval (no Enter required) for fast workflow
- OAuth flow support: when triggered, open the authorization URL in the default browser (`open` / `xdg-open`) and wait for callback or manual token paste

*Display & Formatting:*
- ANSI colors, bold, underline, dim — with automatic detection of terminal capabilities (`$TERM`, `$COLORTERM`)
- Graceful fallback to plain text when colors are not supported (e.g., dumb terminals, piped output)
- Agent responses rendered with markdown-like formatting: `**bold**`, `` `code` ``, code blocks with syntax highlighting
- Spinner/progress indicator while waiting for agent response
- Clear visual distinction between user input, agent response, tool calls, and approval requests

*Terminal Compatibility:*
- Must work in TMUX sessions (handle `$TERM=screen-256color`)
- Must work over SSH (no assumptions about local display)
- Respect `$NO_COLOR` convention
- Handle terminal resize (SIGWINCH)
- Ctrl+C: cancel current request (if in-flight), otherwise prompt to exit
- Ctrl+D: exit gracefully

*Local Storage (`$HOME/.cache/agentic-platform-cli/`):*
- `config.yaml`: default agent URL, color preferences, history size
- `sessions/`: conversation session files
- `history`: readline input history (cross-session)

*Configuration:*
- `--agent-url` flag (or from config.yaml) to specify the agent endpoint
- `--no-color` flag to force plain output
- `--session` flag to resume a specific session by ID

**Potential Benefits:**
- Terminal-native interface for developers and sysadmins who prefer CLI over browser
- Works in headless environments (SSH, containers, CI/CD)
- Session persistence enables long-running workflows across terminal sessions
- Fast approval workflow with single-keystroke responses
- Coexists with web UI — same agent, different frontend
- Lightweight — no browser, no JavaScript, just a Go binary

### Data Transformation Workload (inspired by agentic-data-platform)

**Added:** 2026-02-15

**Description:** Add a data transformation workload similar to the agentic-data-platform's pipeline execution model (FR-008). Once data is ingested (via the file integration workload or other sources), an AI agent proposes a transformation pipeline — an ordered sequence of operations (column rename, type cast, filter, computed columns, joins, aggregations, window functions) — that transforms raw data into a target model/schema. The agent interacts with the user to define or refine the target model, then executes the transformation with human-in-the-loop approval.

**Inspiration:** The `agentic-data-platform` spec defines a two-phase pipeline execution: (1) Stage — load raw data into temporary storage, (2) Transform — execute ordered transformation operations that read from staging, transform, and write to target tables. Pipelines are registered, versioned, and linked to source signatures for automatic reprocessing.

**Key Requirements:**
- Target model definition: AI agent proposes a target schema based on the raw data analysis and user intent
- Transformation pipeline: ordered list of operations (rename, cast, filter, computed columns, joins, aggregations, window functions)
- Two-phase execution: stage raw data, then transform to target — data does not transit through application services
- Pipeline registration: transformations are saved, versioned, and reusable for future data of the same type
- Human-in-the-loop: agent proposes the model and transformations, user validates before execution
- Execution metrics: rows staged, step durations, total rows written, errors

**Dependencies:** Requires the file integration workload for data input. Benefits from the Kafka communication channel.

**Potential Benefits:**
- AI-assisted data modeling — agent proposes schema and transformations from raw data
- Reusable pipelines: once approved, transformations auto-apply to future data of the same type
- No-code data transformation via conversational interface
- Leverages existing approval workflow for safe execution
- Versioned pipelines enable rollback and audit trail

### Google ADK Support Skill Integration

**Added:** 2026-02-22

**Description:** Implement support for the Google Agent Development Kit (ADK) "Support skill" pattern, where agents can dynamically declare, register, and expose skills as part of the A2A protocol. Currently, the platform has a static `Skill` struct in the A2A Agent Card (`internal/a2a/protocol.go`) that lists agent capabilities at discovery time, but skills are not actionable — they're just descriptive metadata. This backlog item would make skills first-class: agents can register skills with input/output schemas, clients can invoke specific skills by ID (rather than sending free-text messages), and the orchestrator can route requests to the appropriate agent based on skill matching.

**Current State:** The A2A Agent Card already includes a `Skills` field (`[]Skill` with `ID`, `Name`, `Description`). The agent card handler in `internal/api/handlers.go` maps available MCP tools to skills. However, skills are purely informational — there is no skill-based invocation, no input/output schema validation, and no skill-based routing in the orchestrator.

**Key Requirements:**
- Skill registration: agents declare skills with ID, name, description, and JSON Schema for input/output
- Skill invocation: A2A `message/send` extended with optional `skillId` parameter to target a specific skill
- Skill-based routing: orchestrator can match incoming requests to the best agent based on declared skills
- Compatibility with Google ADK skill protocol (align with ADK's skill declaration and invocation patterns)
- Backward compatible: existing free-text `message/send` without `skillId` continues to work as today

**Potential Benefits:**
- Structured agent interactions: clients can invoke specific capabilities rather than relying on free-text intent detection
- Better orchestration: route requests to the right sub-agent based on skill matching instead of LLM-based delegation
- Interoperability with Google ADK ecosystem agents
- Skill discovery: clients can enumerate and present available skills in the UI (e.g., skill picker in web chat or CLI)
