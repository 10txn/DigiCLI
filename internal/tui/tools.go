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

type (
	// toolRanMsg carries one finished call back to the UI thread.
	toolRanMsg struct {
		seq    int
		call   types.ToolCall
		result types.Message
	}
	// toolsDoneMsg says the round is over and the model may continue. The
	// results are already in the history: they are appended as each call
	// finishes, so a round that stops halfway to ask the user has its earlier
	// results on screen above the question.
	toolsDoneMsg struct {
		seq int
	}
)

// toolRun is a round of tool calls being worked through. Calls are taken one
// at a time rather than run all at once, because a call that needs approval
// has to stop the round: the answer decides what the model is told, and the
// calls queued behind it may well depend on what this one did.
type toolRun struct {
	seq     int
	pending []types.ToolCall
	// start is where in the history this round began, so an interrupt can
	// take back what it had already added.
	start int
	// inferred marks a round recovered from a reply's prose rather than from a
	// call the model emitted or delimited. Such a round may be a misreading of
	// a code block, so anything it would change on disk is put to the user.
	inferred bool
}

// buildTools assembles the registry for a session, confined to the directory
// DigiCLI was started in. A sandbox that cannot be built is not fatal: the
// session continues without tools rather than refusing to start.
func buildTools(dir string) (*tool.Registry, *file.Sandbox, error) {
	sandbox, err := file.NewSandbox(dir)
	if err != nil {
		return tool.NewRegistry(), nil, err
	}
	return tool.NewRegistry(
		tool.ReadFile{Sandbox: sandbox},
		tool.ListFiles{Sandbox: sandbox},
		tool.WriteFile{Sandbox: sandbox},
	), sandbox, nil
}

// runTools starts a round of calls.
func (m *Model) runTools(calls []types.ToolCall, seq int) tea.Cmd {
	m.run = &toolRun{seq: seq, pending: calls, start: len(m.messages), inferred: m.inferredCalls}
	m.inferredCalls = false
	return m.nextTool()
}

// nextTool works down the round, settling everything that can be settled
// without the user. It returns when there is something to wait for: a call
// running off the UI thread, a question for the user, or the end of the round.
func (m *Model) nextTool() tea.Cmd {
	for m.run != nil {
		if len(m.run.pending) == 0 {
			seq := m.run.seq
			m.run = nil
			return func() tea.Msg { return toolsDoneMsg{seq: seq} }
		}

		call := m.run.pending[0]
		m.run.pending = m.run.pending[1:]

		// An unknown tool still goes back to the model as a result, so it can
		// correct itself rather than the turn dying.
		t, known := m.tools.Get(call.Name)
		if !known {
			log.Printf("tool: unknown %q", call.Name)
			m.append(toolResult(call, fmt.Sprintf("No tool named %q exists. Available: %s.",
				call.Name, strings.Join(m.tools.Names(), ", ")),
				errorStyle.Render("⚙ unknown tool "+call.Name)))
			continue
		}

		reach := t.Reach(call)
		decision := agent.Permit(m.cfg.Mode, reach)
		// A call read out of prose gets the benefit of the doubt for a read,
		// which is recoverable, and none of it for a write.
		if decision == agent.Allow && m.run.inferred && reach.Mutates {
			decision = agent.Ask
		}
		switch decision {
		case agent.Deny:
			log.Printf("tool: denied %s in %s mode (outside=%v refusal=%q)",
				call.Name, m.cfg.Mode, reach.Outside, reach.Refusal)
			m.append(toolResult(call,
				agent.DenialReason(m.cfg.Mode, call.Name, reach),
				errorStyle.Render("⚙ "+denialSummary(m.cfg.Mode, call, reach))))
			continue
		case agent.Ask:
			m.ask(t, call, reach)
			return nil
		}
		return m.execute(call, reach)
	}
	return nil
}

// execute runs one call off the UI thread.
//
// A call that reaches outside the working directory opens the sandbox for that
// exact path first. Auto mode has decided the question by this point, but the
// sandbox is not told the mode and does not infer anything: it stays shut
// until something opens it, one path at a time, so a mistake in the policy
// above cannot turn into filesystem-wide access on its own.
func (m *Model) execute(call types.ToolCall, reach types.Reach) tea.Cmd {
	if reach.Outside && reach.Path != "" && m.sandbox != nil {
		log.Printf("tool: opening %s for %s", reach.Path, call.Name)
		m.sandbox.Open(reach.Path)
	}

	registry := m.tools
	seq := m.run.seq

	log.Printf("tool: running %s %s", call.Name, call.Arguments)
	return func() tea.Msg {
		output, err := registry.Run(context.Background(), call)
		if err != nil {
			// Tool errors are the model's problem to solve — a bad path is
			// usually recoverable — so they go back as results, not failures.
			return toolRanMsg{seq: seq, call: call, result: toolResult(call,
				"Error: "+err.Error(),
				errorStyle.Render("⚙ "+summarise(call)+" — "+err.Error()))}
		}
		return toolRanMsg{seq: seq, call: call, result: toolResult(call, output,
			toolSummary(call, output))}
	}
}

// handleToolRan records a finished call and moves on to the next one.
func (m *Model) handleToolRan(msg toolRanMsg) tea.Cmd {
	if m.run == nil || msg.seq != m.streamSeq {
		log.Printf("tools: discarding %s from superseded round %d", msg.call.Name, msg.seq)
		return nil
	}
	m.append(msg.result)
	return m.nextTool()
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

// denialSummary is the transcript line for a refusal, which says which of the
// three rules refused it — they are not the same problem, and only one of them
// is about the mode the user could change.
func denialSummary(mode types.Mode, call types.ToolCall, reach types.Reach) string {
	switch {
	case reach.Refusal != "":
		return fmt.Sprintf("%s refused — %s", summarise(call), reach.Refusal)
	case reach.Outside:
		return fmt.Sprintf("%s refused — outside the working directory, and %s mode "+
			"does not leave it", summarise(call), mode)
	default:
		return fmt.Sprintf("%s denied — %s mode is read-only", call.Name, mode)
	}
}

// toolSummary is the line the user sees. A result short enough to read whole
// is shown whole: write_file's "Created main.go with 12 lines." tells the user
// more than a line count would. Anything longer is measured instead.
func toolSummary(call types.ToolCall, output string) string {
	trimmed := strings.TrimRight(output, "\n")
	if !strings.Contains(trimmed, "\n") && len(trimmed) <= 120 {
		return fmt.Sprintf("⚙ %s — %s", summarise(call), trimmed)
	}

	lines := strings.Count(trimmed, "\n") + 1
	unit := "lines"
	if lines == 1 {
		unit = "line"
	}
	return fmt.Sprintf("⚙ %s → %d %s", summarise(call), lines, unit)
}

// handleToolsDone lets the model continue with what the tools said.
func (m *Model) handleToolsDone(msg toolsDoneMsg) tea.Cmd {
	if msg.seq != m.streamSeq {
		log.Printf("tools: discarding the end of superseded round %d", msg.seq)
		return nil
	}
	m.tooling = false

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

// abandonRound drops a round that was interrupted part-way through, taking its
// results back out of the history. Half a round is not a coherent thing to
// send back: the assistant turn asked for calls that now have no answers.
func (m *Model) abandonRound() {
	m.closeViewer()
	if m.run == nil {
		m.approval = nil
		return
	}
	if m.run.start >= 0 && m.run.start <= len(m.messages) {
		m.messages = m.messages[:m.run.start]
	}
	m.run = nil
	m.approval = nil
}
