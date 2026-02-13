package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"

	"agent-stop-and-go/internal/a2a"
	"agent-stop-and-go/internal/auth"
	"agent-stop-and-go/internal/conversation"
)

// parseJSON attempts to parse JSON from body regardless of Content-Type.
func parseJSON(c *fiber.Ctx, out any) error {
	body := c.Body()
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// extractContext extracts the Bearer token and session ID from the request
// and returns a context with both stored in it.
func extractContext(c *fiber.Ctx) context.Context {
	ctx := context.Background()
	authHeader := c.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		ctx = auth.WithBearerToken(ctx, token)
	}
	if sid, ok := c.Locals("session_id").(string); ok && sid != "" {
		ctx = auth.WithSessionID(ctx, sid)
	}
	return ctx
}

// healthHandler returns the API health status.
func (s *Server) healthHandler(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status": "ok",
	})
}

// toolsHandler returns the available MCP tools.
func (s *Server) toolsHandler(c *fiber.Ctx) error {
	tools := s.agent.GetTools()
	return c.JSON(fiber.Map{
		"tools": tools,
	})
}

// CreateConversationRequest is the request body for creating a conversation.
type CreateConversationRequest struct {
	Message string `json:"message,omitempty"`
}

// createConversationHandler starts a new conversation.
func (s *Server) createConversationHandler(c *fiber.Ctx) error {
	ctx := extractContext(c)
	conv, err := s.agent.StartConversation(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	var req CreateConversationRequest
	if err := parseJSON(c, &req); err == nil && req.Message != "" {
		result, err := s.agent.ProcessMessage(ctx, conv, req.Message)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		conv, _ = s.agent.GetConversation(conv.ID)

		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"conversation": conv,
			"result":       result,
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"conversation": conv,
	})
}

// listConversationsHandler returns all conversations.
func (s *Server) listConversationsHandler(c *fiber.Ctx) error {
	conversations, err := s.agent.ListConversations()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	var active, waiting, completed int
	for _, conv := range conversations {
		switch conv.Status {
		case "active":
			active++
		case "waiting_approval":
			waiting++
		case "completed":
			completed++
		}
	}

	return c.JSON(fiber.Map{
		"conversations": conversations,
		"summary": fiber.Map{
			"total":            len(conversations),
			"active":           active,
			"waiting_approval": waiting,
			"completed":        completed,
		},
	})
}

// getConversationHandler returns a specific conversation.
func (s *Server) getConversationHandler(c *fiber.Ctx) error {
	id := c.Params("id")

	conv, err := s.agent.GetConversation(id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"conversation": conv,
	})
}

// SendMessageRequest is the request body for sending a message.
type SendMessageRequest struct {
	Message string `json:"message"`
}

// sendMessageHandler processes a user message in a conversation.
func (s *Server) sendMessageHandler(c *fiber.Ctx) error {
	id := c.Params("id")

	var req SendMessageRequest
	if err := parseJSON(c, &req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body: " + err.Error(),
		})
	}

	if req.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "message is required",
		})
	}

	conv, err := s.agent.GetConversation(id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	ctx := extractContext(c)
	result, err := s.agent.ProcessMessage(ctx, conv, req.Message)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	conv, _ = s.agent.GetConversation(id)

	return c.JSON(fiber.Map{
		"conversation": conv,
		"result":       result,
	})
}

// ResolveApprovalRequest is the request body for resolving an approval.
type ResolveApprovalRequest struct {
	Approved bool   `json:"approved"`
	Action   string `json:"action"` // Alternative: "approve" or "reject"
	Answer   string `json:"answer"` // Alternative: "yes" or "no"
}

// resolveApprovalHandler handles approval responses.
func (s *Server) resolveApprovalHandler(c *fiber.Ctx) error {
	uuid := c.Params("uuid")

	var req ResolveApprovalRequest
	if err := parseJSON(c, &req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body: " + err.Error(),
		})
	}

	// Support "approved" boolean, "action" string, and "answer" string
	approved := req.Approved
	if req.Action != "" {
		action := strings.ToLower(req.Action)
		approved = action == "approve" || action == "approved" || action == "yes"
	}
	if req.Answer != "" {
		answer := strings.ToLower(req.Answer)
		approved = answer == "yes" || answer == "y" || answer == "true" || answer == "approve" || answer == "approved"
	}

	ctx := extractContext(c)
	conv, result, err := s.agent.ResolveApproval(ctx, uuid, approved)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"conversation": conv,
		"result":       result,
	})
}

// agentCardHandler returns the A2A Agent Card at /.well-known/agent.json.
func (s *Server) agentCardHandler(c *fiber.Ctx) error {
	tools := s.agent.GetTools()
	skills := make([]a2a.Skill, 0, len(tools))
	for _, t := range tools {
		skills = append(skills, a2a.Skill{
			ID:          t.Name,
			Name:        t.Name,
			Description: t.Description,
		})
	}

	card := a2a.AgentCard{
		Name:        s.config.Name,
		Description: s.config.Description,
		URL:         fmt.Sprintf("http://%s:%d", s.config.Host, s.config.Port),
		Skills:      skills,
	}

	return c.JSON(card)
}

// a2aHandler dispatches JSON-RPC 2.0 requests for the A2A server.
func (s *Server) a2aHandler(c *fiber.Ctx) error {
	var req a2a.Request
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			Error: &a2a.RPCError{
				Code:    -32700,
				Message: "Parse error",
			},
		})
	}

	switch req.Method {
	case "message/send":
		return s.a2aMessageSend(c, req)
	case "tasks/get":
		return s.a2aTasksGet(c, req)
	default:
		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &a2a.RPCError{
				Code:    -32601,
				Message: "Method not found",
			},
		})
	}
}

