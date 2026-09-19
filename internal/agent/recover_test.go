package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// The tools a session actually has. A name outside this list is not a call
// however convincingly it is written.
var tools = []string{"read_file", "list_files", "write_file"}

func argOf(t *testing.T, raw json.RawMessage, name string) string {
	t.Helper()
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatalf("arguments are not an object: %s", raw)
	}
	value, _ := args[name].(string)
	return value
}

// The shapes a model actually emits when its call misses the tool channel.
// Each one has to come back as a real call to write_file, with the file
// content intact — a recovered write that loses the content would replace the
// file with nothing, which is worse than not recovering at all.
func TestRecoverAcceptsTheShapesModelsEmit(t *testing.T) {
	const content = "package new\n\nfunc New() {}\n"
	encoded, _ := json.Marshal(content)

	// The arguments as a JSON object, and the same object encoded into a
	// string — which is the shape that arrives when a provider double-encodes.
	args := `{"path": "new.go", "content": ` + string(encoded) + `}`
	doubled, _ := json.Marshal(args)

	cases := []struct {
		name string
		text string
		// remaining is what should still be shown to the user afterwards.
		remaining string
	}{
		{
			name: "qwen and hermes tool_call tags",
			text: `<tool_call>{"name": "write_file", "arguments": {"path": "new.go", "content": ` + string(encoded) + `}}</tool_call>`,
		},
		{
			name:      "tool_call tags with the model talking around them",
			text:      "I'll create that file.\n" + `<tool_call>{"name": "write_file", "arguments": {"path": "new.go", "content": ` + string(encoded) + `}}</tool_call>`,
			remaining: "I'll create that file.",
		},
		{
			name: "an unclosed tag, from a reply cut off mid-call",
			text: `<tool_call>{"name": "write_file", "arguments": {"path": "new.go", "content": ` + string(encoded) + `}}`,
		},
		{
			name: "mistral's marker",
			text: `[TOOL_CALLS] [{"name": "write_file", "arguments": {"path": "new.go", "content": ` + string(encoded) + `}}]`,
		},
		{
			name: "a bare object that is the whole reply",
			text: `{"name": "write_file", "arguments": {"path": "new.go", "content": ` + string(encoded) + `}}`,
		},
		{
			name: "parameters rather than arguments",
			text: `{"name": "write_file", "parameters": {"path": "new.go", "content": ` + string(encoded) + `}}`,
		},
		{
			name: "openai's nested function spelling",
			text: `{"type": "function", "function": {"name": "write_file", "arguments": {"path": "new.go", "content": ` + string(encoded) + `}}}`,
		},
		{
			name: "a tool_call wrapper",
			text: `{"tool_call": {"name": "write_file", "arguments": {"path": "new.go", "content": ` + string(encoded) + `}}}`,
		},
		{
			name: "arguments double-encoded as a string",
			text: `{"name": "write_file", "arguments": ` + string(doubled) + `}`,
		},
		{
			name: "fenced, because the model fences json out of habit",
			text: "```json\n" + `{"name": "write_file", "arguments": {"path": "new.go", "content": ` + string(encoded) + `}}` + "\n```",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Recover(tt.text, tools)
			if !ok {
				t.Fatalf("not recovered:\n%s", tt.text)
			}
			calls, remaining := got.Calls, got.Text
			if !got.Delimited {
				t.Error("a call that announced itself was marked as inferred")
			}
			if len(calls) != 1 {
				t.Fatalf("got %d calls, want 1", len(calls))
			}
			if calls[0].Name != "write_file" {
				t.Errorf("got tool %q, want write_file", calls[0].Name)
			}
			if calls[0].ID == "" {
				t.Error("a recovered call has no id to match its result to")
			}
			if got := argOf(t, calls[0].Arguments, "path"); got != "new.go" {
				t.Errorf("path = %q, want new.go", got)
			}
			if got := argOf(t, calls[0].Arguments, "content"); got != content {
				t.Errorf("content = %q, want %q", got, content)
			}
			if want := tt.remaining; remaining != want {
				t.Errorf("remaining text = %q, want %q", remaining, want)
			}
		})
	}
}

