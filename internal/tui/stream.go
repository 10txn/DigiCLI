package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/10txn/digicli/internal/agent"
	"github.com/10txn/digicli/internal/llm"
	"github.com/10txn/digicli/internal/types"
	tea "github.com/charmbracelet/bubbletea"
)

// Streaming runs as a chain of commands: start the request, then re-arm a
// receive command for every chunk until the stream ends.
//
// Each message carries the sequence number of the stream that produced it.
// An interrupted stream can still have a chunk in flight, so anything whose
// sequence does not match the current one is dropped rather than appended to
// the wrong reply.
type (
	streamStartedMsg struct {
		seq    int
		stream llm.Stream
	}
	streamChunkMsg struct {
		seq   int
		chunk llm.Chunk
	}
	streamDoneMsg struct {
		seq int
		err error
	}
)

// provider builds a client for the configured backend.
func (m *Model) provider() (llm.Provider, error) {
	switch m.cfg.Provider {
	case "ollama":
		return llm.NewOllama(m.cfg.OllamaEndpoint, m.cfg.Model), nil
	case "claude", "openai":
		return nil, fmt.Errorf("the %s provider is not wired up yet — switch to ollama with /settings", m.cfg.Provider)
	default:
		return nil, fmt.Errorf("unknown provider %q — set one with /settings", m.cfg.Provider)
	}
}

// conversation is the history as the model should see it: the system prompt
// followed by the real exchange. Local notices and the empty placeholder that
// the reply streams into are left out.
func (m *Model) conversation() []types.Message {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}

	// Built from live config every turn, so switching model or mode with
	// /model or tab is reflected in the very next request.
	prompt := agent.SystemPrompt(agent.Session{
		Model:    m.cfg.Model,
		Provider: m.cfg.Provider,
		Mode:     m.cfg.Mode,
		Cwd:      cwd,
		Now:      time.Now(),
		Tools:    m.tools.Names(),
	})

	out := make([]types.Message, 0, len(m.messages)+1)
	out = append(out, types.NewMessage(types.RoleSystem, prompt))
	for _, msg := range m.messages {
		// Local entries are session furniture; the empty one is the
		// placeholder the next reply streams into. An assistant turn with
		// no text but a tool call must be kept — it is half the record of
		// what happened.
		if msg.Local || (msg.Content == "" && len(msg.ToolCalls) == 0) {
			continue
		}
		out = append(out, msg)
	}
	return out
}

// startStream sends the conversation and begins streaming the reply into a
// fresh, empty assistant message.
func (m *Model) startStream() tea.Cmd {
	provider, err := m.provider()
	if err != nil {
		m.showError(err)
		return nil
	}

	// Snapshot the history before adding the placeholder to stream into.
	history := m.conversation()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.streaming = true
	m.busy = true
	m.streamSeq++
	seq := m.streamSeq

	m.append(types.NewMessage(types.RoleAssistant, ""))

	// Traced because a streaming pipeline is near-impossible to debug from
	// the UI alone; the log only opens under DIGICLI_DEBUG.
	tools := m.tools.Definitions()
	log.Printf("stream %d: requesting %d messages, %d tools, from %s",
		seq, len(history), len(tools), provider.Name())
	return func() tea.Msg {
		stream, err := provider.Chat(ctx, history, tools)
		if err != nil {
			log.Printf("stream %d: request failed: %v", seq, err)
			return streamDoneMsg{seq: seq, err: err}
		}
		return streamStartedMsg{seq: seq, stream: stream}
	}
}

// receive waits for the next chunk of a stream.
func receive(stream llm.Stream, seq int) tea.Cmd {
	return func() tea.Msg {
		chunk, err := stream.Recv()
		if err != nil {
			return streamDoneMsg{seq: seq, err: err}
		}
		return streamChunkMsg{seq: seq, chunk: chunk}
	}
}

func (m *Model) handleStreamStarted(msg streamStartedMsg) tea.Cmd {
	if msg.seq != m.streamSeq {
		// Interrupted before the request completed; drop the stream.
		log.Printf("stream %d: superseded by %d before it started", msg.seq, m.streamSeq)
		msg.stream.Close()
		return nil
	}
	log.Printf("stream %d: receiving", msg.seq)
	m.stream = msg.stream
	return receive(msg.stream, msg.seq)
}

func (m *Model) handleStreamChunk(msg streamChunkMsg) tea.Cmd {
	if msg.seq != m.streamSeq || !m.streaming {
		// A chunk from an interrupted stream.
		log.Printf("stream %d: dropped a late chunk (current %d)", msg.seq, m.streamSeq)
		return nil
	}
	if msg.chunk.Text != "" {
		m.appendToReply(msg.chunk.Text)
	}
	// Tool calls are collected and acted on when the turn ends, because a
	// model may emit text and then a call in the same turn.
	if len(msg.chunk.ToolCalls) > 0 {
		m.pendingCalls = append(m.pendingCalls, msg.chunk.ToolCalls...)
	}
	return receive(m.stream, msg.seq)
}

