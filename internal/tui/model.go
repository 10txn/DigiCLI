// Package tui implements DigiCLI's terminal interface: a scrollable chat
// history above a text input, driven by Bubble Tea.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/10txn/digicli/internal/config"
	"github.com/10txn/digicli/internal/llm"
	"github.com/10txn/digicli/internal/tool"
	"github.com/10txn/digicli/internal/types"
	"github.com/10txn/digicli/internal/update"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	// minBodyWidth keeps message text readable in very narrow terminals.
	minBodyWidth = 20
	// statusHeight is the single status bar under the input box.
	statusHeight = 1
	// minWidth and minHeight are the smallest terminal the full layout fits
	// in; below that we show a prompt to resize rather than a broken frame.
	minWidth  = 40
	minHeight = 10
)

// view identifies which screen is on top.
type view int

const (
	viewChat view = iota
	viewSettings
	// viewUpdateConsent is the first-run question about update checking. It
	// is shown before anything else and cannot be reached again: the answer
	// is saved, and /settings owns it from then on.
	viewUpdateConsent
)

// Model is the root Bubble Tea model.
type Model struct {
	cfg      *config.Config
	messages []types.Message

	view     view
	settings settingsModel

	viewport viewport.Model
	input    textinput.Model

	width  int
	height int
	ready  bool

	// busy marks an in-flight background request, such as listing models.
	busy bool

	// Streaming state. streamSeq identifies the current reply so that late
	// chunks from an interrupted one can be discarded.
	streaming bool
	// tooling covers the gap between a reply that ended in tool calls and
	// the reply that follows their results. No stream is open across it,
	// but the turn is still live: ctrl+c has to interrupt rather than quit,
	// and enter must not start a second turn on top of it.
	tooling   bool
	streamSeq int
	stream    llm.Stream
	cancel    context.CancelFunc
	// pendingCalls collects the tools a turn asked for, run once it ends.
	pendingCalls []types.ToolCall
	// toolRounds guards against a model looping on its own tools.
	toolRounds int

	// tools are the capabilities the model may call.
	tools *tool.Registry

	// commands is the slash-command palette shown while typing a command.
	commands palette

	// Update state. version is what this build was stamped with; latest is
	// filled in by a successful check, and the three flags decide what the
	// status bar says about it.
	version         string
	latest          update.Release
	checking        bool
	updateAvailable bool
	updating        bool
	// updateInstalled marks an upgrade that has been written to disk but
	// cannot apply until the session restarts.
	updateInstalled bool
	// quitConfirmed records that quitting during an update has been asked
	// about once already.
	quitConfirmed bool

	// err holds a problem worth reporting on stderr after the TUI exits,
	// such as a failure to save the config.
	err error
}

// New builds the initial model and seeds the welcome message. version is the
// build stamp from main, which the update check compares against the latest
// release.
func New(cfg *config.Config, version string) *Model {
	input := textinput.New()
	input.Placeholder = "Ask anything, or type /help"
	input.Prompt = "› "
	input.PromptStyle = lipgloss.NewStyle().Foreground(colorAccent)
	input.CharLimit = 0
	input.Focus()

	m := &Model{
		cfg:      cfg,
		input:    input,
		settings: newSettings(cfg),
		version:  version,
	}

	// A config that has never answered the update question asks it before the
	// session starts, rather than checking silently or never mentioning it.
	if !cfg.UpdatePrompted {
		m.view = viewUpdateConsent
	}

	// Tools are confined to the directory DigiCLI was started in.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	tools, err := buildTools(cwd)
	m.tools = tools

	m.addSystem(welcome())
	if err != nil {
		m.addSystem(errorStyle.Render(
			"File tools are unavailable: " + err.Error()))
	}
	return m
}

func welcome() string {
	return "Welcome to DigiCLI. Type a message and press enter, or /help for commands.\n" +
		"/models lists your local models, /settings changes anything else."
}

