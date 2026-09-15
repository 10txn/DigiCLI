package tui

import (
	"strings"
	"testing"

	"github.com/10txn/digicli/internal/config"
	"github.com/10txn/digicli/internal/types"
	"github.com/charmbracelet/lipgloss"
)

// sizes covers a typical window, a tall one, a narrow one and the smallest
// size the full layout claims to support.
var sizes = []struct{ w, h int }{
	{96, 30},
	{120, 50},
	{80, 24},
	{minWidth, minHeight},
}

func testModel(t *testing.T, w, h int) *Model {
	t.Helper()
	t.Setenv("DIGICLI_HOME", t.TempDir())

	cfg := config.New()
	// Otherwise every model starts on the first-run update prompt, which is
	// its own view and has its own test below.
	cfg.UpdatePrompted = true

	m := New(cfg, "v0.1.2")
	m.resize(w, h)
	return m
}

// TestViewFillsTerminalExactly is the guard against the frame overflowing the
// screen, which makes the terminal scroll and leaves a duplicated status bar.
func TestViewFillsTerminalExactly(t *testing.T) {
	for _, size := range sizes {
		m := testModel(t, size.w, size.h)

		// A few messages, including one long enough to wrap.
		m.append(types.NewMessage(types.RoleUser, "refactor the auth middleware"))
		m.append(types.NewMessage(types.RoleAssistant, strings.Repeat("wrap me ", 40)))

		got := lipgloss.Height(m.View())
		if got != size.h {
			t.Errorf("%dx%d: view is %d lines, want %d", size.w, size.h, got, size.h)
		}
	}
}

// TestViewNeverExceedsWidth catches borders and status bars that are one
// column too wide and wrap onto an extra line.
func TestViewNeverExceedsWidth(t *testing.T) {
	for _, size := range sizes {
		m := testModel(t, size.w, size.h)
		m.append(types.NewMessage(types.RoleUser, strings.Repeat("long ", 60)))

		for i, line := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(line); got > size.w {
				t.Errorf("%dx%d: line %d is %d columns, want <= %d",
					size.w, size.h, i, got, size.w)
			}
		}
	}
}

// TestConsentViewFitsTerminal holds the first-run update prompt to the same
// contract. It is the first thing a new user sees, so it has to fit whatever
// terminal they see it in.
func TestConsentViewFitsTerminal(t *testing.T) {
	for _, size := range sizes {
		t.Setenv("DIGICLI_HOME", t.TempDir())

		m := New(config.New(), "v0.1.2") // an unanswered config: the prompt
		m.resize(size.w, size.h)
		if m.view != viewUpdateConsent {
			t.Fatal("a config that has never been asked should open on the prompt")
		}

		view := m.View()
		if got := lipgloss.Height(view); got != size.h {
			t.Errorf("%dx%d: consent is %d lines, want %d", size.w, size.h, got, size.h)
		}
		for i, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > size.w {
				t.Errorf("%dx%d: consent line %d is %d columns, want <= %d",
					size.w, size.h, i, got, size.w)
			}
		}
	}
}

// TestSettingsViewFitsTerminal holds the modal to the same contract.
func TestSettingsViewFitsTerminal(t *testing.T) {
	for _, size := range sizes {
		m := testModel(t, size.w, size.h)
		m.openSettings()

		view := m.View()
		if got := lipgloss.Height(view); got != size.h {
			t.Errorf("%dx%d: settings is %d lines, want %d", size.w, size.h, got, size.h)
		}
		for i, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > size.w {
				t.Errorf("%dx%d: settings line %d is %d columns, want <= %d",
					size.w, size.h, i, got, size.w)
			}
		}
	}
}
