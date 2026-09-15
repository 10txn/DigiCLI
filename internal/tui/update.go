package tui

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/10txn/digicli/internal/update"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The update flow has three parts that stay out of each other's way: a check
// at startup that is allowed to fail silently, a status-bar note when it finds
// something, and /update, which runs the package manager in the background
// while the session carries on.

type (
	// updateCheckedMsg carries the result of a version check. asked marks one
	// the user started with /update, which reports either way; the startup
	// check only speaks up when there is something to say.
	updateCheckedMsg struct {
		release update.Release
		newer   bool
		err     error
		asked   bool
	}

	// updateRanMsg is the finished upgrade command.
	updateRanMsg struct {
		version string
		output  string
		err     error
	}
)

// checkTimeout bounds the version check. It runs at startup, so a slow or
// captive network must not hold the first frame back for long.
const checkTimeout = 5 * time.Second

// upgradeTimeout bounds the upgrade itself. Homebrew builds DigiCLI from
// source, Go toolchain and all, which is minutes rather than seconds.
const upgradeTimeout = 15 * time.Minute

// checkUpdate asks GitHub for the latest release without blocking the UI.
func checkUpdate(current string, asked bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()

		release, err := update.Latest(ctx, current)
		if err != nil {
			return updateCheckedMsg{err: err, asked: asked}
		}
		return updateCheckedMsg{
			release: release,
			newer:   update.Newer(current, release.Version),
			asked:   asked,
		}
	}
}

func (m *Model) handleUpdateChecked(msg updateCheckedMsg) tea.Cmd {
	m.checking = false

	if msg.err != nil {
		log.Printf("update check: %v", msg.err)
		if msg.asked {
			m.showError(msg.err)
		}
		// A silent failure is the point: someone offline, or behind a proxy
		// that blocks GitHub, should see no difference from not checking.
		return nil
	}

	m.latest = msg.release
	m.updateAvailable = msg.newer
	log.Printf("update check: running %s, latest %s, newer %v",
		m.version, msg.release.Version, msg.newer)

	if !msg.asked {
		return nil
	}

	if !msg.newer {
		if !update.Comparable(m.version) {
			m.addSystem(fmt.Sprintf(
				"The latest release is %s. This build reports its version as %q, "+
					"so there is nothing to compare it against — it was built "+
					"outside a release tag.", msg.release.Version, m.version))
			return nil
		}
		m.addSystem(fmt.Sprintf("DigiCLI %s is the latest release.", m.version))
		return nil
	}
	return m.startUpgrade()
}

// startUpgrade runs the upgrade command for however DigiCLI was installed, or
// explains the upgrade when there is no command to run.
func (m *Model) startUpgrade() tea.Cmd {
	install := update.Detect()
	if len(install.Command) == 0 {
		m.addSystem(manualUpgrade(install, m.latest))
		return nil
	}

	m.updating = true
	m.addSystem(fmt.Sprintf(
		"Updating %s → %s, installed with %s:\n  %s\n"+
			"This can take a few minutes — Homebrew builds from source. "+
			"The session stays usable meanwhile.",
		m.version, m.latest.Version, install, strings.Join(install.Command, " ")))

	version := m.latest.Version
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), upgradeTimeout)
		defer cancel()

		output, err := install.Run(ctx)
		return updateRanMsg{version: version, output: output, err: err}
	}
}

// manualUpgrade is the message for an install with no package manager behind
// it. Replacing the running binary is not something to do behind someone's
// back, so this stops at telling them where the new one is.
func manualUpgrade(install update.Install, release update.Release) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is available.\n\n", release.Version)
	fmt.Fprintf(&b, "This copy of DigiCLI is %s", install)
	if install.Path != "" {
		fmt.Fprintf(&b, " at %s", install.Path)
	}
	b.WriteString(", so there is no package manager to upgrade it.\n")
	b.WriteString("Download the build for your platform and replace that file:\n  ")
	b.WriteString(release.URL)
	b.WriteString("\n\nOr install it through a package manager instead:\n")
	b.WriteString("  brew install 10txn/digicli/digicli\n")
	b.WriteString("  npm install -g @digicli/cli")
	return b.String()
}

func (m *Model) handleUpdateRan(msg updateRanMsg) tea.Cmd {
	m.updating = false
	m.quitConfirmed = false

	if msg.err != nil {
		var b strings.Builder
		b.WriteString("The update did not finish: " + msg.err.Error())
		if tail := lastLines(msg.output, 10); tail != "" {
			b.WriteString("\n\n" + tail)
		}
		m.addSystem(errorStyle.Render(b.String()))
		return nil
	}

	m.updateAvailable = false
	m.updateInstalled = true

	var b strings.Builder
	fmt.Fprintf(&b, "Updated to %s. Restart DigiCLI to use it — "+
		"this session keeps running %s.", msg.version, m.version)
	if tail := lastLines(msg.output, 5); tail != "" {
		b.WriteString("\n\n" + tail)
	}
	m.addSystem(b.String())
	return nil
}

