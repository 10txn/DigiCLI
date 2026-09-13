// Package tool defines the tools a model may call and the registry that
// dispatches to them.
package tool

import (
	"context"
	"fmt"
	"sort"

	"github.com/10txn/digicli/internal/types"
)

// Tool is one callable capability.
type Tool interface {
	// Definition is what the model is told about this tool.
	Definition() types.ToolDefinition
	// Run executes the call and returns output for the model. A returned
	// error is shown to the model too — it is usually recoverable, and the
	// model should get the chance to correct itself.
	Run(ctx context.Context, call types.ToolCall) (string, error)
	// Mutates reports whether the tool changes anything outside DigiCLI.
	// Read-only tools run in every mode; mutating ones are gated.
	Mutates() bool
}

// Registry holds the tools available in a session.
type Registry struct {
	tools map[string]Tool
}

func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		r.Add(t)
	}
	return r
}

func (r *Registry) Add(t Tool) {
	r.tools[t.Definition().Name] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Names lists the registered tools, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Definitions returns what the model should be told, in a stable order so the
// prompt does not change between turns.
func (r *Registry) Definitions() []types.ToolDefinition {
	defs := make([]types.ToolDefinition, 0, len(r.tools))
	for _, name := range r.Names() {
		defs = append(defs, r.tools[name].Definition())
	}
	return defs
}

func (r *Registry) Len() int { return len(r.tools) }

// Run dispatches a call. An unknown tool is reported back to the model rather
// than failing the turn, since models do occasionally invent tool names.
func (r *Registry) Run(ctx context.Context, call types.ToolCall) (string, error) {
	t, ok := r.tools[call.Name]
	if !ok {
		return "", fmt.Errorf("no tool named %q is available; the tools you have are: %v",
			call.Name, r.Names())
	}
	return t.Run(ctx, call)
}
