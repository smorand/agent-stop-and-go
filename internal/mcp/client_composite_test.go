package mcp

import (
	"fmt"
	"strings"
	"testing"
)

// mockClient is a mock MCP client for testing.
type mockClient struct {
	name      string
	tools     []Tool
	startErr  error
	stopErr   error
	callFunc  func(name string, args map[string]any) (*CallToolResult, error)
	started   bool
	stopped   bool
	callCount int
}

func (m *mockClient) Start() error {
	if m.startErr != nil {
		return m.startErr
	}
	m.started = true
	return nil
}

func (m *mockClient) Stop() error {
	m.stopped = true
	return m.stopErr
}

func (m *mockClient) Tools() []Tool {
	return m.tools
}

func (m *mockClient) GetTool(name string) *Tool {
	for i := range m.tools {
		if m.tools[i].Name == name {
			return &m.tools[i]
		}
	}
	return nil
}

func (m *mockClient) CallTool(name string, args map[string]any) (*CallToolResult, error) {
	m.callCount++
	if m.callFunc != nil {
		return m.callFunc(name, args)
	}
	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("result from %s", m.name)}},
	}, nil
}

func TestCompositeClient_StartStop(t *testing.T) {
	clientA := &mockClient{name: "a", tools: []Tool{{Name: "tool_a"}}}
	clientB := &mockClient{name: "b", tools: []Tool{{Name: "tool_b"}}}

	cc := NewCompositeClient([]NamedClient{
		{Name: "server-a", Client: clientA},
		{Name: "server-b", Client: clientB},
	})

	if err := cc.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if !clientA.started {
		t.Error("expected client A to be started")
	}
	if !clientB.started {
		t.Error("expected client B to be started")
	}

	if err := cc.Stop(); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if !clientA.stopped {
		t.Error("expected client A to be stopped")
	}
	if !clientB.stopped {
		t.Error("expected client B to be stopped")
	}
}

func TestCompositeClient_ToolAggregation(t *testing.T) {
	clientA := &mockClient{
		name: "a",
		tools: []Tool{
			{Name: "tool_x", Description: "Tool X"},
			{Name: "tool_y", Description: "Tool Y"},
		},
	}
	clientB := &mockClient{
		name: "b",
		tools: []Tool{
			{Name: "tool_z", Description: "Tool Z"},
		},
	}

	cc := NewCompositeClient([]NamedClient{
		{Name: "server-a", Client: clientA},
		{Name: "server-b", Client: clientB},
	})

	if err := cc.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer cc.Stop()

	tools := cc.Tools()
	if len(tools) != 3 {
		t.Fatalf("Tools() returned %d tools, want 3", len(tools))
	}

	// Verify Server field is set
	for _, tool := range tools {
		switch tool.Name {
		case "tool_x", "tool_y":
			if tool.Server != "server-a" {
				t.Errorf("tool %s has Server=%q, want %q", tool.Name, tool.Server, "server-a")
			}
		case "tool_z":
			if tool.Server != "server-b" {
				t.Errorf("tool %s has Server=%q, want %q", tool.Name, tool.Server, "server-b")
			}
		default:
			t.Errorf("unexpected tool: %s", tool.Name)
		}
	}
}

func TestCompositeClient_DuplicateToolDetection(t *testing.T) {
	clientA := &mockClient{
		name:  "a",
		tools: []Tool{{Name: "shared_tool"}},
	}
	clientB := &mockClient{
		name:  "b",
		tools: []Tool{{Name: "shared_tool"}},
	}

	cc := NewCompositeClient([]NamedClient{
		{Name: "server-a", Client: clientA},
		{Name: "server-b", Client: clientB},
	})

	err := cc.Start()
	if err == nil {
		cc.Stop()
		t.Fatal("expected Start() to return error for duplicate tools")
	}

	if !strings.Contains(err.Error(), "duplicate tool name") {
		t.Errorf("error %q should contain 'duplicate tool name'", err.Error())
	}
	if !strings.Contains(err.Error(), "server-a") || !strings.Contains(err.Error(), "server-b") {
		t.Errorf("error %q should contain both server names", err.Error())
	}

	// Verify both clients were stopped (cleanup)
	if !clientA.stopped {
		t.Error("expected client A to be stopped after duplicate detection")
	}
	if !clientB.stopped {
		t.Error("expected client B to be stopped after duplicate detection")
	}
}

