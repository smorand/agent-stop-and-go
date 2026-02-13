package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantName  string
		wantHost  string
		wantPort  int
		wantModel string
		wantErr   bool
	}{
		{
			name:      "minimal config uses defaults",
			yaml:      "mcp:\n  command: ./bin/mcp\nprompt: hello\n",
			wantName:  "agent",
			wantHost:  "0.0.0.0",
			wantPort:  8080,
			wantModel: "gemini-2.5-flash",
		},
		{
			name:      "custom values override defaults",
			yaml:      "name: myagent\nhost: localhost\nport: 9090\nllm:\n  model: claude-sonnet-4-5-20250929\nmcp:\n  command: ./bin/mcp\n",
			wantName:  "myagent",
			wantHost:  "localhost",
			wantPort:  9090,
			wantModel: "claude-sonnet-4-5-20250929",
		},
		{
			name:    "invalid yaml returns error",
			yaml:    "invalid: yaml: [[[",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "agent.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
				t.Fatal(err)
			}

			cfg, err := Load(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if cfg.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", cfg.Name, tt.wantName)
			}
			if cfg.Host != tt.wantHost {
				t.Errorf("Host = %q, want %q", cfg.Host, tt.wantHost)
			}
			if cfg.Port != tt.wantPort {
				t.Errorf("Port = %d, want %d", cfg.Port, tt.wantPort)
			}
			if cfg.LLM.Model != tt.wantModel {
				t.Errorf("LLM.Model = %q, want %q", cfg.LLM.Model, tt.wantModel)
			}
		})
	}
}

func TestLoad_SynthesizesDefaultAgentNode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	yaml := "mcp:\n  command: ./bin/mcp\nprompt: test prompt\n"
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agent == nil {
		t.Fatal("expected Agent node to be synthesized")
	}
	if cfg.Agent.Type != "llm" {
		t.Errorf("Agent.Type = %q, want %q", cfg.Agent.Type, "llm")
	}
	if cfg.Agent.Prompt != "test prompt" {
		t.Errorf("Agent.Prompt = %q, want %q", cfg.Agent.Prompt, "test prompt")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/agent.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
