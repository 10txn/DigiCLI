// Package file provides filesystem access confined to a single directory tree.
package file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideSandbox is returned for any path that resolves outside the root.
var ErrOutsideSandbox = errors.New("path escapes the working directory")

// Sandbox confines every operation to one directory tree. It is the only way
// the file tools reach the filesystem, so a model cannot be talked into
// reading ~/.ssh/id_rsa by asking nicely.
type Sandbox struct {
	// root is absolute with symlinks already resolved, so comparisons
	// against resolved paths are like-for-like.
	root string
}

// NewSandbox confines operations to dir.
func NewSandbox(dir string) (*Sandbox, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", dir, err)
	}
	// The root itself may be reached through a symlink — /tmp is a link to
	// /private/tmp on macOS — and an unresolved root would reject every
	// path beneath it.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", dir, err)
	}
	return &Sandbox{root: resolved}, nil
}

// Root returns the directory operations are confined to.
func (s *Sandbox) Root() string { return s.root }

// Resolve turns a model-supplied path into an absolute one inside the sandbox,
// or fails. Relative paths are taken from the root.
//
// Symlinks are resolved before the check, so a link pointing out of the tree is
// rejected rather than followed. The target of a write usually does not exist
// yet, so resolution walks up to the nearest existing ancestor and re-applies
// the remainder.
func (s *Sandbox) Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("no path given")
	}

	joined := path
	if !filepath.IsAbs(joined) {
		joined = filepath.Join(s.root, joined)
	}
	joined = filepath.Clean(joined)

	resolved, err := resolveExisting(joined)
	if err != nil {
		return "", err
	}
	if !s.contains(resolved) {
		return "", fmt.Errorf("%w: %s", ErrOutsideSandbox, path)
	}
	return resolved, nil
}

// contains reports whether p is the root or sits beneath it. The separator
// check stops /home/user-backup matching a /home/user root.
func (s *Sandbox) contains(p string) bool {
	if p == s.root {
		return true
	}
	return strings.HasPrefix(p, s.root+string(filepath.Separator))
}

// resolveExisting resolves symlinks as far down the path as exists, then
// re-appends the components that do not exist yet.
func resolveExisting(path string) (string, error) {
	missing := []string{}
	current := path

	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			// Re-apply the missing tail to the resolved ancestor.
			parts := append([]string{resolved}, reverse(missing)...)
			return filepath.Join(parts...), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolving %s: %w", path, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Walked to the filesystem root without finding anything.
			return filepath.Clean(path), nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func reverse(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

// Rel renders a path relative to the root, for display.
func (s *Sandbox) Rel(path string) string {
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return path
	}
	return rel
}
