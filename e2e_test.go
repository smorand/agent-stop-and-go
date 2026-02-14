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
	"runtime"
	"strings"
	"testing"
	"time"

	"agent-stop-and-go/internal/a2a"
	"agent-stop-and-go/internal/agent"
	"agent-stop-and-go/internal/api"
	"agent-stop-and-go/internal/config"
	"agent-stop-and-go/internal/storage"
)

// mcpResourcesBin returns the platform-specific mcp-resources binary path.
func mcpResourcesBin() string {
	return fmt.Sprintf("./bin/mcp-resources-%s-%s", runtime.GOOS, runtime.GOARCH)
}

const baseURL = "http://localhost:9090"

var testAgent *agent.Agent

func TestMain(m *testing.M) {
	// Start mcp-resources as a separate HTTP server
	mcpDBPath := "./data/e2e_test/resources.db"
	os.MkdirAll("./data/e2e_test", 0755)
	mcpCmd := exec.Command(mcpResourcesBin(), "--db", mcpDBPath)
	mcpCmd.Stdout = os.Stderr
	mcpCmd.Stderr = os.Stderr
	if err := mcpCmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start mcp-resources: %v\n", err)
		os.Exit(1)
	}

	// Wait for mcp-resources to be ready
	mcpReady := waitForHTTP("http://localhost:8090/mcp", 30)
	if !mcpReady {
		mcpCmd.Process.Kill()
		fmt.Fprintf(os.Stderr, "mcp-resources failed to start within timeout\n")
		os.Exit(1)
	}

	cfg, err := config.Load("config/agent.yaml")
	if err != nil {
		mcpCmd.Process.Kill()
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Override for tests
	cfg.Port = 9090
	cfg.DataDir = "./data/e2e_test"
	cfg.MCP.URL = "http://localhost:8090/mcp"
	cfg.MCP.Command = ""
	cfg.MCP.Args = nil

	store, err := storage.New(cfg.DataDir)
	if err != nil {
		mcpCmd.Process.Kill()
		fmt.Fprintf(os.Stderr, "Failed to create storage: %v\n", err)
		os.Exit(1)
	}

	ag := agent.New(cfg, store)
	testAgent = ag

	if err := ag.Start(); err != nil {
		mcpCmd.Process.Kill()
		fmt.Fprintf(os.Stderr, "Failed to start agent: %v\n", err)
		os.Exit(1)
	}

	server := api.New(cfg, ag)

	go func() {
		if err := server.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		}
	}()

	// Wait for agent server to be ready
	ready := waitForHTTP(baseURL+"/health", 30)
	if !ready {
		fmt.Fprintf(os.Stderr, "Server failed to start within timeout\n")
		os.Exit(1)
	}

	code := m.Run()

	// Cleanup
	server.Shutdown()
	ag.Stop()
	mcpCmd.Process.Kill()
	mcpCmd.Wait()
	os.RemoveAll(cfg.DataDir)

	os.Exit(code)
}

