// Package agent holds the agentic behaviour: the system prompt the model is
// given, and (next) the mode filtering that decides what it is allowed to do.
package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/10txn/digicli/internal/types"
)

// Session is what the model is told about the run it is part of. Everything
// here is a fact the model would otherwise guess at — and guess wrong: asked
// its own name it will answer "DigiCLI", asked the date it will answer from its
// training cutoff.
//
// Build this from live state rather than constants. A prompt that claims a
// capability the binary does not have is the same bug in a different place:
// the model offers to read a file, then cannot.
type Session struct {
	// Model is the model's own name, e.g. "qwen2.5:3b".
	Model string
	// Provider serves the model: ollama, claude, openai.
	Provider string
	// Mode is the current interaction mode.
	Mode types.Mode
	// Cwd is the directory DigiCLI was started in.
	Cwd string
	// Now is the session's date. Zero means leave it out.
	Now time.Time
	// Tools names the tools actually wired up and callable. While it is
	// empty the prompt says so plainly, so the model stops offering to do
	// things it cannot yet do.
	Tools []string
}

// identity is deliberately blunt about the difference between the program and
// the model, because the obvious phrasing ("You are DigiCLI") makes the model
// answer "DigiCLI" when asked which model it is.
const identity = `You are a coding assistant running inside DigiCLI, a terminal application.
DigiCLI is the program hosting this conversation. It is not your name, and it is
not the name of your model.`

// modePolicies describe what each mode permits.
var modePolicies = map[types.Mode]string{
	types.ModePlan: `Mode: PLAN (read-only).
Analyse and explain. Do not offer to modify files or run commands; describe what
you would change and let the user decide.`,

	types.ModeManual: `Mode: MANUAL (approval required).
You may propose file changes and commands. Every one is shown to the user for
approval before it runs, so say plainly what each does and why.`,

	types.ModeAuto: `Mode: AUTO (unattended).
You may act without asking. Work in small steps and report what you did after
each one.`,
}

// SystemPrompt builds the prompt for a session.
func SystemPrompt(s Session) string {
	var b strings.Builder

	b.WriteString(identity)
	b.WriteString("\n\nSession facts. These are authoritative — never contradict or guess at them:\n")

	model := s.Model
	if model == "" {
		model = "unknown"
	}
	if s.Provider != "" {
		fmt.Fprintf(&b, "- You are the model %q, served by %s.\n", model, s.Provider)
	} else {
		fmt.Fprintf(&b, "- You are the model %q.\n", model)
	}
	if s.Provider == "ollama" {
		b.WriteString("- You are running locally on the user's machine. Nothing leaves it.\n")
	}
	if !s.Now.IsZero() {
		fmt.Fprintf(&b, "- Today's date is %s.\n", s.Now.Format("2 January 2006"))
	}
	if s.Cwd != "" {
		fmt.Fprintf(&b, "- The working directory is %s.\n", s.Cwd)
	}

	// Without the worked example a small model answers by quoting the line
	// above back verbatim, second person and all: "You are the model ...".
	fmt.Fprintf(&b, "\nIf asked which model or version you are, answer in your own words using the\n"+
		"name above — for example: \"I'm %s", model)
	if s.Provider != "" {
		fmt.Fprintf(&b, ", running via %s", s.Provider)
	}
	b.WriteString(".\"\n")
	b.WriteString("If you do not know something about this session, say so rather than inventing it.\n")

	b.WriteString("\n")
	b.WriteString(capabilities(s.Tools, s.Cwd != ""))

	if policy, ok := modePolicies[s.Mode]; ok {
		b.WriteString("\n\n")
		b.WriteString(policy)
	}

	b.WriteString("\n\nBe concise and practical. Prefer showing code to describing it, and use\n")
	b.WriteString("fenced code blocks with a language tag.")

	return b.String()
}

// capabilities states what the model can actually do right now. hasCwd guards
// the reference back to the working directory, so the prompt never points at a
// fact it did not state.
func capabilities(tools []string, hasCwd bool) string {
	if len(tools) > 0 {
		var b strings.Builder
		b.WriteString("Tools available to you: " + strings.Join(tools, ", ") + ".\n")
		b.WriteString("Use them instead of asking the user to paste code, and instead of\n")
		b.WriteString("guessing at a file's contents. Paths are relative to the working\n")
		b.WriteString("directory")
		if hasCwd {
			b.WriteString(" named above")
		}
		b.WriteString("; you cannot reach anything outside it.")
		return b.String()
	}

	var b strings.Builder
	b.WriteString("You have no tools in this session. You cannot read or write files, run\n")
	b.WriteString("commands, or access the network")
	if hasCwd {
		b.WriteString(" — including anything in the directory named above")
	}
	b.WriteString(".\nAsk the user to paste anything you need to see, and give commands for them\n")
	b.WriteString("to run themselves rather than offering to run them.")
	return b.String()
}
