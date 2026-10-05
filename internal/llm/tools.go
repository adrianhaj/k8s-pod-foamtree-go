package llm

import (
	"context"
	"encoding/json"
)

// Tool is one read-only function the model may call. Parameters is a JSON
// Schema object.
type Tool struct {
	Name, Description string
	Parameters        json.RawMessage
}

// Toolbox runs tools against one cluster. A tool error goes back to the
// model as text, so it can correct itself.
type Toolbox interface {
	Tools() []Tool
	Call(ctx context.Context, name string, args json.RawMessage) (string, error)
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func toolDefs(ts []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, map[string]any{"type": "function", "function": map[string]any{
			"name": t.Name, "description": t.Description, "parameters": t.Parameters}})
	}
	return out
}