// waitForHTTP polls a URL until it responds (any status) or times out.
func waitForHTTP(url string, maxAttempts int) bool {
	for i := 0; i < maxAttempts; i++ {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// httpJSON sends a request and decodes the JSON response.
func httpJSON(method, url string, body any) (map[string]any, int, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to parse response: %w (body: %s)", err, respBody)
	}

	return result, resp.StatusCode, nil
}

// TS-001: Server startup with configuration
func TestHealthAndTools(t *testing.T) {
	// Test health
	result, status, err := httpJSON("GET", baseURL+"/health", nil)
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}
	if result["status"] != "ok" {
		t.Fatalf("Expected status ok, got %v", result["status"])
	}

	// Test tools
	result, status, err = httpJSON("GET", baseURL+"/tools", nil)
	if err != nil {
		t.Fatalf("Tools request failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("Expected tools array, got %T", result["tools"])
	}
	if len(tools) == 0 {
		t.Fatal("Expected at least one tool")
	}

	// Verify at least one tool has destructiveHint
	hasDestructive := false
	for _, tool := range tools {
		toolMap, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		if hint, ok := toolMap["destructiveHint"].(bool); ok && hint {
			hasDestructive = true
			break
		}
	}
	if !hasDestructive {
		t.Log("Warning: no tools with destructiveHint=true found")
	}
}

// TS-002: Create conversation
func TestCreateConversation(t *testing.T) {
	// Create conversation without message
	result, status, err := httpJSON("POST", baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	if status != 201 {
		t.Fatalf("Expected status 201, got %d", status)
	}

	conv, ok := result["conversation"].(map[string]any)
	if !ok {
		t.Fatalf("Expected conversation object, got %T", result["conversation"])
	}
	if conv["id"] == nil || conv["id"] == "" {
		t.Fatal("Expected conversation to have an ID")
	}
	if conv["status"] != "active" {
		t.Fatalf("Expected status active, got %v", conv["status"])
	}

	// Create conversation with initial message
	result, status, err = httpJSON("POST", baseURL+"/conversations", map[string]string{
		"message": "list resources",
	})
	if err != nil {
		t.Fatalf("Create conversation with message failed: %v", err)
	}
	if status != 201 {
		t.Fatalf("Expected status 201, got %d", status)
	}

	conv, ok = result["conversation"].(map[string]any)
	if !ok {
		t.Fatalf("Expected conversation object, got %T", result["conversation"])
	}

	// Should have messages (system + user + tool/assistant)
	messages, ok := conv["messages"].([]any)
	if !ok {
		t.Fatalf("Expected messages array, got %T", conv["messages"])
	}
	if len(messages) < 2 {
		t.Fatalf("Expected at least 2 messages, got %d", len(messages))
	}
}

// TS-003: Non-destructive tool execution (list)
func TestNonDestructiveTool(t *testing.T) {
	// Create conversation
	createResult, _, err := httpJSON("POST", baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	conv := createResult["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Send list message
	result, status, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", baseURL, convID), map[string]string{
		"message": "list all resources",
	})
	if err != nil {
		t.Fatalf("Send message failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	processResult, ok := result["result"].(map[string]any)
	if !ok {
		t.Fatalf("Expected result object, got %T", result["result"])
	}

	// Should not require approval
	if waitingApproval, ok := processResult["waiting_approval"].(bool); ok && waitingApproval {
		t.Fatal("Expected waiting_approval to be false for non-destructive tool")
	}

	// Verify conversation is still active
	getResult, _, err := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", baseURL, convID), nil)
	if err != nil {
		t.Fatalf("Get conversation failed: %v", err)
	}
	updatedConv := getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "active" {
		t.Fatalf("Expected status active, got %v", updatedConv["status"])
	}
}

// TS-004: Destructive tool with approval (add)
func TestDestructiveToolApproval(t *testing.T) {
	// Create conversation
	createResult, _, err := httpJSON("POST", baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	conv := createResult["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Send destructive message
	result, status, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", baseURL, convID), map[string]string{
		"message": "create a server e2e-test-server with value 42",
	})
	if err != nil {
		t.Fatalf("Send message failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	processResult := result["result"].(map[string]any)

	// Should require approval
	waitingApproval, ok := processResult["waiting_approval"].(bool)
	if !ok || !waitingApproval {
		t.Fatal("Expected waiting_approval to be true for destructive tool")
	}

	approval, ok := processResult["approval"].(map[string]any)
	if !ok {
		t.Fatal("Expected approval object")
	}
	approvalUUID := approval["uuid"].(string)

	// Verify conversation is in waiting_approval status
	getResult, _, _ := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", baseURL, convID), nil)
	updatedConv := getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "waiting_approval" {
		t.Fatalf("Expected status waiting_approval, got %v", updatedConv["status"])
	}

	// Approve it
	approveResult, status, err := httpJSON("POST", fmt.Sprintf("%s/approvals/%s", baseURL, approvalUUID), map[string]string{
		"answer": "yes",
	})
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	// Should have executed the tool
	approveProcessResult := approveResult["result"].(map[string]any)
	if approveProcessResult["waiting_approval"].(bool) {
		t.Fatal("Expected waiting_approval to be false after approval")
	}

	// Verify conversation returned to active
	getResult, _, _ = httpJSON("GET", fmt.Sprintf("%s/conversations/%s", baseURL, convID), nil)
	updatedConv = getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "active" {
		t.Fatalf("Expected status active after approval, got %v", updatedConv["status"])
	}
}

// TS-007: Approval rejection
func TestApprovalRejection(t *testing.T) {
	// Create conversation
	createResult, _, err := httpJSON("POST", baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	conv := createResult["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Send destructive message
	result, _, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", baseURL, convID), map[string]string{
		"message": "add resource rejection-test with value 99",
	})
	if err != nil {
		t.Fatalf("Send message failed: %v", err)
	}

	processResult := result["result"].(map[string]any)
	waitingApproval, ok := processResult["waiting_approval"].(bool)
	if !ok || !waitingApproval {
		t.Fatal("Expected waiting_approval to be true")
	}

	approval := processResult["approval"].(map[string]any)
	approvalUUID := approval["uuid"].(string)

	// Reject it
	rejectResult, status, err := httpJSON("POST", fmt.Sprintf("%s/approvals/%s", baseURL, approvalUUID), map[string]string{
		"answer": "no",
	})
	if err != nil {
		t.Fatalf("Reject failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected status 200, got %d", status)
	}

	// Should indicate cancelled
	rejectProcessResult := rejectResult["result"].(map[string]any)
	if rejectProcessResult["waiting_approval"].(bool) {
		t.Fatal("Expected waiting_approval to be false after rejection")
	}
	if response, ok := rejectProcessResult["response"].(string); ok {
		if response != "Operation cancelled by user." {
			t.Fatalf("Expected cancellation message, got: %s", response)
		}
	}

	// Verify conversation returned to active
	getResult, _, _ := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", baseURL, convID), nil)
	updatedConv := getResult["conversation"].(map[string]any)
	if updatedConv["status"] != "active" {
		t.Fatalf("Expected status active after rejection, got %v", updatedConv["status"])
	}
}

// TS-008: Multiple approval formats
func TestMultipleApprovalFormats(t *testing.T) {
	formats := []struct {
		name string
		body map[string]any
	}{
		{"approved_bool", map[string]any{"approved": true}},
		{"action_string", map[string]any{"action": "approve"}},
		{"answer_string", map[string]any{"answer": "yes"}},
	}

	for _, format := range formats {
		t.Run(format.name, func(t *testing.T) {
			// Create conversation
			createResult, _, err := httpJSON("POST", baseURL+"/conversations", nil)
			if err != nil {
				t.Fatalf("Create conversation failed: %v", err)
			}
			conv := createResult["conversation"].(map[string]any)
			convID := conv["id"].(string)

			// Send destructive message
			result, _, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", baseURL, convID), map[string]string{
				"message": fmt.Sprintf("add resource format-test-%s with value 1", format.name),
			})
			if err != nil {
				t.Fatalf("Send message failed: %v", err)
			}

			processResult := result["result"].(map[string]any)
			waitingApproval, ok := processResult["waiting_approval"].(bool)
			if !ok || !waitingApproval {
				t.Fatal("Expected waiting_approval to be true")
			}

			approval := processResult["approval"].(map[string]any)
			approvalUUID := approval["uuid"].(string)

			// Approve with this format
			approveResult, status, err := httpJSON("POST", fmt.Sprintf("%s/approvals/%s", baseURL, approvalUUID), format.body)
			if err != nil {
				t.Fatalf("Approve with format %s failed: %v", format.name, err)
			}
			if status != 200 {
				t.Fatalf("Expected status 200 for format %s, got %d", format.name, status)
			}

			approveProcessResult := approveResult["result"].(map[string]any)
			if approveProcessResult["waiting_approval"].(bool) {
				t.Fatalf("Expected waiting_approval to be false for format %s", format.name)
			}
		})
	}
}

// TS-006: Natural language variations
func TestNaturalLanguageVariations(t *testing.T) {
	listPhrases := []string{
		"list",
		"show me all resources",
	}

	for _, phrase := range listPhrases {
		t.Run("list_"+phrase, func(t *testing.T) {
			// Create conversation
			createResult, _, err := httpJSON("POST", baseURL+"/conversations", nil)
			if err != nil {
				t.Fatalf("Create conversation failed: %v", err)
			}
			conv := createResult["conversation"].(map[string]any)
			convID := conv["id"].(string)

			result, _, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", baseURL, convID), map[string]string{
				"message": phrase,
			})
			if err != nil {
				t.Fatalf("Send message failed: %v", err)
			}

			processResult := result["result"].(map[string]any)

			// Should not require approval (list is non-destructive)
			if waitingApproval, ok := processResult["waiting_approval"].(bool); ok && waitingApproval {
				t.Fatalf("Expected no approval for phrase: %s", phrase)
			}
		})
	}

	addPhrases := []struct {
		phrase string
	}{
		{"create a server nlp-test-1 with value 10"},
		{"add resource nlp-test-2 value 20"},
	}

	for _, tc := range addPhrases {
		t.Run("add_"+tc.phrase, func(t *testing.T) {
			createResult, _, err := httpJSON("POST", baseURL+"/conversations", nil)
			if err != nil {
				t.Fatalf("Create conversation failed: %v", err)
			}
			conv := createResult["conversation"].(map[string]any)
			convID := conv["id"].(string)

			result, _, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", baseURL, convID), map[string]string{
				"message": tc.phrase,
			})
			if err != nil {
				t.Fatalf("Send message failed: %v", err)
			}

			processResult := result["result"].(map[string]any)

			// Should require approval (add is destructive)
			waitingApproval, ok := processResult["waiting_approval"].(bool)
			if !ok || !waitingApproval {
				t.Fatalf("Expected approval for phrase: %s", tc.phrase)
			}
		})
	}
}

