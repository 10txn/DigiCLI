package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/10txn/digicli/internal/llm"
	tea "github.com/charmbracelet/bubbletea"
)

// commandSpec describes a built-in slash command. Handlers mutate the model
// directly (adding system messages, changing config) and return a tea.Cmd only
// when they need one, such as quitting or starting a request.
type commandSpec struct {
	Name    string
	Aliases []string
	Usage   string
	Summary string
	// TakesArgs marks a command that needs something typed after it. The
	// palette completes those into the input rather than running them.
	TakesArgs bool
	Run       func(m *Model, args []string) tea.Cmd
}

var (
	// commandList is the ordered set of built-ins; /help prints it in this
	// order. commandIndex maps every name and alias to its spec.
	//
	// Both are built in init rather than as plain var initialisers: /help
	// reads commandList, and Go rejects that as an initialisation cycle.
	commandList  []commandSpec
	commandIndex map[string]commandSpec
)

func init() {
	commandList = []commandSpec{
		{
			Name:    "help",
			Usage:   "/help",
			Summary: "List the available commands",
			Run:     cmdHelp,
		},
		{
			Name:    "settings",
			Usage:   "/settings",
			Summary: "Open the settings pane",
			Run:     cmdSettings,
		},
		{
			Name:    "models",
			Usage:   "/models",
			Summary: "List the models installed locally",
			Run:     cmdModels,
		},
		{
			Name:      "model",
			Usage:     "/model <name>",
			Summary:   "Switch the active model",
			TakesArgs: true,
			Run:       cmdModel,
		},
		{
			Name:    "clear",
			Usage:   "/clear",
			Summary: "Clear the chat history",
			Run:     cmdClear,
		},
		{
			Name:    "update",
			Usage:   "/update",
			Summary: "Check for a new version and install it",
			Run:     cmdUpdate,
		},
		{
			Name:    "exit",
			Aliases: []string{"quit"},
			Usage:   "/exit",
			Summary: "Save settings and quit",
			Run:     cmdExit,
		},
	}

	commandIndex = make(map[string]commandSpec, len(commandList))
	for _, spec := range commandList {
		commandIndex[spec.Name] = spec
		for _, alias := range spec.Aliases {
			commandIndex[alias] = spec
		}
	}
}

// runCommand dispatches a parsed command, or reports an unknown one.
func (m *Model) runCommand(cmd command) tea.Cmd {
	spec, ok := commandIndex[cmd.Name]
	if !ok {
		m.addSystem(errorStyle.Render(
			fmt.Sprintf("Unknown command /%s — try /help", cmd.Name)))
		return nil
	}
	return spec.Run(m, cmd.Args)
}

func cmdHelp(m *Model, _ []string) tea.Cmd {
	var b strings.Builder
	b.WriteString("Commands\n")
	for _, spec := range commandList {
		b.WriteString(fmt.Sprintf("  %-14s %s\n", spec.Usage, spec.Summary))
	}
	b.WriteString("\nKeys\n")
	b.WriteString("  /            Open this menu — ↑↓ to choose, enter to pick\n")
	b.WriteString("  enter        Send the current message\n")
	b.WriteString("  tab          Cycle mode: plan → manual → auto\n")
	b.WriteString("  pgup/pgdn    Scroll the chat history\n")
	b.WriteString("  ctrl+c       Interrupt a reply, or quit when idle")
	m.addSystem(b.String())
	return nil
}

func cmdSettings(m *Model, _ []string) tea.Cmd {
	m.openSettings()
	return nil
}

// modelsMsg carries the result of a background model listing. switchTo is set
// when the listing was triggered by /model, which validates the requested name
// against what is actually installed.
type modelsMsg struct {
	models   []llm.Model
	switchTo string
	err      error
}

// fetchModels lists the provider's models without blocking the UI.
func fetchModels(endpoint, switchTo string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		models, err := llm.ListOllamaModels(ctx, endpoint)
		return modelsMsg{models: models, switchTo: switchTo, err: err}
	}
}

func cmdModels(m *Model, _ []string) tea.Cmd {
	if m.cfg.Provider != "ollama" {
		m.addSystem(fmt.Sprintf(
			"/models only knows how to list Ollama models so far; the active provider is %s.\n"+
				"Switch with /settings, or set the model directly with /model <name>.",
			m.cfg.Provider))
		return nil
	}

	m.busy = true
	m.addSystem(fmt.Sprintf("Listing models from %s…", m.cfg.OllamaEndpoint))
	return fetchModels(m.cfg.OllamaEndpoint, "")
}

