package types

import (
	"encoding/json"
	"testing"
)

// A provider that encodes arguments into a string instead of sending an object
// must not make every argument read as missing.
func TestNormalizeArguments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"an object is left alone", `{"path":"a.go"}`, `{"path":"a.go"}`},
		{"a string is unwrapped", `"{\"path\":\"a.go\"}"`, `{"path":"a.go"}`},
		{"whitespace around it", `  "{\"path\":\"a.go\"}"  `, `{"path":"a.go"}`},
		{"an empty object encoded", `"{}"`, `{}`},
		// Prose in the arguments is not JSON hiding under a layer of quoting,
		// and must come back untouched for the tool to reject.
		{"a string that is not json", `"please write a.go"`, `"please write a.go"`},
		{"empty", ``, ``},
		{"malformed", `{"path":`, `{"path":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(NormalizeArguments(json.RawMessage(tt.in)))
			if got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// The whole point of normalising: the arguments reach the tool.
func TestStringArgReadsDoubleEncodedArguments(t *testing.T) {
	call := ToolCall{
		Name:      "write_file",
		Arguments: json.RawMessage(`"{\"path\":\"new.go\",\"content\":\"package new\\n\"}"`),
	}

	if got, ok := call.StringArg("path"); !ok || got != "new.go" {
		t.Errorf("path = %q (ok=%v), want new.go", got, ok)
	}
	if got, ok := call.StringArg("content"); !ok || got != "package new\n" {
		t.Errorf("content = %q (ok=%v)", got, ok)
	}
}
