package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"agent-stop-and-go/internal/conversation"
)

// Storage handles JSON file persistence for conversations.
type Storage struct {
	dataDir string
	mu      sync.RWMutex
}

// New creates a new storage instance.
func New(dataDir string) (*Storage, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}
	return &Storage{dataDir: dataDir}, nil
}

func (s *Storage) conversationPath(id string) string {
	return filepath.Join(s.dataDir, fmt.Sprintf("conversation_%s.json", id))
}

// SaveConversation persists a conversation to a JSON file.
func (s *Storage) SaveConversation(conv *conversation.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conversation: %w", err)
	}

	path := s.conversationPath(conv.ID)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write conversation file: %w", err)
	}

	return nil
}

// LoadConversation reads a conversation from its JSON file.
func (s *Storage) LoadConversation(id string) (*conversation.Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := s.conversationPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("conversation not found: %s", id)
		}
		return nil, fmt.Errorf("failed to read conversation file: %w", err)
	}

	var conv conversation.Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, fmt.Errorf("failed to unmarshal conversation: %w", err)
	}

	return &conv, nil
}

// ListConversations returns all stored conversations.
func (s *Storage) ListConversations() ([]*conversation.Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pattern := filepath.Join(s.dataDir, "conversation_*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversation files: %w", err)
	}

	var conversations []*conversation.Conversation
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		var conv conversation.Conversation
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}
		conversations = append(conversations, &conv)
	}

	return conversations, nil
}

// FindConversationByApprovalUUID finds a conversation by its pending approval UUID.
func (s *Storage) FindConversationByApprovalUUID(uuid string) (*conversation.Conversation, error) {
	conversations, err := s.ListConversations()
	if err != nil {
		return nil, err
	}

	for _, conv := range conversations {
		if conv.PendingApproval != nil && conv.PendingApproval.UUID == uuid {
			return conv, nil
		}
	}

	return nil, fmt.Errorf("no conversation found with approval UUID: %s", uuid)
}

// DeleteConversation removes a conversation file.
func (s *Storage) DeleteConversation(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.conversationPath(id)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("conversation not found: %s", id)
		}
		return fmt.Errorf("failed to delete conversation file: %w", err)
	}

	return nil
}
