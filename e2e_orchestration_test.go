//go:build e2e

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"testing"

	"agent-stop-and-go/internal/a2a"
	"agent-stop-and-go/internal/agent"
	"agent-stop-and-go/internal/api"
	"agent-stop-and-go/internal/config"
	"agent-stop-and-go/internal/storage"
)

// orchServer holds a test server for orchestration tests.
type orchServer struct {
	baseURL string
	agent   *agent.Agent
	server  *api.Server
	cfg     *config.Config
}

// startMCPResources starts an mcp-resources HTTP server as a subprocess.
func startMCPResources(t *testing.T, port int, dbPath string) {
	t.Helper()

	os.MkdirAll("./data", 0755)

	tmpCfg := fmt.Sprintf("host: 0.0.0.0\nport: %d\ndb_path: %s\n", port, dbPath)
	tmpFile, err := os.CreateTemp("", "mcp-resources-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp config: %v", err)
	}
	tmpFile.WriteString(tmpCfg)
	tmpFile.Close()

	cmd := exec.Command(mcpResourcesBin(), "--config", tmpFile.Name())
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to start mcp-resources on port %d: %v", port, err)
	}

	// Wait for mcp-resources to be ready
	mcpURL := fmt.Sprintf("http://localhost:%d/mcp", port)
	ready := waitForHTTP(mcpURL, 30)
	if !ready {
		cmd.Process.Kill()
		os.Remove(tmpFile.Name())
		t.Fatalf("mcp-resources failed to start on port %d within timeout", port)
	}

	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		os.Remove(tmpFile.Name())
		os.Remove(dbPath)
	})
}

// startOrchServer starts a test server with the given config and port.
func startOrchServer(t *testing.T, configPath string, port int) *orchServer {
	t.Helper()

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config %s: %v", configPath, err)
	}
	cfg.Port = port

	store, err := storage.New(cfg.DataDir)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ag := agent.New(cfg, store)
	if err := ag.Start(); err != nil {
		t.Fatalf("Failed to start agent: %v", err)
	}

	srv := api.New(cfg, ag)
	go func() {
		srv.Start()
	}()

	base := fmt.Sprintf("http://localhost:%d", port)

	// Wait for server ready
	ready := waitForHTTP(base+"/health", 30)
	if !ready {
		ag.Stop()
		t.Fatalf("Server on port %d failed to start", port)
	}

	t.Cleanup(func() {
		srv.Shutdown()
		ag.Stop()
		os.RemoveAll(cfg.DataDir)
	})

	return &orchServer{baseURL: base, agent: ag, server: srv, cfg: cfg}
}

// orchA2aRPC sends a JSON-RPC 2.0 request to the A2A endpoint at the given base URL.
func orchA2aRPC(baseURL, method string, params any) (*a2a.Response, error) {
	req := a2a.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpResp, err := http.Post(baseURL+"/a2a", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	var rpcResp a2a.Response
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("failed to parse A2A response: %w (body: %s)", err, respBody)
	}
	return &rpcResp, nil
}

