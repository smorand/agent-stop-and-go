package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MCPConfig holds the MCP server configuration.
type MCPConfig struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

// Config holds the agent configuration loaded from agent.yaml.
type Config struct {
	Prompt  string    `yaml:"prompt"`
	Host    string    `yaml:"host"`
	Port    int       `yaml:"port"`
	DataDir string    `yaml:"data_dir"`
	MCP     MCPConfig `yaml:"mcp"`
}

// Load reads and parses the agent.yaml configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}

	return &cfg, nil
}
