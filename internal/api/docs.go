package api

import (
	"github.com/gofiber/fiber/v2"
)

// APISpec represents the API specification.
type APISpec struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Version     string     `json:"version"`
	Endpoints   []Endpoint `json:"endpoints"`
}

// Endpoint represents an API endpoint specification.
type Endpoint struct {
	Method      string              `json:"method"`
	Path        string              `json:"path"`
	Summary     string              `json:"summary"`
	Description string              `json:"description"`
	Request     *RequestSpec        `json:"request,omitempty"`
	Responses   map[string]Response `json:"responses"`
}

// RequestSpec represents request body specification.
type RequestSpec struct {
	ContentType string           `json:"content_type"`
	Schema      map[string]Field `json:"schema"`
	Example     any              `json:"example,omitempty"`
}

// Response represents a response specification.
type Response struct {
	Description string `json:"description"`
	Example     any    `json:"example,omitempty"`
}

// Field represents a schema field.
type Field struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// getAPISpec returns the API specification.
func getAPISpec() APISpec {
	return APISpec{
		Title:       "Agent Stop and Go API",
		Description: "API for async autonomous agents with approval workflows. Agents can pause execution and wait for external approval before proceeding with sensitive actions.",
		Version:     "1.0.0",
		Endpoints: []Endpoint{
			{
				Method:      "GET",
				Path:        "/health",
				Summary:     "Health Check",
				Description: "Returns the health status of the API server.",
				Responses: map[string]Response{
					"200": {
						Description: "Server is healthy",
						Example:     map[string]string{"status": "ok"},
					},
				},
			},
			{
				Method:      "POST",
				Path:        "/conversations",
				Summary:     "Start Conversation",
				Description: "Creates a new conversation with the agent. Optionally send an initial message.",
				Request: &RequestSpec{
					ContentType: "application/json",
					Schema: map[string]Field{
						"message": {Type: "string", Description: "Optional initial message to send", Required: false},
					},
					Example: map[string]string{"message": "Hello, I need help with deployment"},
				},
				Responses: map[string]Response{
					"201": {
						Description: "Conversation created successfully",
						Example: map[string]any{
							"conversation": map[string]any{
								"id":       "uuid",
								"status":   "active",
								"messages": []any{},
							},
						},
					},
				},
			},
			{
				Method:      "GET",
				Path:        "/conversations",
				Summary:     "List Conversations",
				Description: "Returns all conversations with a summary of statuses.",
				Responses: map[string]Response{
					"200": {
						Description: "List of conversations",
						Example: map[string]any{
							"conversations": []any{},
							"summary": map[string]int{
								"total":            2,
								"active":           1,
								"waiting_approval": 1,
								"completed":        0,
							},
						},
					},
				},
			},
			{
				Method:      "GET",
				Path:        "/conversations/:id",
				Summary:     "Get Conversation",
				Description: "Returns a specific conversation by ID with all messages and pending approval status.",
				Responses: map[string]Response{
					"200": {
						Description: "Conversation details",
						Example: map[string]any{
							"conversation": map[string]any{
								"id":               "uuid",
								"status":           "active|waiting_approval|completed",
								"messages":         []any{},
								"pending_approval": nil,
							},
						},
					},
					"404": {
						Description: "Conversation not found",
						Example:     map[string]string{"error": "conversation not found"},
					},
				},
			},
			{
				Method:      "POST",
				Path:        "/conversations/:id/messages",
				Summary:     "Send Message",
				Description: "Sends a message to an existing conversation. If the agent needs approval, it will return a pending_approval object with a UUID. While waiting for approval, no new messages can be processed.",
				Request: &RequestSpec{
					ContentType: "application/json",
					Schema: map[string]Field{
						"message": {Type: "string", Description: "The message to send to the agent", Required: true},
					},
					Example: map[string]string{"message": "Please scale the web deployment to 5 replicas"},
				},
				Responses: map[string]Response{
					"200": {
						Description: "Message processed (may require approval)",
						Example: map[string]any{
							"conversation": map[string]any{},
							"result": map[string]any{
								"response":         "Agent response text",
								"waiting_approval": false,
								"approval":         nil,
							},
						},
					},
					"200 (approval needed)": {
						Description: "Action requires approval",
						Example: map[string]any{
							"conversation": map[string]any{
								"status": "waiting_approval",
								"pending_approval": map[string]any{
									"uuid":     "approval-uuid",
									"question": "Do you approve this action?",
								},
							},
							"result": map[string]any{
								"response":         "[APPROVAL_NEEDED]: ...",
								"waiting_approval": true,
								"approval": map[string]any{
									"uuid":     "approval-uuid",
									"question": "Do you approve this action?",
								},
							},
						},
					},
					"404": {
						Description: "Conversation not found",
						Example:     map[string]string{"error": "conversation not found"},
					},
				},
			},
			{
				Method:      "GET",
				Path:        "/.well-known/agent.json",
				Summary:     "Agent Card (A2A)",
				Description: "Returns the A2A Agent Card describing the agent's identity and skills. Used by other A2A agents for discovery.",
				Responses: map[string]Response{
					"200": {
						Description: "Agent card with skills",
						Example: map[string]any{
							"name":        "resource-manager",
							"description": "A resource management agent",
							"url":         "http://0.0.0.0:8080",
							"skills": []map[string]string{
								{"id": "resources_list", "name": "resources_list", "description": "List resources"},
							},
						},
					},
				},
			},
			{
				Method:      "POST",
				Path:        "/a2a",
				Summary:     "A2A JSON-RPC Endpoint",
				Description: "JSON-RPC 2.0 endpoint for A2A protocol. Supports methods: message/send (send a message and get a task result, or continue an existing task with taskId), and tasks/get (retrieve a task by ID).",
				Request: &RequestSpec{
					ContentType: "application/json",
					Schema: map[string]Field{
						"jsonrpc": {Type: "string", Description: "Must be \"2.0\"", Required: true},
						"id":      {Type: "integer", Description: "Request ID", Required: true},
						"method":  {Type: "string", Description: "\"message/send\" or \"tasks/get\"", Required: true},
						"params":  {Type: "object", Description: "Method-specific parameters", Required: true},
					},
					Example: map[string]any{
						"jsonrpc": "2.0",
						"id":      1,
						"method":  "message/send",
						"params": map[string]any{
							"message": map[string]any{
								"role": "user",
								"parts": []map[string]string{
									{"type": "text", "text": "list resources"},
								},
							},
						},
					},
				},
				Responses: map[string]Response{
					"200": {
						Description: "JSON-RPC response with task result",
						Example: map[string]any{
							"jsonrpc": "2.0",
							"id":      1,
							"result": map[string]any{
								"id":     "task-uuid",
								"status": map[string]string{"state": "completed"},
								"artifact": map[string]any{
									"parts": []map[string]string{
										{"type": "text", "text": "Operation result..."},
									},
								},
							},
						},
					},
				},
			},
			{
				Method:      "POST",
				Path:        "/approvals/:uuid",
				Summary:     "Resolve Approval",
				Description: "Provides an answer to a pending approval request. The UUID is obtained from the pending_approval object when the agent requests approval.",
				Request: &RequestSpec{
					ContentType: "application/json",
					Schema: map[string]Field{
						"answer": {Type: "string", Description: "Your response to the approval request (e.g., 'yes', 'no', 'approved')", Required: true},
					},
					Example: map[string]string{"answer": "yes, proceed with the deployment"},
				},
				Responses: map[string]Response{
					"200": {
						Description: "Approval resolved, conversation resumed",
						Example: map[string]any{
							"conversation": map[string]any{
								"status": "active",
							},
							"result": map[string]any{
								"response":         "Approval received. Proceeding with the requested action.",
								"waiting_approval": false,
							},
						},
					},
					"404": {
						Description: "Approval UUID not found",
						Example:     map[string]string{"error": "approval not found"},
					},
				},
			},
		},
	}
}

