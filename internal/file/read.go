package file

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// MaxReadBytes caps a single read. A model's context is the scarce
	// resource here: a 5MB file would blow it and cost the user the
	// conversation, so truncate and say so.
	MaxReadBytes = 128 * 1024
	// maxListEntries caps a directory listing for the same reason.
	maxListEntries = 500
)

// skipDirs are never worth a model's context and are usually enormous.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"__pycache__":  true,
	".venv":        true,
}

// Read returns the contents of a file inside the sandbox, truncated to
// MaxReadBytes. The bool reports whether truncation happened.
func (s *Sandbox) Read(path string) (string, bool, error) {
	resolved, err := s.access(path, false)
	if err != nil {
		return "", false, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, fmt.Errorf("no such file: %s", s.Rel(resolved))
		}
		return "", false, err
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("%s is a directory — use list_files", s.Rel(resolved))
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", false, err
	}

	// A binary file wastes the context window and tells the model nothing.
	if !utf8.Valid(data) {
		return "", false, fmt.Errorf("%s is not a text file", s.Rel(resolved))
	}

	if len(data) > MaxReadBytes {
		return string(data[:MaxReadBytes]), true, nil
	}
	return string(data), false, nil
}

// Entry is one item in a directory listing.
type Entry struct {
	Name  string
	IsDir bool
	Size  int64
}

// List returns the contents of a directory inside the sandbox, directories
// first and then alphabetically.
func (s *Sandbox) List(path string) ([]Entry, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	resolved, err := s.access(path, false)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no such directory: %s", s.Rel(resolved))
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is a file — use read_file", s.Rel(resolved))
	}

	raw, err := os.ReadDir(resolved)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(raw))
	for _, item := range raw {
		name := item.Name()
		if item.IsDir() && skipDirs[name] {
			continue
		}
		entry := Entry{Name: name, IsDir: item.IsDir()}
		if info, err := item.Info(); err == nil {
			entry.Size = info.Size()
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})

	if len(entries) > maxListEntries {
		entries = entries[:maxListEntries]
	}
	return entries, nil
}

// Format renders a listing the way the model should see it.
func Format(entries []Entry) string {
	if len(entries) == 0 {
		return "(empty directory)"
	}
	var b strings.Builder
	for _, entry := range entries {
		if entry.IsDir {
			fmt.Fprintf(&b, "%s/\n", entry.Name)
			continue
		}
		fmt.Fprintf(&b, "%s (%s)\n", entry.Name, humanSize(entry.Size))
	}
	return strings.TrimRight(b.String(), "\n")
}

func humanSize(n int64) string {
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

// Ext is the file extension without the dot, for code fences.
func Ext(path string) string {
	return strings.TrimPrefix(filepath.Ext(path), ".")
}
