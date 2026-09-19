package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/10txn/digicli/internal/llm"
	"github.com/10txn/digicli/internal/types"
	tea "github.com/charmbracelet/bubbletea"
)

// fakeStream replays a fixed set of chunks.
type fakeStream struct {
	chunks []string
	closed bool
}

func (f *fakeStream) Recv() (llm.Chunk, error) {
	if len(f.chunks) == 0 {
		return llm.Chunk{}, io.EOF
	}
	chunk := f.chunks[0]
	f.chunks = f.chunks[1:]
	return llm.Chunk{Text: chunk}, nil
}

func (f *fakeStream) Close() error {
	f.closed = true
	return nil
}

// streamingModel returns a model mid-reply, as startStream would leave it.
func streamingModel(t *testing.T) (*Model, *fakeStream) {
	t.Helper()
	m := testModel(t, 96, 30)
	m.messages = nil

	stream := &fakeStream{}
	m.append(types.NewMessage(types.RoleUser, "hello"))
	m.streaming = true
	m.busy = true
	m.streamSeq++
	m.stream = stream
	m.cancel = func() {}
	m.append(types.NewMessage(types.RoleAssistant, ""))
	return m, stream
}

func lastMessage(m *Model) types.Message {
	return m.messages[len(m.messages)-1]
}

func TestConversationOmitsNoticesAndPlaceholder(t *testing.T) {
	m := testModel(t, 96, 30)
	m.messages = nil

	m.addSystem("a local notice")
	m.append(types.NewMessage(types.RoleUser, "hello"))
	m.append(types.NewMessage(types.RoleAssistant, "hi"))
	m.append(types.NewMessage(types.RoleAssistant, "")) // streaming placeholder

	got := m.conversation()

	if len(got) != 3 {
		t.Fatalf("got %d messages, want the system prompt plus two turns: %+v", len(got), got)
	}
	if got[0].Role != types.RoleSystem || !strings.Contains(got[0].Content, "DigiCLI") {
		t.Errorf("first message should be the system prompt, got %+v", got[0])
	}
	for _, msg := range got[1:] {
		if msg.Role == types.RoleSystem {
			t.Errorf("local notice leaked into the conversation: %+v", msg)
		}
		if msg.Content == "" {
			t.Error("empty placeholder leaked into the conversation")
		}
	}
}

// Slash commands must never reach the model. Sending them made qwen2.5:3b
// read the transcript as a directory listing and answer about "files named
// model, model, model qwen2.5:3b and test".
func TestCommandsAreNotSentToTheModel(t *testing.T) {
	m := testModel(t, 96, 30)
	m.messages = nil

	typed := []string{"/models", "/model qwen2.5:3b", "/settings", "/help", "/clear"}
	for _, line := range typed {
		m.input.SetValue(line)
		m.submit()
	}
	m.input.SetValue("what does this project do?")
	m.submit()

	for _, msg := range m.conversation() {
		for _, line := range typed {
			if strings.Contains(msg.Content, line) {
				t.Errorf("command %q was sent to the model as: %q", line, msg.Content)
			}
		}
		if strings.HasPrefix(msg.Content, "/") {
			t.Errorf("a slash command reached the model: %q", msg.Content)
		}
	}

	// The real question still has to get through.
	got := m.conversation()
	if len(got) != 2 || got[1].Content != "what does this project do?" {
		t.Errorf("expected the system prompt plus the question, got %+v", got)
	}
}

// Commands are still shown, so the transcript reads back the way it was typed.
func TestCommandsAreStillDisplayed(t *testing.T) {
	m := testModel(t, 96, 30)
	m.messages = nil

	m.input.SetValue("/help")
	m.submit()

	if !strings.Contains(m.viewport.View(), "/help") {
		t.Error("the typed command was not echoed into the chat")
	}
}

// Local entries cost no context, so the readout tracks what is actually sent.
func TestLocalMessagesDoNotCountTowardsContext(t *testing.T) {
	msgs := []types.Message{
		types.NewLocal(types.RoleSystem, strings.Repeat("notice ", 100)),
		types.NewLocal(types.RoleUser, "/models"),
	}
	if got := estimateTokens(msgs); got != 0 {
		t.Errorf("local messages counted %d tokens, want 0", got)
	}
}

// The system prompt has to describe the active mode, or the model offers to do
// things the mode forbids.
func TestConversationPromptFollowsMode(t *testing.T) {
	m := testModel(t, 96, 30)

	m.cfg.Mode = types.ModePlan
	if got := m.conversation()[0].Content; !strings.Contains(got, "PLAN") {
		t.Errorf("plan mode prompt does not mention PLAN: %q", got)
	}

	m.cfg.Mode = types.ModeAuto
	if got := m.conversation()[0].Content; !strings.Contains(got, "AUTO") {
		t.Errorf("auto mode prompt does not mention AUTO: %q", got)
	}
}

