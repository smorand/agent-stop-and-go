package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// WebConfig holds the web app configuration loaded from web.yaml.
type WebConfig struct {
	AgentURL string `yaml:"agent_url"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
}

// LoadWebConfig reads and parses the web.yaml configuration file.
func LoadWebConfig(path string) (*WebConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read web config file: %w", err)
	}

	var cfg WebConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse web config file: %w", err)
	}

	if cfg.AgentURL == "" {
		return nil, fmt.Errorf("agent_url is required in web config")
	}
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 3000
	}

	return &cfg, nil
}
