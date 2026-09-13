package tui

import "testing"

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		want  command
		isCmd bool
	}{
		{"plain text", "hello world", command{}, false},
		{"empty", "", command{}, false},
		{"slash alone", "/", command{}, false},
		{"bare command", "/exit", command{Name: "exit"}, true},
		{"leading space", "  /exit  ", command{Name: "exit"}, true},
		{"uppercase", "/EXIT", command{Name: "exit"}, true},
		{
			"with args",
			"/connect-ollama http://localhost:11434",
			command{Name: "connect-ollama", Args: []string{"http://localhost:11434"}},
			true,
		},
		{"path is not a command", "./run.sh", command{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isCmd := parseCommand(tt.line)
			if isCmd != tt.isCmd {
				t.Fatalf("isCommand = %v, want %v", isCmd, tt.isCmd)
			}
			if !isCmd {
				return
			}
			if got.Name != tt.want.Name {
				t.Errorf("name: got %q, want %q", got.Name, tt.want.Name)
			}
			// An argless command yields an empty slice rather than nil, so
			// compare contents instead of using reflect.DeepEqual.
			if len(got.Args) != len(tt.want.Args) {
				t.Fatalf("args: got %q, want %q", got.Args, tt.want.Args)
			}
			for i := range got.Args {
				if got.Args[i] != tt.want.Args[i] {
					t.Errorf("args[%d]: got %q, want %q", i, got.Args[i], tt.want.Args[i])
				}
			}
		})
	}
}

func TestEveryCommandIsIndexed(t *testing.T) {
	for _, spec := range commandList {
		if _, ok := commandIndex[spec.Name]; !ok {
			t.Errorf("command %q is missing from the index", spec.Name)
		}
		for _, alias := range spec.Aliases {
			if _, ok := commandIndex[alias]; !ok {
				t.Errorf("alias %q of %q is missing from the index", alias, spec.Name)
			}
		}
	}
}