func TestChunksAppendToTheReply(t *testing.T) {
	m, _ := streamingModel(t)

	m.handleStreamChunk(streamChunkMsg{seq: m.streamSeq, chunk: llm.Chunk{Text: "Hel"}})
	m.handleStreamChunk(streamChunkMsg{seq: m.streamSeq, chunk: llm.Chunk{Text: "lo"}})

	if got := lastMessage(m).Content; got != "Hello" {
		t.Errorf("reply is %q, want %q", got, "Hello")
	}
}

// A chunk from an interrupted stream must not land on the next reply.
func TestStaleChunksAreDropped(t *testing.T) {
	m, _ := streamingModel(t)
	stale := m.streamSeq

	m.handleStreamChunk(streamChunkMsg{seq: stale, chunk: llm.Chunk{Text: "kept"}})
	m.interrupt()

	m.handleStreamChunk(streamChunkMsg{seq: stale, chunk: llm.Chunk{Text: " LEAKED"}})

	for _, msg := range m.messages {
		if strings.Contains(msg.Content, "LEAKED") {
			t.Fatalf("a chunk from the interrupted stream was appended: %q", msg.Content)
		}
	}
}

func TestInterruptKeepsPartialReply(t *testing.T) {
	m, stream := streamingModel(t)
	m.handleStreamChunk(streamChunkMsg{seq: m.streamSeq, chunk: llm.Chunk{Text: "partial answer"}})

	m.interrupt()

	if m.streaming {
		t.Error("still marked as streaming after an interrupt")
	}
	if !stream.closed {
		t.Error("the stream was not closed")
	}
	if got := m.messages[len(m.messages)-2].Content; !strings.Contains(got, "partial answer") {
		t.Errorf("partial reply was lost, got %q", got)
	}
	if got := lastMessage(m); got.Role != types.RoleSystem || !strings.Contains(got.Content, "Interrupted") {
		t.Errorf("expected an Interrupted notice, got %+v", got)
	}
}

// Interrupting before any text arrived should not leave a blank entry.
func TestInterruptDropsEmptyReply(t *testing.T) {
	m, _ := streamingModel(t)

	m.interrupt()

	for _, msg := range m.messages {
		if msg.Role == types.RoleAssistant && msg.Content == "" {
			t.Fatal("an empty assistant message was left in the history")
		}
	}
}

func TestStreamDoneOnEOFIsSilent(t *testing.T) {
	m, _ := streamingModel(t)
	m.handleStreamChunk(streamChunkMsg{seq: m.streamSeq, chunk: llm.Chunk{Text: "done"}})

	m.handleStreamDone(streamDoneMsg{seq: m.streamSeq, err: io.EOF})

	if m.streaming || m.busy {
		t.Error("streaming state was not cleared")
	}
	if got := lastMessage(m); got.Role != types.RoleAssistant || got.Content != "done" {
		t.Errorf("EOF should end the reply quietly, got %+v", got)
	}
}

func TestStreamDoneOnCancelIsSilent(t *testing.T) {
	m, _ := streamingModel(t)
	m.handleStreamDone(streamDoneMsg{seq: m.streamSeq, err: context.Canceled})

	if got := lastMessage(m); strings.Contains(got.Content, "context canceled") {
		t.Errorf("cancellation should not be reported as an error, got %+v", got)
	}
}

func TestStreamDoneReportsRealErrors(t *testing.T) {
	m, _ := streamingModel(t)

	m.handleStreamDone(streamDoneMsg{seq: m.streamSeq, err: errors.New("out of memory")})

	got := lastMessage(m)
	if got.Role != types.RoleSystem || !strings.Contains(got.Content, "Out of memory") {
		t.Errorf("expected the error in the chat, got %+v", got)
	}
	for _, msg := range m.messages {
		if msg.Role == types.RoleAssistant && msg.Content == "" {
			t.Error("the empty placeholder should have been removed")
		}
	}
}

// toolingModel returns a model in the window between a reply that asked for
// tools and their results arriving, which is where handleStreamDone leaves it.
func toolingModel(t *testing.T) *Model {
	t.Helper()

	m, _ := streamingModel(t)
	m.handleStreamChunk(streamChunkMsg{seq: m.streamSeq, chunk: llm.Chunk{
		Text:      "let me look",
		ToolCalls: []types.ToolCall{call("read_file", map[string]string{"path": "main.go"})},
	}})
	// The returned command runs the tools; leaving it uncalled is the test
	// standing in the middle of that round.
	m.handleStreamDone(streamDoneMsg{seq: m.streamSeq, err: io.EOF})
	return m
}

