package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
)

func main() {
	configPath := flag.String("config", "config/web.yaml", "path to web configuration file")
	flag.Parse()

	cfg, err := LoadWebConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load web config: %v", err)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("Failed to create data directory %s: %v", cfg.DataDir, err)
	}

	// Initialize OAuth2 handler (optional)
	var oauthHandler *OAuthHandler
	if cfg.OAuth2 != nil {
		dbPath := filepath.Join(cfg.DataDir, "sessions.db")
		sessions, err := NewSessionStore(dbPath)
		if err != nil {
			log.Fatalf("Failed to open session store: %v", err)
		}
		defer sessions.Close()

		oauthHandler = NewOAuthHandler(cfg.OAuth2, sessions, cfg.Host)
		log.Printf("OAuth2 support enabled (redirect: %s)", cfg.OAuth2.RedirectURL)
	}

	// HTTP client to communicate with the agent REST API
	httpClient := &http.Client{}

	app := fiber.New(fiber.Config{
		AppName: "Agent Stop and Go - Web Chat",
	})

	app.Use(logger.New())

	// Register OAuth2 routes if enabled
	if oauthHandler != nil {
		oauthHandler.RegisterRoutes(app)
	}

	// Serve chat UI
	app.Get("/", func(c *fiber.Ctx) error {
		hasOAuth := cfg.OAuth2 != nil
		c.Set("Content-Type", "text/html")
		return c.SendString(chatHTML(cfg.AgentURL, hasOAuth))
	})

	// getBearerToken extracts the Bearer token from the session cookie.
	getBearerToken := func(c *fiber.Ctx) string {
		if oauthHandler == nil {
			return ""
		}
		sessionID := c.Cookies(sessionCookieName)
		return oauthHandler.GetBearerToken(sessionID)
	}

	// proxyRequest creates an HTTP request to the agent API with optional Bearer token.
	proxyRequest := func(c *fiber.Ctx, method, url string, body []byte) (*http.Response, error) {
		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewReader(body)
		}
		httpReq, err := http.NewRequestWithContext(c.Context(), method, url, bodyReader)
		if err != nil {
			return nil, err
		}
		if body != nil {
			httpReq.Header.Set("Content-Type", "application/json")
		}
		if token := getBearerToken(c); token != "" {
			httpReq.Header.Set("Authorization", "Bearer "+token)
		}
		return httpClient.Do(httpReq)
	}

	// API: send message → REST API (create conversation or send message)
	app.Post("/api/send", func(c *fiber.Ctx) error {
		var req struct {
			Message        string `json:"message"`
			ConversationID string `json:"conversation_id"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
		}
		if req.Message == "" {
			return c.Status(400).JSON(fiber.Map{"error": "message is required"})
		}

		var url string
		if req.ConversationID == "" {
			url = strings.TrimRight(cfg.AgentURL, "/") + "/conversations"
		} else {
			url = strings.TrimRight(cfg.AgentURL, "/") + "/conversations/" + req.ConversationID + "/messages"
		}

		body, err := json.Marshal(map[string]string{"message": req.Message})
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("failed to marshal request: %v", err)})
		}

		resp, err := proxyRequest(c, "POST", url, body)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": err.Error()})
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": fmt.Sprintf("failed to read response: %v", err)})
		}
		c.Set("Content-Type", "application/json")
		return c.Status(resp.StatusCode).Send(respBody)
	})

	// API: approve/reject → REST API POST /approvals/:uuid
	app.Post("/api/approve", func(c *fiber.Ctx) error {
		var req struct {
			UUID     string `json:"uuid"`
			Approved bool   `json:"approved"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
		}
		if req.UUID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "uuid is required"})
		}

		url := strings.TrimRight(cfg.AgentURL, "/") + "/approvals/" + req.UUID
		body, err := json.Marshal(map[string]bool{"approved": req.Approved})
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("failed to marshal request: %v", err)})
		}

		resp, err := proxyRequest(c, "POST", url, body)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": err.Error()})
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": fmt.Sprintf("failed to read response: %v", err)})
		}
		c.Set("Content-Type", "application/json")
		return c.Status(resp.StatusCode).Send(respBody)
	})

	// API: get conversation → REST API GET /conversations/:id
	app.Get("/api/conversation/:id", func(c *fiber.Ctx) error {
		convID := c.Params("id")
		url := strings.TrimRight(cfg.AgentURL, "/") + "/conversations/" + convID

		resp, err := proxyRequest(c, "GET", url, nil)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": err.Error()})
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": fmt.Sprintf("failed to read response: %v", err)})
		}
		c.Set("Content-Type", "application/json")
		return c.Status(resp.StatusCode).Send(respBody)
	})

	// Handle graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutting down web server...")
		if err := app.Shutdown(); err != nil {
			log.Printf("Error during shutdown: %v", err)
		}
	}()

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Printf("Starting Web Chat on %s (agent: %s)", addr, cfg.AgentURL)
	if err := app.Listen(addr); err != nil {
		log.Fatalf("Web server error: %v", err)
	}
}
