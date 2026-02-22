package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// WebConfig holds the web app configuration loaded from web.yaml.
type WebConfig struct {
	AgentURL string        `yaml:"agent_url"`
	Host     string        `yaml:"host"`
	Port     int           `yaml:"port"`
	DataDir  string        `yaml:"data_dir"`
	OAuth2   *OAuth2Config `yaml:"oauth2"`
}

// OAuth2Config holds the OAuth2 provider settings.
type OAuth2Config struct {
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	AuthURL      string   `yaml:"auth_url"`
	TokenURL     string   `yaml:"token_url"`
	RevokeURL    string   `yaml:"revoke_url"`
	RedirectURL  string   `yaml:"redirect_url"`
	Scopes       []string `yaml:"scopes"`
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
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}

	// Validate OAuth2 config if provided
	if cfg.OAuth2 != nil {
		if err := cfg.OAuth2.validate(); err != nil {
			return nil, fmt.Errorf("invalid oauth2 config: %w", err)
		}
	}

	return &cfg, nil
}

// validate checks that all required OAuth2 fields are present.
func (o *OAuth2Config) validate() error {
	if o.ClientID == "" {
		return fmt.Errorf("client_id is required")
	}
	if o.ClientSecret == "" {
		return fmt.Errorf("client_secret is required")
	}
	if o.AuthURL == "" {
		return fmt.Errorf("auth_url is required")
	}
	if o.TokenURL == "" {
		return fmt.Errorf("token_url is required")
	}
	if o.RedirectURL == "" {
		return fmt.Errorf("redirect_url is required")
	}
	if len(o.Scopes) == 0 {
		return fmt.Errorf("at least one scope is required")
	}
	return nil
}
