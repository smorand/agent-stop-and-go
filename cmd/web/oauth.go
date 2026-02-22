package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/oauth2"
)

const (
	sessionCookieName = "asg_session"
	stateCookieName   = "asg_oauth_state"
	sessionMaxAge     = 30 * 24 * 60 * 60 // 30 days in seconds
)

// OAuthHandler manages OAuth2 routes and token operations.
type OAuthHandler struct {
	config   *oauth2.Config
	revoke   string // revoke URL
	sessions *SessionStore
	secure   bool // Secure cookie flag
}

// NewOAuthHandler creates a new OAuth handler from config.
func NewOAuthHandler(cfg *OAuth2Config, sessions *SessionStore, host string) *OAuthHandler {
	oauthConfig := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.AuthURL,
			TokenURL: cfg.TokenURL,
		},
		RedirectURL: cfg.RedirectURL,
		Scopes:      cfg.Scopes,
	}

	secure := !strings.HasPrefix(host, "localhost") && !strings.HasPrefix(host, "127.0.0.1") && host != "0.0.0.0"

	return &OAuthHandler{
		config:   oauthConfig,
		revoke:   cfg.RevokeURL,
		sessions: sessions,
		secure:   secure,
	}
}

// RegisterRoutes adds OAuth2 routes to the Fiber app.
func (h *OAuthHandler) RegisterRoutes(app *fiber.App) {
	app.Get("/login", h.loginHandler)
	app.Get("/callback", h.callbackHandler)
	app.Post("/logout", h.logoutHandler)
	app.Get("/api/session", h.sessionHandler)
}

// loginHandler starts the OAuth2 authorization code flow.
func (h *OAuthHandler) loginHandler(c *fiber.Ctx) error {
	state, err := generateState()
	if err != nil {
		log.Printf("ERROR: failed to generate OAuth2 state: %v", err)
		return c.Status(500).SendString("Internal server error")
	}

	// Store state in a short-lived cookie for CSRF validation
	c.Cookie(&fiber.Cookie{
		Name:     stateCookieName,
		Value:    state,
		MaxAge:   300, // 5 minutes
		HTTPOnly: true,
		SameSite: "Lax",
		Secure:   h.secure,
		Path:     "/",
	})

	authURL := h.config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)

	log.Printf("INFO: OAuth2 flow started, redirecting to authorization URL")
	return c.Redirect(authURL, fiber.StatusTemporaryRedirect)
}

// callbackHandler handles the OAuth2 callback from Google.
func (h *OAuthHandler) callbackHandler(c *fiber.Ctx) error {
	// Validate CSRF state
	expectedState := c.Cookies(stateCookieName)
	actualState := c.Query("state")
	if expectedState == "" || actualState != expectedState {
		log.Printf("WARN: OAuth2 callback CSRF state mismatch")
		return c.Status(400).SendString("Invalid state parameter. <a href='/login'>Try again</a>")
	}

	// Clear the state cookie
	c.Cookie(&fiber.Cookie{
		Name:   stateCookieName,
		Value:  "",
		MaxAge: -1,
		Path:   "/",
	})

	// Check for error from provider
	if errParam := c.Query("error"); errParam != "" {
		errDesc := c.Query("error_description")
		log.Printf("WARN: OAuth2 provider returned error: %s — %s", errParam, errDesc)
		return c.Status(400).SendString(fmt.Sprintf("Authorization failed: %s. <a href='/login'>Try again</a>", errDesc))
	}

	// Exchange authorization code for tokens
	code := c.Query("code")
	if code == "" {
		return c.Status(400).SendString("Missing authorization code. <a href='/login'>Try again</a>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token, err := h.config.Exchange(ctx, code)
	if err != nil {
		log.Printf("WARN: OAuth2 token exchange failed: %v", err)
		return c.Status(500).SendString("Token exchange failed. <a href='/login'>Try again</a>")
	}

	// Create session
	refreshToken := token.RefreshToken
	if refreshToken == "" {
		log.Printf("WARN: OAuth2 token response did not include a refresh token")
	}

	sessionID, err := h.sessions.Create(token.AccessToken, refreshToken, token.Expiry)
	if err != nil {
		log.Printf("ERROR: failed to create session: %v", err)
		return c.Status(500).SendString("Failed to create session. <a href='/login'>Try again</a>")
	}

	// Set session cookie
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		MaxAge:   sessionMaxAge,
		HTTPOnly: true,
		SameSite: "Lax",
		Secure:   h.secure,
		Path:     "/",
	})

	log.Printf("INFO: OAuth2 flow completed, session created: %s...%s", sessionID[:8], sessionID[len(sessionID)-4:])
	return c.Redirect("/", fiber.StatusTemporaryRedirect)
}