// TS-022: Sequential pipeline with non-destructive tool
func TestOrchSequentialNonDestructive(t *testing.T) {
	startMCPResources(t, 9290, "./data/e2e_test_seq/resources.db")
	srv := startOrchServer(t, "testdata/e2e-sequential.yaml", 9091)

	// Create conversation
	result, status, err := httpJSON("POST", srv.baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	if status != 201 {
		t.Fatalf("Expected status 201, got %d", status)
	}
	conv := result["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Send non-destructive message through sequential pipeline
	result, status, err = httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", srv.baseURL, convID), map[string]string{
		"message": "list all resources",
	})
	if err != nil {
		t.Fatalf("Send message failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	processResult := result["result"].(map[string]any)

	// Should NOT require approval (resources_list is non-destructive)
	if waitingApproval, ok := processResult["waiting_approval"].(bool); ok && waitingApproval {
		t.Fatal("Expected no approval for non-destructive tool in sequential pipeline")
	}

	// Verify conversation is still active
	getResult, _, err := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", srv.baseURL, convID), nil)
	if err != nil {
		t.Fatalf("Get conversation failed: %v", err)
	}
	updatedConv := getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "active" {
		t.Fatalf("Expected status active, got %v", updatedConv["status"])
	}
}

// TS-023: Sequential pipeline with destructive tool and approval
func TestOrchSequentialDestructive(t *testing.T) {
	startMCPResources(t, 9290, "./data/e2e_test_seq/resources.db")
	srv := startOrchServer(t, "testdata/e2e-sequential.yaml", 9091)

	// Create conversation
	result, _, err := httpJSON("POST", srv.baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	conv := result["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Send destructive message through sequential pipeline
	result, status, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", srv.baseURL, convID), map[string]string{
		"message": "add resource seq-test with value 42",
	})
	if err != nil {
		t.Fatalf("Send message failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	processResult := result["result"].(map[string]any)

	// Should require approval (resources_add is destructive, sequential pauses)
	waitingApproval, ok := processResult["waiting_approval"].(bool)
	if !ok || !waitingApproval {
		t.Fatalf("Expected waiting_approval to be true for destructive tool in sequential pipeline, got result: %v", processResult)
	}

	approval := processResult["approval"].(map[string]any)
	approvalUUID := approval["uuid"].(string)

	// Verify conversation is in waiting_approval status
	getResult, _, _ := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", srv.baseURL, convID), nil)
	updatedConv := getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "waiting_approval" {
		t.Fatalf("Expected status waiting_approval, got %v", updatedConv["status"])
	}

	// Verify pipeline_state is saved
	if updatedConv["pipeline_state"] == nil {
		t.Fatal("Expected pipeline_state to be saved for sequential pipeline pause")
	}

	// Approve the action
	approveResult, status, err := httpJSON("POST", fmt.Sprintf("%s/approvals/%s", srv.baseURL, approvalUUID), map[string]string{
		"answer": "yes",
	})
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200 for approval, got %d", status)
	}

	// Should have resumed and completed
	approveProcessResult := approveResult["result"].(map[string]any)
	if approveProcessResult["waiting_approval"].(bool) {
		t.Fatal("Expected waiting_approval to be false after approval")
	}

	// Verify conversation returned to active
	getResult, _, _ = httpJSON("GET", fmt.Sprintf("%s/conversations/%s", srv.baseURL, convID), nil)
	updatedConv = getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "active" {
		t.Fatalf("Expected status active after approval, got %v", updatedConv["status"])
	}
}

// TS-024: Parallel execution (destructive tools run immediately without approval)
func TestOrchParallel(t *testing.T) {
	startMCPResources(t, 9290, "./data/e2e_test_par/resources.db")
	srv := startOrchServer(t, "testdata/e2e-parallel.yaml", 9091)

	// Create conversation
	result, _, err := httpJSON("POST", srv.baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	conv := result["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Send message through parallel pipeline
	result, status, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", srv.baseURL, convID), map[string]string{
		"message": "give me an overview of all resources",
	})
	if err != nil {
		t.Fatalf("Send message failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	processResult := result["result"].(map[string]any)

	// Parallel nodes should NOT pause for approval
	if waitingApproval, ok := processResult["waiting_approval"].(bool); ok && waitingApproval {
		t.Fatal("Expected no approval for parallel execution")
	}

	// Should have a response (combined from parallel nodes + summarizer)
	if processResult["response"] == nil || processResult["response"] == "" {
		t.Fatal("Expected a response from parallel pipeline")
	}

	// Verify conversation is active
	getResult, _, err := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", srv.baseURL, convID), nil)
	if err != nil {
		t.Fatalf("Get conversation failed: %v", err)
	}
	updatedConv := getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "active" {
		t.Fatalf("Expected status active, got %v", updatedConv["status"])
	}
}

// TS-025: Loop execution with exit condition (destructive tools run immediately)
func TestOrchLoopWithExit(t *testing.T) {
	startMCPResources(t, 9290, "./data/e2e_test_loop/resources.db")
	srv := startOrchServer(t, "testdata/e2e-loop.yaml", 9091)

	// Create conversation
	result, _, err := httpJSON("POST", srv.baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	conv := result["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Send message through loop pipeline
	result, status, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", srv.baseURL, convID), map[string]string{
		"message": "ensure we have at least 2 resources",
	})
	if err != nil {
		t.Fatalf("Send message failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	processResult := result["result"].(map[string]any)

	// Loop should NOT pause for approval (destructive tools run immediately in loops)
	if waitingApproval, ok := processResult["waiting_approval"].(bool); ok && waitingApproval {
		t.Fatal("Expected no approval for loop execution (destructive tools run immediately)")
	}

	// Should have completed with a response
	if processResult["response"] == nil || processResult["response"] == "" {
		t.Fatal("Expected a response from loop pipeline")
	}

	// Verify conversation is active
	getResult, _, err := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", srv.baseURL, convID), nil)
	if err != nil {
		t.Fatalf("Get conversation failed: %v", err)
	}
	updatedConv := getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "active" {
		t.Fatalf("Expected status active, got %v", updatedConv["status"])
	}
}

// TS-026: A2A chain non-destructive (Agent A → A2A → Agent B → resources_list)
func TestOrchA2AChainNonDestructive(t *testing.T) {
	startMCPResources(t, 9290, "./data/e2e_test_chain/resources.db")

	// Start Agent B first (backend with MCP tools)
	_ = startOrchServer(t, "testdata/e2e-chain-b.yaml", 9092)

	// Start Agent A (orchestrator with A2A delegation to Agent B)
	srvA := startOrchServer(t, "testdata/e2e-chain-a.yaml", 9091)

	// Create conversation on Agent A
	result, status, err := httpJSON("POST", srvA.baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	if status != 201 {
		t.Fatalf("Expected status 201, got %d", status)
	}
	conv := result["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Send non-destructive message (flows: Agent A analyzer → A2A delegator → Agent B → resources_list)
	result, status, err = httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", srvA.baseURL, convID), map[string]string{
		"message": "list all resources",
	})
	if err != nil {
		t.Fatalf("Send message failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	processResult := result["result"].(map[string]any)

	// Should NOT require approval (resources_list is non-destructive)
	if waitingApproval, ok := processResult["waiting_approval"].(bool); ok && waitingApproval {
		t.Fatal("Expected no approval for non-destructive A2A chain")
	}

	// Verify conversation is active
	getResult, _, err := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", srvA.baseURL, convID), nil)
	if err != nil {
		t.Fatalf("Get conversation failed: %v", err)
	}
	updatedConv := getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "active" {
		t.Fatalf("Expected status active, got %v", updatedConv["status"])
	}
}

// TS-027: A2A chain destructive with proxy approval (Agent A → A2A → Agent B → resources_add)
func TestOrchA2AChainDestructive(t *testing.T) {
	startMCPResources(t, 9290, "./data/e2e_test_chain/resources.db")

	// Start Agent B (backend with destructive MCP tools)
	_ = startOrchServer(t, "testdata/e2e-chain-b.yaml", 9092)

	// Start Agent A (orchestrator)
	srvA := startOrchServer(t, "testdata/e2e-chain-a.yaml", 9091)

	// Create conversation on Agent A
	result, _, err := httpJSON("POST", srvA.baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	conv := result["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Send destructive message (flows: Agent A → A2A → Agent B → resources_add → input-required → proxy approval)
	result, status, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", srvA.baseURL, convID), map[string]string{
		"message": "add resource chain-test with value 77",
	})
	if err != nil {
		t.Fatalf("Send message failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	processResult := result["result"].(map[string]any)

	// Should require approval (proxy approval from Agent B's destructive tool)
	waitingApproval, ok := processResult["waiting_approval"].(bool)
	if !ok || !waitingApproval {
		t.Fatalf("Expected waiting_approval for destructive A2A chain, got result: %v", processResult)
	}

	approval := processResult["approval"].(map[string]any)
	approvalUUID := approval["uuid"].(string)

	// Verify conversation is in waiting_approval
	getResult, _, _ := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", srvA.baseURL, convID), nil)
	updatedConv := getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "waiting_approval" {
		t.Fatalf("Expected status waiting_approval, got %v", updatedConv["status"])
	}

	// Approve on Agent A (should forward to Agent B via A2A)
	approveResult, status, err := httpJSON("POST", fmt.Sprintf("%s/approvals/%s", srvA.baseURL, approvalUUID), map[string]string{
		"answer": "yes",
	})
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200 for approval, got %d", status)
	}

	// Should have completed after proxy approval forwarding
	approveProcessResult := approveResult["result"].(map[string]any)
	if approveProcessResult["waiting_approval"].(bool) {
		t.Fatal("Expected waiting_approval to be false after proxy approval")
	}

	// Verify conversation returned to active
	getResult, _, _ = httpJSON("GET", fmt.Sprintf("%s/conversations/%s", srvA.baseURL, convID), nil)
	updatedConv = getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "active" {
		t.Fatalf("Expected status active after proxy approval, got %v", updatedConv["status"])
	}
}

// TS-028: Sequential pipeline accessed via A2A protocol (non-destructive)
func TestOrchSequentialViaA2A(t *testing.T) {
	startMCPResources(t, 9290, "./data/e2e_test_seq/resources.db")
	srv := startOrchServer(t, "testdata/e2e-sequential.yaml", 9091)

	// Send message via A2A protocol to an orchestrated agent
	params := a2a.MessageSendParams{
		Message: a2a.Message{
			Role: "user",
			Parts: []a2a.Part{
				{Type: "text", Text: "list all resources"},
			},
		},
	}

	rpcResp, err := orchA2aRPC(srv.baseURL, "message/send", params)
	if err != nil {
		t.Fatalf("A2A message/send failed: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("A2A message/send returned error: %d %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	var task a2a.Task
	if err := json.Unmarshal(rpcResp.Result, &task); err != nil {
		t.Fatalf("Failed to parse task: %v", err)
	}

	if task.ID == "" {
		t.Fatal("Expected task to have an ID")
	}
	// Non-destructive sequential pipeline should complete
	if task.Status.State != "completed" {
		t.Fatalf("Expected task state 'completed', got '%s'", task.Status.State)
	}
	if task.Artifact == nil || len(task.Artifact.Parts) == 0 {
		t.Fatal("Expected task to have an artifact with parts")
	}
}
