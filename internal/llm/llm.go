// Package llm holds the client integrations for each LLM provider.
package llm

import (
	"context"

	"github.com/10txn/digicli/internal/types"
)

// Provider is the common surface every backend implements, so the TUI never
// needs to know which one is active.
type Provider interface {
	// Name is the provider's key in the config: "ollama", "claude", …
	Name() string
	// Chat sends the conversation and returns a stream of reply chunks.
	// Cancelling ctx aborts the request. Passing no tools asks for a plain
	// reply.
	Chat(ctx context.Context, messages []types.Message, tools []types.ToolDefinition) (Stream, error)
}

// Chunk is one piece of a streamed reply: either text, or the model asking to
// run tools. A model that calls a tool usually emits some text first.
type Chunk struct {
	Text string
	// ToolCalls arrive together in a single frame rather than streamed
	// piecemeal, so a chunk carries all of them.
	ToolCalls []types.ToolCall
}

// Stream yields a reply chunk by chunk as it arrives.
type Stream interface {
	// Recv blocks until the next chunk is ready. It returns io.EOF when the
	// reply is complete.
	Recv() (Chunk, error)
	Close() error
}
