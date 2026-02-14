# Multi-MCP Server Support -- Change Specification

> Generated on: 2026-02-14
> Project: Agent Stop and Go
> Version: 1.0
> Status: Draft
> Type: Change Specification

## 1. Change Summary

The agent configuration currently supports 0 or 1 MCP server. This change enables an agent to connect to 0 to N MCP servers simultaneously. The YAML configuration key is renamed from `mcp` (single object) to `mcp_servers` (list of objects). This is a breaking change to the configuration format.

### Modifications Overview

| MOD ID  | Type   | Title                                             | Priority  |
|---------|--------|---------------------------------------------------|-----------|
| MOD-001 | Change | Rename `mcp` config key to `mcp_servers` as list  | Must-have |
| MOD-002 | Add    | Composite MCP client wrapping multiple sub-clients | Must-have |
| MOD-003 | Change | Agent startup initializes multiple MCP clients     | Must-have |
| MOD-004 | Add    | Duplicate tool name detection at startup           | Must-have |
| MOD-005 | Change | `/tools` API response includes `server` field      | Must-have |
| MOD-006 | Change | Update all config files and test configs            | Must-have |

## 2. Current State Analysis

### 2.1 Project Overview

Agent Stop and Go is a Go-based API for async autonomous agents with MCP tool support, A2A sub-agent delegation, and approval workflows. Agents can pause execution and wait for external approval before proceeding with destructive actions. The project supports orchestration trees (sequential, parallel, loop, LLM, A2A node types).

### 2.2 Existing Specifications

No existing specification documents exist in `specs/`. This is the first spec.

### 2.3 Relevant Architecture

**MCP configuration flow:**
1. `config/agent.yaml` defines a single `mcp:` block with either `url:` (HTTP) or `command:`/`args:` (stdio)
2. `internal/config/config.go` parses this into `Config.MCP` (type `MCPConfig`)
3. `internal/agent/agent.go` `Start()` creates a single `mcp.Client` via `mcp.NewClient()` from `Config.MCP`
4. The `Agent` struct holds `mcpClient mcp.Client` (single client) and `mcpMu sync.Mutex` (serializes parallel calls)
5. `mcp.NewClient()` factory returns `HTTPClient`, `StdioClient`, or `NopClient` based on config fields
6. `mcp.Client` interface: `Start()`, `Stop()`, `Tools()`, `GetTool()`, `CallTool()`
7. All tool access goes through `a.mcpClient` -- in `agent.go` (simple mode) and `orchestrator.go` (tree mode)

**Key call sites for `a.mcpClient`:**
- `agent.go:Start()` -- creates and starts the client
- `agent.go:Stop()` -- stops the client
- `agent.go:getAllTools()` -- calls `a.mcpClient.Tools()`
- `agent.go:processSimpleMessage()` -- calls `a.mcpClient.GetTool()`
- `agent.go:executeToolAndRespond()` -- calls `a.mcpClient.CallTool()`
- `agent.go:ResolveApproval()` -- calls `a.mcpClient.CallTool()` under `a.mcpMu` lock
- `orchestrator.go:getNodeTools()` -- calls `a.mcpClient.Tools()`
- `orchestrator.go:executeLLMNode()` -- calls `a.mcpClient.GetTool()`, `a.mcpClient.CallTool()` under `a.mcpMu` lock

**External references to `cfg.MCP`:**
- `cmd/agent/main.go` -- logs MCP server info at startup
- `e2e_test.go` -- overrides `cfg.MCP.URL`, `cfg.MCP.Command`, `cfg.MCP.Args` for test isolation

## 3. Requested Modifications

### MOD-001: Rename `mcp` config key to `mcp_servers` as list

