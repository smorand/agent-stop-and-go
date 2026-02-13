package a2a

import "encoding/json"

// JSON-RPC 2.0 protocol types for A2A communication.

// Request represents a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// AgentCard describes an A2A agent's capabilities.
type AgentCard struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	URL         string  `json:"url"`
	Skills      []Skill `json:"skills,omitempty"`
}

// Skill describes a capability of an A2A agent.
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// MessageSendParams is the params for the message/send method.
type MessageSendParams struct {
	TaskID  string  `json:"taskId,omitempty"`
	Message Message `json:"message"`
}

// Message represents an A2A message.
type Message struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

// Part represents a content part in an A2A message.
type Part struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Task represents an A2A task.
type Task struct {
	ID       string     `json:"id"`
	Status   TaskStatus `json:"status"`
	Artifact *Artifact  `json:"artifact,omitempty"`
}

// TaskStatus represents the status of an A2A task.
type TaskStatus struct {
	State   string  `json:"state"` // "submitted", "input-required", "completed", "failed"
	Message *string `json:"message,omitempty"`
}

// Artifact represents output from an A2A task.
type Artifact struct {
	Parts []Part `json:"parts"`
}

// TaskGetParams is the params for the tasks/get method.
type TaskGetParams struct {
	ID string `json:"id"`
}