// The other half of the bargain. Recovery runs a write nobody approved at the
// model's word, so anything that is not unmistakably a call has to stay text.
func TestRecoverRefusesAnythingThatIsNotACall(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{
			name: "ordinary prose",
			text: "I'd write a new file called new.go containing a New function.",
		},
		{
			name: "json quoted in the middle of an explanation",
			text: `The call would look like {"name": "write_file", "arguments": {"path": "new.go", "content": "x"}} — shall I run it?`,
		},
		{
			name: "a tool this session does not have",
			text: `{"name": "delete_file", "arguments": {"path": "new.go"}}`,
		},
		{
			name: "a tool name the model invented, in tags",
			text: `<tool_call>{"name": "run_command", "arguments": {"cmd": "rm -rf /"}}</tool_call>`,
		},
		{
			name: "json that is not a call at all",
			text: `{"path": "new.go", "content": "package new"}`,
		},
		{
			name: "a code sample that happens to be json",
			text: "```json\n{\"version\": 2, \"name\": \"my-package\"}\n```",
		},
		{
			name: "arguments that are prose rather than an object",
			text: `{"name": "write_file", "arguments": "please write new.go for me"}`,
		},
		{
			name: "malformed json in tags",
			text: `<tool_call>{"name": "write_file", "arguments": {"path":</tool_call>`,
		},
		{
			name: "an empty reply",
			text: "   \n  ",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Recover(tt.text, tools)
			if ok {
				t.Fatalf("text was treated as a call to %s:\n%s", got.Calls[0].Name, tt.text)
			}
			if remaining := got.Text; remaining != tt.text {
				t.Errorf("the reply was altered despite not being a call:\ngot  %q\nwant %q",
					remaining, tt.text)
			}
		})
	}
}

// A model that writes out several calls should have all of them recovered, or
// none: running half of what it asked for is its own kind of wrong.
func TestRecoverHandlesSeveralCalls(t *testing.T) {
	text := `<tool_call>{"name": "read_file", "arguments": {"path": "a.go"}}</tool_call>` +
		`<tool_call>{"name": "read_file", "arguments": {"path": "b.go"}}</tool_call>`

	got, ok := Recover(text, tools)
	if !ok {
		t.Fatal("not recovered")
	}
	calls := got.Calls
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	if calls[0].ID == calls[1].ID {
		t.Errorf("both calls share the id %q, so their results cannot be told apart", calls[0].ID)
	}

	// One bad call in the set takes the whole set down.
	mixed := `<tool_call>{"name": "read_file", "arguments": {"path": "a.go"}}</tool_call>` +
		`<tool_call>{"name": "not_a_tool", "arguments": {}}</tool_call>`
	if _, ok := Recover(mixed, tools); ok {
		t.Error("a set containing an unknown tool was recovered anyway")
	}
}

// A call that takes no arguments is still a call.
func TestRecoverAllowsAnArgumentlessCall(t *testing.T) {
	got, ok := Recover(`<tool_call>{"name": "list_files"}</tool_call>`, tools)
	if !ok {
		t.Fatal("not recovered")
	}
	if string(got.Calls[0].Arguments) != "{}" {
		t.Errorf("arguments = %s, want {}", got.Calls[0].Arguments)
	}
}

// Recovery must not be reachable when the session has no tools.
func TestRecoverWithNoToolsRecoversNothing(t *testing.T) {
	text := `<tool_call>{"name": "write_file", "arguments": {"path": "new.go", "content": "x"}}</tool_call>`
	if _, ok := Recover(text, nil); ok {
		t.Error("a call was recovered for a session with no tools")
	}
}

// A fenced call in the middle of prose. This is what a model working through a
// task actually emits when it drifts out of the tool format — the reply talks
// about the call on both sides of it — so it has to be recovered. It is also
// what a model explaining itself emits, so it is marked as inferred and the
// caller is left to be careful with it.
func TestRecoverInfersAFencedCallInProse(t *testing.T) {
	// Taken from a real session, down to the wording.
	text := "Got it. Let's start by reading the current contents of `index.html`.\n\n" +
		"```json\n{\n  \"name\": \"read_file\",\n  \"arguments\": {\n    \"path\": \"index.html\"\n  }\n}\n```\n\n" +
		"Once you provide the contents of `index.html`, I'll proceed."

	got, ok := Recover(text, tools)
	if !ok {
		t.Fatalf("a call fenced in prose was not recovered:\n%s", text)
	}
	if len(got.Calls) != 1 || got.Calls[0].Name != "read_file" {
		t.Fatalf("got %+v, want one call to read_file", got.Calls)
	}
	if path, _ := got.Calls[0].StringArg("path"); path != "index.html" {
		t.Errorf("path = %q, want index.html", path)
	}
	if got.Delimited {
		t.Error("a call inferred from prose was reported as delimited")
	}
	// What the model said around the call is worth keeping.
	if !strings.Contains(got.Text, "Got it.") || !strings.Contains(got.Text, "Once you provide") {
		t.Errorf("the prose around the call was lost: %q", got.Text)
	}
	if strings.Contains(got.Text, "read_file") {
		t.Errorf("the raw call was left in the reply: %q", got.Text)
	}
}

