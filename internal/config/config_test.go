package config

import (
	"os"
	"path/filepath"
	"strings"
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
			yaml:      "mcp_servers:\n  - name: default\n    command: ./bin/mcp\nprompt: hello\n",
			wantName:  "agent",
			wantHost:  "0.0.0.0",
			wantPort:  8080,
			wantModel: "gemini-2.5-flash",
		},
		{
			name:      "custom values override defaults",
			yaml:      "name: myagent\nhost: localhost\nport: 9090\nllm:\n  model: claude-sonnet-4-5-20250929\nmcp_servers:\n  - name: default\n    command: ./bin/mcp\n",
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
	yaml := "mcp_servers:\n  - name: default\n    command: ./bin/mcp\nprompt: test prompt\n"
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

func TestLoad_MultipleMCPServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	yaml := `
mcp_servers:
  - name: server-a
    url: http://a:8090/mcp
  - name: server-b
    url: http://b:9090/mcp
prompt: hello
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.MCPServers) != 2 {
		t.Fatalf("MCPServers length = %d, want 2", len(cfg.MCPServers))
	}
	if cfg.MCPServers[0].Name != "server-a" {
		t.Errorf("MCPServers[0].Name = %q, want %q", cfg.MCPServers[0].Name, "server-a")
	}
	if cfg.MCPServers[0].URL != "http://a:8090/mcp" {
		t.Errorf("MCPServers[0].URL = %q, want %q", cfg.MCPServers[0].URL, "http://a:8090/mcp")
	}
	if cfg.MCPServers[1].Name != "server-b" {
		t.Errorf("MCPServers[1].Name = %q, want %q", cfg.MCPServers[1].Name, "server-b")
	}
	if cfg.MCPServers[1].URL != "http://b:9090/mcp" {
		t.Errorf("MCPServers[1].URL = %q, want %q", cfg.MCPServers[1].URL, "http://b:9090/mcp")
	}
}

func TestLoad_MCPServerValidation_MissingName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	yaml := "mcp_servers:\n  - url: http://localhost:8090/mcp\nprompt: hello\n"
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error %q should contain 'name is required'", err.Error())
	}
}

func TestLoad_MCPServerValidation_DuplicateName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	yaml := `
mcp_servers:
  - name: resources
    url: http://a:8090/mcp
  - name: resources
    url: http://b:9090/mcp
prompt: hello
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error %q should contain 'duplicate'", err.Error())
	}
	if !strings.Contains(err.Error(), "resources") {
		t.Errorf("error %q should contain 'resources'", err.Error())
	}
}

func TestLoad_MCPServerValidation_EmptyList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	yaml := "prompt: hello\n"
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.MCPServers) != 0 {
		t.Errorf("MCPServers length = %d, want 0", len(cfg.MCPServers))
	}
}
