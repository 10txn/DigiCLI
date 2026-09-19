package file

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteCreatesAFile(t *testing.T) {
	s, root, _ := newTestSandbox(t)

	change, err := s.Write("new.txt", "one\ntwo\n")
	if err != nil {
		t.Fatal(err)
	}
	if !change.Created {
		t.Error("a new file was not reported as created")
	}
	if change.Lines != 2 {
		t.Errorf("Lines = %d, want 2", change.Lines)
	}

	got, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one\ntwo\n" {
		t.Errorf("file contains %q", got)
	}
}

// The parent directories of a new file are created, because a model asked to
// add internal/thing/thing.go should not have to discover it cannot.
func TestWriteCreatesMissingParents(t *testing.T) {
	s, root, _ := newTestSandbox(t)

	if _, err := s.Write("a/b/c/deep.txt", "here\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", "c", "deep.txt")); err != nil {
		t.Fatal(err)
	}
}

// An overwrite says how much it replaced, which is the number that tells the
// user — and the model — whether it meant to.
func TestWriteReportsWhatItReplaced(t *testing.T) {
	s, root, _ := newTestSandbox(t)
	path := filepath.Join(root, "big.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 40)), 0o644); err != nil {
		t.Fatal(err)
	}

	change, err := s.Write("big.txt", "just this\n")
	if err != nil {
		t.Fatal(err)
	}
	if change.Created {
		t.Error("an overwrite was reported as a creation")
	}
	if change.WasLines != 40 || change.Lines != 1 {
		t.Errorf("WasLines = %d, Lines = %d; want 40 and 1", change.WasLines, change.Lines)
	}
}

// An existing file keeps its permissions: a script that was executable before
// an edit should still be executable after one.
func TestWriteKeepsExistingPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	s, root, _ := newTestSandbox(t)

	path := filepath.Join(root, "run.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write("run.sh", "#!/bin/sh\necho hi\n"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("permissions became %v", info.Mode().Perm())
	}
}

// The write goes through a temporary neighbour and a rename, so nothing is
// left behind when it is done.
func TestWriteLeavesNoTemporaryFiles(t *testing.T) {
	s, root, _ := newTestSandbox(t)

	if _, err := s.Write("a.txt", "replaced\n"); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "digicli-") {
			t.Errorf("a temporary file was left behind: %s", entry.Name())
		}
	}
}

func TestWriteRefusesToEscapeTheSandbox(t *testing.T) {
	s, _, outside := newTestSandbox(t)

	_, err := s.Write(filepath.Join(outside, "planted.txt"), "hello")
	if !errors.Is(err, ErrOutsideSandbox) {
		t.Fatalf("err = %v, want ErrOutsideSandbox", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.txt")); err == nil {
		t.Fatal("the file was written outside the working directory")
	}
}

// A symlink inside the tree pointing out of it is the interesting version of
// the same escape, since the path itself looks entirely innocent.
func TestWriteRefusesToFollowASymlinkOutOfTheSandbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	s, _, outside := newTestSandbox(t)

	_, err := s.Write("link-out/planted.txt", "hello")
	if !errors.Is(err, ErrOutsideSandbox) {
		t.Fatalf("err = %v, want ErrOutsideSandbox", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.txt")); err == nil {
		t.Fatal("the file was written through the symlink")
	}
}

// Open is the only way out of the tree, and it opens exactly one path.
func TestOpenAllowsOneApprovedPathAndNoOthers(t *testing.T) {
	s, _, outside := newTestSandbox(t)
	approved := filepath.Join(outside, "allowed.txt")

	s.Open(approved)

	if _, err := s.Write(approved, "fine\n"); err != nil {
		t.Fatalf("an opened path was refused: %v", err)
	}
	// Its neighbour was never approved, and approving a file is not
	// approving the directory it happens to be in.
	if _, err := s.Write(filepath.Join(outside, "other.txt"), "not fine\n"); !errors.Is(err, ErrOutsideSandbox) {
		t.Errorf("a neighbouring path was allowed by the grant: %v", err)
	}
}

// Changing mode closes them all again: a path allowed under auto must not
// still be allowed once the session is back in manual.
func TestCloseTakesBackEveryGrant(t *testing.T) {
	s, _, outside := newTestSandbox(t)
	approved := filepath.Join(outside, "allowed.txt")

	s.Open(approved)
	s.Close()

	if _, err := s.Write(approved, "hello"); !errors.Is(err, ErrOutsideSandbox) {
		t.Errorf("a grant survived Close: %v", err)
	}
}

// The guard sits underneath the grant, so approving a path on the list does
// not open it. This is the case that matters: a user can be talked into
// approving something, and a model that keeps asking will eventually get a yes.
func TestOpenDoesNotOverrideTheGuard(t *testing.T) {
	home := fakeHome(t)
	s, _, _ := newTestSandbox(t)

	key := filepath.Join(home, ".ssh", "authorized_keys")
	s.Open(key)

	if _, err := s.Write(key, "ssh-rsa AAAA...\n"); err == nil {
		t.Fatal("an approved path on the guard list was written")
	}
	if _, _, err := s.Read(key); err == nil {
		t.Fatal("an approved path on the guard list was read")
	}
}

func TestWriteRefusesOversizedContent(t *testing.T) {
	s, _, _ := newTestSandbox(t)

	_, err := s.Write("huge.txt", strings.Repeat("x", MaxWriteBytes+1))
	if err == nil {
		t.Fatal("an oversized write was allowed")
	}
	if !strings.Contains(err.Error(), "smaller pieces") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestWriteRefusesBinaryContent(t *testing.T) {
	s, _, _ := newTestSandbox(t)

	if _, err := s.Write("bin.txt", "before\x00after"); err == nil {
		t.Fatal("content with a NUL byte was written")
	}
}

func TestWriteRefusesADirectory(t *testing.T) {
	s, _, _ := newTestSandbox(t)

	if _, err := s.Write("sub", "hello"); err == nil {
		t.Fatal("a directory was overwritten")
	}
}

// Preview has to agree with Write, or the user approves one thing and gets
// another.
func TestPreviewDescribesTheWriteWithoutDoingIt(t *testing.T) {
	s, root, _ := newTestSandbox(t)

	change, err := s.Preview("a.txt", "new\ncontent\n")
	if err != nil {
		t.Fatal(err)
	}
	if change.Created {
		t.Error("an existing file was previewed as a creation")
	}
	if change.Lines != 2 {
		t.Errorf("Lines = %d, want 2", change.Lines)
	}

	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alpha" {
		t.Errorf("Preview changed the file: %q", got)
	}
}

// The prompt asking whether to leave the working directory is shown before the
// path has been opened, so a preview that enforced the boundary would answer
// the question with an error instead of showing the change.
func TestPreviewDescribesAWriteOutsideTheTree(t *testing.T) {
	s, _, outside := newTestSandbox(t)

	change, err := s.Preview(filepath.Join(outside, "new.txt"), "hello\n")
	if err != nil {
		t.Fatalf("a preview outside the tree failed: %v", err)
	}
	if !change.Created || change.Lines != 1 {
		t.Errorf("unexpected change: %+v", change)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); err == nil {
		t.Fatal("Preview wrote the file")
	}
}

// The guard is the part of the refusal a preview does keep, since nothing on
// that list should ever be put to the user in the first place.
func TestPreviewRefusesGuardedPaths(t *testing.T) {
	home := fakeHome(t)
	s, _, _ := newTestSandbox(t)

	if _, err := s.Preview(filepath.Join(home, ".zshrc"), "evil\n"); err == nil {
		t.Fatal("a guarded path was previewed as an ordinary write")
	}
}
