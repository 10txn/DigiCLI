package tui

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/10txn/digicli/internal/agent"
	"github.com/10txn/digicli/internal/tool"
	"github.com/10txn/digicli/internal/types"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Approval is manual mode's half of the bargain: the model may propose
// anything, and nothing happens until the user has seen it and said yes. The
// question goes into the transcript rather than over the top of it, so the
// round's earlier results stay visible above it — what the model already read
// is usually the reason the write it is proposing makes sense.

// Panel sizing for the viewer. It borrows the settings pane's chrome so the
// two read as the same kind of surface, but runs wider: this one is showing
// code, and wrapping it early makes it harder to read than it has to be.
const (
	viewerMaxWidth = 100
	// viewerFixedRows counts the header, the two blank separators and the
	// footer — everything in the panel that is not the file itself.
	viewerFixedRows = 4
)

// pendingApproval is one call stopped in front of the user, with the round it
// belongs to still waiting behind it.
type pendingApproval struct {
	call  types.ToolCall
	reach types.Reach
	// preview is worked out once, when the question is asked, so what the
	// viewer shows and what the prompt claims cannot drift apart — and so
	// re-rendering the transcript does not re-run it.
	preview tool.Preview
}

// ask puts a call to the user and stops the round until it is answered.
func (m *Model) ask(t tool.Tool, call types.ToolCall, reach types.Reach) {
	log.Printf("tool: %s awaiting approval (outside=%v)", call.Name, reach.Outside)

	pending := &pendingApproval{call: call, reach: reach}
	if previewer, ok := t.(tool.Previewer); ok {
		pending.preview = previewer.Preview(call)
	}
	m.approval = pending
	m.addSystem(m.approvalPrompt(pending))
}

// approvalPrompt is what the user reads before deciding. It leads with the one
// thing that decides the answer — which file, and whether it is somewhere the
// session has no business being — then says what the call would do in a line.
//
// The file itself is deliberately not here. A few hundred lines pasted into the
// transcript push the question off the top of the screen and read as output
// rather than as a proposal, so the content lives behind o instead, where it
// can be scrolled properly and answered from.
func (m *Model) approvalPrompt(pending *pendingApproval) string {
	var b strings.Builder

	b.WriteString(approvalTitleStyle.Render("⚠ " + pending.call.Name + " needs your approval"))
	b.WriteString("\n")

	if pending.reach.Outside {
		b.WriteString(approvalOutsideStyle.Render(
			"This is outside the working directory: " + m.display(pending.reach.Path)))
		b.WriteString("\n")
	}

	switch {
	case pending.preview.Summary != "":
		b.WriteString(approvalBodyStyle.Render(pending.preview.Summary))
		b.WriteString("\n")
	case pending.reach.Path != "":
		b.WriteString(approvalBodyStyle.Render(m.display(pending.reach.Path)))
		b.WriteString("\n")
	}

	keys := "y approve · n deny · ctrl+c stop the turn"
	if pending.preview.Body != "" {
		keys = fmt.Sprintf("y approve · n deny · o open in cli (%d %s) · ctrl+c stop the turn",
			countLines(pending.preview.Body), plural(countLines(pending.preview.Body), "line"))
	}
	b.WriteString(approvalKeysStyle.Render(keys))
	return b.String()
}

// display renders a path the way the sandbox would, falling back to the raw
// path when there is no sandbox to ask.
func (m *Model) display(path string) string {
	if m.sandbox == nil {
		return path
	}
	return m.sandbox.Display(path)
}

// awaitingApproval reports whether a question is on screen. While it is, the
// keyboard belongs to the answer rather than to the input box.
func (m *Model) awaitingApproval() bool { return m.approval != nil }

// handleApprovalKey answers the prompt. Anything that is not an answer is
// ignored: an unanswered question is the only state that makes sense to stay
// in, and enter must not be a way to approve a write by muscle memory.
func (m *Model) handleApprovalKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		return m.approve()
	case "n", "N", "esc":
		return m.deny()
	case "o", "O":
		m.openViewer()
	}
	return nil
}

// openViewer shows the full proposal in a pane of its own, for a change too
// long to judge from a summary. There is nothing to open for a call with no
// body, and the key is not offered in that case.
func (m *Model) openViewer() {
	if m.approval == nil || m.approval.preview.Body == "" {
		return
	}
	m.view = viewApproval
	m.viewerReady = false
	m.resizeViewer()
}

// closeViewer returns to the chat, leaving the question unanswered. It is also
// how the pane comes down when the round it belongs to goes away.
func (m *Model) closeViewer() {
	if m.view == viewApproval {
		m.view = viewChat
	}
}

