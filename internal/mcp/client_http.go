package mcp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"agent-stop-and-go/internal/auth"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

const (
	httpClientTimeout = 30 * time.Second
	connectRetryDelay = 500 * time.Millisecond
	connectMaxRetries = 20 // 20 * 500ms = 10s max wait
)

// HTTPClient communicates with an MCP server over Streamable HTTP.
type HTTPClient struct {
	url     string
	client  *mcpclient.Client
	tools   []Tool
	mu      sync.Mutex
	started bool
}

// NewHTTPClient creates a new HTTP MCP client.
func NewHTTPClient(url string) *HTTPClient {
	return &HTTPClient{url: url}
}

// Start connects to the MCP server and loads tools.
// It retries the connection to handle startup race conditions (e.g., Docker Compose).
func (c *HTTPClient) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return nil
	}

	var lastErr error
	for attempt := range connectMaxRetries {
		if err := c.connect(); err != nil {
			lastErr = err
			if attempt < connectMaxRetries-1 {
				log.Printf("MCP connection attempt %d/%d failed: %v, retrying in %v",
					attempt+1, connectMaxRetries, err, connectRetryDelay)
				time.Sleep(connectRetryDelay)
			}
			continue
		}
		c.started = true
		return nil
	}

	return fmt.Errorf("failed to connect to MCP server after %d attempts: %w", connectMaxRetries, lastErr)
}

// bearerHeaderFunc extracts the Bearer token from context and returns it as an Authorization header.
func bearerHeaderFunc(ctx context.Context) map[string]string {
	if token := auth.BearerToken(ctx); token != "" {
		return map[string]string{"Authorization": "Bearer " + token}
	}
	return nil
}

// connect attempts a single connection to the MCP server.
func (c *HTTPClient) connect() error {
	t, err := transport.NewStreamableHTTP(c.url, transport.WithHTTPHeaderFunc(bearerHeaderFunc))
	if err != nil {
		return fmt.Errorf("transport error: %w", err)
	}
	client := mcpclient.NewClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), httpClientTimeout)
	defer cancel()

	initReq := mcpgo.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpgo.Implementation{
		Name:    "agent",
		Version: "1.0.0",
	}
	initReq.Params.Capabilities = mcpgo.ClientCapabilities{}

	if _, err := client.Initialize(ctx, initReq); err != nil {
		client.Close()
		return fmt.Errorf("failed to send request: %w", err)
	}

	// Load available tools
	if err := c.loadToolsFrom(ctx, client); err != nil {
		client.Close()
		return fmt.Errorf("failed to load tools: %w", err)
	}

	c.client = client
	return nil
}

// Stop closes the connection to the MCP server.
func (c *HTTPClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return nil
	}

	c.started = false
	return c.client.Close()
}

// Tools returns the available tools.
func (c *HTTPClient) Tools() []Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tools
}

// GetTool returns a specific tool by name.
func (c *HTTPClient) GetTool(name string) *Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.tools {
		if t.Name == name {
			return &t
		}
	}
	return nil
}

// CallTool executes a tool with the given arguments.
// The caller's context is used as a parent so Bearer tokens are available to the transport.
// Returns AuthRequiredError if the MCP server responds with HTTP 401.
func (c *HTTPClient) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, httpClientTimeout)
	defer cancel()

	req := mcpgo.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := c.client.CallTool(ctx, req)
	if err != nil {
		if isHTTP401Error(err) {
			log.Printf("WARN: MCP server %s returned HTTP 401 for tool %s", c.url, name)
			return nil, &AuthRequiredError{Server: c.url, Tool: name}
		}
		return nil, fmt.Errorf("MCP tool call failed: %w", err)
	}

	return adaptCallToolResult(result), nil
}

// isHTTP401Error checks if an error from mcp-go indicates an HTTP 401 Unauthorized response.
func isHTTP401Error(err error) bool {
	return strings.Contains(err.Error(), "status 401")
}

// loadToolsFrom fetches tools from the given client and converts them.
func (c *HTTPClient) loadToolsFrom(ctx context.Context, client *mcpclient.Client) error {
	result, err := client.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		return err
	}

	c.tools = make([]Tool, 0, len(result.Tools))
	for _, t := range result.Tools {
		c.tools = append(c.tools, adaptTool(t))
	}
	return nil
}

// adaptTool converts an mcp-go Tool to our internal Tool type.
func adaptTool(t mcpgo.Tool) Tool {
	tool := Tool{
		Name:        t.Name,
		Description: t.Description,
	}

	// Convert input schema
	tool.InputSchema = adaptInputSchema(t.InputSchema)

	// Extract destructiveHint from annotations.
	// ReadOnlyHint=true always means non-destructive.
	// Per MCP spec, destructiveHint defaults to true when unset, so we
	// only mark a tool as destructive when explicitly annotated.
	if t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint {
		tool.DestructiveHint = false
	} else if t.Annotations.DestructiveHint != nil {
		tool.DestructiveHint = *t.Annotations.DestructiveHint
	}

	return tool
}

// adaptInputSchema converts an mcp-go ToolInputSchema to our InputSchema.
func adaptInputSchema(schema mcpgo.ToolInputSchema) InputSchema {
	is := InputSchema{
		Type:     schema.Type,
		Required: schema.Required,
	}

	if schema.Properties != nil {
		is.Properties = make(map[string]Property)
		for name, prop := range schema.Properties {
			p := Property{}
			if propMap, ok := prop.(map[string]any); ok {
				if t, ok := propMap["type"].(string); ok {
					p.Type = t
				}
				if d, ok := propMap["description"].(string); ok {
					p.Description = d
				}
			}
			is.Properties[name] = p
		}
	}

	return is
}

// adaptCallToolResult converts an mcp-go CallToolResult to our internal type.
func adaptCallToolResult(result *mcpgo.CallToolResult) *CallToolResult {
	r := &CallToolResult{
		IsError: result.IsError,
	}

	for _, content := range result.Content {
		if tc, ok := mcpgo.AsTextContent(content); ok {
			r.Content = append(r.Content, ContentBlock{
				Type: "text",
				Text: tc.Text,
			})
		}
	}

	return r
}
