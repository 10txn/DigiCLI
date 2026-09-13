package tui

import "strings"

// command is a parsed slash command: "/connect-ollama http://host:11434"
// becomes {Name: "connect-ollama", Args: ["http://host:11434"]}.
type command struct {
	Name string
	Args []string
}

// parseCommand splits a line into a command if it starts with "/". The second
// return value is false for ordinary chat input.
func parseCommand(line string) (command, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return command{}, false
	}
	fields := strings.Fields(line[1:])
	if len(fields) == 0 {
		return command{}, false
	}
	return command{
		Name: strings.ToLower(fields[0]),
		Args: fields[1:],
	}, true
}
