package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// maxPaletteRows caps how much of the chat the palette may cover.
const maxPaletteRows = 8

// palette is the slash-command menu that appears while a command is being
// typed. It filters as you type, and arrow keys move through what is left.
type palette struct {
	matches []commandSpec
	cursor  int
	// offset is the first visible row, for lists taller than maxPaletteRows.
	offset int
	// dismissedFor is the input the user pressed esc on. The palette stays
	// hidden until the input changes, so esc is not undone by the next
	// keystroke.
	dismissedFor string
	hidden       bool
}

// paletteQuery reports the command prefix being typed, if any. The palette is
// for choosing a command, so it stops as soon as there is a space: by then the
// command is chosen and the user is typing arguments.
func paletteQuery(input string) (string, bool) {
	if !strings.HasPrefix(input, "/") {
		return "", false
	}
	rest := input[1:]
	if strings.ContainsAny(rest, " \t") {
		return "", false
	}
	return strings.ToLower(rest), true
}

// sync recomputes the palette for the current input. It is called after every
// keystroke, so typing narrows the list live.
func (p *palette) sync(input string) {
	query, ok := paletteQuery(input)
	if !ok {
		p.reset()
		return
	}
	if input != p.dismissedFor {
		// The input moved on from whatever esc dismissed.
		p.hidden = false
		p.dismissedFor = ""
	}

	previous := p.currentName()
	p.matches = matchCommands(query)

	// Keep the highlight on the same command where possible, so narrowing
	// the list does not move the selection out from under the user.
	p.cursor = 0
	for i, spec := range p.matches {
		if spec.Name == previous {
			p.cursor = i
			break
		}
	}
	p.clampOffset()
}

// matchCommands returns the commands whose name starts with the query, in the
// order /help lists them.
func matchCommands(query string) []commandSpec {
	matches := make([]commandSpec, 0, len(commandList))
	for _, spec := range commandList {
		if strings.HasPrefix(spec.Name, query) {
			matches = append(matches, spec)
		}
	}
	return matches
}

func (p *palette) reset() {
	p.matches = nil
	p.cursor = 0
	p.offset = 0
	p.hidden = false
	p.dismissedFor = ""
}

// active reports whether the palette should be drawn and should take keys.
func (p *palette) active() bool {
	return !p.hidden && len(p.matches) > 0
}

func (p *palette) currentName() string {
	if spec, ok := p.selected(); ok {
		return spec.Name
	}
	return ""
}

func (p *palette) selected() (commandSpec, bool) {
	if p.cursor < 0 || p.cursor >= len(p.matches) {
		return commandSpec{}, false
	}
	return p.matches[p.cursor], true
}

// move steps the highlight, wrapping at both ends so holding an arrow key
// cycles rather than sticking.
func (p *palette) move(delta int) {
	if len(p.matches) == 0 {
		return
	}
	p.cursor = (p.cursor + delta + len(p.matches)) % len(p.matches)
	p.clampOffset()
}

// dismiss hides the palette until the input changes.
func (p *palette) dismiss(input string) {
	p.hidden = true
	p.dismissedFor = input
}

// clampOffset scrolls the window so the cursor stays visible.
func (p *palette) clampOffset() {
	rows := p.visibleRows()
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	if max := len(p.matches) - rows; p.offset > max {
		p.offset = max
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

func (p *palette) visibleRows() int {
	if len(p.matches) < maxPaletteRows {
		return len(p.matches)
	}
	return maxPaletteRows
}

// View renders the palette as a bordered box the width of the input.
func (p *palette) View(width int) string {
	if !p.active() {
		return ""
	}

	// Match the input box: border (2) plus padding (2).
	inner := width - 4
	if inner < minBodyWidth {
		inner = minBodyWidth
	}

	// Usage column, so the summaries line up.
	usageWidth := 0
	for _, spec := range p.matches {
		if w := lipgloss.Width(spec.Usage); w > usageWidth {
			usageWidth = w
		}
	}

	rows := p.visibleRows()
	lines := make([]string, 0, rows+1)

	for i := p.offset; i < p.offset+rows && i < len(p.matches); i++ {
		spec := p.matches[i]
		selected := i == p.cursor

		usage := spec.Usage
		if pad := usageWidth - lipgloss.Width(usage); pad > 0 {
			usage += strings.Repeat(" ", pad)
		}

		marker, usageStyle := "  ", paletteUsageStyle
		if selected {
			marker, usageStyle = paletteCursorStyle.Render("› "), paletteUsageSelectedStyle
		}

		line := marker + usageStyle.Render(usage) + "  " + paletteSummaryStyle.Render(spec.Summary)
		lines = append(lines, truncateStyled(line, inner))
	}

	if len(p.matches) > rows {
		lines = append(lines, paletteSummaryStyle.Render(
			formatMore(len(p.matches)-rows)))
	}

	body := strings.Join(lines, "\n")
	return paletteBoxStyle.Width(width - 2).Render(body)
}

func formatMore(n int) string {
	if n == 1 {
		return "  … 1 more"
	}
	return "  … " + itoa(n) + " more"
}

// itoa avoids pulling strconv in for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// truncateStyled trims a rendered line to width without cutting an escape
// sequence, by giving up on the tail rather than slicing runes.
func truncateStyled(line string, width int) string {
	if lipgloss.Width(line) <= width {
		return line
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

// overlayBottom draws popup over the last lines of body, keeping the total
// line count identical so the layout does not shift.
func overlayBottom(body, popup string) string {
	if popup == "" {
		return body
	}
	bodyLines := strings.Split(body, "\n")
	popupLines := strings.Split(popup, "\n")

	if len(popupLines) > len(bodyLines) {
		// Taller than the space available: show its bottom.
		popupLines = popupLines[len(popupLines)-len(bodyLines):]
	}

	start := len(bodyLines) - len(popupLines)
	for i, line := range popupLines {
		bodyLines[start+i] = line
	}
	return strings.Join(bodyLines, "\n")
}
