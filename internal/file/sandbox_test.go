package file

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newTestSandbox builds a sandbox over a temp dir containing:
//
//	root/a.txt
//	root/sub/b.txt
//	root/link-in    -> root/sub          (allowed: stays inside)
//	root/link-out   -> outside/          (denied: escapes)
//	outside/secret.txt
func newTestSandbox(t *testing.T) (*Sandbox, string, string) {
	t.Helper()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")

	for _, dir := range []string{root, filepath.Join(root, "sub"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "a.txt"), "alpha")
	write(filepath.Join(root, "sub", "b.txt"), "beta")
	write(filepath.Join(outside, "secret.txt"), "secret")

	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "link-in")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "link-out")); err != nil {
			t.Fatal(err)
		}
	}

	sandbox, err := NewSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	return sandbox, root, outside
}

func TestResolveAllowsPathsInside(t *testing.T) {
	s, root, _ := newTestSandbox(t)

	for _, path := range []string{
		"a.txt",
		"./a.txt",
		"sub/b.txt",
		"sub/../a.txt",               // climbs but stays inside
		"new-file.txt",               // does not exist yet, for writes
		"sub/deep/new.txt",           // nor does its parent
		filepath.Join(root, "a.txt"), // absolute, inside
		".",
	} {
		got, err := s.Resolve(path)
		if err != nil {
			t.Errorf("Resolve(%q) was rejected: %v", path, err)
			continue
		}
		if !strings.HasPrefix(got, s.Root()) {
			t.Errorf("Resolve(%q) = %q, which is outside the root %q", path, got, s.Root())
		}
	}
}

// The important half: a model asking for any of these must be refused.
func TestResolveRejectsEscapes(t *testing.T) {
	s, _, outside := newTestSandbox(t)

	escapes := []string{
		"../outside/secret.txt",
		"../../etc/passwd",
		"sub/../../outside/secret.txt",
		"/etc/passwd",
		"/",
		filepath.Join(outside, "secret.txt"), // absolute, outside
		"",
		"   ",
	}
	if runtime.GOOS != "windows" {
		// Following a symlink out of the tree is the subtle one.
		escapes = append(escapes,
			"link-out/secret.txt",
			"link-out",
		)
	}

	for _, path := range escapes {
		got, err := s.Resolve(path)
		if err == nil {
			t.Errorf("Resolve(%q) was allowed and returned %q; it must be rejected", path, got)
		}
	}
}

// A symlink that stays inside the tree is fine and should still work.
func TestResolveFollowsSymlinksThatStayInside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on Windows")
	}
	s, _, _ := newTestSandbox(t)

	got, err := s.Resolve("link-in/b.txt")
	if err != nil {
		t.Fatalf("an internal symlink was rejected: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join("sub", "b.txt")) {
		t.Errorf("got %q, want it resolved to sub/b.txt", got)
	}
}

func TestResolveReportsEscapesDistinctly(t *testing.T) {
	s, _, _ := newTestSandbox(t)

	_, err := s.Resolve("../outside/secret.txt")
	if !errors.Is(err, ErrOutsideSandbox) {
		t.Errorf("got %v, want it to wrap ErrOutsideSandbox", err)
	}
}

// A root reached through a symlink (/tmp on macOS) must not reject everything
// beneath it.
func TestSandboxRootItselfMayBeASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on Windows")
	}

	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	s, err := NewSandbox(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve("a.txt"); err != nil {
		t.Errorf("a path under a symlinked root was rejected: %v", err)
	}
}

// /home/user-backup must not pass a /home/user root on a prefix match.
func TestSiblingWithSharedPrefixIsRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	sibling := filepath.Join(base, "project-backup")
	for _, dir := range []string{root, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sibling, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(filepath.Join(sibling, "secret.txt")); err == nil {
		t.Error("a sibling directory sharing the root's prefix was allowed")
	}
}