// A reply that is nothing but a fenced call is not a judgement call.
func TestRecoverTreatsAWholeReplyFenceAsDelimited(t *testing.T) {
	text := "```json\n" + `{"name": "read_file", "arguments": {"path": "a.go"}}` + "\n```"

	got, ok := Recover(text, tools)
	if !ok {
		t.Fatal("not recovered")
	}
	if !got.Delimited {
		t.Error("a reply that was only a call was marked as inferred")
	}
}

// Visible is what stands between the user and a file being typed out in front
// of them: it runs on every frame of a stream, including the frames where the
// call is only half there.
func TestVisibleHidesCallsAndKeepsEverythingElse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "prose is untouched",
			in:   "I'll create that file for you.",
			want: "I'll create that file for you.",
		},
		{
			name: "a completed tool_call block",
			in:   "Here goes.\n<tool_call>{\"name\": \"write_file\", \"arguments\": {\"path\": \"a.go\", \"content\": \"x\"}}</tool_call>\nDone.",
			want: "Here goes.\n\nDone.",
		},
		{
			name: "a tool_call block still arriving",
			in:   `Here goes.` + "\n" + `<tool_call>{"name": "write_file", "arguments": {"path": "a.go", "content": "package ma`,
			want: "Here goes.\n",
		},
		{
			name: "the opening tag alone",
			in:   "Working on it.\n<tool_call>",
			want: "Working on it.\n",
		},
		{
			// The tag arrives a character at a time like everything else.
			name: "an opening tag only half arrived",
			in:   "Working on it.\n<tool_ca",
			want: "Working on it.\n",
		},
		{
			// But a lone bracket is far likelier to be prose than a tag.
			name: "a trailing bracket is left alone",
			in:   "Compare a < b",
			want: "Compare a < b",
		},
		{
			name: "mistral's marker and everything after it",
			in:   "On it.\n[TOOL_CALLS] [{\"name\": \"read_file\", \"arguments\": {\"path\": \"a.go\"}}]",
			want: "On it.\n",
		},
		{
			name: "a fenced call",
			in:   "Reading it.\n\n```json\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"a.go\"}}\n```\n\nThen I'll edit.",
			want: "Reading it.\n\n\n\nThen I'll edit.",
		},
		{
			name: "a fenced call still arriving",
			in:   "Writing it.\n\n```json\n{\"name\": \"write_file\", \"arguments\": {\"path\": \"a.go\", \"content\": \"package m",
			want: "Writing it.\n\n",
		},
		// The whole point of the tool: code the model is showing on purpose
		// has to survive, even though it is also a fenced block.
		{
			name: "a fenced code block is kept",
			in:   "Here is the function:\n\n```go\nfunc main() {}\n```\n\nShall I write it?",
			want: "Here is the function:\n\n```go\nfunc main() {}\n```\n\nShall I write it?",
		},
		{
			name: "fenced json that is not a call is kept",
			in:   "```json\n{\"version\": 2}\n```",
			want: "```json\n{\"version\": 2}\n```",
		},
		{
			name: "code kept and a call hidden in the same reply",
			in:   "Like this:\n\n```go\nfunc main() {}\n```\n\n```json\n{\"name\": \"write_file\", \"arguments\": {\"path\": \"a.go\", \"content\": \"x\"}}\n```",
			want: "Like this:\n\n```go\nfunc main() {}\n```\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Visible(tt.in, tools); got != tt.want {
				t.Errorf("Visible():\ngot  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// A reply arriving a character at a time must never flash the file, at any
// length of prefix.
func TestVisibleNeverLeaksAFileMidStream(t *testing.T) {
	const secret = "TOP_SECRET_CONTENT"
	full := "Writing it now.\n<tool_call>" +
		`{"name": "write_file", "arguments": {"path": "a.go", "content": "` + secret + `"}}` +
		"</tool_call>"

	for i := 0; i <= len(full); i++ {
		if got := Visible(full[:i], tools); strings.Contains(got, secret) {
			t.Fatalf("the file leaked after %d characters: %q", i, got)
		}
	}

	// And the same for the fenced spelling.
	full = "Writing it now.\n\n```json\n" +
		`{"name": "write_file", "arguments": {"path": "a.go", "content": "` + secret + `"}}` +
		"\n```"
	for i := 0; i <= len(full); i++ {
		if got := Visible(full[:i], tools); strings.Contains(got, secret) {
			t.Fatalf("the fenced file leaked after %d characters: %q", i, got)
		}
	}
}