// handleViewerKey drives the pane: the same two answers as the prompt, plus
// scrolling, plus a way back out without answering.
func (m *Model) handleViewerKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		m.closeViewer()
		return m.approve()
	case "n", "N":
		m.closeViewer()
		return m.deny()
	case "esc", "q", "o", "O":
		m.closeViewer()
		return nil
	case "home", "g":
		m.viewer.GotoTop()
		return nil
	case "end", "G":
		m.viewer.GotoBottom()
		return nil
	}

	var cmd tea.Cmd
	m.viewer, cmd = m.viewer.Update(msg)
	return cmd
}

// resizeViewer fits the pane to the terminal and loads the proposal into it.
func (m *Model) resizeViewer() {
	outer := m.width - 4
	if outer > viewerMaxWidth {
		outer = viewerMaxWidth
	}
	if outer < minBodyWidth+settingsBorderW+settingsPadW {
		outer = minBodyWidth + settingsBorderW + settingsPadW
	}
	inner := outer - settingsBorderW - settingsPadW

	height := m.height - settingsChromeH - viewerFixedRows
	if height < 1 {
		height = 1
	}

	offset := m.viewer.YOffset
	m.viewer = viewport.New(inner, height)
	if m.approval != nil {
		m.viewer.SetContent(numberedBody(m.approval.preview.Body, inner))
	}
	// Keep the reader's place across a resize, but not across a new question.
	if m.viewerReady {
		m.viewer.SetYOffset(offset)
	}
	m.viewerReady = true
}

// numberedBody renders the proposal with line numbers, wrapping rather than
// truncating: this is the one place the user sees everything they are about to
// approve, so nothing may be cut off the right-hand side.
func numberedBody(body string, width int) string {
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")

	gutter := len(strconv.Itoa(len(lines)))
	textWidth := width - gutter - 1
	if textWidth < 1 {
		textWidth = 1
	}
	wrap := lipgloss.NewStyle().Width(textWidth)

	out := make([]string, 0, len(lines))
	for i, line := range lines {
		// A tab renders as one column in a terminal but indents by more, which
		// throws the wrap width out; spaces keep the arithmetic honest.
		line = strings.ReplaceAll(line, "\t", "    ")
		for j, segment := range strings.Split(wrap.Render(line), "\n") {
			number := strings.Repeat(" ", gutter)
			if j == 0 {
				number = fmt.Sprintf("%*d", gutter, i+1)
			}
			out = append(out, viewerGutterStyle.Render(number)+" "+segment)
		}
	}
	return strings.Join(out, "\n")
}

// viewerView is the pane itself: the same panel the settings pane uses, so the
// two read as the same kind of thing.
func (m *Model) viewerView() string {
	if m.approval == nil {
		return ""
	}
	inner := m.viewer.Width

	title := settingsTitleStyle.Render(m.approval.call.Name)
	header := title + "  " + metaStyle.Render(
		truncate(m.approval.preview.Summary, inner-lipgloss.Width(title)-2))

	position := fmt.Sprintf("%3.0f%%", m.viewer.ScrollPercent()*100)
	keys := "y approve · n deny · ↑↓ scroll · esc back"
	footer := approvalKeysStyle.Render(keys) +
		settingsHintStyle.Render(strings.Repeat(" ", max(1, inner-lipgloss.Width(keys)-len(position)))+position)

	sections := []string{header, "", m.viewer.View(), "", footer}
	panel := settingsPanelStyle.Width(inner + settingsPadW).Render(strings.Join(sections, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

// countLines counts the lines the body will show, the way an editor counts: a
// trailing newline ends the last line rather than starting an empty one.
func countLines(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(content, "\n"), "\n") + 1
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// approve runs the call. For a path outside the working directory this is also
// the grant: execute opens the sandbox for that exact path, and it stays open
// for the rest of the session, so a model working through a file the user has
// already allowed does not ask again about the same one. Changing mode closes
// them all again.
func (m *Model) approve() tea.Cmd {
	pending := m.approval
	m.approval = nil
	m.closeViewer()
	if pending == nil || m.run == nil {
		return nil
	}

	log.Printf("tool: %s approved", pending.call.Name)
	m.addSystem(approvalGrantedStyle.Render("✓ Approved " + summarise(pending.call)))
	return m.execute(pending.call, pending.reach)
}

// deny tells the model no and carries on with the rest of the round. The
// refusal goes back as a result rather than ending the turn: the model should
// get the chance to say what it would do instead.
func (m *Model) deny() tea.Cmd {
	pending := m.approval
	m.approval = nil
	m.closeViewer()
	if pending == nil || m.run == nil {
		return nil
	}

	log.Printf("tool: %s denied by the user", pending.call.Name)
	m.append(toolResult(pending.call, agent.RefusalReason(pending.call.Name),
		approvalDeniedStyle.Render(fmt.Sprintf("✗ Denied %s", summarise(pending.call)))))
	return m.nextTool()
}
