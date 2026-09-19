// Package agent holds the agentic behaviour: the system prompt the model is
// given, and (next) the mode filtering that decides what it is allowed to do.
package agent

import (
	"fmt"
	"slices"
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

	// The obvious phrasing here — that changes are "proposed" for "approval" —
	// reads to a model as an instruction to ask permission in the chat. It then
	// describes the change, asks whether to proceed, is told yes, and asks
	// again, because the answer it is waiting for is not one the chat can give:
	// DigiCLI raises the approval prompt itself, off the back of the call. So
	// this says whose job that is, in those words.
	types.ModeManual: `Mode: MANUAL (approval required).
Call the tool. DigiCLI shows the user what the call would do and asks them to
approve it — that prompt is not yours to write, and a question you ask in the
chat cannot be answered with an approval. Do not describe a change and wait for
permission, and do not ask whether to go ahead: make the call. Their answer
reaches you as the call's result, and a refusal reaches you the same way, which
is the point at which to ask what they would prefer instead.`,

	types.ModeAuto: `Mode: AUTO (unattended).
You may act without asking, including outside the working directory. Nobody is
being shown what you are about to do, so the care that normally comes from the
approval prompt has to come from you: work in small steps, report what you did
after each one, and stay inside the working directory unless the user has asked
for something that cannot be done there.`,
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
	b.WriteString(capabilities(s.Tools, s.Cwd != "", s.Mode))

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
func capabilities(tools []string, hasCwd bool, mode types.Mode) string {
	if len(tools) > 0 {
		var b strings.Builder
		b.WriteString("Tools available to you: " + strings.Join(tools, ", ") + ".\n")
		b.WriteString("Call them yourself: a result comes back to you automatically, in this\n")
		b.WriteString("same turn. Never ask the user to paste a file, to run a tool for you,\n")
		b.WriteString("or to tell you what a call returned — they are not standing between\n")
		b.WriteString("you and the tools.\n")
		b.WriteString("Use them instead of guessing at a file's contents. Paths are relative\n")
		b.WriteString("to the working directory")
		if hasCwd {
			b.WriteString(" named above")
		}
		b.WriteString(".\n")
		b.WriteString(boundary(mode))

		// Worth stating even when write_file is not wired up, since it is
		// the mistake that costs the user their file rather than their time:
		// there is no patching, so a partial write is a deletion.
		if slices.Contains(tools, "write_file") {
			b.WriteString("\n\nwrite_file replaces a file completely. To change an existing file,\n")
			b.WriteString("read it first and send back the whole thing with your edit in it.\n")
			b.WriteString("Sending only the lines you changed deletes everything else.")
		}

		b.WriteString("\n\n")
		b.WriteString(refusals)
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

// boundary tells the model what the working directory means in this mode. It
// differs enough between the three that a single sentence would be wrong in
// two of them, and a model told it cannot leave the directory will not try
// even when the user has asked it to.
func boundary(mode types.Mode) string {
	switch mode {
	case types.ModeAuto:
		return "You can reach outside that directory, but treat it as the boundary of the\n" +
			"job unless the user has pointed you somewhere else."
	case types.ModeManual:
		return "Anything outside that directory is put to the user for approval first, so\n" +
			"stay inside it unless they have asked for something that cannot be."
	default:
		return "You cannot reach anything outside it."
	}
}

// A small set of paths is refused whatever the mode, and no approval reaches
// past them: credentials, the operating system's own files, shell startup
// files, and git's internal directory. Saying so up front is cheaper than a
// model discovering it one refusal at a time and trying to route around it.
const refusals = `Some paths are refused in every mode, and the user cannot approve them:
credentials such as ~/.ssh, system directories, files that run automatically
when a shell or login session starts, and the .git directory. If you are
refused, say what you were trying to do — do not look for another path to the
same place.`
