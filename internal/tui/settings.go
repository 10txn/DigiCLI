package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/10txn/digicli/internal/config"
	"github.com/10txn/digicli/internal/types"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// settingField is one editable row in the settings pane. Fields with Options
// cycle through fixed choices; the rest open a text input.
type settingField struct {
	Label  string
	Desc   string
	Secret bool
	// Options, when set, makes this a cycling field rather than a text one.
	Options []string
	Get     func(*config.Config) string
	Set     func(*config.Config, string) error
}

// settingsFields is the layout of the settings pane, top to bottom.
var settingsFields = []settingField{
	{
		Label:   "provider",
		Desc:    "Which backend answers your messages.",
		Options: config.Providers,
		Get:     func(c *config.Config) string { return c.Provider },
		Set: func(c *config.Config, v string) error {
			for _, p := range config.Providers {
				if p == v {
					c.Provider = v
					return nil
				}
			}
			return fmt.Errorf("unknown provider %q", v)
		},
	},
	{
		Label: "model",
		Desc:  "Model name passed to the provider. /models lists what is installed.",
		Get:   func(c *config.Config) string { return c.Model },
		Set: func(c *config.Config, v string) error {
			if v == "" {
				return fmt.Errorf("model cannot be empty")
			}
			c.Model = v
			return nil
		},
	},
	{
		Label:   "mode",
		Desc:    "plan reads only · manual asks first · auto runs unattended.",
		Options: []string{"plan", "manual", "auto"},
		Get:     func(c *config.Config) string { return c.Mode.String() },
		Set: func(c *config.Config, v string) error {
			mode, err := types.ParseMode(v)
			if err != nil {
				return err
			}
			c.Mode = mode
			return nil
		},
	},
	{
		Label: "ollama endpoint",
		Desc:  "Base URL of the local Ollama server.",
		Get:   func(c *config.Config) string { return c.OllamaEndpoint },
		Set: func(c *config.Config, v string) error {
			if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
				return fmt.Errorf("endpoint must start with http:// or https://")
			}
			c.OllamaEndpoint = v
			return nil
		},
	},
	{
		Label: "context window",
		Desc:  "Tokens the model can hold, used for the usage readout.",
		Get:   func(c *config.Config) string { return strconv.Itoa(c.ContextWindow) },
		Set: func(c *config.Config, v string) error {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n <= 0 {
				return fmt.Errorf("context window must be a positive number")
			}
			c.ContextWindow = n
			return nil
		},
	},
	{
		Label:   "update checks",
		Desc:    "Ask api.github.com for the latest release at startup. Off means DigiCLI contacts nothing but your model endpoint.",
		Options: []string{"on", "off"},
		Get: func(c *config.Config) string {
			if c.UpdateCheck {
				return "on"
			}
			return "off"
		},
		Set: func(c *config.Config, v string) error {
			switch v {
			case "on":
				c.UpdateCheck = true
			case "off":
				c.UpdateCheck = false
			default:
				return fmt.Errorf("update checks are either on or off")
			}
			// Setting it here answers the first-run question too, so nobody
			// who has been to this row is asked about it again.
			c.UpdatePrompted = true
			return nil
		},
	},
	{
		Label:  "claude api key",
		Desc:   "Stored in plain text in ~/.digicli/config.json (file mode 0600).",
		Secret: true,
		Get:    func(c *config.Config) string { return c.APIKeys["claude"] },
		Set: func(c *config.Config, v string) error {
			c.APIKeys["claude"] = strings.TrimSpace(v)
			return nil
		},
	},
	{
		Label:  "openai api key",
		Desc:   "Stored in plain text in ~/.digicli/config.json (file mode 0600).",
		Secret: true,
		Get:    func(c *config.Config) string { return c.APIKeys["openai"] },
		Set: func(c *config.Config, v string) error {
			c.APIKeys["openai"] = strings.TrimSpace(v)
			return nil
		},
	},
}

// settingsModel is the modal settings pane. It edits the live *config.Config
// and saves on close.
type settingsModel struct {
	cfg      *config.Config
	cursor   int
	editing  bool
	input    textinput.Model
	viewport viewport.Model
	// status is plain text; statusErr picks the colour at render time so
	// the line can be truncated without cutting an escape sequence.
	status    string
	statusErr bool
	width     int
	height    int
	// showDesc is cleared when the terminal is too short to spare the lines.
	showDesc bool
}

