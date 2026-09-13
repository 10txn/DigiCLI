package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestPaletteQuery(t *testing.T) {
	tests := []struct {
		input string
		query string
		open  bool
	}{
		{"/", "", true},
		{"/mo", "mo", true},
		{"/MODEL", "model", true}, // case-insensitive
		{"/model ", "", false},    // a space means arguments are being typed
		{"/model qwen2.5:3b", "", false},
		{"hello", "", false},
		{"", "", false},
		{"a/b", "", false}, // only a leading slash counts
	}
	for _, tt := range tests {
		query, open := paletteQuery(tt.input)
		if open != tt.open {
			t.Errorf("paletteQuery(%q): open = %v, want %v", tt.input, open, tt.open)
			continue
		}
		if open && query != tt.query {
			t.Errorf("paletteQuery(%q) = %q, want %q", tt.input, query, tt.query)
		}
	}
}

func TestPaletteListsEverythingForBareSlash(t *testing.T) {
	var p palette
	p.sync("/")

	if !p.active() {
		t.Fatal("the palette did not open for a bare slash")
	}
	if len(p.matches) != len(commandList) {
		t.Errorf("got %d commands, want all %d", len(p.matches), len(commandList))
	}
}

func TestPaletteFiltersAsYouType(t *testing.T) {
	var p palette
	p.sync("/mo")

	names := make([]string, 0, len(p.matches))
	for _, spec := range p.matches {
		names = append(names, spec.Name)
	}
	want := []string{"models", "model"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestPaletteClosesWhenNothingMatches(t *testing.T) {
	var p palette
	p.sync("/zzz")

	if p.active() {
		t.Error("the palette stayed open with no matches")
	}
}

// Narrowing the list must not move the highlight onto a different command.
func TestPaletteKeepsSelectionWhileNarrowing(t *testing.T) {
	var p palette
	p.sync("/mo")
	p.move(1) // highlight /model

	if got := p.currentName(); got != "model" {
		t.Fatalf("setup: highlighted %q, want model", got)
	}

	p.sync("/mod") // still matches both
	if got := p.currentName(); got != "model" {
		t.Errorf("after narrowing, highlighted %q, want model", got)
	}
}

func TestPaletteMoveWraps(t *testing.T) {
	var p palette
	p.sync("/")

	last := len(p.matches) - 1

	p.move(-1)
	if p.cursor != last {
		t.Errorf("moving up from the top gave %d, want %d", p.cursor, last)
	}
	p.move(1)
	if p.cursor != 0 {
		t.Errorf("moving down from the bottom gave %d, want 0", p.cursor)
	}
}

func TestPaletteEscDismissesUntilInputChanges(t *testing.T) {
	var p palette
	p.sync("/mo")
	p.dismiss("/mo")

	p.sync("/mo") // same input: stays dismissed
	if p.active() {
		t.Error("the palette reopened without the input changing")
	}

	p.sync("/mod") // typing again brings it back
	if !p.active() {
		t.Error("the palette did not reopen after further typing")
	}
}

// Enter on a command that needs no arguments should run it, not just complete
// it — there is nothing left to type.
func TestAcceptRunsArglessCommand(t *testing.T) {
	m := testModel(t, 96, 30)
	m.messages = nil

	m.input.SetValue("/he")
	m.commands.sync(m.input.Value())
	m.acceptCommand()

	if m.input.Value() != "" {
		t.Errorf("the input was not cleared: %q", m.input.Value())
	}
	if m.commands.active() {
		t.Error("the palette stayed open after selecting")
	}
	// /help writes its output into the chat.
	var sawHelp bool
	for _, msg := range m.messages {
		if strings.Contains(msg.Content, "Cycle mode") {
			sawHelp = true
		}
	}
	if !sawHelp {
		t.Error("/help did not run")
	}
}

// A command that takes arguments should be completed into the input with a
// trailing space, ready for them.
func TestAcceptCompletesCommandWithArgs(t *testing.T) {
	m := testModel(t, 96, 30)
	m.messages = nil

	m.input.SetValue("/mo")
	m.commands.sync(m.input.Value())
	m.commands.move(1) // /model

	m.acceptCommand()

	if got := m.input.Value(); got != "/model " {
		t.Errorf("input is %q, want %q", got, "/model ")
	}
	if m.commands.active() {
		t.Error("the palette stayed open after completing")
	}
	if len(m.messages) != 0 {
		t.Errorf("the command ran instead of completing: %+v", m.messages)
	}
}

// The palette is drawn over the chat, so the frame must not grow.
func TestOverlayPreservesLineCount(t *testing.T) {
	body := strings.Repeat("line\n", 19) + "line" // 20 lines

	var p palette
	p.sync("/")
	popup := p.View(80)

	got := overlayBottom(body, popup)
	if want := 20; strings.Count(got, "\n")+1 != want {
		t.Errorf("overlay produced %d lines, want %d", strings.Count(got, "\n")+1, want)
	}
}

// A palette taller than the space available must still not grow the frame.
func TestOverlayClipsWhenTallerThanBody(t *testing.T) {
	body := "one\ntwo"

	var p palette
	p.sync("/")
	popup := p.View(80)

	got := overlayBottom(body, popup)
	if lines := strings.Count(got, "\n") + 1; lines != 2 {
		t.Errorf("overlay produced %d lines, want 2", lines)
	}
}

// The palette must respect the layout contract at every size.
func TestPaletteFitsTerminal(t *testing.T) {
	for _, size := range sizes {
		m := testModel(t, size.w, size.h)
		m.input.SetValue("/")
		m.commands.sync(m.input.Value())

		view := m.View()
		if got := lipgloss.Height(view); got != size.h {
			t.Errorf("%dx%d: view is %d lines, want %d", size.w, size.h, got, size.h)
		}
		for i, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > size.w {
				t.Errorf("%dx%d: line %d is %d columns, want <= %d",
					size.w, size.h, i, got, size.w)
			}
		}
	}
}

// Typing an ordinary message must not open the palette.
func TestPaletteStaysClosedForOrdinaryText(t *testing.T) {
	m := testModel(t, 96, 30)

	m.input.SetValue("what does this do?")
	m.commands.sync(m.input.Value())

	if m.commands.active() {
		t.Error("the palette opened for ordinary text")
	}
}