- **Type:** Change
- **Description:** Replace the single `mcp` YAML key (object) with `mcp_servers` (list of objects). Each entry has a required `name` field plus the existing transport fields (`url` or `command`/`args`). The `name` field must be unique across entries and is required -- config validation fails at load time if missing or duplicated.
- **Rationale:** The current single-object format cannot express multiple MCP servers. Renaming to `mcp_servers` is more semantically consistent with the list format.
- **Priority:** Must-have
- **Details:**
  - Old format:
    ```yaml
    mcp:
      url: http://localhost:8090/mcp
    ```
  - New format:
    ```yaml
    mcp_servers:
      - name: resources
        url: http://localhost:8090/mcp
      - name: analytics
        command: ./bin/analytics-mcp
        args: [--db, ./data/analytics.db]
    ```
  - Empty list or omitted key is valid (agent uses only A2A, no MCP tools)
  - Each entry reuses the existing `MCPConfig` fields plus a new `name` field
  - Config loader must validate: (a) `name` is non-empty for every entry, (b) `name` is unique across entries

### MOD-002: Composite MCP client wrapping multiple sub-clients

- **Type:** Add
- **Description:** Create a new `CompositeClient` in `internal/mcp/` that implements the `mcp.Client` interface and wraps 0 to N underlying `mcp.Client` instances. The `CompositeClient` aggregates tools from all sub-clients, maps each tool name to its owning sub-client, and routes `CallTool()` to the correct sub-client.
- **Rationale:** Encapsulates multi-server complexity behind the existing `mcp.Client` interface, minimizing changes to `agent.go` and `orchestrator.go`.
- **Priority:** Must-have
- **Details:**
  - `Start()`: starts all sub-clients sequentially; if any fails, stops all already-started clients and returns error (all-or-nothing)
  - `Stop()`: stops all sub-clients, collects errors
  - `Tools()`: returns the merged list of tools from all sub-clients. Each tool carries a `Server` field with the MCP server name.
  - `GetTool(name)`: looks up in the merged tool map
  - `CallTool(name, args)`: routes to the sub-client that owns the tool
  - Duplicate tool name detection at `Start()` time: if two sub-clients expose a tool with the same name, `Start()` returns an error
  - Thread-safe: the composite client handles its own locking for `CallTool()` (replaces the `mcpMu` in `Agent`)

### MOD-003: Agent startup initializes multiple MCP clients

- **Type:** Change
- **Description:** Modify `Agent.Start()` to iterate over `config.MCPServers` (the new list), create individual MCP clients for each entry, wrap them in a `CompositeClient`, and use that as `a.mcpClient`.
- **Rationale:** The agent must initialize all configured MCP servers at startup.
- **Priority:** Must-have
- **Details:**
  - Replace the current single `mcp.NewClient()` call with a loop creating clients from each `MCPServerConfig` entry
  - Pass the list of clients (with their names) to `mcp.NewCompositeClient()`
  - The composite client's `Start()` handles connection, tool loading, and duplicate detection
  - If the `mcp_servers` list is empty, the composite client wraps zero sub-clients (equivalent to current `NopClient` behavior)
  - Remove `Agent.mcpMu` -- the composite client handles serialization internally

### MOD-004: Duplicate tool name detection at startup

- **Type:** Add
- **Description:** During `CompositeClient.Start()`, after all sub-clients have started and their tools are loaded, check for duplicate tool names across all sub-clients. If duplicates are found, return an error listing the conflicting tool names and the servers that provide them.
- **Rationale:** Prevents ambiguous tool routing. The user must ensure no tool name collisions across MCP servers.
- **Priority:** Must-have
- **Details:**
  - Error message format: `duplicate tool name "X" found in MCP servers "server-a" and "server-b"`
  - All sub-clients are stopped on error (cleanup)

### MOD-005: `/tools` API response includes `server` field

- **Type:** Change
- **Description:** Add a `Server` field to the `mcp.Tool` struct. The `/tools` API response will include this field for each tool, indicating which MCP server provides it. A2A synthetic tools will have an empty `Server` field.
- **Rationale:** The MCP server name is programmatically significant. Consumers of the `/tools` endpoint need to know which server provides each tool.
- **Priority:** Must-have
- **Details:**
  - Add `Server string` field to `mcp.Tool` struct with JSON tag `"server,omitempty"`
  - The `CompositeClient` populates this field when merging tools
  - A2A synthetic tools (created in `getAllTools()` and `getNodeTools()`) do not set this field

