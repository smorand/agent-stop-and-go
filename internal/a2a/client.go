package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"agent-stop-and-go/internal/auth"
)

const httpClientTimeout = 60 * time.Second

// Client communicates with an A2A agent over HTTPS.
type Client struct {
	name            string
	url             string
	description     string
	destructiveHint bool
	httpClient      *http.Client
	nextID          int
}

// NewClient creates a new A2A client.
func NewClient(name, url, description string, destructiveHint bool) *Client {
	return &Client{
		name:            name,
		url:             url,
		description:     description,
		destructiveHint: destructiveHint,
		httpClient:      &http.Client{Timeout: httpClientTimeout},
		nextID:          1,
	}
}

// Name returns the agent name.
func (c *Client) Name() string { return c.name }

// URL returns the agent URL.
func (c *Client) URL() string { return c.url }

// Description returns the agent description.
func (c *Client) Description() string { return c.description }

// DestructiveHint returns whether calls to this agent require approval.
func (c *Client) DestructiveHint() bool { return c.destructiveHint }

// FetchAgentCard retrieves the agent card from /.well-known/agent.json.
func (c *Client) FetchAgentCard(ctx context.Context) (*AgentCard, error) {
	url := c.url + "/.well-known/agent.json"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent card request: %w", err)
	}

	if token := auth.BearerToken(ctx); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if sid := auth.SessionID(ctx); sid != "" {
		req.Header.Set("X-Session-ID", sid)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch agent card: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent card response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent card request failed with status %d: %s", resp.StatusCode, body)
	}

	var card AgentCard
	if err := json.Unmarshal(body, &card); err != nil {
		return nil, fmt.Errorf("failed to parse agent card: %w", err)
	}

	return &card, nil
}

// SendMessage sends a message to the A2A agent and returns the task result.
func (c *Client) SendMessage(ctx context.Context, message string) (*Task, error) {
	params := MessageSendParams{
		Message: Message{
			Role: "user",
			Parts: []Part{
				{Type: "text", Text: message},
			},
		},
	}

	var task Task
	if err := c.call(ctx, "message/send", params, &task); err != nil {
		return nil, err
	}

	return &task, nil
}

// GetTask retrieves a task by ID.
func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	params := TaskGetParams{ID: taskID}

	var task Task
	if err := c.call(ctx, "tasks/get", params, &task); err != nil {
		return nil, err
	}

	return &task, nil
}

// ContinueTask continues an existing task by sending a message/send with taskId.
func (c *Client) ContinueTask(ctx context.Context, taskID string, message string) (*Task, error) {
	params := MessageSendParams{
		TaskID: taskID,
		Message: Message{
			Role: "user",
			Parts: []Part{
				{Type: "text", Text: message},
			},
		},
	}

	var task Task
	if err := c.call(ctx, "message/send", params, &task); err != nil {
		return nil, err
	}

	return &task, nil
}

// call sends a JSON-RPC 2.0 request to the A2A agent.
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	id := c.nextID
	c.nextID++

	rpcReq := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return fmt.Errorf("failed to marshal A2A request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create A2A request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Forward Bearer token and session ID from context
	if token := auth.BearerToken(ctx); token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	if sid := auth.SessionID(ctx); sid != "" {
		httpReq.Header.Set("X-Session-ID", sid)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send A2A request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read A2A response: %w", err)
	}

	var rpcResp Response
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return fmt.Errorf("failed to parse A2A response: %w", err)
	}

	if rpcResp.Error != nil {
		return fmt.Errorf("A2A error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if result != nil && rpcResp.Result != nil {
		if err := json.Unmarshal(rpcResp.Result, result); err != nil {
			return fmt.Errorf("failed to unmarshal A2A result: %w", err)
		}
	}

	return nil
}
