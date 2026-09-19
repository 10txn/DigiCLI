package file

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// MaxWriteBytes caps a single write. A model cannot produce much more
	// than this in one turn anyway, so the cap is not really a limit on
	// useful work — it is a limit on how much a confused one can do in a
	// single call before anybody sees it.
	MaxWriteBytes = 256 * 1024

	// newDirPerm and newFilePerm are what a created directory and a created
	// file get. An existing file keeps the permissions it already had.
	newDirPerm  os.FileMode = 0o755
	newFilePerm os.FileMode = 0o644
)

// Change is what a write did, for the transcript and for the approval prompt
// that precedes it.
type Change struct {
	// Path is the absolute path written.
	Path string
	// Created marks a file that did not exist beforehand.
	Created bool
	// WasBytes and WasLines describe what was there before an overwrite.
	WasBytes int64
	WasLines int
	// Bytes and Lines describe what is there now.
	Bytes int
	Lines int
}

// Summary renders a change the way the user should see it afterwards.
func (c Change) Summary(name string) string {
	if c.Created {
		return fmt.Sprintf("created %s (%d %s)", name, c.Lines, plural(c.Lines, "line"))
	}
	return fmt.Sprintf("rewrote %s (%d %s → %d %s)",
		name, c.WasLines, plural(c.WasLines, "line"), c.Lines, plural(c.Lines, "line"))
}

// Preview is what a write would do, worked out without doing it, so the user
// can be asked about a change that has not happened yet.
//
// It locates the path rather than resolving it, which is the one place the
// boundary is deliberately not enforced: the prompt for a path outside the
// working directory is shown before that path is opened, and a preview that
// refused it would put an error in front of the user in place of the change
// they are being asked to rule on. The guard still applies — though a guarded
// path is refused before anyone is asked, so it should not reach here.
func (s *Sandbox) Preview(path, content string) (Change, error) {
	resolved, _, err := s.Locate(path)
	if err != nil {
		return Change{}, err
	}
	if why := Guard(resolved, true); why != "" {
		return Change{}, fmt.Errorf("refused: %s", why)
	}
	return s.plan(resolved, content)
}

// Write replaces a file's entire contents, creating it and any missing parent
// directories. It cannot delete, move or recurse: replacing one named file is
// the whole of what it does.
func (s *Sandbox) Write(path, content string) (Change, error) {
	resolved, err := s.access(path, true)
	if err != nil {
		return Change{}, err
	}

	change, err := s.plan(resolved, content)
	if err != nil {
		return Change{}, err
	}

	if err := os.MkdirAll(filepath.Dir(resolved), newDirPerm); err != nil {
		return Change{}, fmt.Errorf("creating the directory for %s: %w", s.Display(resolved), err)
	}

	perm := newFilePerm
	if info, err := os.Stat(resolved); err == nil {
		perm = info.Mode().Perm()
	}

	// Write to a neighbour and rename over the target, so an interrupted
	// write leaves the original file intact rather than half of each. The
	// temporary file is in the same directory because a rename across
	// filesystems is not atomic — and on a separate /tmp, not even possible.
	temp, err := os.CreateTemp(filepath.Dir(resolved), "."+filepath.Base(resolved)+".digicli-*")
	if err != nil {
		return Change{}, fmt.Errorf("writing %s: %w", s.Display(resolved), err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName) // no-op once the rename below has succeeded

	if _, err := temp.WriteString(content); err != nil {
		temp.Close()
		return Change{}, fmt.Errorf("writing %s: %w", s.Display(resolved), err)
	}
	if err := temp.Close(); err != nil {
		return Change{}, fmt.Errorf("writing %s: %w", s.Display(resolved), err)
	}
	if err := os.Chmod(tempName, perm); err != nil {
		return Change{}, fmt.Errorf("writing %s: %w", s.Display(resolved), err)
	}
	if err := os.Rename(tempName, resolved); err != nil {
		return Change{}, fmt.Errorf("writing %s: %w", s.Display(resolved), err)
	}
	return change, nil
}

// plan validates the content and describes the change, doing none of it. Both
// Preview and Write go through it so that what the user approves is what
// happens — and so a write that will be refused is refused before anyone is
// asked about it.
func (s *Sandbox) plan(resolved, content string) (Change, error) {
	name := s.Display(resolved)

	if len(content) > MaxWriteBytes {
		return Change{}, fmt.Errorf("that is %d KB, and a single write is capped at %d KB — "+
			"write it in smaller pieces", len(content)/1024, MaxWriteBytes/1024)
	}
	if !utf8.ValidString(content) {
		return Change{}, fmt.Errorf("the content for %s is not valid UTF-8; "+
			"write_file writes text files only", name)
	}
	if i := strings.IndexByte(content, 0); i >= 0 {
		return Change{}, fmt.Errorf("the content for %s contains a NUL byte at offset %d, "+
			"which no text file should have", name, i)
	}

	change := Change{
		Path:  resolved,
		Bytes: len(content),
		Lines: countLines(content),
	}

	info, err := os.Stat(resolved)
	switch {
	case os.IsNotExist(err):
		change.Created = true
		// A missing parent is created, but only if what is in the way is a
		// directory rather than a file: MkdirAll's own error for that is
		// about mkdir, which will not mean much to a model.
		if err := checkParents(resolved); err != nil {
			return Change{}, err
		}
		return change, nil
	case err != nil:
		return Change{}, err
	}

	if info.IsDir() {
		return Change{}, fmt.Errorf("%s is a directory", name)
	}
	if !info.Mode().IsRegular() {
		return Change{}, fmt.Errorf("%s is not a regular file", name)
	}

	change.WasBytes = info.Size()
	if previous, err := os.ReadFile(resolved); err == nil {
		change.WasLines = countLines(string(previous))
	}
	return change, nil
}

// checkParents walks up to the first component that exists and complains if it
// is a file, which is the one way MkdirAll fails that a model can fix.
func checkParents(resolved string) error {
	for dir := filepath.Dir(resolved); !isRoot(dir); dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is a file, so it cannot also be a directory in the path", dir)
		}
		return nil
	}
	return nil
}

// countLines counts the lines a file will have. A trailing newline ends the
// last line rather than starting an empty one, which is how an editor counts.
func countLines(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(content, "\n"), "\n") + 1
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
