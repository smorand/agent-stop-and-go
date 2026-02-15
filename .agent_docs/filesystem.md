# MCP Filesystem Server

Sandboxed filesystem MCP server providing 15 tools with chroot-like security.

## Architecture

- Entry point: `cmd/mcp-filesystem/main.go`
- Core package: `internal/filesystem/`
  - `config.go` - Config struct, YAML loading, root resolution with `ResolveRoots()`
  - `security.go` - `ValidatePath()`, `CheckAllowlist()`, `GetRoot()`, symlink-aware chroot enforcement
  - `patch.go` - `ParsePatch()` / `ApplyPatch()` for unified diff format
  - `tools.go` - `Server` struct with 15 tool handler methods, plus `copyFile`/`copyDir` helpers
- Config file: `config/mcp-filesystem.yaml`
- Default port: 8091

## Security Model

- Each root directory is resolved to an absolute real path at startup via `EvalSymlinks` + `Abs`
- Every tool call validates paths with `ValidatePath()` which:
  1. Rejects null bytes
  2. Cleans the relative path
  3. Resolves symlinks (or walks up to nearest existing ancestor for new files)
  4. Checks the resolved path is within the root boundary
- Per-root `allowed_tools` allowlist controls which operations are permitted
- `"*"` in allowlist enables all tools; otherwise list specific tool names
- Root directory itself cannot be removed (`remove_folder` checks `resolved == root.RealPath`)
- Binary file detection: reads first 8KB and checks for null bytes

## Tools (14 per-root + 1 meta)

**Meta**: `list_roots` (no root param required)

**Read-only**: `list_folder`, `read_file`, `stat_file`, `hash_file`, `permissions_file`, `grep`, `glob`

**Write**: `write_file` (overwrite/append/create_only), `remove_file`, `patch_file`, `create_folder`, `remove_folder`, `copy`, `move`

## Key Behaviors

- `read_file`: Byte-based and line-based params are mutually exclusive. Full reads capped by `max_full_read_size` (default 1MB).
- `write_file`: Auto-creates parent directories. Modes: `overwrite`, `append`, `create_only`.
- `patch_file`: Atomic — all hunks must apply or none do. Can create files from empty.
- `copy`/`move`: Cross-root operations supported. Both roots must have the tool in their allowlist. Move uses `os.Rename` when possible, falls back to copy+delete for cross-device.
- `grep`: Regex search with optional `glob_filter`, `context_lines`, `max_results` (default 100), `timeout_seconds` (default 300), `max_depth`. Skips binary files.
- `glob`: Supports `**` patterns and regex. `type_filter`: file/directory/symlink/all. `max_results` (default 100).

## Config Schema

```yaml
host: 0.0.0.0           # default
port: 8091               # default
max_full_read_size: 1048576  # default 1MB

roots:
  - name: workspace      # unique name, used in tool params
    path: ./data/workspace
    allowed_tools:
      - "*"              # or list specific tools
```

## CLI Flags

- `--config` (required): Path to YAML config file
- `--port`: Override listen port from config

## E2E Tests

File: `e2e_filesystem_test.go` (build tag: `e2e`)
- Each test starts its own `mcp-filesystem` binary with isolated temp dirs on ports 9190+
- Creates five roots: workspace, source, dest, readonly (read-only tools only), restricted
- Tests: core journeys, read/write, patch, copy/move, grep/glob, security (symlink escape, path traversal), error handling

## ValidToolNames

The complete list of valid tool names for allowlists (from `config.go`):
`list_folder`, `read_file`, `write_file`, `remove_file`, `patch_file`, `create_folder`, `remove_folder`, `stat_file`, `hash_file`, `permissions_file`, `copy`, `move`, `grep`, `glob`

Note: `list_roots` is always available and not in ValidToolNames (not per-root).