// handleDocsJSON returns the API specification as JSON.
func handleDocsJSON(c *fiber.Ctx) error {
	return c.JSON(getAPISpec())
}

// handleDocsHTML returns an HTML documentation page.
func handleDocsHTML(c *fiber.Ctx) error {
	spec := getAPISpec()

	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>` + spec.Title + ` - API Documentation</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif; background: #1a1a2e; color: #eee; line-height: 1.6; }
        .container { max-width: 1200px; margin: 0 auto; padding: 20px; }
        header { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); padding: 40px 20px; margin-bottom: 30px; border-radius: 10px; }
        h1 { font-size: 2.5em; margin-bottom: 10px; }
        .version { background: rgba(255,255,255,0.2); padding: 4px 12px; border-radius: 20px; font-size: 0.9em; display: inline-block; }
        .description { margin-top: 15px; opacity: 0.9; max-width: 800px; }
        .endpoint { background: #252540; border-radius: 10px; margin-bottom: 20px; overflow: hidden; border: 1px solid #3a3a5c; }
        .endpoint-header { padding: 15px 20px; cursor: pointer; display: flex; align-items: center; gap: 15px; }
        .endpoint-header:hover { background: #2a2a4a; }
        .method { padding: 6px 12px; border-radius: 5px; font-weight: bold; font-size: 0.85em; min-width: 70px; text-align: center; }
        .method.GET { background: #61affe; color: #fff; }
        .method.POST { background: #49cc90; color: #fff; }
        .method.PUT { background: #fca130; color: #fff; }
        .method.DELETE { background: #f93e3e; color: #fff; }
        .path { font-family: 'Monaco', 'Menlo', monospace; font-size: 1.1em; color: #fff; }
        .summary { color: #aaa; margin-left: auto; }
        .endpoint-body { padding: 20px; border-top: 1px solid #3a3a5c; display: none; }
        .endpoint.open .endpoint-body { display: block; }
        .section { margin-bottom: 20px; }
        .section-title { color: #667eea; font-size: 0.9em; text-transform: uppercase; margin-bottom: 10px; font-weight: 600; }
        .schema-table { width: 100%; border-collapse: collapse; }
        .schema-table th, .schema-table td { padding: 10px; text-align: left; border-bottom: 1px solid #3a3a5c; }
        .schema-table th { color: #888; font-weight: normal; font-size: 0.85em; }
        .field-name { font-family: monospace; color: #49cc90; }
        .field-type { color: #61affe; font-size: 0.9em; }
        .required { color: #f93e3e; font-size: 0.8em; }
        pre { background: #1e1e3f; padding: 15px; border-radius: 8px; overflow-x: auto; font-size: 0.9em; }
        code { font-family: 'Monaco', 'Menlo', monospace; }
        .response-code { display: inline-block; padding: 4px 10px; border-radius: 4px; font-weight: bold; margin-right: 10px; font-size: 0.9em; }
        .response-code.success { background: #49cc90; color: #fff; }
        .response-code.error { background: #f93e3e; color: #fff; }
        .response-item { margin-bottom: 15px; }
        .response-desc { color: #aaa; margin-bottom: 8px; }
        .status-badge { display: inline-block; padding: 3px 8px; border-radius: 4px; font-size: 0.8em; margin-left: 8px; }
        .status-active { background: #49cc90; }
        .status-waiting { background: #fca130; }
        .status-completed { background: #61affe; }
        .workflow { background: #1e1e3f; padding: 20px; border-radius: 10px; margin-top: 30px; }
        .workflow h2 { color: #667eea; margin-bottom: 15px; }
        .workflow ol { padding-left: 20px; }
        .workflow li { margin-bottom: 10px; }
        .arrow { color: #667eea; margin: 0 5px; }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>` + spec.Title + `</h1>
            <span class="version">v` + spec.Version + `</span>
            <p class="description">` + spec.Description + `</p>
        </header>

        <div class="workflow">
            <h2>Workflow</h2>
            <ol>
                <li><strong>Start a conversation</strong> <span class="arrow">→</span> POST /conversations</li>
                <li><strong>Send messages</strong> <span class="arrow">→</span> POST /conversations/:id/messages</li>
                <li><strong>If approval needed</strong> <span class="arrow">→</span> Status becomes <span class="status-badge status-waiting">waiting_approval</span></li>
                <li><strong>Resolve approval</strong> <span class="arrow">→</span> POST /approvals/:uuid</li>
                <li><strong>Continue conversation</strong> <span class="arrow">→</span> Status returns to <span class="status-badge status-active">active</span></li>
            </ol>
        </div>

        <h2 style="margin: 30px 0 20px; color: #667eea;">Endpoints</h2>
`

	for _, ep := range spec.Endpoints {
		html += `
        <div class="endpoint" onclick="this.classList.toggle('open')">
            <div class="endpoint-header">
                <span class="method ` + ep.Method + `">` + ep.Method + `</span>
                <span class="path">` + ep.Path + `</span>
                <span class="summary">` + ep.Summary + `</span>
            </div>
            <div class="endpoint-body">
                <p style="margin-bottom: 20px; color: #aaa;">` + ep.Description + `</p>
`
		if ep.Request != nil {
			html += `
                <div class="section">
                    <div class="section-title">Request Body</div>
                    <table class="schema-table">
                        <tr><th>Field</th><th>Type</th><th>Description</th></tr>
`
			for name, field := range ep.Request.Schema {
				required := ""
				if field.Required {
					required = `<span class="required">required</span>`
				}
				html += `<tr><td><span class="field-name">` + name + `</span> ` + required + `</td><td><span class="field-type">` + field.Type + `</span></td><td>` + field.Description + `</td></tr>`
			}
			html += `
                    </table>
                </div>
`
		}

		html += `
                <div class="section">
                    <div class="section-title">Responses</div>
`
		for code, resp := range ep.Responses {
			codeClass := "success"
			if code[0] == '4' || code[0] == '5' {
				codeClass = "error"
			}
			html += `
                    <div class="response-item">
                        <span class="response-code ` + codeClass + `">` + code + `</span>
                        <span class="response-desc">` + resp.Description + `</span>
                    </div>
`
		}
		html += `
                </div>
            </div>
        </div>
`
	}

	html += `
    </div>
</body>
</html>`

	c.Set("Content-Type", "text/html")
	return c.SendString(html)
}
