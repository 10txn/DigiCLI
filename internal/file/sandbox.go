// Package file provides filesystem access confined to a single directory tree.
package file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/10txn/digicli/internal/types"
)

// ErrOutsideSandbox is returned for any path that resolves outside the root
// and has not been opened.
var ErrOutsideSandbox = errors.New("path escapes the working directory")

// Sandbox confines every operation to one directory tree. It is the only way
// the file tools reach the filesystem, so a model cannot be talked into
// reading ~/.ssh/id_rsa by asking nicely.
//
// The tree is not the whole of the policy — the user can approve a path
// outside it, and auto mode reaches outside without asking — but the widening
// is always explicit and always one path at a time. Open is the only thing
// that grants it, so a mistake in the mode policy cannot quietly become
// filesystem-wide access: the sandbox refuses every path it was not opened
// for, whatever the policy thought it decided.
type Sandbox struct {
	// root is absolute with symlinks already resolved, so comparisons
	// against resolved paths are like-for-like.
	root string

	// mu guards open, which is written from the UI thread when an approval
	// comes back and read from the goroutine a tool runs on.
	mu sync.Mutex
	// open holds the resolved paths outside root that this session may
	// touch. Grants are per exact path rather than per directory: approving
	// one file in ~/notes is not approving ~/notes.
	open map[string]bool
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
	return &Sandbox{root: resolved, open: map[string]bool{}}, nil
}

// Root returns the directory operations are confined to.
func (s *Sandbox) Root() string { return s.root }

// Locate turns a model-supplied path into an absolute, symlink-free one and
// says whether it landed outside the working directory. It enforces nothing:
// it is what the mode policy needs in order to decide, and what Resolve is
// built on. Relative paths are taken from the root.
//
// Symlinks are resolved first, so a link pointing out of the tree is seen for
// what it is rather than followed blindly. The target of a write usually does
// not exist yet, so resolution walks up to the nearest existing ancestor and
// re-applies the remainder.
func (s *Sandbox) Locate(path string) (resolved string, outside bool, err error) {
	if strings.TrimSpace(path) == "" {
		return "", false, errors.New("no path given")
	}

	joined := path
	if !filepath.IsAbs(joined) {
		joined = filepath.Join(s.root, joined)
	}
	joined = filepath.Clean(joined)

	resolved, err = resolveExisting(joined)
	if err != nil {
		return "", false, err
	}
	return resolved, !under(resolved, s.root), nil
}

// Reach works out what a call naming this path would touch, for the mode
// policy to rule on. A path that will not resolve is reported as reaching
// nothing: the tool will fail on it in a moment with a better message than
// the policy could give, and there is nothing to ask the user about.
func (s *Sandbox) Reach(path string, mutating bool) types.Reach {
	resolved, outside, err := s.Locate(path)
	if err != nil {
		return types.Reach{Mutates: mutating}
	}
	return types.Reach{
		Mutates: mutating,
		Path:    resolved,
		Outside: outside && !s.opened(resolved),
		Refusal: Guard(resolved, mutating),
	}
}

// Open widens the sandbox to one path outside the working directory for the
// rest of the session, after the user has approved it or auto mode has decided
// for them. It is the only way in: see the note on Sandbox.
//
// It does not override Guard. A path on that list is refused at the point of
// use whether or not it was opened, so an approval given by mistake — or one
// the model talked the user into — still cannot reach ~/.ssh.
func (s *Sandbox) Open(path string) {
	// Resolved, because that is the form the grant is checked against later.
	// A path that will not resolve is not granted and not an error here: the
	// operation itself is about to fail on it with a better message.
	resolved, _, err := s.Locate(path)
	if err != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open == nil {
		s.open = map[string]bool{}
	}
	s.open[resolved] = true
}

// Close drops every grant, putting the sandbox back to the working directory
// alone. Changing mode calls this: a path opened under auto must not stay open
// once the user has tightened the session back to manual or plan.
func (s *Sandbox) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open = map[string]bool{}
}

func (s *Sandbox) opened(resolved string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open[filepath.Clean(resolved)]
}

// Resolve turns a model-supplied path into an absolute one the session is
// allowed to touch, or fails. It is what the file operations call, so every
// one of them is closed by default: a path outside the working directory has
// to have been through Open first.
func (s *Sandbox) Resolve(path string) (string, error) {
	resolved, outside, err := s.Locate(path)
	if err != nil {
		return "", err
	}
	if outside && !s.opened(resolved) {
		return "", fmt.Errorf("%w: %s", ErrOutsideSandbox, path)
	}
	return resolved, nil
}

// access is Resolve plus the guard, and is what the operations themselves
// call. Checking here rather than only in the policy layer means the refusal
// holds even if a call reaches an operation without having been ruled on.
func (s *Sandbox) access(path string, mutating bool) (string, error) {
	resolved, err := s.Resolve(path)
	if err != nil {
		return "", err
	}
	if why := Guard(resolved, mutating); why != "" {
		return "", fmt.Errorf("refused: %s", why)
	}
	return resolved, nil
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

// Display renders a path for a person to read: relative to the working
// directory when it is inside it, and absolute with the home directory
// shortened to ~ when it is not. An approval prompt for something outside the
// tree should say ~/notes/todo.md, not a run of "..".
func (s *Sandbox) Display(path string) string {
	if under(path, s.root) {
		return s.Rel(path)
	}
	return tildeify(path, homeDir())
}