func newSettings(cfg *config.Config) settingsModel {
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = 0

	return settingsModel{
		cfg:      cfg,
		input:    input,
		viewport: viewport.New(0, 0),
	}
}

// Panel sizing. The modal is centred with a rounded border and padding, capped
// so it stays readable on very wide terminals.
//
// lipgloss's Width() counts padding but not the border, so a panel rendered at
// Width(w) occupies w+2 columns; settingsBorderW accounts for that and
// settingsPadW for the padding inside it.
const (
	settingsMaxWidth = 78
	settingsBorderW  = 2 // left and right border runes
	settingsPadW     = 4 // Padding(1, 2): two columns each side
	settingsChromeH  = 4 // border top/bottom plus one padding row each
	// settingsFixedRows counts the title, two blank separators and the
	// footer — everything in the panel that is not a field row or the
	// description.
	settingsFixedRows = 4
)

func (s *settingsModel) resize(width, height int) {
	s.width, s.height = width, height

	// Leave a small margin so the modal reads as floating over the chat.
	outer := width - 4
	if outer > settingsMaxWidth {
		outer = settingsMaxWidth
	}
	if outer < minBodyWidth+settingsBorderW+settingsPadW {
		outer = minBodyWidth + settingsBorderW + settingsPadW
	}
	inner := outer - settingsBorderW - settingsPadW
	s.viewport.Width = inner

	// Fit the rows into whatever the fixed furniture leaves behind, giving
	// up the description before giving up rows.
	budget := height - settingsChromeH
	descHeight := s.maxDescHeight(inner)

	s.showDesc = true
	rows := budget - settingsFixedRows - descHeight
	if rows < 1 {
		s.showDesc = false
		rows = budget - settingsFixedRows
	}
	if rows > len(settingsFields) {
		rows = len(settingsFields)
	}
	if rows < 1 {
		rows = 1
	}

	s.viewport.Height = rows
	// The rows go in before scrolling: a viewport with no content clamps every
	// offset to zero, so ensureVisible would silently do nothing the first
	// time the pane is sized. View sets them again with the cursor drawn in.
	s.viewport.SetContent(s.rows())
	s.ensureVisible()
}

// maxDescHeight is the tallest description at this width. Sizing to the worst
// case keeps the panel from resizing as the cursor moves.
func (s *settingsModel) maxDescHeight(width int) int {
	tallest := 1
	for _, field := range settingsFields {
		h := lipgloss.Height(settingsDescStyle.Width(width).Render(field.Desc))
		if h > tallest {
			tallest = h
		}
	}
	return tallest
}

// Update handles a key while the settings pane is open. It returns true when
// the pane should close.
func (s *settingsModel) Update(msg tea.KeyMsg) (closed bool, cmd tea.Cmd) {
	if s.editing {
		return false, s.updateEditing(msg)
	}

	switch msg.String() {
	case "esc", "q":
		return true, nil
	case "up", "k":
		s.move(-1)
	case "down", "j":
		s.move(1)
	case "home", "g":
		s.cursor = 0
		s.ensureVisible()
	case "end", "G":
		s.cursor = len(settingsFields) - 1
		s.ensureVisible()
	case "pgup":
		s.move(-s.viewport.Height)
	case "pgdown":
		s.move(s.viewport.Height)
	case "left":
		s.cycle(-1)
	case "right":
		s.cycle(1)
	case "enter":
		s.activate()
	}
	return false, nil
}

func (s *settingsModel) updateEditing(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		s.editing = false
		s.input.Blur()
		s.setStatus("Cancelled.", false)
		return nil
	case tea.KeyEnter:
		field := settingsFields[s.cursor]
		if err := field.Set(s.cfg, s.input.Value()); err != nil {
			s.setStatus(err.Error(), true)
			return nil
		}
		s.editing = false
		s.input.Blur()
		s.setStatus(fmt.Sprintf("Set %s.", field.Label), false)
		return nil
	}

	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd
}

// setStatus records the footer message and whether it is an error.
func (s *settingsModel) setStatus(text string, isErr bool) {
	s.status, s.statusErr = text, isErr
}

func (s *settingsModel) move(delta int) {
	s.cursor += delta
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= len(settingsFields) {
		s.cursor = len(settingsFields) - 1
	}
	s.setStatus("", false)
	s.ensureVisible()
}

