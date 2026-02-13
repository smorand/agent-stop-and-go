package storage

import (
	"testing"

	"agent-stop-and-go/internal/conversation"
)

func TestSaveAndLoadConversation(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	conv := conversation.New("test prompt", "sess1")
	conv.AddMessage(conversation.RoleUser, "hello")

	if err := store.SaveConversation(conv); err != nil {
		t.Fatalf("SaveConversation failed: %v", err)
	}

	loaded, err := store.LoadConversation(conv.ID)
	if err != nil {
		t.Fatalf("LoadConversation failed: %v", err)
	}

	if loaded.ID != conv.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, conv.ID)
	}
	if loaded.SessionID != "sess1" {
		t.Errorf("SessionID = %q, want %q", loaded.SessionID, "sess1")
	}
	// system prompt + user message
	if len(loaded.Messages) != 2 {
		t.Errorf("Messages count = %d, want 2", len(loaded.Messages))
	}
}

func TestLoadConversation_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	_, err = store.LoadConversation("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent conversation")
	}
}

func TestListConversations(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	// Empty list
	convs, err := store.ListConversations()
	if err != nil {
		t.Fatalf("ListConversations failed: %v", err)
	}
	if len(convs) != 0 {
		t.Errorf("expected 0 conversations, got %d", len(convs))
	}

	// Add two conversations
	conv1 := conversation.New("prompt1", "")
	conv2 := conversation.New("prompt2", "")
	if err := store.SaveConversation(conv1); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveConversation(conv2); err != nil {
		t.Fatal(err)
	}

	convs, err = store.ListConversations()
	if err != nil {
		t.Fatalf("ListConversations failed: %v", err)
	}
	if len(convs) != 2 {
		t.Errorf("expected 2 conversations, got %d", len(convs))
	}
}

func TestFindConversationByApprovalUUID(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	conv := conversation.New("prompt", "")
	approval := conv.SetWaitingApproval("resources_add", map[string]any{"name": "test"}, "Add test")
	if err := store.SaveConversation(conv); err != nil {
		t.Fatal(err)
	}

	found, err := store.FindConversationByApprovalUUID(approval.UUID)
	if err != nil {
		t.Fatalf("FindConversationByApprovalUUID failed: %v", err)
	}
	if found.ID != conv.ID {
		t.Errorf("found ID = %q, want %q", found.ID, conv.ID)
	}

	// Not found
	_, err = store.FindConversationByApprovalUUID("nonexistent-uuid")
	if err == nil {
		t.Fatal("expected error for nonexistent UUID")
	}
}

func TestDeleteConversation(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	conv := conversation.New("prompt", "")
	if err := store.SaveConversation(conv); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteConversation(conv.ID); err != nil {
		t.Fatalf("DeleteConversation failed: %v", err)
	}

	_, err = store.LoadConversation(conv.ID)
	if err == nil {
		t.Fatal("expected error after deletion")
	}

	// Delete nonexistent
	err = store.DeleteConversation("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent conversation")
	}
}
