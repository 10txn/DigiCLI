package tui

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/10txn/digicli/internal/agent"
	"github.com/10txn/digicli/internal/file"
	"github.com/10txn/digicli/internal/tool"
	"github.com/10txn/digicli/internal/types"
	tea "github.com/charmbracelet/bubbletea"
)

// maxToolRounds caps how many times a single user turn may bounce between the
// model and its tools. Without it, a model that keeps re-reading the same file
// would loop until the context window filled.
const maxToolRounds = 12

// toolsDoneMsg carries the results of a round of tool calls.
type toolsDoneMsg struct {
	seq     int
	results []types.Message
}

// buildTools assembles the registry for a session, confined to the directory
// DigiCLI was started in. A sandbox that cannot be built is not fatal: the
// session continues without tools rather than refusing to start.
func buildTools(dir string) (*tool.Registry, error) {
	sandbox, err := file.NewSandbox(dir)
	if err != nil {
		return tool.NewRegistry(), err
	}
	return tool.NewRegistry(
		tool.ReadFile{Sandbox: sandbox},
		tool.ListFiles{Sandbox: sandbox},
	), nil
}

// runTools executes a round of calls off the UI thread.
func (m *Model) runTools(calls []types.ToolCall, seq int) tea.Cmd {
	registry := m.tools
	mode := m.cfg.Mode

	return func() tea.Msg {
		results := make([]types.Message, 0, len(calls))
		for _, call := range calls {
			results = append(results, runOne(registry, mode, call))
		}
		return toolsDoneMsg{seq: seq, results: results}
	}
}

// runOne executes a single call, applying the mode policy first.
func runOne(registry *tool.Registry, mode types.Mode, call types.ToolCall) types.Message {
	t, known := registry.Get(call.Name)

	// An unknown tool still goes back to the model as a result, so it can
	// correct itself rather than the turn dying.
	if !known {
		log.Printf("tool: unknown %q", call.Name)
		return toolResult(call, fmt.Sprintf("No tool named %q exists. Available: %s.",
			call.Name, strings.Join(registry.Names(), ", ")),
			errorStyle.Render("⚙ unknown tool "+call.Name))
	}

	switch agent.Permit(mode, t.Mutates()) {
	case agent.Deny:
		log.Printf("tool: denied %q in %s mode", call.Name, mode)
		return toolResult(call, agent.DenialReason(mode, call.Name),
			errorStyle.Render(fmt.Sprintf("⚙ %s denied — %s mode is read-only", call.Name, mode)))
	case agent.Ask:
		// The approval prompt lands with the first mutating tool; until
		// then nothing reaches here, since every tool is read-only.
		log.Printf("tool: %q needs approval, which is not built yet", call.Name)
		return toolResult(call, "Refused: this action needs the user's approval, "+
			"which is not available yet. Describe what you would do instead.",
			errorStyle.Render("⚙ "+call.Name+" needs approval (not yet implemented)"))
	}

	log.Printf("tool: running %s %s", call.Name, call.Arguments)
	output, err := t.Run(context.Background(), call)
	if err != nil {
		// Tool errors are the model's problem to solve — a bad path is
		// usually recoverable — so they go back as results, not failures.
		return toolResult(call, "Error: "+err.Error(),
			errorStyle.Render("⚙ "+summarise(call)+" — "+err.Error()))
	}
	return toolResult(call, output, toolSummary(call, output))
}

// toolResult builds the message that carries a tool's output back to the model
// and its one-line summary to the user.
func toolResult(call types.ToolCall, content, display string) types.Message {
	msg := types.NewMessage(types.RoleTool, content)
	msg.ToolName = call.Name
	msg.ToolCallID = call.ID
	msg.Display = display
	return msg
}

// summarise renders a call the way it would be typed, for the transcript.
func summarise(call types.ToolCall) string {
	if path, ok := call.StringArg("path"); ok && path != "" {
		return call.Name + " " + path
	}
	return call.Name
}

// toolSummary is the line the user sees: what ran, and how much came back.
func toolSummary(call types.ToolCall, output string) string {
	lines := strings.Count(strings.TrimRight(output, "\n"), "\n") + 1
	unit := "lines"
	if lines == 1 {
		unit = "line"
	}
	return fmt.Sprintf("⚙ %s → %d %s", summarise(call), lines, unit)
}

// handleToolsDone records the results and lets the model continue with them.
func (m *Model) handleToolsDone(msg toolsDoneMsg) tea.Cmd {
	if msg.seq != m.streamSeq {
		log.Printf("tools: discarding results from superseded stream %d", msg.seq)
		return nil
	}
	m.tooling = false
	for _, result := range msg.results {
		m.append(result)
	}

	if m.toolRounds >= maxToolRounds {
		m.busy = false
		m.addSystem(errorStyle.Render(fmt.Sprintf(
			"Stopped after %d rounds of tool calls — the model appears to be looping.",
			maxToolRounds)))
		return nil
	}
	m.toolRounds++

	// Continue the same turn: the model now gets to see what the tools said.
	return m.startStream()
}