// Err returns an error worth printing after the program exits, if any.
func (m *Model) Err() error { return m.err }

func (m *Model) Init() tea.Cmd {
	// The check waits for an answer when the prompt is up; answering it
	// starts one.
	if m.view != viewUpdateConsent && m.cfg.UpdateCheck {
		m.checking = true
		return tea.Batch(textinput.Blink, checkUpdate(m.version, false))
	}
	return textinput.Blink
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil

	case modelsMsg:
		m.busy = false
		m.handleModels(msg)
		return m, nil

	case streamStartedMsg:
		return m, m.handleStreamStarted(msg)

	case streamChunkMsg:
		return m, m.handleStreamChunk(msg)

	case streamDoneMsg:
		return m, m.handleStreamDone(msg)

	case toolsDoneMsg:
		return m, m.handleToolsDone(msg)

	case updateCheckedMsg:
		return m, m.handleUpdateChecked(msg)

	case updateRanMsg:
		return m, m.handleUpdateRan(msg)

	case tea.KeyMsg:
		// Ctrl+C interrupts a turn in progress — streaming or running the
		// tools it asked for — and otherwise quits from anywhere,
		// including the settings pane.
		if msg.Type == tea.KeyCtrlC {
			if m.streaming || m.tooling {
				m.interrupt()
				return m, nil
			}
			return m, m.quit()
		}
		switch m.view {
		case viewUpdateConsent:
			return m, m.handleConsentKey(msg)
		case viewSettings:
			closed, cmd := m.settings.Update(msg)
			if closed {
				return m, m.closeSettings()
			}
			return m, cmd
		}
		return m, m.handleChatKey(msg)
	}

	// Anything else (cursor blink, and so on) belongs to the text input.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleChatKey(msg tea.KeyMsg) tea.Cmd {
	// The palette owns navigation and selection while it is open.
	if m.commands.active() {
		switch msg.Type {
		case tea.KeyUp:
			m.commands.move(-1)
			return nil
		case tea.KeyDown:
			m.commands.move(1)
			return nil
		case tea.KeyEsc:
			m.commands.dismiss(m.input.Value())
			return nil
		case tea.KeyEnter, tea.KeyTab:
			return m.acceptCommand()
		}
	}

	switch msg.Type {
	case tea.KeyCtrlD:
		// EOF only on an empty prompt, as a shell would.
		if m.input.Value() == "" {
			return m.quit()
		}
	case tea.KeyEnter:
		if m.streaming || m.tooling {
			return nil // still working on the last turn; ctrl+c interrupts
		}
		return m.submit()
	case tea.KeyTab:
		m.cycleMode()
		return nil
	case tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return cmd
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.commands.sync(m.input.Value())
	return cmd
}

// acceptCommand applies the highlighted palette entry. A command that needs
// arguments is completed into the input so they can be typed; one that does
// not is run straight away, since there is nothing left to add.
func (m *Model) acceptCommand() tea.Cmd {
	spec, ok := m.commands.selected()
	if !ok {
		return nil
	}

	if spec.TakesArgs {
		m.input.SetValue("/" + spec.Name + " ")
		m.input.CursorEnd()
		m.commands.reset()
		return nil
	}

	m.input.SetValue("/" + spec.Name)
	m.commands.reset()
	return m.submit()
}

func (m *Model) View() string {
	if !m.ready {
		return "Starting DigiCLI…"
	}
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("Terminal too small — need at least %d×%d, have %d×%d.",
			minWidth, minHeight, m.width, m.height)
	}
	switch m.view {
	case viewSettings:
		return m.settings.View()
	case viewUpdateConsent:
		return m.consentView()
	}
	return strings.Join([]string{
		m.headerView(),
		overlayBottom(m.viewport.View(), m.commands.View(m.width)),
		m.inputView(),
		m.statusView(),
	}, "\n")
}

