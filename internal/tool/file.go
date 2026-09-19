package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/10txn/digicli/internal/file"
	"github.com/10txn/digicli/internal/types"
)

// ReadFile lets the model read a file inside the working directory.
type ReadFile struct {
	Sandbox *file.Sandbox
}

func (t ReadFile) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name: "read_file",
		Description: "Read the contents of a text file in the working directory. " +
			"Use a path relative to the working directory, such as cmd/main.go.",
		Parameters: types.Schema(map[string]string{
			"path": "Path to the file, relative to the working directory.",
		}, "path"),
	}
}

func (t ReadFile) Reach(call types.ToolCall) types.Reach {
	path, _ := call.StringArg("path")
	return t.Sandbox.Reach(path, false)
}

func (t ReadFile) Run(_ context.Context, call types.ToolCall) (string, error) {
	path, ok := call.StringArg("path")
	if !ok {
		return "", fmt.Errorf("read_file needs a %q argument", "path")
	}

	content, truncated, err := t.Sandbox.Read(path)
	if err != nil {
		return "", err
	}
	if truncated {
		return fmt.Sprintf("%s\n\n[truncated at %d KB — the file is larger]",
			content, file.MaxReadBytes/1024), nil
	}
	if content == "" {
		return "(the file is empty)", nil
	}
	return content, nil
}

// ListFiles lets the model see what is in a directory.
type ListFiles struct {
	Sandbox *file.Sandbox
}

func (t ListFiles) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name: "list_files",
		Description: "List the files and directories at a path in the working directory. " +
			"Omit the path or pass \".\" for the working directory itself.",
		Parameters: types.Schema(map[string]string{
			"path": "Directory to list, relative to the working directory. Defaults to \".\".",
		}),
	}
}

func (t ListFiles) Reach(call types.ToolCall) types.Reach {
	// path is optional: a model omitting it means the working directory.
	path, _ := call.StringArg("path")
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	return t.Sandbox.Reach(path, false)
}

func (t ListFiles) Run(_ context.Context, call types.ToolCall) (string, error) {
	path, _ := call.StringArg("path")

	entries, err := t.Sandbox.List(path)
	if err != nil {
		return "", err
	}
	return file.Format(entries), nil
}

// WriteFile lets the model put a file on disk. It replaces the whole file:
// there is no append, no patch and no delete, which keeps what a single call
// can do to one named path, and keeps the approval prompt able to show the
// entire consequence of saying yes.
type WriteFile struct {
	Sandbox *file.Sandbox
}

func (t WriteFile) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name: "write_file",
		Description: "Write a text file in the working directory, creating it and any " +
			"missing parent directories. This replaces the file's entire contents, so " +
			"read it first and send back the whole file with your changes in it — " +
			"sending only the part you changed deletes the rest. There is no way to " +
			"delete or rename a file.",
		Parameters: types.Schema(map[string]string{
			"path":    "Path to the file, relative to the working directory.",
			"content": "The complete new contents of the file.",
		}, "path", "content"),
	}
}

func (t WriteFile) Reach(call types.ToolCall) types.Reach {
	path, _ := call.StringArg("path")
	return t.Sandbox.Reach(path, true)
}

// Preview describes the write for the approval prompt: a line saying what it
// does to which file, and the file itself for the viewer behind it.
func (t WriteFile) Preview(call types.ToolCall) Preview {
	path, _ := call.StringArg("path")
	content, _ := call.StringArg("content")

	change, err := t.Sandbox.Preview(path, content)
	if err != nil {
		// Worth showing rather than hiding: it is why the call is about to
		// fail, and it saves the user approving something that cannot work.
		// There is nothing to read behind it, so no body.
		return Preview{Summary: "This write would fail: " + err.Error()}
	}

	name := t.Sandbox.Display(change.Path)
	summary := fmt.Sprintf("Replace all %d lines of %s with %d lines.",
		change.WasLines, name, change.Lines)
	if change.Created {
		summary = fmt.Sprintf("Create %s — %d %s.",
			name, change.Lines, plural(change.Lines, "line"))
	}
	return Preview{Summary: summary, Body: content}
}

// plural keeps the summary reading as a sentence for a one-line file.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func (t WriteFile) Run(_ context.Context, call types.ToolCall) (string, error) {
	path, ok := call.StringArg("path")
	if !ok {
		return "", fmt.Errorf("write_file needs a %q argument", "path")
	}
	// Distinguished from a missing one: a model that means to empty a file
	// should say so with "", and one that forgot the argument should be told
	// rather than have the file emptied for it.
	content, ok := call.StringArg("content")
	if !ok {
		return "", fmt.Errorf("write_file needs a %q argument holding the complete new "+
			"contents of the file; pass an empty string to empty it", "content")
	}

	change, err := t.Sandbox.Write(path, content)
	if err != nil {
		return "", err
	}

	// The model is told what it actually did, in particular how much it
	// replaced: a model that meant to add a function and finds it rewrote a
	// 400-line file down to 12 should have the chance to notice.
	name := t.Sandbox.Display(change.Path)
	if change.Created {
		return fmt.Sprintf("Created %s with %d lines.", name, change.Lines), nil
	}
	return fmt.Sprintf("Rewrote %s: its %d lines were replaced with %d.",
		name, change.WasLines, change.Lines), nil
}
