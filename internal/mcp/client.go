package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// Client manages communication with an MCP server.
type Client struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	tools   []Tool
	mu      sync.Mutex
	nextID  int
	started bool
}

// NewClient creates a new MCP client.
func NewClient(command string, args []string) *Client {
	return &Client{
		cmd:    exec.Command(command, args...),
		nextID: 1,
	}
}

// Start launches the MCP server and initializes the connection.
func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return nil
	}

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}
	c.stdin = stdin

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	c.stdout = bufio.NewReader(stdout)

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start MCP server: %w", err)
	}

	c.started = true

	// Initialize the connection
	if err := c.initialize(); err != nil {
		c.Stop()
		return fmt.Errorf("failed to initialize MCP connection: %w", err)
	}

	// Load available tools
	if err := c.loadTools(); err != nil {
		c.Stop()
		return fmt.Errorf("failed to load MCP tools: %w", err)
	}

	return nil
}

// Stop terminates the MCP server.
func (c *Client) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return nil
	}

	c.started = false

	if c.stdin != nil {
		c.stdin.Close()
	}

	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}

	return nil
}

// Tools returns the available tools from the MCP server.
func (c *Client) Tools() []Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tools
}

// GetTool returns a specific tool by name.
func (c *Client) GetTool(name string) *Tool {
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
func (c *Client) CallTool(name string, args map[string]any) (*CallToolResult, error) {
	params := CallToolParams{
		Name:      name,
		Arguments: args,
	}

	var result CallToolResult
	if err := c.call("tools/call", params, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// initialize sends the initialize request to the MCP server.
func (c *Client) initialize() error {
	params := InitializeParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo: ClientInfo{
			Name:    "agent-stop-and-go",
			Version: "1.0.0",
		},
		Capabilities: map[string]any{},
	}

	var result InitializeResult
	if err := c.call("initialize", params, &result); err != nil {
		return err
	}

	// Send initialized notification
	return c.notify("notifications/initialized", nil)
}

// loadTools fetches the available tools from the MCP server.
func (c *Client) loadTools() error {
	var result ListToolsResult
	if err := c.call("tools/list", nil, &result); err != nil {
		return err
	}
	c.tools = result.Tools
	return nil
}

// call sends a JSON-RPC request and waits for the response.
func (c *Client) call(method string, params any, result any) error {
	id := c.nextID
	c.nextID++

	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	if err := c.send(req); err != nil {
		return err
	}

	resp, err := c.receive()
	if err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	if result != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("failed to unmarshal result: %w", err)
		}
	}

	return nil
}

// notify sends a JSON-RPC notification (no response expected).
func (c *Client) notify(method string, params any) error {
	req := Request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  method,
		Params:  params,
	}
	return c.send(req)
}

// send writes a JSON-RPC message to the MCP server.
func (c *Client) send(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("failed to write to MCP server: %w", err)
	}

	return nil
}

// receive reads a JSON-RPC response from the MCP server.
func (c *Client) receive() (*Response, error) {
	line, err := c.stdout.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read from MCP server: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &resp, nil
}
