package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/10txn/digicli/internal/config"
	"github.com/10txn/digicli/internal/update"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// unansweredModel is a session whose config has never answered the update
// question, which is what puts the first-run prompt on screen.
func unansweredModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("DIGICLI_HOME", t.TempDir())

	m := New(config.New(), "v0.1.2")
	m.resize(96, 30)
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestFirstRunAsksBeforeCheckingAnything(t *testing.T) {
	m := unansweredModel(t)

	if m.view != viewUpdateConsent {
		t.Fatalf("view: got %v, want the update prompt", m.view)
	}
	// Init must not start a check while the question is still open.
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init returned no command at all")
	}
	if m.checking {
		t.Error("a check started before the question was answered")
	}

	// The prompt has to say what it contacts.
	if view := m.View(); !strings.Contains(view, "api.github.com") {
		t.Error("the prompt does not name what it contacts")
	}
}

func TestConsentAnswersArePersisted(t *testing.T) {
	tests := []struct {
		key         string
		wantEnabled bool
	}{
		{"y", true},
		{"enter", true},
		{"n", false},
		{"esc", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			m := unansweredModel(t)
			m.handleConsentKey(key(tt.key))

			if m.view != viewChat {
				t.Error("answering the prompt did not return to the chat")
			}
			if m.cfg.UpdateCheck != tt.wantEnabled {
				t.Errorf("UpdateCheck: got %v, want %v", m.cfg.UpdateCheck, tt.wantEnabled)
			}

			// Saved, or the question comes back at the next start.
			saved, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !saved.UpdatePrompted {
				t.Error("the answer was not written to the config")
			}
			if saved.UpdateCheck != tt.wantEnabled {
				t.Errorf("saved UpdateCheck: got %v, want %v", saved.UpdateCheck, tt.wantEnabled)
			}
		})
	}
}

// Anything that is not an answer leaves the prompt up: an unanswered config is
// the only state that gets asked again, so it must not be dismissible.
func TestConsentIgnoresOtherKeys(t *testing.T) {
	m := unansweredModel(t)

	for _, k := range []string{"x", "tab", " "} {
		if cmd := m.handleConsentKey(key(k)); cmd != nil {
			t.Errorf("%q returned a command", k)
		}
	}
	if m.view != viewUpdateConsent {
		t.Error("the prompt was dismissed by a key that is not an answer")
	}
	if m.cfg.UpdatePrompted {
		t.Error("the question was recorded as answered")
	}
}

