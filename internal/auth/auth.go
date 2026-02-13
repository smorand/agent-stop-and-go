package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type bearerKey struct{}
type sessionIDKey struct{}

// WithBearerToken stores a Bearer token in the context.
func WithBearerToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, bearerKey{}, token)
}

// BearerToken retrieves the Bearer token from the context.
// Returns empty string if no token is present.
func BearerToken(ctx context.Context) string {
	token, _ := ctx.Value(bearerKey{}).(string)
	return token
}

// WithSessionID stores a session ID in the context.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, id)
}

// SessionID retrieves the session ID from the context.
// Returns empty string if no session ID is present.
func SessionID(ctx context.Context) string {
	id, _ := ctx.Value(sessionIDKey{}).(string)
	return id
}

// GenerateSessionID creates a new 8-char hex session ID using crypto/rand.
func GenerateSessionID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