// cycle steps a fixed-choice field forwards or backwards.
func (s *settingsModel) cycle(delta int) {
	field := settingsFields[s.cursor]
	if len(field.Options) == 0 {
		return
	}

	current := field.Get(s.cfg)
	index := 0
	for i, opt := range field.Options {
		if opt == current {
			index = i
			break
		}
	}
	index = (index + delta + len(field.Options)) % len(field.Options)

	if err := field.Set(s.cfg, field.Options[index]); err != nil {
		s.setStatus(err.Error(), true)
		return
	}
	s.setStatus(fmt.Sprintf("%s → %s", field.Label, field.Options[index]), false)
}

// activate cycles a choice field, or opens the text editor for a free field.
func (s *settingsModel) activate() {
	field := settingsFields[s.cursor]
	if len(field.Options) > 0 {
		s.cycle(1)
		return
	}

	s.input.SetValue(field.Get(s.cfg))
	s.input.CursorEnd()
	s.input.Width = s.viewport.Width - valueColumn - 2
	s.input.EchoMode = textinput.EchoNormal
	if field.Secret {
		// Keys are typed in the clear so they can be checked, but the
		// stored value is masked everywhere else.
		s.input.Placeholder = "paste the key"
	} else {
		s.input.Placeholder = ""
	}
	s.editing = true
	s.setStatus("enter saves · esc cancels", false)
	s.input.Focus()
}

// ensureVisible scrolls the row list so the cursor stays on screen.
func (s *settingsModel) ensureVisible() {
	if s.viewport.Height <= 0 {
		return
	}
	top := s.viewport.YOffset
	bottom := top + s.viewport.Height - 1
	switch {
	case s.cursor < top:
		s.viewport.SetYOffset(s.cursor)
	case s.cursor > bottom:
		s.viewport.SetYOffset(s.cursor - s.viewport.Height + 1)
	}
}

// valueColumn is where values start, so labels and values line up.
const valueColumn = 18

// maskSecret shows enough of a key to recognise it without exposing it.
func maskSecret(value string) string {
	if value == "" {
		return "not set"
	}
	if len(value) <= 8 {
		return strings.Repeat("•", len(value))
	}
	return value[:4] + strings.Repeat("•", 6) + value[len(value)-4:]
}

func (s *settingsModel) rows() string {
	lines := make([]string, 0, len(settingsFields))

	for i, field := range settingsFields {
		selected := i == s.cursor

		var value string
		switch {
		case selected && s.editing:
			value = s.input.View()
		case field.Secret:
			value = maskSecret(field.Get(s.cfg))
		default:
			value = field.Get(s.cfg)
		}

		label := field.Label
		if pad := valueColumn - lipgloss.Width(label) - 2; pad > 0 {
			label += strings.Repeat(" ", pad)
		}

		marker := "  "
		labelStyle := settingsLabelStyle
		valueStyle := settingsValueStyle
		if selected {
			marker = settingsCursorStyle.Render("› ")
			labelStyle = settingsLabelSelectedStyle
			valueStyle = settingsValueSelectedStyle
		}

		line := marker + labelStyle.Render(label) + "  " + valueStyle.Render(value)
		if selected && len(field.Options) > 0 && !s.editing {
			line += settingsHintStyle.Render("  ← →")
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func (s *settingsModel) View() string {
	s.viewport.SetContent(s.rows())

	inner := s.viewport.Width

	title := settingsTitleStyle.Render("Settings")
	path, err := config.Path()
	if err != nil {
		path = "(unknown)"
	}
	header := title + "  " + metaStyle.Render(truncate(path, inner-lipgloss.Width(title)-2))

	footer, footerStyle := s.status, errorStyle
	if footer == "" {
		footer, footerStyle = "↑↓ move · ← → change · enter edit · esc close", settingsHintStyle
	} else if !s.statusErr {
		footerStyle = settingsHintStyle
	}

	sections := []string{header, "", s.viewport.View(), ""}
	if s.showDesc {
		sections = append(sections,
			settingsDescStyle.Width(inner).Render(settingsFields[s.cursor].Desc))
	}
	sections = append(sections, footerStyle.Render(truncate(footer, inner)))

	panel := settingsPanelStyle.Width(inner + settingsPadW).Render(strings.Join(sections, "\n"))
	return lipgloss.Place(s.width, s.height, lipgloss.Center, lipgloss.Center, panel)
}