### MOD-006: Update all config files and test configs

- **Type:** Change
- **Description:** Update all YAML configuration files to use the new `mcp_servers` list format.
- **Rationale:** All existing configs use the old `mcp:` key and must be migrated.
- **Priority:** Must-have
- **Details:**
  - Files to update: `config/agent.yaml`, `config/agent-b.yaml`, `testdata/e2e-sequential.yaml`, `testdata/e2e-parallel.yaml`, `testdata/e2e-loop.yaml`, `testdata/e2e-chain-a.yaml`, `testdata/e2e-chain-b.yaml`
  - Files that have no `mcp:` key (no change needed): `config/agent-a.yaml`, `config/web.yaml`, `config/web-compose.yaml`
  - The `name` field for the single MCP server will be `resources` (matching the existing `mcp-resources` server)

## 4. Impact Analysis

### 4.1 Affected Components

| File/Module | Impact Type | Description |
|-------------|-------------|-------------|
| `internal/config/config.go` | Modified | Replace `MCP MCPConfig` with `MCPServers []MCPServerConfig`. Add `MCPServerConfig` struct with `Name` field. Add validation for name presence and uniqueness. Remove `MCPConfig` struct. |
| `internal/config/config_test.go` | Modified | Update test YAML strings from `mcp:` to `mcp_servers:`. Add tests for validation (missing name, duplicate names, multiple servers). |
| `internal/mcp/protocol.go` | Modified | Add `Server string` field to `Tool` struct. |
| `internal/mcp/client.go` | Modified | Update `NewClient` to accept `MCPServerConfig`-compatible params. No structural change to `Client` interface. |
| `internal/mcp/client_composite.go` | Added | New file: `CompositeClient` implementing `mcp.Client`. Handles multi-client aggregation, duplicate detection, tool routing, and serialized `CallTool()`. |
| `internal/agent/agent.go` | Modified | `Start()`: iterate `config.MCPServers`, create sub-clients, wrap in `CompositeClient`. Remove `mcpMu` field. Update startup logging. All other `a.mcpClient` call sites remain unchanged (interface is preserved). |
| `internal/agent/orchestrator.go` | Modified | Remove `a.mcpMu.Lock()/Unlock()` calls around `a.mcpClient.CallTool()` (serialization moves into `CompositeClient`). |
| `cmd/agent/main.go` | Modified | Update startup log from `cfg.MCP.URL`/`cfg.MCP.Command` to iterate `cfg.MCPServers`. |
| `config/agent.yaml` | Modified | Rename `mcp:` to `mcp_servers:` list with `name: resources`. |
| `config/agent-b.yaml` | Modified | Rename `mcp:` to `mcp_servers:` list with `name: resources`. |
| `testdata/e2e-sequential.yaml` | Modified | Rename `mcp:` to `mcp_servers:` list with `name: resources`. |
| `testdata/e2e-parallel.yaml` | Modified | Rename `mcp:` to `mcp_servers:` list with `name: resources`. |
| `testdata/e2e-loop.yaml` | Modified | Rename `mcp:` to `mcp_servers:` list with `name: resources`. |
| `testdata/e2e-chain-a.yaml` | Modified | Rename `mcp:` to `mcp_servers:` list with `name: resources`. |
| `testdata/e2e-chain-b.yaml` | Modified | Rename `mcp:` to `mcp_servers:` list with `name: resources`. |
| `e2e_test.go` | Modified | Update `cfg.MCP.URL`/`cfg.MCP.Command`/`cfg.MCP.Args` overrides to use `cfg.MCPServers` slice. |
| `e2e_orchestration_test.go` | Not modified | Uses config files (already updated above). No direct `cfg.MCP` references. |

