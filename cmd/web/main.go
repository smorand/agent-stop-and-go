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

	// HTTP client to communicate with the agent REST API
	httpClient := &http.Client{}

	app := fiber.New(fiber.Config{
		AppName: "Agent Stop and Go - Web Chat",
	})

	app.Use(logger.New())

	// Serve chat UI
	app.Get("/", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/html")
		return c.SendString(chatHTML(cfg.AgentURL))
	})

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
			// Create new conversation with initial message
			url = strings.TrimRight(cfg.AgentURL, "/") + "/conversations"
		} else {
			// Send message to existing conversation
			url = strings.TrimRight(cfg.AgentURL, "/") + "/conversations/" + req.ConversationID + "/messages"
		}

		body, _ := json.Marshal(map[string]string{"message": req.Message})
		httpReq, err := http.NewRequestWithContext(c.Context(), "POST", url, bytes.NewReader(body))
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": err.Error()})
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
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
		body, _ := json.Marshal(map[string]bool{"approved": req.Approved})
		httpReq, err := http.NewRequestWithContext(c.Context(), "POST", url, bytes.NewReader(body))
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": err.Error()})
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		c.Set("Content-Type", "application/json")
		return c.Status(resp.StatusCode).Send(respBody)
	})

	// API: get conversation → REST API GET /conversations/:id
	app.Get("/api/conversation/:id", func(c *fiber.Ctx) error {
		convID := c.Params("id")
		url := strings.TrimRight(cfg.AgentURL, "/") + "/conversations/" + convID

		httpReq, err := http.NewRequestWithContext(c.Context(), "GET", url, nil)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": err.Error()})
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		c.Set("Content-Type", "application/json")
		return c.Status(resp.StatusCode).Send(respBody)
	})

	// Handle graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutting down web server...")
		app.Shutdown()
	}()

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Printf("Starting Web Chat on %s (agent: %s)", addr, cfg.AgentURL)
	if err := app.Listen(addr); err != nil {
		log.Fatalf("Web server error: %v", err)
	}
}
