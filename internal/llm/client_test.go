package llm

import (
	"testing"

	"agent-stop-and-go/internal/mcp"
)

func TestCoerceToolCallArgs(t *testing.T) {
	tools := []mcp.Tool{
		{
			Name: "resources_add",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"name":  {Type: "string"},
					"value": {Type: "string"},
				},
			},
		},
		{
			Name: "other_tool",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"count": {Type: "number"},
				},
			},
		},
	}

	tests := []struct {
		name     string
		tc       *ToolCall
		wantArgs map[string]any
	}{
		{
			name: "nil tool call",
			tc:   nil,
		},
		{
			name: "float64 to string (IP as 32-bit int)",
			tc: &ToolCall{
				Name:      "resources_add",
				Arguments: map[string]any{"name": "server", "value": float64(3232235876)},
			},
			wantArgs: map[string]any{"name": "server", "value": "3232235876"},
		},
		{
			name: "float64 integer to string",
			tc: &ToolCall{
				Name:      "resources_add",
				Arguments: map[string]any{"name": "test", "value": float64(42)},
			},
			wantArgs: map[string]any{"name": "test", "value": "42"},
		},
		{
			name: "float64 decimal to string",
			tc: &ToolCall{
				Name:      "resources_add",
				Arguments: map[string]any{"name": "test", "value": float64(3.14)},
			},
			wantArgs: map[string]any{"name": "test", "value": "3.14"},
		},
		{
			name: "bool to string",
			tc: &ToolCall{
				Name:      "resources_add",
				Arguments: map[string]any{"name": "test", "value": true},
			},
			wantArgs: map[string]any{"name": "test", "value": "true"},
		},
		{
			name: "string stays string",
			tc: &ToolCall{
				Name:      "resources_add",
				Arguments: map[string]any{"name": "server", "value": "192.168.1.100"},
			},
			wantArgs: map[string]any{"name": "server", "value": "192.168.1.100"},
		},
		{
			name: "number type not coerced",
			tc: &ToolCall{
				Name:      "other_tool",
				Arguments: map[string]any{"count": float64(42)},
			},
			wantArgs: map[string]any{"count": float64(42)},
		},
		{
			name: "unknown tool leaves args unchanged",
			tc: &ToolCall{
				Name:      "unknown_tool",
				Arguments: map[string]any{"value": float64(123)},
			},
			wantArgs: map[string]any{"value": float64(123)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			CoerceToolCallArgs(tt.tc, tools)
			if tt.tc == nil {
				return
			}
			for k, want := range tt.wantArgs {
				got := tt.tc.Arguments[k]
				if got != want {
					t.Errorf("arg %q = %v (%T), want %v (%T)", k, got, got, want, want)
				}
			}
		})
	}
}