func TestCompositeClient_CallToolRouting(t *testing.T) {
	clientA := &mockClient{
		name:  "a",
		tools: []Tool{{Name: "tool_x"}},
		callFunc: func(name string, args map[string]any) (*CallToolResult, error) {
			return &CallToolResult{
				Content: []ContentBlock{{Type: "text", Text: "from-a"}},
			}, nil
		},
	}
	clientB := &mockClient{
		name:  "b",
		tools: []Tool{{Name: "tool_z"}},
		callFunc: func(name string, args map[string]any) (*CallToolResult, error) {
			return &CallToolResult{
				Content: []ContentBlock{{Type: "text", Text: "from-b"}},
			}, nil
		},
	}

	cc := NewCompositeClient([]NamedClient{
		{Name: "server-a", Client: clientA},
		{Name: "server-b", Client: clientB},
	})

	if err := cc.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer cc.Stop()

	// Call tool_z → should route to server-b
	result, err := cc.CallTool("tool_z", nil)
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if result.Content[0].Text != "from-b" {
		t.Errorf("CallTool routed to wrong client, got %q", result.Content[0].Text)
	}

	// Verify server-a was not called
	if clientA.callCount != 0 {
		t.Errorf("expected client A callCount=0, got %d", clientA.callCount)
	}
	if clientB.callCount != 1 {
		t.Errorf("expected client B callCount=1, got %d", clientB.callCount)
	}
}

func TestCompositeClient_CallToolUnknown(t *testing.T) {
	clientA := &mockClient{
		name:  "a",
		tools: []Tool{{Name: "tool_x"}},
	}

	cc := NewCompositeClient([]NamedClient{
		{Name: "server-a", Client: clientA},
	})

	if err := cc.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer cc.Stop()

	_, err := cc.CallTool("nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "tool not found") {
		t.Errorf("error %q should contain 'tool not found'", err.Error())
	}
}

func TestCompositeClient_PartialStartFailure(t *testing.T) {
	clientA := &mockClient{
		name:  "a",
		tools: []Tool{{Name: "tool_x"}},
	}
	clientB := &mockClient{
		name:     "b",
		startErr: fmt.Errorf("connection refused"),
	}

	cc := NewCompositeClient([]NamedClient{
		{Name: "server-a", Client: clientA},
		{Name: "server-b", Client: clientB},
	})

	err := cc.Start()
	if err == nil {
		cc.Stop()
		t.Fatal("expected Start() to return error")
	}

	if !strings.Contains(err.Error(), "server-b") {
		t.Errorf("error %q should mention the failing server", err.Error())
	}

	// Verify client A was started then rolled back (stopped)
	if !clientA.started {
		t.Error("expected client A to have been started")
	}
	if !clientA.stopped {
		t.Error("expected client A to be stopped (rollback)")
	}
}

func TestCompositeClient_GetTool(t *testing.T) {
	clientA := &mockClient{
		name: "a",
		tools: []Tool{
			{Name: "tool_x", Description: "Tool X"},
		},
	}

	cc := NewCompositeClient([]NamedClient{
		{Name: "server-a", Client: clientA},
	})

	if err := cc.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer cc.Stop()

	tool := cc.GetTool("tool_x")
	if tool == nil {
		t.Fatal("GetTool returned nil for existing tool")
	}
	if tool.Name != "tool_x" {
		t.Errorf("GetTool returned wrong tool: %s", tool.Name)
	}
	if tool.Server != "server-a" {
		t.Errorf("GetTool returned wrong server: %s", tool.Server)
	}

	// Non-existent tool
	if cc.GetTool("nonexistent") != nil {
		t.Error("GetTool should return nil for non-existent tool")
	}
}

func TestCompositeClient_EmptyClients(t *testing.T) {
	cc := NewCompositeClient(nil)

	if err := cc.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer cc.Stop()

	tools := cc.Tools()
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}

	_, err := cc.CallTool("anything", nil)
	if err == nil {
		t.Fatal("expected error for CallTool on empty composite")
	}
}
