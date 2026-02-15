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
