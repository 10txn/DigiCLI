package tool

import (
	"context"
	"fmt"

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

func (t ReadFile) Mutates() bool { return false }

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

func (t ListFiles) Mutates() bool { return false }

func (t ListFiles) Run(_ context.Context, call types.ToolCall) (string, error) {
	// path is optional: a model omitting it means the working directory.
	path, _ := call.StringArg("path")

	entries, err := t.Sandbox.List(path)
	if err != nil {
		return "", err
	}
	return file.Format(entries), nil
}