// a2aRPC sends a JSON-RPC 2.0 request to the A2A endpoint and returns the parsed response.
func a2aRPC(method string, params any) (*a2a.Response, error) {
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

// TestAgentCard verifies the agent card endpoint returns valid skills from MCP tools.
func TestAgentCard(t *testing.T) {
	resp, err := http.Get(baseURL + "/.well-known/agent.json")
	if err != nil {
		t.Fatalf("Agent card request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read body: %v", err)
	}

	var card a2a.AgentCard
	if err := json.Unmarshal(body, &card); err != nil {
		t.Fatalf("Failed to parse agent card: %v (body: %s)", err, body)
	}

	if card.Name == "" {
		t.Fatal("Expected agent card to have a name")
	}
	if card.URL == "" {
		t.Fatal("Expected agent card to have a URL")
	}
	if len(card.Skills) == 0 {
		t.Fatal("Expected agent card to have at least one skill")
	}

	// Verify skills match MCP tools
	hasResourcesList := false
	for _, skill := range card.Skills {
		if skill.ID == "resources_list" {
			hasResourcesList = true
		}
		if skill.Name == "" {
			t.Fatal("Expected skill to have a name")
		}
	}
	if !hasResourcesList {
		t.Fatal("Expected agent card skills to include resources_list")
	}
}

// TestA2AMessageSendNonDestructive verifies message/send with a non-destructive request returns a completed task.
func TestA2AMessageSendNonDestructive(t *testing.T) {
	params := a2a.MessageSendParams{
		Message: a2a.Message{
			Role: "user",
			Parts: []a2a.Part{
				{Type: "text", Text: "list all resources"},
			},
		},
	}

	rpcResp, err := a2aRPC("message/send", params)
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
	if task.Status.State != "completed" {
		t.Fatalf("Expected task state 'completed', got '%s'", task.Status.State)
	}
	if task.Artifact == nil || len(task.Artifact.Parts) == 0 {
		t.Fatal("Expected task to have an artifact with parts")
	}
}

// TestA2AMessageSendDestructive verifies message/send with a destructive request returns an input-required task,
// then approve via REST, then tasks/get returns completed.
func TestA2AMessageSendDestructive(t *testing.T) {
	params := a2a.MessageSendParams{
		Message: a2a.Message{
			Role: "user",
			Parts: []a2a.Part{
				{Type: "text", Text: "add resource a2a-test-item with value 77"},
			},
		},
	}

	rpcResp, err := a2aRPC("message/send", params)
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
	if task.Status.State != "input-required" {
		t.Fatalf("Expected task state 'input-required', got '%s'", task.Status.State)
	}
	if task.Status.Message == nil {
		t.Fatal("Expected task status to have a message with approval info")
	}

	// Get the conversation to find the approval UUID
	convResult, _, err := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", baseURL, task.ID), nil)
	if err != nil {
		t.Fatalf("Get conversation failed: %v", err)
	}
	conv := convResult["conversation"].(map[string]any)
	pendingApproval := conv["pending_approval"].(map[string]any)
	approvalUUID := pendingApproval["uuid"].(string)

	// Approve via REST API
	_, status, err := httpJSON("POST", fmt.Sprintf("%s/approvals/%s", baseURL, approvalUUID), map[string]string{
		"answer": "yes",
	})
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Expected approval status 200, got %d", status)
	}

	// Now tasks/get should return completed
	getResp, err := a2aRPC("tasks/get", a2a.TaskGetParams{ID: task.ID})
	if err != nil {
		t.Fatalf("A2A tasks/get failed: %v", err)
	}
	if getResp.Error != nil {
		t.Fatalf("A2A tasks/get returned error: %d %s", getResp.Error.Code, getResp.Error.Message)
	}

	var updatedTask a2a.Task
	if err := json.Unmarshal(getResp.Result, &updatedTask); err != nil {
		t.Fatalf("Failed to parse updated task: %v", err)
	}

	if updatedTask.Status.State != "completed" {
		t.Fatalf("Expected task state 'completed' after approval, got '%s'", updatedTask.Status.State)
	}
}