// logoutHandler revokes the token and deletes the session.
func (h *OAuthHandler) logoutHandler(c *fiber.Ctx) error {
	sessionID := c.Cookies(sessionCookieName)
	if sessionID == "" {
		return c.Redirect("/", fiber.StatusTemporaryRedirect)
	}

	sess, err := h.sessions.Get(sessionID)
	if err != nil {
		log.Printf("WARN: failed to get session for logout: %v", err)
	}

	// Revoke token at Google (best effort)
	if sess != nil && h.revoke != "" {
		if err := revokeToken(h.revoke, sess.AccessToken); err != nil {
			log.Printf("WARN: token revocation failed: %v", err)
		}
	}

	// Delete session from DB
	if err := h.sessions.Delete(sessionID); err != nil {
		log.Printf("WARN: failed to delete session: %v", err)
	}

	// Clear cookie
	c.Cookie(&fiber.Cookie{
		Name:   sessionCookieName,
		Value:  "",
		MaxAge: -1,
		Path:   "/",
	})

	log.Printf("INFO: session deleted: %s...%s", sessionID[:8], sessionID[len(sessionID)-4:])
	return c.Redirect("/", fiber.StatusTemporaryRedirect)
}

// sessionHandler returns the current session status.
func (h *OAuthHandler) sessionHandler(c *fiber.Ctx) error {
	sessionID := c.Cookies(sessionCookieName)
	if sessionID == "" {
		return c.JSON(fiber.Map{"authenticated": false})
	}

	sess, err := h.sessions.Get(sessionID)
	if err != nil || sess == nil {
		return c.JSON(fiber.Map{"authenticated": false})
	}

	return c.JSON(fiber.Map{"authenticated": true})
}

// GetBearerToken reads the session and returns a valid Bearer token.
// If the token is expired, it attempts a refresh. Returns empty string if no valid session.
func (h *OAuthHandler) GetBearerToken(sessionID string) string {
	if sessionID == "" {
		return ""
	}

	sess, err := h.sessions.Get(sessionID)
	if err != nil || sess == nil {
		return ""
	}

	if !sess.IsExpired() {
		return sess.AccessToken
	}

	// Attempt token refresh
	if sess.RefreshToken == "" {
		log.Printf("WARN: session %s...%s has expired token and no refresh token", sessionID[:8], sessionID[len(sessionID)-4:])
		h.sessions.Delete(sessionID)
		return ""
	}

	token, err := h.refreshToken(sess.RefreshToken)
	if err != nil {
		log.Printf("WARN: token refresh failed for session %s...%s: %v", sessionID[:8], sessionID[len(sessionID)-4:], err)
		h.sessions.Delete(sessionID)
		return ""
	}

	// Update session with new token
	refreshToken := token.RefreshToken
	if refreshToken == "" {
		refreshToken = sess.RefreshToken // keep existing if not rotated
	}
	if err := h.sessions.Update(sessionID, token.AccessToken, refreshToken, token.Expiry); err != nil {
		log.Printf("WARN: failed to update session after refresh: %v", err)
	}

	log.Printf("INFO: token refreshed for session %s...%s", sessionID[:8], sessionID[len(sessionID)-4:])
	return token.AccessToken
}

// ClearSession deletes the session and clears the cookie (used on auth failure after retry).
func (h *OAuthHandler) ClearSession(c *fiber.Ctx) {
	sessionID := c.Cookies(sessionCookieName)
	if sessionID != "" {
		h.sessions.Delete(sessionID)
		c.Cookie(&fiber.Cookie{
			Name:   sessionCookieName,
			Value:  "",
			MaxAge: -1,
			Path:   "/",
		})
	}
}

// refreshToken uses the refresh token to obtain a new access token.
func (h *OAuthHandler) refreshToken(refreshToken string) (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tokenSource := h.config.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	return tokenSource.Token()
}

// revokeToken calls the revocation endpoint.
func revokeToken(revokeURL, token string) error {
	data := url.Values{"token": {token}}
	resp, err := http.Post(revokeURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("revocation request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revocation returned status %d", resp.StatusCode)
	}
	return nil
}

// generateState creates a cryptographically random state parameter.
func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
