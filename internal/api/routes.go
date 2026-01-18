package api

func (s *Server) setupRoutes() {
	// Documentation
	s.app.Get("/docs", handleDocsHTML)
	s.app.Get("/docs/json", handleDocsJSON)

	// Health check
	s.app.Get("/health", s.healthHandler)

	// Tools info
	s.app.Get("/tools", s.toolsHandler)

	// Conversation routes
	s.app.Post("/conversations", s.createConversationHandler)
	s.app.Get("/conversations", s.listConversationsHandler)
	s.app.Get("/conversations/:id", s.getConversationHandler)
	s.app.Post("/conversations/:id/messages", s.sendMessageHandler)

	// Approval routes
	s.app.Post("/approvals/:uuid", s.resolveApprovalHandler)
}