// TestA2AMessageSendContinuation verifies message/send with taskId approves a pending task via A2A.
func TestA2AMessageSendContinuation(t *testing.T) {
	// Send a destructive message via A2A to get an "input-required" task
	params := a2a.MessageSendParams{
		Message: a2a.Message{
			Role: "user",
			Parts: []a2a.Part{
				{Type: "text", Text: "add resource a2a-approve-test with value 88"},
			},
		},
	}

	rpcResp, err := a2aRPC("message/send", params)
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

	if task.Status.State != "input-required" {
		t.Fatalf("Expected task state 'input-required', got '%s'", task.Status.State)
	}

	// Approve via A2A message/send with taskId
	approveResp, err := a2aRPC("message/send", a2a.MessageSendParams{
		TaskID: task.ID,
		Message: a2a.Message{
			Role: "user",
			Parts: []a2a.Part{
				{Type: "text", Text: "approved"},
			},
		},
	})
	if err != nil {
		t.Fatalf("A2A message/send continuation failed: %v", err)
	}
	if approveResp.Error != nil {
		t.Fatalf("A2A message/send continuation returned error: %d %s", approveResp.Error.Code, approveResp.Error.Message)
	}

	var approvedTask a2a.Task
	if err := json.Unmarshal(approveResp.Result, &approvedTask); err != nil {
		t.Fatalf("Failed to parse approved task: %v", err)
	}

	if approvedTask.Status.State != "completed" {
		t.Fatalf("Expected task state 'completed' after approval, got '%s'", approvedTask.Status.State)
	}
	if approvedTask.Artifact == nil || len(approvedTask.Artifact.Parts) == 0 {
		t.Fatal("Expected approved task to have an artifact with parts")
	}
}