// Ctrl+C while the tools a reply asked for are still running has to interrupt
// the turn. It used to quit DigiCLI outright, since no stream is open across
// that window and ctrl+c fell through to the quit path.
func TestCtrlCDuringToolsInterruptsRatherThanQuitting(t *testing.T) {
	m := toolingModel(t)
	if !m.tooling {
		t.Fatal("a reply that ended in tool calls should leave the turn live")
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("ctrl+c quit DigiCLI instead of interrupting the turn")
		}
	}
	if m.tooling {
		t.Error("still marked as running tools after an interrupt")
	}
	if got := lastMessage(m); got.Role != types.RoleSystem || !strings.Contains(got.Content, "Interrupted") {
		t.Errorf("expected an Interrupted notice, got %+v", got)
	}
}

// Enter must not start a second turn on top of a round of tools.
func TestEnterIsIgnoredWhileToolsRun(t *testing.T) {
	m := toolingModel(t)
	before := len(m.messages)

	m.input.SetValue("another question")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(m.messages) != before {
		t.Errorf("enter started a turn while tools were running: %d messages, want %d",
			len(m.messages), before)
	}
}

// Results from an interrupted round must not land in the history, and must not
// hand the turn back to the model. A round runs one call at a time now, so
// this covers both halves: a call that finishes after the interrupt, and the
// end of the round arriving behind it.
func TestInterruptedToolResultsAreDiscarded(t *testing.T) {
	m := toolingModel(t)
	seq := m.streamSeq
	finished := call("read_file", map[string]string{"path": "main.go"})

	m.interrupt()

	if cmd := m.handleToolRan(toolRanMsg{
		seq:    seq,
		call:   finished,
		result: toolResult(finished, "package main", "⚙ read_file"),
	}); cmd != nil {
		t.Error("a result from an interrupted round carried the round on")
	}
	if cmd := m.handleToolsDone(toolsDoneMsg{seq: seq}); cmd != nil {
		t.Error("an interrupted round of tools carried on into another request")
	}

	for _, msg := range m.messages {
		if msg.Role == types.RoleTool {
			t.Fatalf("a discarded tool result was appended: %+v", msg)
		}
	}
}

// An interrupt while a call is waiting on the user has to take the question
// down with the round, rather than leaving a prompt on screen that no longer
// has anything behind it.
func TestInterruptClearsAPendingApproval(t *testing.T) {
	m, _, _ := toolModel(t, types.ModeManual)

	drive(t, m, call("write_file", map[string]string{"path": "new.go", "content": "package new\n"}))
	if m.approval == nil {
		t.Fatal("nothing was put to the user")
	}

	m.interrupt()

	if m.approval != nil {
		t.Error("the prompt survived the interrupt")
	}
	if m.awaitingApproval() {
		t.Error("the session still thinks it is waiting on an answer")
	}
}

// A turn that asked for tools which never ran must not go back to the model
// still carrying the request, because nothing follows to answer it.
func TestInterruptLeavesNoUnansweredToolCalls(t *testing.T) {
	m := toolingModel(t)

	m.interrupt()

	for _, msg := range m.conversation() {
		if len(msg.ToolCalls) > 0 {
			t.Errorf("an unanswered tool call was sent to the model: %+v", msg)
		}
	}
}

// Tool calls collected from a reply that was then interrupted must not be
// carried into the next turn and run late.
func TestInterruptClearsPendingToolCalls(t *testing.T) {
	m, _ := streamingModel(t)
	m.handleStreamChunk(streamChunkMsg{seq: m.streamSeq, chunk: llm.Chunk{
		ToolCalls: []types.ToolCall{call("read_file", map[string]string{"path": "main.go"})},
	}})

	m.interrupt()

	if len(m.pendingCalls) != 0 {
		t.Errorf("%d tool call(s) survived the interrupt: %+v", len(m.pendingCalls), m.pendingCalls)
	}
}

func TestProviderSelection(t *testing.T) {
	m := testModel(t, 96, 30)

	m.cfg.Provider = "ollama"
	if _, err := m.provider(); err != nil {
		t.Errorf("ollama: %v", err)
	}

	for _, name := range []string{"claude", "openai", "nonsense"} {
		m.cfg.Provider = name
		if _, err := m.provider(); err == nil {
			t.Errorf("%s: expected an error until it is implemented", name)
		}
	}
}

// The cursor marks a reply that is still arriving, and must not linger once it
// has finished.
func TestStreamCursorOnlyWhileStreaming(t *testing.T) {
	msgs := []types.Message{types.NewMessage(types.RoleAssistant, "text")}

	if got := renderMessages(msgs, 40, true, nil); !strings.Contains(got, streamCursor) {
		t.Error("no cursor while streaming")
	}
	if got := renderMessages(msgs, 40, false, nil); strings.Contains(got, streamCursor) {
		t.Error("cursor left behind after the reply finished")
	}
}