// lastLines keeps the tail of a package manager's output, which is where it
// says what it did. The whole of `brew upgrade` would bury the chat.
func lastLines(output string, n int) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	if len(lines) <= n {
		return metaStyle.Render(strings.Join(lines, "\n"))
	}
	kept := append([]string{"…"}, lines[len(lines)-n:]...)
	return metaStyle.Render(strings.Join(kept, "\n"))
}

// updateBadge is the status-bar note in the bottom right. It is empty unless
// there is something to act on, so a session with nothing to update looks
// exactly as it did before.
func (m *Model) updateBadge() string {
	switch {
	case m.updating:
		return updateBadgeStyle.Render("↻ updating…")
	case m.updateInstalled:
		return updateBadgeStyle.Render("✓ " + m.latest.Version + " — restart")
	case m.updateAvailable:
		return updateBadgeStyle.Render("↑ " + m.latest.Version + " · /update")
	default:
		return ""
	}
}

// The first-run prompt. The check contacts a third party, so it is a question
// rather than a default, and it is asked once: the answer is saved either way.

const consentPrompt = `DigiCLI can check for a new version when it starts, and say so in the status bar when there is one. /update then installs it.

This is the only request DigiCLI makes that does not go to your model endpoint. If you agree DigiCLI sends requests to api.github.com for the latest release tag, so it needs an internet connection.

You can change this at any time in /settings.`

// consentPromptShort is the same question for a terminal with no room for the
// long version. What it must not lose is what gets contacted.
const consentPromptShort = `Check api.github.com at startup for a new release, and say so in the status bar? Needs an internet connection. /settings changes it later.`

func (m *Model) consentView() string {
	inner := m.width - 8
	if inner > settingsMaxWidth-settingsPadW {
		inner = settingsMaxWidth - settingsPadW
	}
	if inner < minBodyWidth {
		inner = minBodyWidth
	}

	// The panel's own furniture: border and padding (consentChromeH), plus
	// the title and the key hint. What is left belongs to the explanation.
	const consentChromeH = 4
	body, spaced := consentBody(inner, m.height-consentChromeH-2)

	sections := []string{settingsTitleStyle.Render("Check for updates?")}
	if spaced {
		sections = append(sections, "")
	}
	sections = append(sections, body)
	if spaced {
		sections = append(sections, "")
	}
	sections = append(sections, settingsHintStyle.Render(
		truncate("y  check at startup  ·  n  never check", inner)))

	// Width counts the padding but not the border, as in the settings pane.
	panel := settingsPanelStyle.Width(inner + settingsPadW).
		Render(strings.Join(sections, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

// consentBody fits the explanation into the lines available, giving up detail
// before it gives up the question: a short terminal gets the short version of
// the text, and a very short one gets as much of that as there is room for.
func consentBody(width, budget int) (body string, spaced bool) {
	render := func(text string) []string {
		return strings.Split(settingsDescStyle.Width(width).Render(text), "\n")
	}

	lines := render(consentPrompt)
	spaced = true
	if len(lines)+2 > budget {
		lines = render(consentPromptShort)
	}
	if len(lines)+2 > budget {
		// Reclaim the blank lines either side rather than cut more text.
		spaced = false
	}

	limit := budget
	if spaced {
		limit -= 2
	}
	if limit < 1 {
		limit = 1
	}
	if len(lines) > limit {
		lines = lines[:limit]
		lines[limit-1] = settingsDescStyle.Render("…")
	}
	return strings.Join(lines, "\n"), spaced
}

// handleConsentKey answers the first-run prompt. Anything that is not an
// answer is ignored rather than dismissing it, since an unanswered prompt is
// the only state that gets asked again.
func (m *Model) handleConsentKey(msg tea.KeyMsg) tea.Cmd {
	var enabled bool
	switch msg.String() {
	case "y", "Y", "enter":
		enabled = true
	case "n", "N", "esc":
		enabled = false
	default:
		return nil
	}
	return m.answerConsent(enabled)
}

func (m *Model) answerConsent(enabled bool) tea.Cmd {
	m.cfg.UpdateCheck = enabled
	m.cfg.UpdatePrompted = true
	m.view = viewChat

	if err := m.cfg.Save(); err != nil {
		// Worth saying out loud: an unsaved answer means being asked again.
		m.addSystem(errorStyle.Render(
			"Could not save that answer, so DigiCLI will ask again next time: " +
				err.Error()))
	}

	if !enabled {
		m.addSystem("Update checks are off. Turn them on in /settings, or run " +
			"/update to check once.")
		return nil
	}

	m.addSystem("Update checks are on. /settings turns them off again.")
	m.checking = true
	return checkUpdate(m.version, false)
}