// TestA2AMessageSendRejection verifies message/send with taskId rejects a pending task via A2A.
func TestA2AMessageSendRejection(t *testing.T) {
	params := a2a.MessageSendParams{
		Message: a2a.Message{
			Role: "user",
			Parts: []a2a.Part{
				{Type: "text", Text: "add resource a2a-reject-test with value 99"},
			},
		},
	}

	rpcResp, err := a2aRPC("message/send", params)
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

	if task.Status.State != "input-required" {
		t.Fatalf("Expected task state 'input-required', got '%s'", task.Status.State)
	}

	// Reject via A2A message/send with taskId
	rejectResp, err := a2aRPC("message/send", a2a.MessageSendParams{
		TaskID: task.ID,
		Message: a2a.Message{
			Role: "user",
			Parts: []a2a.Part{
				{Type: "text", Text: "rejected"},
			},
		},
	})
	if err != nil {
		t.Fatalf("A2A message/send rejection failed: %v", err)
	}
	if rejectResp.Error != nil {
		t.Fatalf("A2A message/send rejection returned error: %d %s", rejectResp.Error.Code, rejectResp.Error.Message)
	}

	var rejectedTask a2a.Task
	if err := json.Unmarshal(rejectResp.Result, &rejectedTask); err != nil {
		t.Fatalf("Failed to parse rejected task: %v", err)
	}

	// After rejection, task should be completed (conversation returns to active)
	if rejectedTask.Status.State != "completed" {
		t.Fatalf("Expected task state 'completed' after rejection, got '%s'", rejectedTask.Status.State)
	}
}

