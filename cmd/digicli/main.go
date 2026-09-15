// Command digicli is a local-first agentic coding assistant for the terminal.
package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/10txn/digicli/internal/config"
	"github.com/10txn/digicli/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

// version is stamped at build time by the Makefile; see the ldflags there.
var version = "dev"

const usage = `digicli — a local-first agentic coding assistant.

Usage:
  digicli              Start a session in the current directory
  digicli --version    Print the version
  digicli --help       Print this message

File tools are confined to the directory you start in, so run digicli from
the project you want to work on.

Environment:
  DIGICLI_DEBUG=1      Trace the session to digicli-debug.log
  DIGICLI_HOME=<dir>   Use a different config directory (default ~/.digicli)`

func main() {
	// Flags are handled by hand rather than with the flag package, which
	// would print its own usage and exit codes for a two-flag program.
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-v", "--version", "version":
			fmt.Printf("digicli %s\n", version)
			return
		case "-h", "--help", "help":
			fmt.Println(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "digicli: unknown option %q\n\n%s\n", arg, usage)
			os.Exit(2)
		}
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "digicli: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// DIGICLI_DEBUG=1 writes a trace to digicli-debug.log in the working
	// directory. The TUI owns the terminal, so logging anywhere else would
	// corrupt the display.
	if os.Getenv("DIGICLI_DEBUG") != "" {
		f, err := tea.LogToFile("digicli-debug.log", "digicli")
		if err != nil {
			return fmt.Errorf("opening the debug log: %w", err)
		}
		defer f.Close()
		log.Println("--- session start ---")
	} else {
		log.SetOutput(io.Discard)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	model := tui.New(cfg, version)
	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		return err
	}

	// Problems the TUI could not display, such as a failed config save.
	return model.Err()
}
