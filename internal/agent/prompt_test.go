package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/10txn/digicli/internal/types"
)

func testSession() Session {
	return Session{
		Model:    "qwen2.5:3b",
		Provider: "ollama",
		Mode:     types.ModePlan,
		Cwd:      "/workspace/digicli",
		Now:      time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
	}
}

// Asked "what model are you?", the model answered "DigiCLI" — because the
// prompt opened with "You are DigiCLI". The model's own name has to be in
// there, and the program's name has to be clearly not it.
func TestPromptNamesTheModel(t *testing.T) {
	got := SystemPrompt(testSession())

	if !strings.Contains(got, "qwen2.5:3b") {
		t.Error("the prompt never names the model")
	}
	if strings.Contains(got, "You are DigiCLI") {
		t.Error(`the prompt says "You are DigiCLI", which the model adopts as its name`)
	}
	if !strings.Contains(got, "not your name") {
		t.Error("the prompt does not separate the program's name from the model's")
	}
}

func TestPromptCarriesSessionFacts(t *testing.T) {
	s := testSession()
	got := SystemPrompt(s)

	for _, want := range []string{
		"ollama",             // provider
		"/workspace/digicli", // working directory
		"13 September 2026",  // date, not the training cutoff
		"locally",            // local-only, for an ollama session
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt is missing %q", want)
		}
	}
}

// Every mode must be described, or switching to one silently drops the policy.
func TestPromptCoversEveryMode(t *testing.T) {
	for _, mode := range []types.Mode{types.ModePlan, types.ModeManual, types.ModeAuto} {
		s := testSession()
		s.Mode = mode

		got := SystemPrompt(s)
		if !strings.Contains(got, strings.ToUpper(mode.String())) {
			t.Errorf("%v: the prompt does not state the mode", mode)
		}
	}
}

// The prompt used to promise "You may read files" while no file tools existed,
// so the model offered to read files and then could not.
func TestPromptDoesNotPromiseToolsItLacks(t *testing.T) {
	s := testSession()
	s.Tools = nil

	got := SystemPrompt(s)
	if !strings.Contains(got, "no tools in this session") {
		t.Error("with no tools wired up, the prompt must say so")
	}
	if !strings.Contains(got, "cannot read or write files") {
		t.Error("the prompt must be explicit that file access is unavailable")
	}
}

func TestPromptListsToolsOnceTheyExist(t *testing.T) {
	s := testSession()
	s.Tools = []string{"read_file", "write_file"}

	got := SystemPrompt(s)
	if !strings.Contains(got, "read_file, write_file") {
		t.Error("the prompt does not list the available tools")
	}
	if strings.Contains(got, "no tools in this session") {
		t.Error("the prompt still claims there are no tools")
	}
}

// A half-built session should degrade rather than print empty facts.
func TestPromptHandlesMissingFacts(t *testing.T) {
	got := SystemPrompt(Session{Mode: types.ModeManual})

	if strings.Contains(got, `""`) {
		t.Errorf("empty fields leaked into the prompt:\n%s", got)
	}
	if !strings.Contains(got, "unknown") {
		t.Error("an unset model should be reported as unknown, not omitted")
	}
	for _, unwanted := range []string{"Today's date", "working directory"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("%q was included with no value to report", unwanted)
		}
	}
}
