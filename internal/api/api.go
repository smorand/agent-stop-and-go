package api

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"

	"agent-stop-and-go/internal/agent"
	"agent-stop-and-go/internal/config"
)

// Server holds the API server components.
type Server struct {
	app    *fiber.App
	agent  *agent.Agent
	config *config.Config
}

// New creates a new API server.
func New(cfg *config.Config, ag *agent.Agent) *Server {
	app := fiber.New(fiber.Config{
		AppName: "Agent Stop and Go",
	})

	app.Use(logger.New())

	server := &Server{
		app:    app,
		agent:  ag,
		config: cfg,
	}

	server.setupRoutes()

	return server
}

// Start begins listening on the configured host and port.
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	return s.app.Listen(addr)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() error {
	return s.app.Shutdown()
}