// resize recomputes the layout for a new terminal size. The viewport takes
// whatever vertical space the header, input box and status bar leave behind.
func (m *Model) resize(width, height int) {
	m.width, m.height = width, height

	// Input box: rounded border (2) plus horizontal padding (2).
	const inputChrome = 4
	inputWidth := width - inputChrome - lipgloss.Width(m.input.Prompt)
	if inputWidth < minBodyWidth {
		inputWidth = minBodyWidth
	}
	m.input.Width = inputWidth

	// View joins the sections with newlines, which separate rather than add
	// lines, so the chrome is just the sum of the section heights.
	chrome := lipgloss.Height(m.headerView()) +
		lipgloss.Height(m.inputView()) +
		statusHeight

	viewportHeight := height - chrome
	if viewportHeight < 1 {
		viewportHeight = 1
	}

	if !m.ready {
		m.viewport = viewport.New(width, viewportHeight)
		m.ready = true
	} else {
		m.viewport.Width = width
		m.viewport.Height = viewportHeight
	}
	m.settings.resize(width, height)
	m.refresh()
}

// headerView is the title bar. Provider, model, mode and context usage all
// live in the status bar at the bottom instead.
func (m *Model) headerView() string {
	title := titleStyle.Render("DigiCLI")
	right := metaStyle.Render(m.cfg.Provider)

	gap := m.width - lipgloss.Width(title) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return headerStyle.Width(m.width).Render(title + strings.Repeat(" ", gap) + right)
}

func (m *Model) inputView() string {
	return inputBoxStyle.Width(m.width - 2).Render(m.input.View())
}

// statusView is the bottom bar: mode, active model and context usage on the
// left, a short key hint on the right. Narrow terminals shed the least
// important segments rather than wrapping onto a second line.
func (m *Model) statusView() string {
	// One column of padding each side.
	available := m.width - 2

	modeName := m.cfg.Mode.String()
	modeStyle, ok := modeStyles[modeName]
	if !ok {
		modeStyle = metaStyle
	}
	sep := metaStyle.Render(" · ")

	// Segments in order of what to drop first when space runs short.
	type segment struct {
		text     string
		required bool
	}
	segments := []segment{
		{modeStyle.Render(modeName), true},
		{modelStyle.Render(truncate(m.cfg.Model, available/2)), true},
		{m.contextView(), false},
	}
	if m.streaming {
		segments = append(segments, segment{metaStyle.Render("streaming…"), false})
	} else if m.busy {
		segments = append(segments, segment{metaStyle.Render("working…"), false})
	}

	build := func(keepOptional bool) string {
		parts := make([]string, 0, len(segments))
		for _, s := range segments {
			if s.required || keepOptional {
				parts = append(parts, s.text)
			}
		}
		return strings.Join(parts, sep)
	}

	left := build(true)
	if lipgloss.Width(left) > available {
		left = build(false)
	}

	hint := "tab mode · /help · ctrl+c quit"
	if m.streaming || m.tooling {
		hint = "ctrl+c interrupts"
	}
	right := hintStyle.Render(hint)

	// An available update is the more interesting thing to have in the corner
	// than a hint the user has already read, so the hint is what goes when
	// the two cannot both fit — by the one column of gap the padding below
	// insists on, not merely by width.
	if badge := m.updateBadge(); badge != "" {
		both := badge + sep + right
		if lipgloss.Width(left)+lipgloss.Width(both)+1 <= available {
			right = both
		} else {
			right = badge
		}
	}

	gap := available - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// No room for the hint; pad out to the full width instead.
		right = ""
		gap = available - lipgloss.Width(left)
	}
	if gap < 0 {
		gap = 0
	}

	return statusBarStyle.Render(left + strings.Repeat(" ", gap) + right)
}