### 4.2 Affected Requirements

No existing specification documents exist. This is the first spec. No cross-reference conflicts.

### 4.3 Affected Tests

| Test File | Test ID/Name | Action | Description |
|-----------|-------------|--------|-------------|
| `internal/config/config_test.go` | `TestLoad` | Modified | Update YAML strings from `mcp:` to `mcp_servers:` format. |
| `internal/config/config_test.go` | `TestLoad_SynthesizesDefaultAgentNode` | Modified | Update YAML string from `mcp:` to `mcp_servers:` format. |
| `internal/config/config_test.go` | `TestLoad_MCPServerValidation` | New | Test missing name, duplicate name, and empty list scenarios. |
| `internal/config/config_test.go` | `TestLoad_MultipleMCPServers` | New | Test parsing of multiple MCP server entries. |
| `internal/mcp/client_composite_test.go` | `TestCompositeClient_*` | New | Unit tests for `CompositeClient`: tool aggregation, duplicate detection, routing, start/stop lifecycle. |
| `e2e_test.go` | `TestMain` | Modified | Update `cfg.MCP.*` overrides to `cfg.MCPServers` slice format. |
| `e2e_test.go` | All existing E2E tests | Unchanged | Behavior is unchanged; only the config initialization in `TestMain` changes. |
| `e2e_orchestration_test.go` | All existing orch tests | Unchanged | Config files are updated; test code has no direct `cfg.MCP` references. |

### 4.4 Affected Documentation

| Document | Section | Action | Description |
|----------|---------|--------|-------------|
| `CLAUDE.md` | Configuration section | Modified | Replace all `mcp:` examples with `mcp_servers:` list format. Update `Config` struct description. Update Project Structure to note `client_composite.go`. |
| `CLAUDE.md` | Key Concepts | Modified | Update MCP Server description to mention multi-server support. |
| `.agent_docs/golang.md` | Package Organization | Modified | Update `internal/mcp/` description to mention `client_composite.go`. |
| `docs/overview.md` | Project Structure | Modified | Add `client_composite.go` to `internal/mcp/` listing. |
| `docs/architecture.md` | Component table | Modified | Update `mcp` component description to mention multi-server support. |
| `docs/functionalities.md` | MCP configuration examples | Modified | Replace `mcp:` examples with `mcp_servers:` list format. Update tool serialization description. |
| `docs/deployment.md` | Docker Compose diagram | No change | MCP server references are about the `mcp-resources` binary, not the config key. |
| `README.md` | (if MCP config examples exist) | Modified | Update any MCP configuration examples. |

### 4.5 Dependencies & Risks

- **No new external dependencies.** The `CompositeClient` uses only standard library types and the existing `mcp.Client` interface.
- **No removed dependencies.**
- **Breaking change:** All YAML configurations using `mcp:` must be updated to `mcp_servers:`. Any external tooling or scripts that generate agent configs will break.
- **Migration:** This is a configuration-only breaking change. No data migration is needed. No API breaking changes.
- **Rollback:** Revert the code changes and restore config files to `mcp:` format.
- **Risk: Tool name collisions.** Users who connect to multiple MCP servers must ensure unique tool names. The startup validation mitigates this risk by failing fast.

## 5. New & Modified Requirements

### New Requirements

#### FR-NEW-001: Multiple MCP Server Configuration

- **Description:** The agent must support configuring 0 to N MCP servers via the `mcp_servers` YAML key. Each entry is an object with `name` (required, unique), and either `url` (for HTTP transport) or `command`/`args` (for stdio transport).
- **Inputs:** YAML configuration file with `mcp_servers` list.
- **Outputs:** Agent starts with all configured MCP servers connected, or fails with a descriptive error.
- **Business Rules:**
  - `name` field is required for every entry; config loading fails if missing
  - `name` must be unique across entries; config loading fails on duplicates
  - Empty list or omitted `mcp_servers` key is valid (agent has no MCP tools)
  - Each entry must have either `url` or `command` set (not both, not neither) -- existing validation from `mcp.NewClient()` applies
