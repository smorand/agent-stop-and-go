package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"agent-stop-and-go/internal/filesystem"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file")
	port := flag.Int("port", 0, "override listen port")
	flag.Parse()

	// Setup structured JSON logging
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg := &filesystem.Config{
		Host:            "0.0.0.0",
		Port:            8091,
		MaxFullReadSize: 1048576,
	}

	if *configPath != "" {
		var err error
		cfg, err = filesystem.LoadConfig(*configPath)
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	}

	if *port != 0 {
		cfg.Port = *port
	}

	if len(cfg.Roots) == 0 {
		log.Fatal("No roots configured. Provide a config file with --config")
	}

	roots, err := filesystem.ResolveRoots(cfg)
	if err != nil {
		log.Fatalf("Failed to resolve roots: %v", err)
	}

	// Log startup info (root names only, not paths — security)
	rootNames := make([]string, 0, len(roots))
	for name := range roots {
		rootNames = append(rootNames, name)
	}
	slog.Info("starting mcp-filesystem",
		"address", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		"roots", rootNames,
		"config", *configPath,
	)

	fs := filesystem.NewServer(roots, cfg.MaxFullReadSize)

	// Create MCP server
	mcpServer := server.NewMCPServer("mcp-filesystem", "1.0.0",
		server.WithToolCapabilities(true),
	)

	// Register tools
	registerTools(mcpServer, fs)

	// Start HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	httpServer := server.NewStreamableHTTPServer(mcpServer)

	mux := http.NewServeMux()
	mux.Handle("/mcp", httpServer)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	slog.Info("server listening", "address", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func registerTools(mcpServer *server.MCPServer, fs *filesystem.Server) {
	// list_roots — always available meta-tool
	mcpServer.AddTool(
		mcp.NewTool("list_roots",
			mcp.WithDescription("List all configured root directories and their allowed tools. Use this to discover available roots before performing operations."),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		fs.ListRoots(),
	)

	// list_folder
	mcpServer.AddTool(
		mcp.NewTool("list_folder",
			mcp.WithDescription("List the contents of a directory within a root. Returns entries sorted alphabetically with name, type (file/directory/symlink), size, and modification time."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name (use list_roots to discover available roots)")),
			mcp.WithString("path", mcp.Required(), mcp.Description("Relative path within the root (use '.' for root directory)")),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		fs.ListFolder(),
	)

	// read_file
	mcpServer.AddTool(
		mcp.NewTool("read_file",
			mcp.WithDescription("Read file contents, fully or partially. For large files, use offset/limit parameters. Byte-based and line-based parameters are mutually exclusive."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("path", mcp.Required(), mcp.Description("Relative file path within the root")),
			mcp.WithNumber("offset_bytes", mcp.Description("Byte offset to start reading from")),
			mcp.WithNumber("limit_bytes", mcp.Description("Maximum number of bytes to read")),
			mcp.WithNumber("offset_lines", mcp.Description("Line number to start reading from (1-based)")),
			mcp.WithNumber("limit_lines", mcp.Description("Maximum number of lines to read")),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		fs.ReadFile(),
	)

	// write_file
	mcpServer.AddTool(
		mcp.NewTool("write_file",
			mcp.WithDescription("Write content to a file. Modes: 'overwrite' (default, replaces content), 'append' (adds to end), 'create_only' (fails if file exists). Parent directories are created automatically."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("path", mcp.Required(), mcp.Description("Relative file path within the root")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Content to write")),
			mcp.WithString("mode", mcp.Description("Write mode: overwrite (default), append, or create_only")),
		),
		fs.WriteFile(),
	)

	// remove_file
	mcpServer.AddTool(
		mcp.NewTool("remove_file",
			mcp.WithDescription("Delete a single file. Fails if the path is a directory (use remove_folder instead)."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("path", mcp.Required(), mcp.Description("Relative file path to delete")),
		),
		fs.RemoveFile(),
	)

	// patch_file
	mcpServer.AddTool(
		mcp.NewTool("patch_file",
			mcp.WithDescription("Apply a unified diff patch to a file. The patch must be in standard unified diff format. If the file doesn't exist, it's treated as patching an empty file (creation). The operation is atomic: all hunks must apply or none do."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("path", mcp.Required(), mcp.Description("Relative file path to patch")),
			mcp.WithString("patch", mcp.Required(), mcp.Description("Unified diff content to apply")),
		),
		fs.PatchFile(),
	)

	// create_folder
	mcpServer.AddTool(
		mcp.NewTool("create_folder",
			mcp.WithDescription("Create a directory and any missing parent directories. Idempotent: succeeds if directory already exists."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("path", mcp.Required(), mcp.Description("Relative directory path to create")),
		),
		fs.CreateFolder(),
	)

	// remove_folder
	mcpServer.AddTool(
		mcp.NewTool("remove_folder",
			mcp.WithDescription("Remove a directory and all its contents recursively. Cannot remove the root directory itself. Fails if path is a file (use remove_file instead)."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("path", mcp.Required(), mcp.Description("Relative directory path to remove")),
		),
		fs.RemoveFolder(),
	)

	// stat_file
	mcpServer.AddTool(
		mcp.NewTool("stat_file",
			mcp.WithDescription("Return metadata about a file or directory: name, size, type, modification time, symlink status."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("path", mcp.Required(), mcp.Description("Relative path to inspect")),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		fs.StatFile(),
	)

	// hash_file
	mcpServer.AddTool(
		mcp.NewTool("hash_file",
			mcp.WithDescription("Compute a cryptographic hash of a file. Supported algorithms: md5, sha1, sha256. The file is streamed (not loaded into memory)."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("path", mcp.Required(), mcp.Description("Relative file path")),
			mcp.WithString("algorithm", mcp.Required(), mcp.Description("Hash algorithm: md5, sha1, or sha256")),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		fs.HashFile(),
	)

	// permissions_file
	mcpServer.AddTool(
		mcp.NewTool("permissions_file",
			mcp.WithDescription("Return the owner, group, and permission bits of a file or directory."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("path", mcp.Required(), mcp.Description("Relative path to inspect")),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		fs.PermissionsFile(),
	)

	// copy
	mcpServer.AddTool(
		mcp.NewTool("copy",
			mcp.WithDescription("Copy a file or directory, potentially across roots. Both source and destination roots must have 'copy' in their allowlist. Directories are copied recursively. Parent directories are created automatically."),
			mcp.WithString("source_root", mcp.Required(), mcp.Description("Source root name")),
			mcp.WithString("source_path", mcp.Required(), mcp.Description("Relative source path")),
			mcp.WithString("dest_root", mcp.Required(), mcp.Description("Destination root name")),
			mcp.WithString("dest_path", mcp.Required(), mcp.Description("Relative destination path")),
		),
		fs.Copy(),
	)

	// move
	mcpServer.AddTool(
		mcp.NewTool("move",
			mcp.WithDescription("Move (rename) a file or directory, potentially across roots. Both source and destination roots must have 'move' in their allowlist. Same-root moves use atomic rename. Cross-root moves use copy + delete."),
			mcp.WithString("source_root", mcp.Required(), mcp.Description("Source root name")),
			mcp.WithString("source_path", mcp.Required(), mcp.Description("Relative source path")),
			mcp.WithString("dest_root", mcp.Required(), mcp.Description("Destination root name")),
			mcp.WithString("dest_path", mcp.Required(), mcp.Description("Relative destination path")),
		),
		fs.Move(),
	)

	// grep
	mcpServer.AddTool(
		mcp.NewTool("grep",
			mcp.WithDescription("Search for a regex pattern within file contents under a root. Supports context lines, file type filtering, case-insensitive search, result limits, and timeouts. Binary files are automatically skipped."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("pattern", mcp.Required(), mcp.Description("Go-compatible regular expression")),
			mcp.WithString("path", mcp.Description("Subdirectory to search within (default: root directory)")),
			mcp.WithString("glob_filter", mcp.Description("Glob pattern to filter files (e.g., '*.go')")),
			mcp.WithBoolean("case_insensitive", mcp.Description("Enable case-insensitive matching (default: false)")),
			mcp.WithNumber("context_lines", mcp.Description("Lines of context before and after each match (default: 0)")),
			mcp.WithNumber("max_results", mcp.Description("Maximum matches to return (default: 100)")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Maximum search duration in seconds (default: 300)")),
			mcp.WithNumber("max_depth", mcp.Description("Maximum directory depth to traverse")),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		fs.Grep(),
	)

	// glob
	mcpServer.AddTool(
		mcp.NewTool("glob",
			mcp.WithDescription("Search for files by name pattern within a root. Use either a glob pattern (with ** for recursive) or a regex. Returns matches sorted alphabetically with path, type, size, and modification time."),
			mcp.WithString("root", mcp.Required(), mcp.Description("Root name")),
			mcp.WithString("pattern", mcp.Description("Glob pattern (e.g., '**/*.go'). Mutually exclusive with regex.")),
			mcp.WithString("regex", mcp.Description("Regex pattern matched against relative file paths. Mutually exclusive with pattern.")),
			mcp.WithString("path", mcp.Description("Subdirectory to search within (default: root directory)")),
			mcp.WithNumber("max_results", mcp.Description("Maximum results to return (default: 100)")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Maximum search duration in seconds (default: 300)")),
			mcp.WithNumber("max_depth", mcp.Description("Maximum directory depth to traverse")),
			mcp.WithString("type_filter", mcp.Description("Filter by type: file, directory, symlink, or all (default: all)")),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		fs.Glob(),
	)
}
