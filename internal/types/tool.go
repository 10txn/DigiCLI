package types

import "encoding/json"

// ToolDefinition describes a tool to a model. Parameters is a JSON Schema
// object; every provider we support accepts that shape, so the definitions
// never need translating per provider.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCall is a model's request to run a tool.
type ToolCall struct {
	// ID correlates a call with its result. Ollama does not supply one, so
	// it is generated locally; OpenAI and Anthropic do.
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	// Arguments is the raw JSON object the model produced, validated by the
	// tool rather than here.
	Arguments json.RawMessage `json:"arguments"`
}

// StringArg pulls a string field out of a call's arguments.
func (c ToolCall) StringArg(name string) (string, bool) {
	var args map[string]any
	if err := json.Unmarshal(c.Arguments, &args); err != nil {
		return "", false
	}
	value, ok := args[name].(string)
	return value, ok
}

// Schema is a small helper for building a JSON Schema object without writing
// nested map literals at every call site.
func Schema(properties map[string]string, required ...string) map[string]any {
	props := make(map[string]any, len(properties))
	for name, description := range properties {
		props[name] = map[string]any{
			"type":        "string",
			"description": description,
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
