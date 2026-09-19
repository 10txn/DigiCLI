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
	// Reach reports what this particular call would touch, before it runs,
	// so the mode policy can rule on the call rather than on the tool. The
	// same tool reads inside the working directory on one call and outside
	// it on the next, and those are not the same question.
	Reach(call types.ToolCall) types.Reach
}

// Preview is what a call would do while it is still only a proposal, split by
// how much room it needs. Approving a write means little without seeing it, but
// a whole file pasted into the transcript buries the question it is asking — so
// the prompt shows Summary, and Body waits behind a key for anyone who wants to
// read it.
type Preview struct {
	// Summary is the one line the decision is usually made on: what happens to
	// which file, and how much of it changes.
	Summary string
	// Body is the full proposal — the file as it would be written. Empty for a
	// call with nothing to show beyond its summary.
	Body string
}

// Previewer is a tool that can describe a call before it runs. write_file
// implements this; a tool with nothing to add beyond its name and path does
// not have to.
type Previewer interface {
	Preview(call types.ToolCall) Preview
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