// showError renders an error into the chat. Go error strings start lowercase
// by convention, which reads oddly as a sentence in the UI.
func (m *Model) showError(err error) {
	m.addSystem(errorStyle.Render(capitalize(err.Error())))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	upper := unicode.ToUpper(runes[0])
	if upper == runes[0] {
		return s // already capitalised, or not a letter
	}
	runes[0] = upper
	return string(runes)
}

// truncate shortens a string to width columns, marking the cut with an
// ellipsis. It counts runes so multi-byte names are not split.
func truncate(s string, width int) string {
	if width < 1 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

// contextView renders estimated context usage, turning red once the history
// is close to filling the window.
func (m *Model) contextView() string {
	used := estimateTokens(m.messages)
	total := m.cfg.ContextWindow

	percent := 0
	if total > 0 {
		percent = used * 100 / total
	}

	text := fmt.Sprintf("ctx %s/%s (%d%%)", formatTokens(used), formatTokens(total), percent)
	if percent >= 80 {
		return contextWarnStyle.Render(text)
	}
	return contextStyle.Render(text)
}

// formatTokens abbreviates counts so the status bar stays narrow.
func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// submit handles the current input line: a slash command runs locally,
// anything else becomes a chat message.
func (m *Model) submit() tea.Cmd {
	line := strings.TrimSpace(m.input.Value())
	if line == "" {
		return nil
	}
	m.input.Reset()
	m.commands.reset()

	// A command is echoed so the transcript reads back correctly, but it is
	// a local event: sending it would have the model treat "/models" as
	// something the user said to it.
	if cmd, ok := parseCommand(line); ok {
		m.append(types.NewLocal(types.RoleUser, line))
		return m.runCommand(cmd)
	}

	m.append(types.NewMessage(types.RoleUser, line))
	m.toolRounds = 0

	return m.startStream()
}

func (m *Model) cycleMode() {
	m.cfg.Mode = m.cfg.Mode.Next()
	m.addSystem(fmt.Sprintf("Mode: %s", m.cfg.Mode))
}

func (m *Model) openSettings() {
	m.view = viewSettings
	m.settings.status = ""
	m.settings.resize(m.width, m.height)
}

// closeSettings saves whatever was changed in the pane and returns to chat.
// Switching update checks on there is the one change that has something to do
// straight away, so it starts a check rather than waiting for the next start.
func (m *Model) closeSettings() tea.Cmd {
	m.view = viewChat
	if err := m.cfg.Save(); err != nil {
		m.addSystem(errorStyle.Render("Could not save settings: " + err.Error()))
		return nil
	}
	m.addSystem("Settings saved.")

	if m.cfg.UpdateCheck && !m.checking && m.latest.Version == "" {
		m.checking = true
		return checkUpdate(m.version, false)
	}
	return nil
}

// append adds a message and scrolls to it.
func (m *Model) append(msg types.Message) {
	m.messages = append(m.messages, msg)
	m.refresh()
}

func (m *Model) addSystem(content string) {
	m.append(types.NewLocal(types.RoleSystem, content))
}

// refresh re-renders the history into the viewport and pins it to the bottom.
func (m *Model) refresh() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(renderMessages(m.messages, m.viewport.Width, m.streaming))
	m.viewport.GotoBottom()
}

// quit persists settings that the session may have changed, then stops the
// program. A save failure is surfaced on stderr by main rather than lost.
func (m *Model) quit() tea.Cmd {
	// Leaving takes the package manager doing the upgrade down with it: it
	// writes its output to a pipe this process owns. That is worth asking
	// about once, rather than discovering it afterwards.
	if m.updating && !m.quitConfirmed {
		m.quitConfirmed = true
		m.addSystem("An update is still installing. Quit again to stop waiting for " +
			"it — the upgrade would be interrupted, and may need running again.")
		return nil
	}

	if m.cancel != nil {
		m.cancel()
	}
	if err := m.cfg.Save(); err != nil {
		m.err = err
	}
	return tea.Quit
}
