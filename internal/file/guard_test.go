package file

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeHome points the guard at a temp directory, so the credential and startup
// file rules can be exercised without going near the real home directory.
func fakeHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the home-relative rules are written for unix paths")
	}

	home := t.TempDir()
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", resolved)
	return resolved
}

// The credential rules are the ones that hold for reading as well as writing:
// a tool result goes into the model's context, and with a remote provider that
// means out of the building.
func TestGuardRefusesCredentialsInBothDirections(t *testing.T) {
	home := fakeHome(t)

	paths := []string{
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".gnupg", "secring.gpg"),
		filepath.Join(home, ".netrc"),
		filepath.Join(home, ".git-credentials"),
	}

	for _, path := range paths {
		for _, mutating := range []bool{false, true} {
			if why := Guard(path, mutating); why == "" {
				t.Errorf("Guard(%q, mutating=%v) allowed it", path, mutating)
			}
		}
	}
}

// Everything else on the list is about what a write would set in motion, so
// reading it is nobody's emergency.
func TestGuardRefusesExecutionVectorsForWritesOnly(t *testing.T) {
	home := fakeHome(t)

	paths := []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".config", "fish", "config.fish"),
		filepath.Join(home, "Library", "LaunchAgents", "com.example.plist"),
		filepath.Join(home, "project", ".git", "hooks", "pre-commit"),
		filepath.Join(home, "project", ".git", "config"),
	}

	for _, path := range paths {
		if why := Guard(path, true); why == "" {
			t.Errorf("Guard(%q, mutating=true) allowed a write", path)
		}
		if why := Guard(path, false); why != "" {
			t.Errorf("Guard(%q, mutating=false) refused a read: %s", path, why)
		}
	}
}

func TestGuardRefusesSystemDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the system directory list is written for unix paths")
	}

	for _, path := range []string{
		"/etc/passwd", "/usr/bin/git", "/bin/sh", "/System/Library/thing",
		"/var/spool/cron/crontabs/jack",
	} {
		if why := Guard(path, true); why == "" {
			t.Errorf("Guard(%q, mutating=true) allowed a write", path)
		}
	}
}

// Writing a file straight into / is the nearest thing a tool with no delete
// and no shell has to the catastrophic command everyone pictures.
func TestGuardRefusesTheFilesystemRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix path")
	}
	if why := Guard("/something", true); why == "" {
		t.Error("a write into the filesystem root was allowed")
	}
}

// The guard must not get in the way of the working directory, which is where
// everything DigiCLI is for actually happens.
func TestGuardAllowsOrdinaryProjectFiles(t *testing.T) {
	home := fakeHome(t)

	for _, path := range []string{
		filepath.Join(home, "project", "main.go"),
		filepath.Join(home, "project", "internal", "tui", "model.go"),
		filepath.Join(home, "project", ".gitignore"),
		filepath.Join(home, "project", ".github", "workflows", "ci.yml"),
		filepath.Join(os.TempDir(), "scratch", "notes.txt"),
	} {
		if why := Guard(path, true); why != "" {
			t.Errorf("Guard(%q, mutating=true) refused an ordinary file: %s", path, why)
		}
	}
}

// .gitignore and .github are not .git, and refusing them would refuse half of
// every repository.
func TestGuardMatchesGitDirectoryNotItsNeighbours(t *testing.T) {
	home := fakeHome(t)
	project := filepath.Join(home, "project")

	if why := Guard(filepath.Join(project, ".git", "config"), true); why == "" {
		t.Error(".git/config was allowed")
	}
	if why := Guard(filepath.Join(project, ".gitignore"), true); why != "" {
		t.Errorf(".gitignore was refused: %s", why)
	}
	if why := Guard(filepath.Join(project, ".github", "dependabot.yml"), true); why != "" {
		t.Errorf(".github was refused: %s", why)
	}
}

// DigiCLI's own config holds the mode this whole policy is read from, so a
// model that could write it could grant itself auto.
func TestGuardRefusesDigiCLIsOwnConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DIGICLI_HOME", dir)

	if why := Guard(filepath.Join(dir, "config.json"), true); why == "" {
		t.Error("DigiCLI's own config was writable")
	}
}

// The separator check is what stops a prefix match on a longer name.
func TestGuardDoesNotMatchOnPrefixAlone(t *testing.T) {
	home := fakeHome(t)

	path := filepath.Join(home, ".ssh-notes", "todo.md")
	if why := Guard(path, true); why != "" {
		t.Errorf("%q was refused by prefix against ~/.ssh: %s", path, why)
	}
}

// A refusal is shown to the user and sent to the model, so it has to say
// which path it is about in a form either of them can act on.
func TestGuardExplainsItselfWithATildePath(t *testing.T) {
	home := fakeHome(t)

	why := Guard(filepath.Join(home, ".ssh", "id_rsa"), false)
	if !strings.Contains(why, "~/.ssh") {
		t.Errorf("the refusal does not name the path in a readable form: %q", why)
	}
}
