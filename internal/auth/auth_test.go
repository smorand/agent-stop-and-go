package auth

import (
	"context"
	"testing"
)

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		wantBack string
	}{
		{"stores and retrieves token", "my-secret-token", "my-secret-token"},
		{"empty token returns empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.token != "" {
				ctx = WithBearerToken(ctx, tt.token)
			}
			got := BearerToken(ctx)
			if got != tt.wantBack {
				t.Errorf("BearerToken() = %q, want %q", got, tt.wantBack)
			}
		})
	}
}

func TestSessionID(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		wantBack string
	}{
		{"stores and retrieves session ID", "abc12345", "abc12345"},
		{"empty context returns empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.id != "" {
				ctx = WithSessionID(ctx, tt.id)
			}
			got := SessionID(ctx)
			if got != tt.wantBack {
				t.Errorf("SessionID() = %q, want %q", got, tt.wantBack)
			}
		})
	}
}

func TestGenerateSessionID(t *testing.T) {
	id := GenerateSessionID()
	if len(id) != 8 {
		t.Errorf("GenerateSessionID() length = %d, want 8", len(id))
	}

	// Verify uniqueness
	id2 := GenerateSessionID()
	if id == id2 {
		t.Error("GenerateSessionID() returned duplicate IDs")
	}
}