- **Priority:** Must-have

#### FR-NEW-002: Composite MCP Client

- **Description:** The system must provide a `CompositeClient` that implements the `mcp.Client` interface and wraps multiple underlying MCP clients. It must aggregate tools, detect duplicates, and route `CallTool()` to the correct sub-client.
- **Inputs:** A list of named MCP clients.
- **Outputs:** A single `mcp.Client` that exposes the merged tool set and routes calls correctly.
- **Business Rules:**
  - `Start()` starts all sub-clients in order; if any fails, all previously started clients are stopped and the error is returned
  - `Stop()` stops all sub-clients; errors are collected but all clients are attempted
  - `Tools()` returns the merged, deduplicated tool list with each tool's `Server` field set
  - `GetTool(name)` returns the tool or nil
  - `CallTool(name, args)` routes to the owning sub-client; returns error if tool not found
  - Duplicate tool names across sub-clients cause `Start()` to fail with a descriptive error
  - `CallTool()` is serialized with a mutex for thread safety in parallel orchestration
- **Priority:** Must-have

#### FR-NEW-003: Duplicate Tool Name Detection

- **Description:** During startup, after all MCP servers have been connected and their tools loaded, the system must check for duplicate tool names across servers. If duplicates are found, startup must fail with an error identifying the conflicting tool names and the servers that provide them.
- **Inputs:** Tool lists from all connected MCP servers.
- **Outputs:** Startup success (no duplicates) or startup failure with descriptive error message.
- **Business Rules:**
  - Error message format: `duplicate tool name "X" found in MCP servers "server-a" and "server-b"`
  - All sub-clients must be stopped on duplicate detection (cleanup)
- **Priority:** Must-have

#### FR-NEW-004: Tool Server Attribution in API Response

- **Description:** Each tool returned by the `/tools` API endpoint must include a `server` field indicating which MCP server provides it. A2A synthetic tools will have an empty `server` field.
- **Inputs:** GET `/tools` request.
- **Outputs:** JSON response with `tools` array, each tool having a `server` field (string, omitted if empty).
- **Business Rules:**
  - `server` field value matches the `name` from the `mcp_servers` configuration entry
  - A2A synthetic tools (prefixed with `a2a_`) do not set the `server` field
- **Priority:** Must-have

### Modified Requirements

#### FR-MOD-001: Agent Startup MCP Initialization (references agent.go Start())

- **Original behavior:** `Start()` creates a single `mcp.Client` from `config.MCP` (single `MCPConfig`).
- **New behavior:** `Start()` iterates `config.MCPServers`, creates an `mcp.Client` for each entry, wraps them in a `CompositeClient`, and assigns it to `a.mcpClient`. If any server fails to connect, the agent fails to start entirely.
- **Reason for change:** Supporting multiple MCP servers requires initializing multiple clients.
- **Business Rules:**
  - All-or-nothing startup: failure of any MCP server connection fails the entire agent startup
  - The `mcpMu` mutex is removed from `Agent` -- serialization is handled by the `CompositeClient`

#### FR-MOD-002: Agent Shutdown MCP Cleanup (references agent.go Stop())

- **Original behavior:** `Stop()` calls `a.mcpClient.Stop()` on the single client.
- **New behavior:** `Stop()` calls `a.mcpClient.Stop()` on the `CompositeClient`, which in turn stops all sub-clients.
- **Reason for change:** All sub-clients must be properly cleaned up.
- **Business Rules:** Errors from individual sub-client stops are logged but do not prevent other sub-clients from being stopped.

#### FR-MOD-003: Startup Logging (references cmd/agent/main.go)