// TestA2ATasksGet verifies tasks/get retrieves a task by conversation ID.
func TestA2ATasksGet(t *testing.T) {
	// First create a conversation via REST
	createResult, _, err := httpJSON("POST", baseURL+"/conversations", map[string]string{
		"message": "list resources",
	})
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	conv := createResult["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Retrieve via A2A tasks/get
	rpcResp, err := a2aRPC("tasks/get", a2a.TaskGetParams{ID: convID})
	if err != nil {
		t.Fatalf("A2A tasks/get failed: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("A2A tasks/get returned error: %d %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	var task a2a.Task
	if err := json.Unmarshal(rpcResp.Result, &task); err != nil {
		t.Fatalf("Failed to parse task: %v", err)
	}

	if task.ID != convID {
		t.Fatalf("Expected task ID '%s', got '%s'", convID, task.ID)
	}
	if task.Status.State != "completed" {
		t.Fatalf("Expected task state 'completed', got '%s'", task.Status.State)
	}
	if task.Artifact == nil {
		t.Fatal("Expected task to have an artifact")
	}
}

// --- Error-path tests (deterministic, no LLM calls) ---

// TestErrorGetConversationNotFound verifies 404 for a non-existent conversation.
func TestErrorGetConversationNotFound(t *testing.T) {
	result, status, err := httpJSON("GET", baseURL+"/conversations/nonexistent-id", nil)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if status != 404 {
		t.Fatalf("Expected status 404, got %d", status)
	}
	if result["error"] == nil || result["error"] == "" {
		t.Fatal("Expected error field in response")
	}
}

// TestErrorSendMessageMalformedJSON verifies 400 for malformed JSON body.
func TestErrorSendMessageMalformedJSON(t *testing.T) {
	// Create a conversation first
	createResult, _, err := httpJSON("POST", baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	conv := createResult["conversation"].(map[string]any)
	convID := conv["id"].(string)

	// Send malformed JSON (can't use httpJSON which always produces valid JSON)
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/conversations/%s/messages", baseURL, convID), strings.NewReader("{invalid json"))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	errMsg, ok := result["error"].(string)
	if !ok || !strings.HasPrefix(errMsg, "invalid request body:") {
		t.Fatalf("Expected error starting with 'invalid request body:', got %q", errMsg)
	}
}

// TestErrorSendMessageEmptyMessage verifies 400 for empty message.
func TestErrorSendMessageEmptyMessage(t *testing.T) {
	createResult, _, err := httpJSON("POST", baseURL+"/conversations", nil)
	if err != nil {
		t.Fatalf("Create conversation failed: %v", err)
	}
	conv := createResult["conversation"].(map[string]any)
	convID := conv["id"].(string)

	result, status, err := httpJSON("POST", fmt.Sprintf("%s/conversations/%s/messages", baseURL, convID), map[string]string{
		"message": "",
	})
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if status != 400 {
		t.Fatalf("Expected status 400, got %d", status)
	}
	if result["error"] != "message is required" {
		t.Fatalf("Expected 'message is required', got %v", result["error"])
	}
}

// TestErrorSendMessageConversationNotFound verifies 404 for sending to non-existent conversation.
func TestErrorSendMessageConversationNotFound(t *testing.T) {
	result, status, err := httpJSON("POST", baseURL+"/conversations/bad-id/messages", map[string]string{
		"message": "hello",
	})
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if status != 404 {
		t.Fatalf("Expected status 404, got %d", status)
	}
	if result["error"] == nil || result["error"] == "" {
		t.Fatal("Expected error field in response")
	}
}

// TestErrorApprovalNotFound verifies 404 for a non-existent approval UUID.
func TestErrorApprovalNotFound(t *testing.T) {
	result, status, err := httpJSON("POST", baseURL+"/approvals/00000000-0000-0000-0000-000000000000", map[string]string{
		"answer": "yes",
	})
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if status != 404 {
		t.Fatalf("Expected status 404, got %d", status)
	}
	if result["error"] == nil || result["error"] == "" {
		t.Fatal("Expected error field in response")
	}
}

// TestErrorApprovalMalformedJSON verifies 400 for malformed JSON in approval.
func TestErrorApprovalMalformedJSON(t *testing.T) {
	req, err := http.NewRequest("POST", baseURL+"/approvals/some-uuid", strings.NewReader("{bad json"))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	errMsg, ok := result["error"].(string)
	if !ok || !strings.HasPrefix(errMsg, "invalid request body:") {
		t.Fatalf("Expected error starting with 'invalid request body:', got %q", errMsg)
	}
}

// TestErrorA2AMalformedJSON verifies JSON-RPC parse error for malformed JSON.
func TestErrorA2AMalformedJSON(t *testing.T) {
	req, err := http.NewRequest("POST", baseURL+"/a2a", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var rpcResp a2a.Response
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		t.Fatalf("Failed to parse response: %v (body: %s)", err, body)
	}

	if rpcResp.Error == nil {
		t.Fatal("Expected JSON-RPC error")
	}
	if rpcResp.Error.Code != -32700 {
		t.Fatalf("Expected error code -32700, got %d", rpcResp.Error.Code)
	}
	if rpcResp.Error.Message != "Parse error" {
		t.Fatalf("Expected 'Parse error', got %q", rpcResp.Error.Message)
	}
}

// TestErrorA2AMethodNotFound verifies JSON-RPC error for unknown method.
func TestErrorA2AMethodNotFound(t *testing.T) {
	rpcResp, err := a2aRPC("nonexistent/method", nil)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if rpcResp.Error == nil {
		t.Fatal("Expected JSON-RPC error")
	}
	if rpcResp.Error.Code != -32601 {
		t.Fatalf("Expected error code -32601, got %d", rpcResp.Error.Code)
	}
	if rpcResp.Error.Message != "Method not found" {
		t.Fatalf("Expected 'Method not found', got %q", rpcResp.Error.Message)
	}
}

// TestErrorA2ATaskNotFound verifies JSON-RPC error for non-existent task.
func TestErrorA2ATaskNotFound(t *testing.T) {
	rpcResp, err := a2aRPC("tasks/get", a2a.TaskGetParams{ID: "nonexistent-task-id"})
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if rpcResp.Error == nil {
		t.Fatal("Expected JSON-RPC error")
	}
	if rpcResp.Error.Code != -32602 {
		t.Fatalf("Expected error code -32602, got %d", rpcResp.Error.Code)
	}
	if !strings.HasPrefix(rpcResp.Error.Message, "Task not found:") {
		t.Fatalf("Expected error starting with 'Task not found:', got %q", rpcResp.Error.Message)
	}
}

// --- Session ID propagation tests ---

// TestSessionIDPropagation verifies session ID is stored and returned in conversations.
func TestSessionIDPropagation(t *testing.T) {
	t.Run("custom_session_id", func(t *testing.T) {
		// Create conversation with custom X-Session-ID header
		req, err := http.NewRequest("POST", baseURL+"/conversations", nil)
		if err != nil {
			t.Fatalf("Failed to create request: %v", err)
		}
		req.Header.Set("X-Session-ID", "test-sid-e2e-1234")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 201 {
			t.Fatalf("Expected status 201, got %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		var result map[string]any
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		conv := result["conversation"].(map[string]any)
		convID := conv["id"].(string)

		// Get the conversation and verify session_id
		getResult, _, err := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", baseURL, convID), nil)
		if err != nil {
			t.Fatalf("Get conversation failed: %v", err)
		}

		getConv := getResult["conversation"].(map[string]any)
		sessionID, ok := getConv["session_id"].(string)
		if !ok || sessionID != "test-sid-e2e-1234" {
			t.Fatalf("Expected session_id 'test-sid-e2e-1234', got %q", sessionID)
		}
	})

	t.Run("auto_generated_session_id", func(t *testing.T) {
		// Create conversation without X-Session-ID header
		result, status, err := httpJSON("POST", baseURL+"/conversations", nil)
		if err != nil {
			t.Fatalf("Create conversation failed: %v", err)
		}
		if status != 201 {
			t.Fatalf("Expected status 201, got %d", status)
		}

		conv := result["conversation"].(map[string]any)
		convID := conv["id"].(string)

		// Get the conversation and verify session_id is auto-generated
		getResult, _, err := httpJSON("GET", fmt.Sprintf("%s/conversations/%s", baseURL, convID), nil)
		if err != nil {
			t.Fatalf("Get conversation failed: %v", err)
		}

		getConv := getResult["conversation"].(map[string]any)
		sessionID, ok := getConv["session_id"].(string)
		if !ok || sessionID == "" {
			t.Fatal("Expected non-empty auto-generated session_id")
		}
		if len(sessionID) != 8 {
			t.Fatalf("Expected 8-char session_id, got %d chars: %q", len(sessionID), sessionID)
		}
	})
}
