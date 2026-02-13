package conversation

import (
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name         string
		prompt       string
		sessionID    string
		wantMsgCount int
		wantStatus   Status
	}{
		{"with system prompt", "hello", "sess1", 1, StatusActive},
		{"without system prompt", "", "sess2", 0, StatusActive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conv := New(tt.prompt, tt.sessionID)
			if conv.ID == "" {
				t.Error("expected non-empty ID")
			}
			if conv.SessionID != tt.sessionID {
				t.Errorf("SessionID = %q, want %q", conv.SessionID, tt.sessionID)
			}
			if conv.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", conv.Status, tt.wantStatus)
			}
			if len(conv.Messages) != tt.wantMsgCount {
				t.Errorf("Messages count = %d, want %d", len(conv.Messages), tt.wantMsgCount)
			}
		})
	}
}

func TestAddMessage(t *testing.T) {
	conv := New("", "")
	msg := conv.AddMessage(RoleUser, "hello")

	if msg.Role != RoleUser {
		t.Errorf("Role = %q, want %q", msg.Role, RoleUser)
	}
	if msg.Content != "hello" {
		t.Errorf("Content = %q, want %q", msg.Content, "hello")
	}
	if len(conv.Messages) != 1 {
		t.Errorf("Messages count = %d, want 1", len(conv.Messages))
	}
}

func TestAddToolCall(t *testing.T) {
	conv := New("", "")
	args := map[string]any{"name": "test"}
	msg := conv.AddToolCall("resources_add", args)

	if msg.ToolCall == nil {
		t.Fatal("expected ToolCall to be set")
	}
	if msg.ToolCall.Name != "resources_add" {
		t.Errorf("ToolCall.Name = %q, want %q", msg.ToolCall.Name, "resources_add")
	}
	if msg.Role != RoleAssistant {
		t.Errorf("Role = %q, want %q", msg.Role, RoleAssistant)
	}
}

func TestAddToolResult(t *testing.T) {
	conv := New("", "")
	msg := conv.AddToolResult("resources_list", "result data", false)

	if msg.ToolCall == nil {
		t.Fatal("expected ToolCall to be set")
	}
	if msg.ToolCall.Result != "result data" {
		t.Errorf("ToolCall.Result = %q, want %q", msg.ToolCall.Result, "result data")
	}
	if msg.ToolCall.IsError {
		t.Error("expected IsError to be false")
	}
	if msg.Role != RoleTool {
		t.Errorf("Role = %q, want %q", msg.Role, RoleTool)
	}
}

func TestSetWaitingApprovalAndResolve(t *testing.T) {
	conv := New("", "")
	args := map[string]any{"name": "test"}

	approval := conv.SetWaitingApproval("resources_add", args, "Add resource")

	if conv.Status != StatusWaitingApproval {
		t.Errorf("Status = %q, want %q", conv.Status, StatusWaitingApproval)
	}
	if approval.UUID == "" {
		t.Error("expected non-empty approval UUID")
	}
	if approval.ToolName != "resources_add" {
		t.Errorf("ToolName = %q, want %q", approval.ToolName, "resources_add")
	}
	if conv.PendingApproval == nil {
		t.Error("expected PendingApproval to be set")
	}

	conv.ResolveApproval()

	if conv.Status != StatusActive {
		t.Errorf("after resolve Status = %q, want %q", conv.Status, StatusActive)
	}
	if conv.PendingApproval != nil {
		t.Error("expected PendingApproval to be nil after resolve")
	}
}

func TestComplete(t *testing.T) {
	conv := New("", "")
	conv.Complete()
	if conv.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", conv.Status, StatusCompleted)
	}
}