- **Original behavior:** Logs a single MCP server URL or command at startup.
- **New behavior:** Logs each configured MCP server with its name and transport type.
- **Reason for change:** Multiple servers need individual log lines for visibility.
- **Business Rules:**
  - Log format: `MCP Server [name]: url` or `MCP Server [name]: command`
  - If no MCP servers configured, log: `No MCP servers configured`

### Removed Requirements

None. No existing functionality is removed.

## 6. Non-Functional Requirements Changes

#### NFR-001: Startup Performance

- **Description:** Agent startup time will increase linearly with the number of configured MCP servers, since each server connection includes retries (up to 10 seconds per server). This is acceptable and inherent to the all-or-nothing approach.
- **Impact:** Existing behavior for 1 server is unchanged. Each additional server adds up to 10 seconds worst-case to startup.

#### NFR-002: Thread Safety

- **Description:** The `CompositeClient` must be thread-safe. `CallTool()` must be serialized with a mutex to prevent concurrent calls to the same sub-client (maintaining the current `mcpMu` safety guarantee but moving it into the MCP package).
- **Impact:** Same concurrency guarantees as today, with the serialization responsibility moved from `Agent` to `CompositeClient`.

## 7. Documentation Updates

All documentation changes listed below MUST be implemented as part of this change.

### 7.1 CLAUDE.md & .agent_docs/

**CLAUDE.md:**
- Replace the `Configuration (config/agent.yaml)` section's `mcp:` examples with `mcp_servers:` list format in both "Simple mode" and "Orchestrated mode" examples
- Update the `Config` struct implied by the YAML examples: `mcp` key becomes `mcp_servers` list
- In the "Project Structure" section, add `client_composite.go` under `internal/mcp/`
- Update "Key Concepts" to state: "**MCP Server**: One or more standalone services providing tools. Configured as a list under `mcp_servers`."
- In the "Agent node fields" table, no changes needed (MCP config is at the top level, not per-node)

**.agent_docs/golang.md:**
- Update the `internal/mcp/` line in "Package Organization" to: `internal/mcp/`: MCP client: `Client` interface, `StdioClient`, `HTTPClient`, `CompositeClient` (multi-server aggregation), and protocol types

### 7.2 docs/*

**docs/overview.md:**
- Update the project structure tree to add `client_composite.go` under `internal/mcp/`

**docs/architecture.md:**
- Update the `mcp` component table row to mention multi-server support and `CompositeClient`

**docs/functionalities.md:**
- Replace the two `mcp:` YAML examples (lines 105 and 544) with `mcp_servers:` list format
- Update the tool serialization paragraph (line 121) to mention that serialization is handled by `CompositeClient` rather than `Agent.mcpMu`
- Add a subsection documenting multi-MCP configuration: format, name requirements, duplicate detection

### 7.3 README.md

- Update any MCP configuration examples from `mcp:` to `mcp_servers:` format (if present)

## 8. End-to-End Test Updates

All test changes MUST be implemented in the `tests/` directory (and root-level E2E test files). Every modification MUST have tests covering happy paths, failure paths, edge cases, and error recovery.

### 8.1 Test Summary

| Test ID | Action | Category | Scenario | Priority |
|---------|--------|----------|----------|----------|
| UT-NEW-001 | New | Unit | Config: parse multiple MCP servers | Critical |
| UT-NEW-002 | New | Unit | Config: validation rejects missing name | Critical |
| UT-NEW-003 | New | Unit | Config: validation rejects duplicate names | Critical |
| UT-NEW-004 | New | Unit | Config: empty mcp_servers list is valid | High |
| UT-NEW-005 | New | Unit | CompositeClient: start/stop lifecycle | Critical |
| UT-NEW-006 | New | Unit | CompositeClient: tool aggregation from multiple clients | Critical |
| UT-NEW-007 | New | Unit | CompositeClient: duplicate tool detection | Critical |
| UT-NEW-008 | New | Unit | CompositeClient: CallTool routes to correct client | Critical |
| UT-NEW-009 | New | Unit | CompositeClient: CallTool returns error for unknown tool | High |
| UT-NEW-010 | New | Unit | CompositeClient: partial start failure rolls back | High |
| UT-MOD-001 | Modified | Unit | Config: existing tests use new mcp_servers format | Critical |
| E2E-MOD-001 | Modified | E2E | TestMain config override uses MCPServers slice | Critical |
| E2E-EXIST-* | Unchanged | E2E | All existing E2E tests pass unchanged | Critical |

