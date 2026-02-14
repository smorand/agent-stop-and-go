package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

// Config holds the mcp-resources server configuration.
type Config struct {
	Host   string `yaml:"host"`
	Port   int    `yaml:"port"`
	DBPath string `yaml:"db_path"`
}

// Resource represents a managed resource.
type Resource struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Value     int    `json:"value"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func main() {
	configPath := flag.String("config", "", "path to YAML config file")
	dbPath := flag.String("db", "", "path to SQLite database (overrides config)")
	flag.Parse()

	cfg := Config{
		Host:   "0.0.0.0",
		Port:   8090,
		DBPath: "./data/resources.db",
	}

	// Load config from file if provided
	if *configPath != "" {
		data, err := os.ReadFile(*configPath)
		if err != nil {
			log.Fatalf("Failed to read config file: %v", err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			log.Fatalf("Failed to parse config file: %v", err)
		}
	}

	// CLI flag overrides config
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}

	// Defaults
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 8090
	}

	// Ensure database directory exists
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0755); err != nil {
		log.Fatalf("Failed to create database directory: %v", err)
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	if err := initDB(db); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Create MCP server
	mcpServer := server.NewMCPServer("mcp-resources", "1.0.0",
		server.WithToolCapabilities(true),
	)

	// Register tools
	mcpServer.AddTool(
		mcp.NewTool("resources_add",
			mcp.WithDescription("Add a new resource with a name and integer value"),
			mcp.WithString("name", mcp.Required(), mcp.Description("The name of the resource")),
			mcp.WithNumber("value", mcp.Required(), mcp.Description("The integer value of the resource")),
			mcp.WithDestructiveHintAnnotation(true),
		),
		makeToolAdd(db),
	)

	mcpServer.AddTool(
		mcp.NewTool("resources_remove",
			mcp.WithDescription("Remove resources by ID or by name pattern (regex)"),
			mcp.WithString("id", mcp.Description("The ID of the resource to remove")),
			mcp.WithString("pattern", mcp.Description("Regex pattern to match resource names to remove")),
			mcp.WithDestructiveHintAnnotation(true),
		),
		makeToolRemove(db),
	)

	mcpServer.AddTool(
		mcp.NewTool("resources_list",
			mcp.WithDescription("List resources, optionally filtered by name pattern (regex)"),
			mcp.WithString("pattern", mcp.Description("Optional regex pattern to filter resource names")),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		makeToolList(db),
	)

	// Start Streamable HTTP server with health endpoint
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Printf("Starting mcp-resources on %s (db: %s)", addr, cfg.DBPath)

	httpServer := server.NewStreamableHTTPServer(mcpServer)

	mux := http.NewServeMux()
	mux.Handle("/mcp", httpServer)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func initDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS resources (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			value INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	return err
}

func makeToolAdd(db *sql.DB) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := req.GetString("name", "")
		if name == "" {
			return mcp.NewToolResultError("name is required"), nil
		}

		value := req.GetInt("value", 0)

		id := generateID()
		now := time.Now().UTC().Format(time.RFC3339)

		_, err := db.Exec(
			"INSERT INTO resources (id, name, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
			id, name, value, now, now,
		)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to add resource: %v", err)), nil
		}

		result := Resource{
			ID:        id,
			Name:      name,
			Value:     value,
			CreatedAt: now,
			UpdatedAt: now,
		}
		text, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(text)), nil
	}
}

func makeToolRemove(db *sql.DB) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := req.GetString("id", "")
		pattern := req.GetString("pattern", "")

		if id == "" && pattern == "" {
			return mcp.NewToolResultError("either id or pattern is required"), nil
		}

		var removed []string

		if id != "" {
			result, err := db.Exec("DELETE FROM resources WHERE id = ?", id)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to remove resource: %v", err)), nil
			}
			affected, _ := result.RowsAffected()
			if affected > 0 {
				removed = append(removed, id)
			}
		}

		if pattern != "" {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid pattern: %v", err)), nil
			}

			rows, err := db.Query("SELECT id, name FROM resources")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to query resources: %v", err)), nil
			}
			defer rows.Close()

			var toRemove []string
			for rows.Next() {
				var resID, name string
				if err := rows.Scan(&resID, &name); err != nil {
					continue
				}
				if re.MatchString(name) {
					toRemove = append(toRemove, resID)
				}
			}

			for _, resID := range toRemove {
				_, err := db.Exec("DELETE FROM resources WHERE id = ?", resID)
				if err == nil {
					removed = append(removed, resID)
				}
			}
		}

		result := map[string]any{
			"removed_count": len(removed),
			"removed_ids":   removed,
		}
		text, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(text)), nil
	}
}

func makeToolList(db *sql.DB) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		pattern := req.GetString("pattern", "")

		rows, err := db.Query("SELECT id, name, value, created_at, updated_at FROM resources ORDER BY name")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query resources: %v", err)), nil
		}
		defer rows.Close()

		var re *regexp.Regexp
		if pattern != "" {
			re, err = regexp.Compile(pattern)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid pattern: %v", err)), nil
			}
		}

		var resources []Resource
		for rows.Next() {
			var r Resource
			if err := rows.Scan(&r.ID, &r.Name, &r.Value, &r.CreatedAt, &r.UpdatedAt); err != nil {
				continue
			}
			if re == nil || re.MatchString(r.Name) {
				resources = append(resources, r)
			}
		}

		result := map[string]any{
			"resources": resources,
			"count":     len(resources),
		}
		text, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(text)), nil
	}
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
