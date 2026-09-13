package tui

import "github.com/charmbracelet/lipgloss"

// Adaptive colours so the UI stays readable on light and dark terminals.
var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#7D56F4", Dark: "#A98FFF"}
	colorUser   = lipgloss.AdaptiveColor{Light: "#0B7285", Dark: "#4DD0E1"}
	colorAI     = lipgloss.AdaptiveColor{Light: "#2B8A3E", Dark: "#69DB7C"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "#6C757D", Dark: "#8A8F98"}
	colorTool   = lipgloss.AdaptiveColor{Light: "#8250DF", Dark: "#B392F0"}
	colorError  = lipgloss.AdaptiveColor{Light: "#C92A2A", Dark: "#FF6B6B"}
	colorBorder = lipgloss.AdaptiveColor{Light: "#CED4DA", Dark: "#3A3F47"}
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	// BorderStyle alone does not switch any side on, so the bottom rule has
	// to be enabled explicitly.
	headerStyle = lipgloss.NewStyle().
			Padding(0, 1).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(colorBorder).
			BorderBottom(true)

	// Messages carry no speaker labels, so colour does the work.
	userMessageStyle = lipgloss.NewStyle().
				Foreground(colorUser).
				Bold(true)

	messageStyle = lipgloss.NewStyle()

	systemMessageStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	// Tool activity sits between chat and notice: visible, but never
	// competing with the reply itself.
	toolMessageStyle = lipgloss.NewStyle().
				Foreground(colorTool)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError)

	inputBoxStyle = lipgloss.NewStyle().
			Padding(0, 1).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder)

	hintStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	// Slash-command palette, drawn over the bottom of the chat.
	paletteBoxStyle = lipgloss.NewStyle().
			Padding(0, 1).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent)

	paletteUsageStyle = lipgloss.NewStyle()

	paletteUsageSelectedStyle = lipgloss.NewStyle().
					Foreground(colorAccent).
					Bold(true)

	paletteSummaryStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	paletteCursorStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	statusBarStyle = lipgloss.NewStyle().
			Padding(0, 1)

	metaStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	// Status-bar segments. Each mode gets its own colour so the current one
	// is recognisable at a glance rather than by reading it.
	modeStyles = map[string]lipgloss.Style{
		"plan":   badge(lipgloss.AdaptiveColor{Light: "#1971C2", Dark: "#74C0FC"}),
		"manual": badge(lipgloss.AdaptiveColor{Light: "#E8590C", Dark: "#FFA94D"}),
		"auto":   badge(lipgloss.AdaptiveColor{Light: "#2F9E44", Dark: "#8CE99A"}),
	}

	modelStyle = lipgloss.NewStyle().
			Foreground(colorAI)

	contextStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	contextWarnStyle = lipgloss.NewStyle().
				Foreground(colorError).
				Bold(true)
)

func badge(color lipgloss.TerminalColor) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(color).Bold(true)
}

// Settings pane.
var (
	settingsPanelStyle = lipgloss.NewStyle().
				Padding(1, 2).
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(colorAccent)

	settingsTitleStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	settingsLabelStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	settingsLabelSelectedStyle = lipgloss.NewStyle().
					Foreground(colorAccent).
					Bold(true)

	settingsValueStyle = lipgloss.NewStyle()

	settingsValueSelectedStyle = lipgloss.NewStyle().
					Bold(true)

	settingsCursorStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	settingsDescStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true)

	settingsHintStyle = lipgloss.NewStyle().
				Foreground(colorMuted)
)
