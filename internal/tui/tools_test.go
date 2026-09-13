package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/10txn/digicli/internal/tool"
	"github.com/10txn/digicli/internal/types"
)

// toolProject builds a small tree and a registry confined to it.
func toolProject(t *testing.T) (*tool.Registry, string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}

	registry, err := buildTools(dir)
	if err != nil {
		t.Fatal(err)
	}
	return registry, dir
}

func call(name string, args map[string]string) types.ToolCall {
	encoded, _ := json.Marshal(args)
	return types.ToolCall{ID: name + "-0", Name: name, Arguments: encoded}
}

func TestReadFileToolReturnsContents(t *testing.T) {
	registry, _ := toolProject(t)

	got := runOne(registry, types.ModePlan, call("read_file", map[string]string{"path": "main.go"}))

	if got.Role != types.RoleTool || got.ToolName != "read_file" {
		t.Fatalf("unexpected result message: %+v", got)
	}
	if !strings.Contains(got.Content, "package main") {
		t.Errorf("the file contents did not reach the model: %q", got.Content)
	}
	// The user sees a summary, not the whole file.
	if strings.Contains(got.Display, "package main") {
		t.Errorf("the whole file was rendered into the transcript: %q", got.Display)
	}
}

func TestListFilesTool(t *testing.T) {
	registry, _ := toolProject(t)

	got := runOne(registry, types.ModePlan, call("list_files", map[string]string{"path": "."}))

	if !strings.Contains(got.Content, "main.go") || !strings.Contains(got.Content, "internal/") {
		t.Errorf("listing is missing entries: %q", got.Content)
	}
}

// list_files with no path at all should mean the working directory, since
// models routinely omit optional arguments.
func TestListFilesDefaultsToWorkingDirectory(t *testing.T) {
	registry, _ := toolProject(t)

	got := runOne(registry, types.ModePlan, types.ToolCall{
		Name: "list_files", Arguments: json.RawMessage(`{}`),
	})

	if !strings.Contains(got.Content, "main.go") {
		t.Errorf("an argument-less listing failed: %q", got.Content)
	}
}

// A tool escaping the working directory must fail as a result the model can
// read, not as a crash.
func TestToolsCannotEscapeTheWorkingDirectory(t *testing.T) {
	registry, _ := toolProject(t)

	got := runOne(registry, types.ModeAuto, call("read_file", map[string]string{
		"path": "../../../../etc/passwd",
	}))

	if !strings.HasPrefix(got.Content, "Error:") {
		t.Errorf("the escape was not refused: %q", got.Content)
	}
	if strings.Contains(got.Content, "root:") {
		t.Fatal("the tool read a file outside the working directory")
	}
}

// A model inventing a tool name should be told, not crash the turn.
func TestUnknownToolIsReportedToTheModel(t *testing.T) {
	registry, _ := toolProject(t)

	got := runOne(registry, types.ModeAuto, call("delete_everything", nil))

	if !strings.Contains(got.Content, "No tool named") {
		t.Errorf("unexpected result: %q", got.Content)
	}
	if !strings.Contains(got.Content, "read_file") {
		t.Error("the model was not told which tools it does have")
	}
}

// A bad path is the model's to fix, so it comes back as a result.
func TestMissingFileComesBackAsAResult(t *testing.T) {
	registry, _ := toolProject(t)

	got := runOne(registry, types.ModeAuto, call("read_file", map[string]string{"path": "nope.go"}))

	if got.Role != types.RoleTool {
		t.Errorf("a missing file should be a tool result, got role %q", got.Role)
	}
	if !strings.Contains(got.Content, "no such file") {
		t.Errorf("unexpected result: %q", got.Content)
	}
}

// Reading is allowed in every mode: plan mode exists to prevent changes, not
// to stop the model looking at code.
func TestReadingIsAllowedInEveryMode(t *testing.T) {
	registry, _ := toolProject(t)

	for _, mode := range []types.Mode{types.ModePlan, types.ModeManual, types.ModeAuto} {
		got := runOne(registry, mode, call("read_file", map[string]string{"path": "main.go"}))
		if !strings.Contains(got.Content, "package main") {
			t.Errorf("%v: reading was blocked: %q", mode, got.Content)
		}
	}
}

// Tool results are part of the conversation and must reach the model, unlike
// slash commands.
func TestToolResultsAreSentToTheModel(t *testing.T) {
	m := testModel(t, 96, 30)
	m.messages = nil

	m.append(types.NewMessage(types.RoleUser, "what is in main.go?"))

	assistant := types.NewMessage(types.RoleAssistant, "")
	assistant.ToolCalls = []types.ToolCall{call("read_file", map[string]string{"path": "main.go"})}
	m.append(assistant)

	result := types.NewMessage(types.RoleTool, "package main")
	result.ToolName = "read_file"
	result.Display = "⚙ read_file main.go → 1 line"
	m.append(result)

	got := m.conversation()

	var sawCall, sawResult bool
	for _, msg := range got {
		if len(msg.ToolCalls) > 0 {
			sawCall = true
		}
		if msg.Role == types.RoleTool && msg.Content == "package main" {
			sawResult = true
		}
	}
	if !sawCall {
		t.Error("the assistant's tool call was dropped from the conversation")
	}
	if !sawResult {
		t.Error("the tool result never reached the model")
	}
}

// The transcript shows the summary; the model gets the full content.
func TestToolResultDisplaysASummary(t *testing.T) {
	m := testModel(t, 96, 30)
	m.messages = nil

	result := types.NewMessage(types.RoleTool, strings.Repeat("a line of file\n", 200))
	result.ToolName = "read_file"
	result.Display = "⚙ read_file main.go → 200 lines"
	m.append(result)

	view := m.viewport.View()
	if !strings.Contains(view, "200 lines") {
		t.Errorf("the summary was not rendered: %q", view)
	}
	if strings.Contains(view, "a line of file") {
		t.Error("the full tool output was rendered into the transcript")
	}
}

// A model that keeps calling tools must eventually be stopped.
func TestToolLoopIsBounded(t *testing.T) {
	m := testModel(t, 96, 30)
	m.messages = nil
	m.toolRounds = maxToolRounds

	m.handleToolsDone(toolsDoneMsg{seq: m.streamSeq, results: nil})

	got := lastMessage(m)
	if !strings.Contains(got.Content, "looping") {
		t.Errorf("the loop was not cut off: %+v", got)
	}
	if m.busy {
		t.Error("the session is still marked busy after cutting off the loop")
	}
}

// Results from an interrupted turn must not be appended to the next one.
func TestToolResultsFromSupersededStreamAreDropped(t *testing.T) {
	m := testModel(t, 96, 30)
	m.messages = nil
	stale := m.streamSeq
	m.streamSeq++

	m.handleToolsDone(toolsDoneMsg{
		seq:     stale,
		results: []types.Message{types.NewMessage(types.RoleTool, "LEAKED")},
	})

	for _, msg := range m.messages {
		if strings.Contains(msg.Content, "LEAKED") {
			t.Fatal("results from a superseded stream were appended")
		}
	}
}