### 8.2 New Tests

#### UT-NEW-001: Config parse multiple MCP servers

- **Category:** Unit
- **Modification:** MOD-001
- **Preconditions:** Temp YAML file with two `mcp_servers` entries
- **Steps:**
  - Given a YAML config with `mcp_servers:` containing two entries (name: "server-a", url: "http://a:8090/mcp") and (name: "server-b", url: "http://b:9090/mcp")
  - When `config.Load()` is called
  - Then `cfg.MCPServers` has length 2, with correct names and URLs
- **Priority:** Critical

#### UT-NEW-002: Config validation rejects missing name

- **Category:** Unit
- **Modification:** MOD-001
- **Preconditions:** Temp YAML file with an `mcp_servers` entry missing the `name` field
- **Steps:**
  - Given a YAML config with `mcp_servers:` containing one entry with `url` but no `name`
  - When `config.Load()` is called
  - Then an error is returned containing "name is required"
- **Priority:** Critical

#### UT-NEW-003: Config validation rejects duplicate names

- **Category:** Unit
- **Modification:** MOD-001
- **Preconditions:** Temp YAML file with two `mcp_servers` entries having the same name
- **Steps:**
  - Given a YAML config with `mcp_servers:` containing two entries both named "resources"
  - When `config.Load()` is called
  - Then an error is returned containing "duplicate" and "resources"
- **Priority:** Critical

#### UT-NEW-004: Config empty mcp_servers list is valid

- **Category:** Unit
- **Modification:** MOD-001
- **Preconditions:** Temp YAML file with no `mcp_servers` key
- **Steps:**
  - Given a YAML config with no `mcp_servers` key
  - When `config.Load()` is called
  - Then `cfg.MCPServers` is an empty slice (or nil), no error
- **Priority:** High

#### UT-NEW-005: CompositeClient start/stop lifecycle

- **Category:** Unit
- **Modification:** MOD-002
- **Preconditions:** Mock MCP clients
- **Steps:**
  - Given a `CompositeClient` wrapping two mock clients
  - When `Start()` is called
  - Then both mock clients' `Start()` are called in order
  - When `Stop()` is called
  - Then both mock clients' `Stop()` are called
- **Priority:** Critical

#### UT-NEW-006: CompositeClient tool aggregation

- **Category:** Unit
- **Modification:** MOD-002
- **Preconditions:** Mock clients with distinct tool sets
- **Steps:**
  - Given mock client "server-a" with tools ["tool_x", "tool_y"] and mock client "server-b" with tools ["tool_z"]
  - When the composite client is started and `Tools()` is called
  - Then 3 tools are returned, each with correct `Server` field
- **Priority:** Critical

#### UT-NEW-007: CompositeClient duplicate tool detection

- **Category:** Unit
- **Modification:** MOD-004
- **Preconditions:** Mock clients with overlapping tool names
- **Steps:**
  - Given mock client "server-a" with tool "shared_tool" and mock client "server-b" with tool "shared_tool"
  - When the composite client's `Start()` is called
  - Then an error is returned containing "duplicate tool name" and both server names
  - And both mock clients' `Stop()` are called (cleanup)
- **Priority:** Critical

#### UT-NEW-008: CompositeClient CallTool routes correctly

- **Category:** Unit
- **Modification:** MOD-002
- **Preconditions:** Mock clients with distinct tools
- **Steps:**
  - Given mock client "server-a" with tool "tool_x" and mock client "server-b" with tool "tool_z"
  - When `CallTool("tool_z", args)` is called
  - Then mock client "server-b"'s `CallTool` is invoked with the correct arguments
  - And mock client "server-a"'s `CallTool` is not invoked
