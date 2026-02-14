package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MCPServerConfig holds the configuration for a single MCP server.
type MCPServerConfig struct {
	Name    string   `yaml:"name"`    // Unique server name (required)
	URL     string   `yaml:"url"`     // Streamable HTTP endpoint
	Command string   `yaml:"command"` // stdio subprocess command
	Args    []string `yaml:"args"`    // stdio subprocess args
}

// LLMConfig holds the LLM configuration.
type LLMConfig struct {
	Model string `yaml:"model"`
}

// A2AAgent holds the configuration for an A2A sub-agent.
type A2AAgent struct {
	Name            string `yaml:"name"`
	URL             string `yaml:"url"`
	Description     string `yaml:"description"`
	DestructiveHint bool   `yaml:"destructiveHint"`
}

// AgentNode defines a node in the agent orchestration tree.
type AgentNode struct {
	Name            string      `yaml:"name"`
	Type            string      `yaml:"type"`                      // llm, sequential, parallel, loop, a2a
	Model           string      `yaml:"model,omitempty"`           // llm: Gemini model name
	Prompt          string      `yaml:"prompt,omitempty"`          // llm: system prompt, a2a: message template
	OutputKey       string      `yaml:"output_key,omitempty"`      // key to store output in session state
	CanExitLoop     bool        `yaml:"can_exit_loop,omitempty"`   // llm: gets exit_loop tool
	MaxIterations   int         `yaml:"max_iterations,omitempty"`  // loop: max iterations (0 = 10 safety cap)
	Agents          []AgentNode `yaml:"agents,omitempty"`          // sequential, parallel, loop: sub-agents
	URL             string      `yaml:"url,omitempty"`             // a2a: remote agent URL
	Description     string      `yaml:"description,omitempty"`     // a2a: agent description
	DestructiveHint bool        `yaml:"destructiveHint,omitempty"` // a2a: requires approval
	A2A             []A2AAgent  `yaml:"a2a,omitempty"`             // llm: local A2A tools
}

// Config holds the agent configuration loaded from agent.yaml.
type Config struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Prompt      string            `yaml:"prompt"`
	Host        string            `yaml:"host"`
	Port        int               `yaml:"port"`
	DataDir     string            `yaml:"data_dir"`
	LLM         LLMConfig         `yaml:"llm"`
	MCPServers  []MCPServerConfig `yaml:"mcp_servers"`
	A2A         []A2AAgent        `yaml:"a2a"`
	Agent       *AgentNode        `yaml:"agent,omitempty"` // Agent tree (overrides top-level prompt/llm/a2a)
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

	if cfg.Name == "" {
		cfg.Name = "agent"
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
	if cfg.LLM.Model == "" {
		cfg.LLM.Model = "gemini-2.5-flash"
	}

	// Validate MCP server configs
	if err := validateMCPServers(cfg.MCPServers); err != nil {
		return nil, err
	}

	// Synthesize default agent node from top-level fields when agent tree is not defined
	if cfg.Agent == nil {
		cfg.Agent = &AgentNode{
			Type:   "llm",
			Name:   cfg.Name,
			Model:  cfg.LLM.Model,
			Prompt: cfg.Prompt,
			A2A:    cfg.A2A,
		}
	}

	return &cfg, nil
}

// validateMCPServers checks that all MCP server entries have a non-empty, unique name.
func validateMCPServers(servers []MCPServerConfig) error {
	seen := make(map[string]bool, len(servers))
	for i, s := range servers {
		if s.Name == "" {
			return fmt.Errorf("mcp_servers[%d]: name is required", i)
		}
		if seen[s.Name] {
			return fmt.Errorf("mcp_servers: duplicate name %q", s.Name)
		}
		seen[s.Name] = true
	}
	return nil
}