// handleModels renders the result of a /models request, or completes the
// switch that /model started.
func (m *Model) handleModels(msg modelsMsg) {
	if msg.err != nil {
		m.showError(msg.err)
		return
	}
	if msg.switchTo != "" {
		m.applyModelSwitch(msg.switchTo, msg.models)
		return
	}
	if len(msg.models) == 0 {
		m.addSystem("No models installed. Pull one first, for example: ollama pull qwen2.5-coder:7b")
		return
	}

	models := append([]llm.Model(nil), msg.models...)
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })

	width := 0
	for _, model := range models {
		if len(model.Name) > width {
			width = len(model.Name)
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d model(s) available — switch with /model <name>\n", len(models)))
	for _, model := range models {
		marker := "  "
		if model.Name == m.cfg.Model {
			marker = "› " // the active model
		}
		b.WriteString(fmt.Sprintf("%s%-*s  %s\n", marker, width, model.Name, describeModel(model)))
	}
	m.addSystem(strings.TrimRight(b.String(), "\n"))
}

// describeModel is the trailing detail column: parameter count, quantization
// and on-disk size, skipping whatever Ollama did not report.
func describeModel(model llm.Model) string {
	parts := make([]string, 0, 3)
	if model.Parameters != "" {
		parts = append(parts, model.Parameters)
	}
	if model.Quantized != "" {
		parts = append(parts, model.Quantized)
	}
	if model.ContextLength > 0 {
		parts = append(parts, formatTokens(model.ContextLength)+" ctx")
	}
	if model.Size > 0 {
		parts = append(parts, formatBytes(model.Size))
	}
	return strings.Join(parts, " · ")
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func cmdModel(m *Model, args []string) tea.Cmd {
	if len(args) == 0 {
		m.addSystem(fmt.Sprintf(
			"Active model: %s (%s)\nUsage: /model <name> — /models lists what is installed.",
			m.cfg.Model, m.cfg.Provider))
		return nil
	}

	name := args[0]
	if name == m.cfg.Model {
		m.addSystem(fmt.Sprintf("Already using %s.", name))
		return nil
	}

	// For Ollama the installed list is a cheap lookup away, so check the
	// name before switching and pick up the model's real context window.
	if m.cfg.Provider == "ollama" {
		m.busy = true
		return fetchModels(m.cfg.OllamaEndpoint, name)
	}

	m.setModel(name, 0)
	return nil
}

// applyModelSwitch completes a /model request once the installed list arrives.
func (m *Model) applyModelSwitch(name string, models []llm.Model) {
	for _, model := range models {
		if model.Name == name {
			m.setModel(name, model.ContextLength)
			return
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("No model named %s is installed.", name))
	if near := suggest(name, models); len(near) > 0 {
		b.WriteString("\nDid you mean: " + strings.Join(near, ", ") + "?")
	} else if len(models) > 0 {
		b.WriteString("\nRun /models to see what is available.")
	}
	m.addSystem(errorStyle.Render(b.String()))
}

// suggest finds installed models whose name contains the requested one, which
// catches the common case of omitting the :tag.
func suggest(name string, models []llm.Model) []string {
	var near []string
	lower := strings.ToLower(name)
	for _, model := range models {
		if strings.Contains(strings.ToLower(model.Name), lower) {
			near = append(near, model.Name)
		}
	}
	sort.Strings(near)
	return near
}

// setModel switches the active model and persists it. A non-zero context
// length replaces the configured window, keeping the status bar honest.
func (m *Model) setModel(name string, contextLength int) {
	previous := m.cfg.Model
	previousWindow := m.cfg.ContextWindow

	m.cfg.Model = name
	if contextLength > 0 {
		m.cfg.ContextWindow = contextLength
	}

	if err := m.cfg.Save(); err != nil {
		m.cfg.Model = previous
		m.cfg.ContextWindow = previousWindow
		m.addSystem(errorStyle.Render("Could not save the model change: " + err.Error()))
		return
	}

	note := fmt.Sprintf("Model: %s → %s", previous, name)
	if contextLength > 0 && contextLength != previousWindow {
		note += fmt.Sprintf(" (context window %s)", formatTokens(contextLength))
	}
	m.addSystem(note)
}

func cmdClear(m *Model, _ []string) tea.Cmd {
	m.messages = nil
	m.refresh()
	return nil
}

// cmdUpdate installs a release the startup check already found, and otherwise
// checks first. It works with the checker switched off — running /update is a
// clear enough request to ask GitHub this once.
func cmdUpdate(m *Model, _ []string) tea.Cmd {
	switch {
	case m.updating:
		m.addSystem("An update is already running.")
		return nil
	case m.updateInstalled:
		m.addSystem(fmt.Sprintf(
			"%s is installed — restart DigiCLI to use it.", m.latest.Version))
		return nil
	case m.updateAvailable:
		return m.startUpgrade()
	}

	m.checking = true
	m.addSystem("Checking for a new version…")
	return checkUpdate(m.version, true)
}

func cmdExit(m *Model, _ []string) tea.Cmd {
	return m.quit()
}