- **Priority:** Critical

#### UT-NEW-009: CompositeClient CallTool unknown tool

- **Category:** Unit
- **Modification:** MOD-002
- **Preconditions:** Mock clients with known tools
- **Steps:**
  - Given a started composite client with tools ["tool_x", "tool_z"]
  - When `CallTool("nonexistent", args)` is called
  - Then an error is returned containing "tool not found"
- **Priority:** High

#### UT-NEW-010: CompositeClient partial start failure rolls back

- **Category:** Unit
- **Modification:** MOD-002
- **Preconditions:** Mock client "server-a" starts successfully, mock client "server-b" fails to start
- **Steps:**
  - Given two mock clients where "server-b" returns an error on `Start()`
  - When the composite client's `Start()` is called
  - Then an error is returned
  - And mock client "server-a"'s `Stop()` is called (rollback)
- **Priority:** High

### 8.3 Modified Tests

#### UT-MOD-001: Config existing tests (was `TestLoad`, `TestLoad_SynthesizesDefaultAgentNode`)

- **Original test:** Used YAML with `mcp:\n  command: ./bin/mcp\n` format.
- **Modified to validate:** Same test logic but YAML strings updated to `mcp_servers:\n  - name: default\n    command: ./bin/mcp\n` format. Assertions remain the same (test is about defaults, not MCP config parsing).
- **Steps:**
  - Given a YAML config with `mcp_servers:` list format
  - When `config.Load()` is called
  - Then defaults are applied correctly (same assertions as before)

#### E2E-MOD-001: TestMain config override (was `e2e_test.go:TestMain`)

- **Original test:** Overrides `cfg.MCP.URL`, `cfg.MCP.Command`, `cfg.MCP.Args` directly.
- **Modified to validate:** Overrides `cfg.MCPServers` with a single-element slice: `[]config.MCPServerConfig{{Name: "resources", URL: "http://localhost:8090/mcp"}}`.
- **Steps:**
  - Given the loaded config from `config/agent.yaml`
  - When `cfg.MCPServers` is overridden with test values
  - Then the agent starts successfully with the test MCP server

### 8.4 Removed Tests

None. No tests are removed.

## 9. Consistency Notes

No existing specification documents exist in `specs/`. No inconsistencies to report.

The `CLAUDE.md` documentation currently describes the `mcp:` configuration format extensively. All examples must be updated atomically with the code change to avoid inconsistency between documentation and implementation.

## 10. Migration & Implementation Notes

### Suggested Implementation Order

1. **Config layer** (MOD-001): Update `MCPConfig` to `MCPServerConfig` with `Name` field. Change `Config.MCP` to `Config.MCPServers []MCPServerConfig`. Add validation. Update config tests.
2. **MCP protocol** (MOD-005): Add `Server` field to `mcp.Tool` struct.
3. **Composite client** (MOD-002, MOD-004): Create `internal/mcp/client_composite.go` with `CompositeClient`. Write unit tests.
4. **Agent integration** (MOD-003): Update `Agent.Start()` and `Agent.Stop()`. Remove `mcpMu`. Update `cmd/agent/main.go` logging.
5. **Config files** (MOD-006): Update all YAML files.
6. **E2E test fix** (E2E-MOD-001): Update `e2e_test.go` `TestMain`.
7. **Documentation**: Update `CLAUDE.md`, `.agent_docs/golang.md`, `docs/overview.md`, `docs/architecture.md`, `docs/functionalities.md`.
8. **Validation**: Run `make check` (unit tests + linting) and `make e2e` (full E2E suite).

### Rollback Strategy

Revert the git branch. No data migration is involved -- the change is purely in configuration parsing and client initialization.

### Feature Flags

None needed. This is a clean breaking change to the configuration format.

## 11. Open Questions & TBDs

None. All decisions have been made during the interview.