func (m *Model) handleStreamDone(msg streamDoneMsg) tea.Cmd {
	log.Printf("stream %d: finished (%v)", msg.seq, msg.err)
	if msg.seq != m.streamSeq {
		return nil
	}

	calls := m.pendingCalls
	m.pendingCalls = nil
	m.finishStream()

	// io.EOF is the normal end of a reply, and a cancelled context is the
	// user pressing ctrl+c — neither is worth reporting.
	switch {
	case msg.err == nil, errors.Is(msg.err, io.EOF):
	case errors.Is(msg.err, context.Canceled):
		return nil // interrupted; do not run the tools it asked for
	default:
		m.dropEmptyReply()
		m.showError(msg.err)
		return nil
	}

	// A reply that emitted no call of its own may still have written one out
	// as text, which is how a write turns into a wall of JSON in the
	// transcript and no file on disk. Recovered calls are proposals like any
	// other: they go through the policy and the approval prompt below.
	if len(calls) == 0 {
		calls = m.recoverToolCalls()
	}

	if len(calls) == 0 {
		m.dropEmptyReply()
		return nil
	}

	// Record what the model asked for, then run it. The turn is not over:
	// the results go back and the model continues.
	m.attachToolCalls(calls)
	m.busy = true
	m.tooling = true
	m.refresh()
	return m.runTools(calls, msg.seq)
}

// recoverToolCalls rescues a call the model wrote into its reply as text. The
// call is taken out of the visible reply, so the user sees the tool line — or
// the approval prompt — rather than the raw JSON the model meant to be a call,
// and so the reply that goes back into the history reads as the model asking
// for a tool rather than talking about one.
func (m *Model) recoverToolCalls() []types.ToolCall {
	if len(m.messages) == 0 {
		return nil
	}
	last := &m.messages[len(m.messages)-1]
	if last.Role != types.RoleAssistant || last.Content == "" {
		return nil
	}

	recovered, ok := agent.Recover(last.Content, m.tools.Names())
	if !ok {
		return nil
	}

	log.Printf("stream: recovered %d tool call(s) written as text (delimited=%v)",
		len(recovered.Calls), recovered.Delimited)
	last.Content = recovered.Text
	// An inferred recovery is a judgement about prose, so a change to disk is
	// put to the user even in auto mode: being wrong there is a file replaced
	// on the strength of a fenced code block.
	m.inferredCalls = !recovered.Delimited
	m.refresh()
	return recovered.Calls
}

// attachToolCalls records the calls on the assistant turn that requested them,
// which the model needs to see alongside the results.
func (m *Model) attachToolCalls(calls []types.ToolCall) {
	if len(m.messages) == 0 {
		return
	}
	last := &m.messages[len(m.messages)-1]
	if last.Role != types.RoleAssistant {
		return
	}
	last.ToolCalls = calls
}

// detachToolCalls takes the calls back off the assistant turn that requested
// them, for a round that was interrupted before its results arrived.
func (m *Model) detachToolCalls() {
	if len(m.messages) == 0 {
		return
	}
	last := &m.messages[len(m.messages)-1]
	if last.Role != types.RoleAssistant {
		return
	}
	last.ToolCalls = nil
}

// appendToReply adds text to the assistant message currently being streamed.
func (m *Model) appendToReply(text string) {
	if len(m.messages) == 0 {
		return
	}
	last := &m.messages[len(m.messages)-1]
	if last.Role != types.RoleAssistant {
		return
	}
	last.Content += text
	m.refresh()
}

// finishStream tears down the current stream and clears the streaming state.
func (m *Model) finishStream() {
	if m.stream != nil {
		m.stream.Close()
		m.stream = nil
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.streaming = false
	m.busy = false
	m.refresh()
}

// interrupt stops the current turn, keeping whatever text already arrived. It
// covers both halves of a turn: a reply still streaming, and the round of
// tools a finished reply asked for.
func (m *Model) interrupt() {
	// Bumping the sequence orphans anything still in flight — a chunk from
	// the stream, or a tool call that is still running.
	m.streamSeq++
	m.tooling = false
	m.pendingCalls = nil
	m.finishStream()

	// A round stopped part-way through has answered some of the assistant
	// turn's calls and not others, which is not a coherent exchange to send
	// back, so its results come out of the history along with the calls.
	m.abandonRound()

	// Results that will now be discarded leave the assistant turn asking
	// for tools with nothing answering it, which is not a coherent exchange
	// to send back next turn.
	m.detachToolCalls()

	if !m.dropEmptyReply() {
		m.appendToReply(" …")
	}
	m.addSystem("Interrupted.")
}

// dropEmptyReply removes the placeholder when a reply ended before any text
// arrived, so the history does not keep a blank entry. It reports whether one
// was removed.
func (m *Model) dropEmptyReply() bool {
	if len(m.messages) == 0 {
		return false
	}
	last := m.messages[len(m.messages)-1]
	if last.Role != types.RoleAssistant || last.Content != "" || len(last.ToolCalls) > 0 {
		return false
	}
	m.messages = m.messages[:len(m.messages)-1]
	m.refresh()
	return true
}
