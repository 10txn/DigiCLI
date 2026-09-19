package types

import (
	"bytes"
	"encoding/json"
	"strings"
)

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

// Reach describes what one specific call would touch, worked out before the
// call runs. The mode policy is applied to this rather than to the tool,
// because the same tool reaches inside the working directory on one call and
// outside it on the next.
type Reach struct {
	// Mutates reports whether running the call changes anything on disk.
	Mutates bool
	// Path is the absolute path the call resolved to, shown in the approval
	// prompt and used as the key for a session grant. Empty when the call
	// names no path, or names one that could not be resolved — a bad path is
	// the tool's error to report, not the policy's.
	Path string
	// Outside reports that Path lies beyond the working directory.
	Outside bool
	// Refusal, when set, is why this call is refused in every mode. It is the
	// floor beneath the policy: neither auto mode nor the user's approval
	// reaches past it.
	Refusal string
}

// StringArg pulls a string field out of a call's arguments.
func (c ToolCall) StringArg(name string) (string, bool) {
	var args map[string]any
	if err := json.Unmarshal(NormalizeArguments(c.Arguments), &args); err != nil {
		return "", false
	}
	value, ok := args[name].(string)
	return value, ok
}

// NormalizeArguments unwraps arguments that arrived as a JSON-encoded string
// rather than as an object. Ollama's native API sends an object, but its
// OpenAI-compatible path — and several builds and models through it — send the
// same thing encoded into a string, the way OpenAI does. Undecoded, every
// argument reads as missing, so write_file answers a perfectly good call with
// "write_file needs a path argument".
//
// Anything that is already an object, or is not a string at all, is returned
// untouched: this only unwraps one layer of quoting that should not be there.
func NormalizeArguments(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return raw
	}

	var unquoted string
	if err := json.Unmarshal(trimmed, &unquoted); err != nil {
		return raw
	}
	inner := json.RawMessage(strings.TrimSpace(unquoted))
	if !json.Valid(inner) {
		return raw
	}
	return inner
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