// a2aMessageSend handles the message/send JSON-RPC method.
func (s *Server) a2aMessageSend(c *fiber.Ctx, req a2a.Request) error {
	paramsBytes, err := json.Marshal(req.Params)
	if err != nil {
		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &a2a.RPCError{Code: -32602, Message: "Invalid params"},
		})
	}
	var params a2a.MessageSendParams
	if err := json.Unmarshal(paramsBytes, &params); err != nil {
		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &a2a.RPCError{Code: -32602, Message: "Invalid params"},
		})
	}

	// Extract text from message parts
	var text string
	for _, p := range params.Message.Parts {
		if p.Type == "text" && p.Text != "" {
			text = p.Text
			break
		}
	}
	if text == "" {
		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &a2a.RPCError{Code: -32602, Message: "No text part in message"},
		})
	}

	ctx := extractContext(c)

	// If taskId is provided, this is a continuation of an existing task
	if params.TaskID != "" {
		conv, err := s.agent.GetConversation(params.TaskID)
		if err != nil {
			return c.JSON(a2a.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &a2a.RPCError{Code: -32602, Message: "Task not found: " + params.TaskID},
			})
		}

		// If conversation is waiting for approval, interpret message as approval/rejection
		if conv.Status == conversation.StatusWaitingApproval && conv.PendingApproval != nil {
			approvalUUID := conv.PendingApproval.UUID
			approved := isApprovalMessage(text)

			conv, result, err := s.agent.ResolveApproval(ctx, approvalUUID, approved)
			if err != nil {
				return c.JSON(a2a.Response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &a2a.RPCError{Code: -32603, Message: err.Error()},
				})
			}

			task := conversationToTask(conv, result.Response)
			taskBytes, _ := json.Marshal(task)

			return c.JSON(a2a.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(taskBytes),
			})
		}

		// Active conversation: process as a new message
		result, err := s.agent.ProcessMessage(ctx, conv, text)
		if err != nil {
			return c.JSON(a2a.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &a2a.RPCError{Code: -32603, Message: err.Error()},
			})
		}

		conv, _ = s.agent.GetConversation(conv.ID)
		task := conversationToTask(conv, result.Response)
		taskBytes, _ := json.Marshal(task)

		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(taskBytes),
		})
	}

	// No taskId: create a new conversation
	conv, err := s.agent.StartConversation(ctx)
	if err != nil {
		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &a2a.RPCError{Code: -32603, Message: err.Error()},
		})
	}

	result, err := s.agent.ProcessMessage(ctx, conv, text)
	if err != nil {
		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &a2a.RPCError{Code: -32603, Message: err.Error()},
		})
	}

	// Reload conversation to get updated state
	conv, _ = s.agent.GetConversation(conv.ID)

	task := conversationToTask(conv, result.Response)
	taskBytes, _ := json.Marshal(task)

	return c.JSON(a2a.Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  json.RawMessage(taskBytes),
	})
}

// isApprovalMessage checks if a message text represents an approval.
func isApprovalMessage(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch lower {
	case "yes", "y", "true", "approve", "approved", "ok", "confirm":
		return true
	default:
		return false
	}
}

// a2aTasksGet handles the tasks/get JSON-RPC method.
func (s *Server) a2aTasksGet(c *fiber.Ctx, req a2a.Request) error {
	paramsBytes, err := json.Marshal(req.Params)
	if err != nil {
		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &a2a.RPCError{Code: -32602, Message: "Invalid params"},
		})
	}
	var params a2a.TaskGetParams
	if err := json.Unmarshal(paramsBytes, &params); err != nil {
		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &a2a.RPCError{Code: -32602, Message: "Invalid params"},
		})
	}

	conv, err := s.agent.GetConversation(params.ID)
	if err != nil {
		return c.JSON(a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &a2a.RPCError{Code: -32602, Message: "Task not found: " + params.ID},
		})
	}

	task := conversationToTask(conv, "")
	taskBytes, _ := json.Marshal(task)

	return c.JSON(a2a.Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  json.RawMessage(taskBytes),
	})
}

// conversationToTask maps a conversation to an A2A Task.
func conversationToTask(conv *conversation.Conversation, responseText string) a2a.Task {
	task := a2a.Task{
		ID: conv.ID,
	}

	switch conv.Status {
	case conversation.StatusWaitingApproval:
		msg := "Waiting for approval"
		if conv.PendingApproval != nil {
			msg = fmt.Sprintf("Approval required (id: %s): %s", conv.PendingApproval.UUID, conv.PendingApproval.Description)
		}
		task.Status = a2a.TaskStatus{State: "input-required", Message: &msg}
	default:
		task.Status = a2a.TaskStatus{State: "completed"}
	}

	// Build artifact from the response or last assistant message
	artifactText := responseText
	if artifactText == "" {
		for i := len(conv.Messages) - 1; i >= 0; i-- {
			if conv.Messages[i].Role == conversation.RoleAssistant && conv.Messages[i].Content != "" {
				artifactText = conv.Messages[i].Content
				break
			}
		}
	}

	if artifactText != "" {
		task.Artifact = &a2a.Artifact{
			Parts: []a2a.Part{{Type: "text", Text: artifactText}},
		}
	}

	return task
}
