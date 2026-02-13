package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"agent-stop-and-go/internal/agent"
	"agent-stop-and-go/internal/api"
	"agent-stop-and-go/internal/config"
	"agent-stop-and-go/internal/storage"
)

func main() {
	configPath := flag.String("config", "config/agent.yaml", "path to agent configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	store, err := storage.New(cfg.DataDir)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	ag := agent.New(cfg, store)

	// Start the MCP server
	if err := ag.Start(); err != nil {
		log.Fatalf("Failed to start agent: %v", err)
	}

	server := api.New(cfg, ag)

	// Handle graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutting down server...")
		if err := ag.Stop(); err != nil {
			log.Printf("Error stopping agent: %v", err)
		}
		if err := server.Shutdown(); err != nil {
			log.Printf("Error during shutdown: %v", err)
		}
	}()

	log.Printf("Starting Agent Stop and Go API on %s:%d", cfg.Host, cfg.Port)
	log.Printf("MCP Server: %s", cfg.MCP.Command)
	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
