package api

import (
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// parseJSON attempts to parse JSON from body regardless of Content-Type.
func parseJSON(c *fiber.Ctx, out any) error {
	body := c.Body()
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
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
	conv, err := s.agent.StartConversation()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	var req CreateConversationRequest
	if err := parseJSON(c, &req); err == nil && req.Message != "" {
		result, err := s.agent.ProcessMessage(conv, req.Message)
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

	result, err := s.agent.ProcessMessage(conv, req.Message)
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

	conv, result, err := s.agent.ResolveApproval(uuid, approved)
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