func TestSettingsAnswersTheQuestionToo(t *testing.T) {
	cfg := config.New()

	var field settingField
	for _, f := range settingsFields {
		if f.Label == "update checks" {
			field = f
		}
	}
	if field.Label == "" {
		t.Fatal("there is no update checks row in the settings pane")
	}

	if got := field.Get(cfg); got != "off" {
		t.Errorf("a fresh config reads as %q, want off", got)
	}
	if err := field.Set(cfg, "on"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !cfg.UpdateCheck || !cfg.UpdatePrompted {
		t.Error("choosing in /settings should both switch it on and count as answering")
	}
	if err := field.Set(cfg, "sometimes"); err == nil {
		t.Error("expected an error for a value that is not on or off")
	}
}

func TestUpdateBadge(t *testing.T) {
	m := unansweredModel(t)
	m.latest = update.Release{Version: "v0.1.3"}

	if got := m.updateBadge(); got != "" {
		t.Errorf("a session with nothing to update shows %q, want nothing", got)
	}

	m.updateAvailable = true
	if got := m.updateBadge(); !strings.Contains(got, "v0.1.3") || !strings.Contains(got, "/update") {
		t.Errorf("badge %q should name the version and the command", got)
	}

	m.updating = true
	if got := m.updateBadge(); !strings.Contains(got, "updating") {
		t.Errorf("badge %q should say an update is running", got)
	}

	m.updating, m.updateAvailable, m.updateInstalled = false, false, true
	if got := m.updateBadge(); !strings.Contains(got, "restart") {
		t.Errorf("badge %q should ask for a restart", got)
	}
}

// The badge lives in the bottom right, and must not push the status bar wider
// than the terminal at any size the layout claims to support.
func TestStatusBarFitsWithABadge(t *testing.T) {
	for _, size := range sizes {
		m := testModel(t, size.w, size.h)
		m.latest = update.Release{Version: "v0.1.3"}
		m.updateAvailable = true

		status := m.statusView()
		if got := lipgloss.Width(status); got > size.w {
			t.Errorf("%dx%d: status bar is %d columns, want <= %d",
				size.w, size.h, got, size.w)
		}

		// On a terminal with room, the note itself has to be visible.
		if size.w >= 96 && !strings.Contains(status, "v0.1.3") {
			t.Errorf("%dx%d: the status bar does not mention the new version",
				size.w, size.h)
		}
	}
}

// A failed startup check is invisible: offline, or behind something that
// blocks GitHub, should look no different from not checking.
func TestFailedStartupCheckIsSilent(t *testing.T) {
	m := testModel(t, 96, 30)
	before := len(m.messages)

	m.handleUpdateChecked(updateCheckedMsg{err: errors.New("no route to host")})

	if len(m.messages) != before {
		t.Errorf("the failed check added %d message(s)", len(m.messages)-before)
	}
	if m.updateAvailable {
		t.Error("a failed check should not claim an update is available")
	}
}

// A check the user asked for reports either way.
func TestRequestedCheckReports(t *testing.T) {
	m := testModel(t, 96, 30)
	before := len(m.messages)

	m.handleUpdateChecked(updateCheckedMsg{err: errors.New("no route to host"), asked: true})
	if len(m.messages) == before {
		t.Error("/update said nothing about a check that failed")
	}

	m = testModel(t, 96, 30)
	m.handleUpdateChecked(updateCheckedMsg{
		release: update.Release{Version: "v0.1.2"},
		asked:   true,
	})
	if last := m.messages[len(m.messages)-1].Content; !strings.Contains(last, "latest") {
		t.Errorf("up-to-date message was %q", last)
	}
}

// An untagged build has no version to compare, and should be told that rather
// than quietly reported as up to date.
func TestRequestedCheckExplainsADevBuild(t *testing.T) {
	m := testModel(t, 96, 30)
	m.version = "dev"

	m.handleUpdateChecked(updateCheckedMsg{
		release: update.Release{Version: "v0.1.3"},
		asked:   true,
	})

	last := m.messages[len(m.messages)-1].Content
	if !strings.Contains(last, "v0.1.3") || !strings.Contains(last, "dev") {
		t.Errorf("message %q should name both the release and this build", last)
	}
}

func TestUpdateCommandOnAnInstalledUpdate(t *testing.T) {
	m := testModel(t, 96, 30)
	m.latest = update.Release{Version: "v0.1.3"}
	m.updateInstalled = true

	if cmd := cmdUpdate(m, nil); cmd != nil {
		t.Error("/update should not run anything when the update is already installed")
	}
	if last := m.messages[len(m.messages)-1].Content; !strings.Contains(last, "restart") {
		t.Errorf("message %q should ask for a restart", last)
	}
}

func TestUpdateCommandChecksWhenNothingIsKnown(t *testing.T) {
	m := testModel(t, 96, 30)

	// The command itself is not run here: it would reach the network.
	if cmd := cmdUpdate(m, nil); cmd == nil {
		t.Fatal("/update did not start a check")
	}
	if !m.checking {
		t.Error("/update did not mark a check as in flight")
	}
}

// An install with no package manager behind it gets instructions, not a
// silently skipped update.
func TestManualUpgradeExplainsItself(t *testing.T) {
	release := update.Release{
		Version: "v0.1.3",
		URL:     "https://github.com/10txn/digicli/releases/tag/v0.1.3",
	}
	got := manualUpgrade(update.Install{Method: update.Manual, Path: "/usr/local/bin/digicli"}, release)

	for _, want := range []string{"v0.1.3", release.URL, "/usr/local/bin/digicli"} {
		if !strings.Contains(got, want) {
			t.Errorf("the message does not mention %q:\n%s", want, got)
		}
	}
}

func TestUpdateFailureKeepsTheOutput(t *testing.T) {
	m := testModel(t, 96, 30)
	m.updating = true

	m.handleUpdateRan(updateRanMsg{
		version: "v0.1.3",
		output:  "Error: digicli not installed",
		err:     errors.New("brew exited with an error: exit status 1"),
	})

	if m.updating {
		t.Error("the updating flag was left set")
	}
	if m.updateInstalled {
		t.Error("a failed update was recorded as installed")
	}
	if last := m.messages[len(m.messages)-1].Content; !strings.Contains(last, "not installed") {
		t.Errorf("message %q does not include what the command printed", last)
	}
}

func TestUpdateSuccessAsksForARestart(t *testing.T) {
	m := testModel(t, 96, 30)
	m.updating = true
	m.updateAvailable = true

	m.handleUpdateRan(updateRanMsg{version: "v0.1.3", output: "🍺  digicli was upgraded"})

	if m.updating || m.updateAvailable {
		t.Error("the session still thinks an update is pending")
	}
	if !m.updateInstalled {
		t.Error("the installed update was not recorded")
	}
	if last := m.messages[len(m.messages)-1].Content; !strings.Contains(last, "Restart") {
		t.Errorf("message %q should ask for a restart", last)
	}
}

// Quitting mid-upgrade kills the package manager writing the new binary, so
// it takes a second attempt.
func TestQuitDuringAnUpdateAsksFirst(t *testing.T) {
	m := testModel(t, 96, 30)
	m.updating = true

	if cmd := m.quit(); cmd != nil {
		t.Error("the first quit should have asked rather than quit")
	}
	if last := m.messages[len(m.messages)-1].Content; !strings.Contains(last, "still installing") {
		t.Errorf("message %q does not explain why quitting waited", last)
	}
	if cmd := m.quit(); cmd == nil {
		t.Error("the second quit should have gone through")
	}
}

func TestQuitIsNotDelayedOtherwise(t *testing.T) {
	m := testModel(t, 96, 30)
	if cmd := m.quit(); cmd == nil {
		t.Error("an idle session should quit on the first ask")
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("   \n  ", 5); got != "" {
		t.Errorf("empty output rendered as %q", got)
	}

	short := lastLines("one\ntwo", 5)
	if !strings.Contains(short, "one") || !strings.Contains(short, "two") {
		t.Errorf("short output was trimmed: %q", short)
	}

	long := lastLines("1\n2\n3\n4\n5\n6", 2)
	if strings.Contains(long, "1") {
		t.Errorf("long output kept its head: %q", long)
	}
	if !strings.Contains(long, "6") || !strings.Contains(long, "…") {
		t.Errorf("long output should keep the tail and mark the cut: %q", long)
	}
}
