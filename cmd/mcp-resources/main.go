package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// JSON-RPC types.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP types.
type Tool struct {
	Name            string      `json:"name"`
	Description     string      `json:"description"`
	InputSchema     InputSchema `json:"inputSchema"`
	DestructiveHint bool        `json:"destructiveHint,omitempty"`
}

type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

type Property struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Resource represents a managed resource.
type Resource struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Value     int    `json:"value"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Server is the MCP resources server.
type Server struct {
	db     *sql.DB
	tools  []Tool
	reader *bufio.Reader
}

func main() {
	dbPath := flag.String("db", "./resources.db", "path to SQLite database")
	flag.Parse()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0755); err != nil {
		log.Fatalf("Failed to create database directory: %v", err)
	}

	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	server := &Server{
		db:     db,
		reader: bufio.NewReader(os.Stdin),
	}

	if err := server.initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	server.initTools()
	server.run()
}

func (s *Server) initDB() error {
	_, err := s.db.Exec(`
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

func (s *Server) initTools() {
	s.tools = []Tool{
		{
			Name:            "resources_add",
			Description:     "Add a new resource with a name and integer value",
			DestructiveHint: true,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"name":  {Type: "string", Description: "The name of the resource"},
					"value": {Type: "integer", Description: "The integer value of the resource"},
				},
				Required: []string{"name", "value"},
			},
		},
		{
			Name:            "resources_remove",
			Description:     "Remove resources by ID or by name pattern (regex)",
			DestructiveHint: true,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"id":      {Type: "string", Description: "The ID of the resource to remove"},
					"pattern": {Type: "string", Description: "Regex pattern to match resource names to remove"},
				},
			},
		},
		{
			Name:            "resources_list",
			Description:     "List resources, optionally filtered by name pattern (regex)",
			DestructiveHint: false,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"pattern": {Type: "string", Description: "Optional regex pattern to filter resource names"},
				},
			},
		},
	}
}

func (s *Server) run() {
	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error")
			continue
		}

		s.handleRequest(&req)
	}
}

func (s *Server) handleRequest(req *Request) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "notifications/initialized":
		// No response needed for notifications
	case "tools/list":
		s.handleToolsList(req)
	case "tools/call":
		s.handleToolsCall(req)
	default:
		s.sendError(req.ID, -32601, "Method not found: "+req.Method)
	}
}

func (s *Server) handleInitialize(req *Request) {
	result := map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]string{
			"name":    "mcp-resources",
			"version": "1.0.0",
		},
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
	}
	s.sendResult(req.ID, result)
}

func (s *Server) handleToolsList(req *Request) {
	result := map[string]any{
		"tools": s.tools,
	}
	s.sendResult(req.ID, result)
}

func (s *Server) handleToolsCall(req *Request) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, -32602, "Invalid params")
		return
	}

	var result any
	var err error

	switch params.Name {
	case "resources_add":
		result, err = s.toolAdd(params.Arguments)
	case "resources_remove":
		result, err = s.toolRemove(params.Arguments)
	case "resources_list":
		result, err = s.toolList(params.Arguments)
	default:
		s.sendError(req.ID, -32602, "Unknown tool: "+params.Name)
		return
	}

	if err != nil {
		s.sendResult(req.ID, map[string]any{
			"content": []ContentBlock{{Type: "text", Text: "Error: " + err.Error()}},
			"isError": true,
		})
		return
	}

	text, _ := json.MarshalIndent(result, "", "  ")
	s.sendResult(req.ID, map[string]any{
		"content": []ContentBlock{{Type: "text", Text: string(text)}},
	})
}

func (s *Server) toolAdd(args map[string]any) (any, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return nil, fmt.Errorf("name is required")
	}

	valueFloat, ok := args["value"].(float64)
	if !ok {
		return nil, fmt.Errorf("value is required and must be an integer")
	}
	value := int(valueFloat)

	id := generateID()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.Exec(
		"INSERT INTO resources (id, name, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		id, name, value, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to add resource: %w", err)
	}

	return Resource{
		ID:        id,
		Name:      name,
		Value:     value,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *Server) toolRemove(args map[string]any) (any, error) {
	id, hasID := args["id"].(string)
	pattern, hasPattern := args["pattern"].(string)

	if !hasID && !hasPattern {
		return nil, fmt.Errorf("either id or pattern is required")
	}

	var removed []string

	if hasID && id != "" {
		result, err := s.db.Exec("DELETE FROM resources WHERE id = ?", id)
		if err != nil {
			return nil, fmt.Errorf("failed to remove resource: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected > 0 {
			removed = append(removed, id)
		}
	}

	if hasPattern && pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern: %w", err)
		}

		rows, err := s.db.Query("SELECT id, name FROM resources")
		if err != nil {
			return nil, fmt.Errorf("failed to query resources: %w", err)
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
			_, err := s.db.Exec("DELETE FROM resources WHERE id = ?", resID)
			if err == nil {
				removed = append(removed, resID)
			}
		}
	}

	return map[string]any{
		"removed_count": len(removed),
		"removed_ids":   removed,
	}, nil
}

func (s *Server) toolList(args map[string]any) (any, error) {
	pattern, _ := args["pattern"].(string)

	rows, err := s.db.Query("SELECT id, name, value, created_at, updated_at FROM resources ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("failed to query resources: %w", err)
	}
	defer rows.Close()

	var re *regexp.Regexp
	if pattern != "" {
		re, err = regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern: %w", err)
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

	return map[string]any{
		"resources": resources,
		"count":     len(resources),
	}, nil
}

func (s *Server) sendResult(id any, result any) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	s.send(resp)
}

func (s *Server) sendError(id any, code int, message string) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	}
	s.send(resp)
}

func (s *Server) send(resp Response) {
	data, _ := json.Marshal(resp)
	fmt.Println(string(data))
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
