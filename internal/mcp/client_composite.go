package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// NamedClient pairs a Client with its configured server name.
type NamedClient struct {
	Name   string
	Client Client
}

// CompositeClient implements Client by wrapping multiple sub-clients.
// It aggregates tools from all sub-clients and routes CallTool to the correct one.
type CompositeClient struct {
	clients []NamedClient
	tools   []Tool
	toolMap map[string]int // tool name → index into clients
	mu      sync.Mutex     // serializes CallTool for thread safety
	started bool
}

// NewCompositeClient creates a CompositeClient wrapping the given named clients.
func NewCompositeClient(clients []NamedClient) *CompositeClient {
	return &CompositeClient{
		clients: clients,
		toolMap: make(map[string]int),
	}
}

// Start starts all sub-clients and loads their tools.
// If any sub-client fails, all previously started clients are stopped (all-or-nothing).
// Duplicate tool names across sub-clients cause Start to fail.
func (c *CompositeClient) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return nil
	}

	// Start all sub-clients
	for i, nc := range c.clients {
		if err := nc.Client.Start(); err != nil {
			// Rollback: stop all previously started clients
			for j := i - 1; j >= 0; j-- {
				c.clients[j].Client.Stop()
			}
			return fmt.Errorf("failed to start MCP server %q: %w", nc.Name, err)
		}
	}

	// Aggregate tools and check for duplicates
	var allTools []Tool
	toolOwner := make(map[string]string) // tool name → server name

	for i, nc := range c.clients {
		for _, tool := range nc.Client.Tools() {
			if existingServer, ok := toolOwner[tool.Name]; ok {
				// Duplicate detected: stop all clients and fail
				c.stopAllLocked()
				return fmt.Errorf("duplicate tool name %q found in MCP servers %q and %q", tool.Name, existingServer, nc.Name)
			}
			toolOwner[tool.Name] = nc.Name
			tool.Server = nc.Name
			c.toolMap[tool.Name] = i
			allTools = append(allTools, tool)
		}
	}

	c.tools = allTools
	c.started = true
	return nil
}

// Stop stops all sub-clients, collecting errors.
func (c *CompositeClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return nil
	}

	c.started = false
	return c.stopAllLocked()
}

// stopAllLocked stops all sub-clients. Must be called with mu held.
func (c *CompositeClient) stopAllLocked() error {
	var errs []string
	for _, nc := range c.clients {
		if err := nc.Client.Stop(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", nc.Name, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors stopping MCP clients: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Tools returns the merged tool list from all sub-clients.
func (c *CompositeClient) Tools() []Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tools
}

// GetTool returns a tool by name from the merged tool set.
func (c *CompositeClient) GetTool(name string) *Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.tools {
		if c.tools[i].Name == name {
			return &c.tools[i]
		}
	}
	return nil
}

// CallTool routes the call to the sub-client that owns the tool.
// It is serialized with a mutex for thread safety.
func (c *CompositeClient) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	c.mu.Lock()
	idx, ok := c.toolMap[name]
	if !ok {
		c.mu.Unlock()
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	client := c.clients[idx].Client
	c.mu.Unlock()

	return client.CallTool(ctx, name, args)
}
