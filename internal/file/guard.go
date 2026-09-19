package file

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/10txn/digicli/internal/config"
)

// The guard is the floor beneath the mode policy. Auto mode may reach outside
// the working directory and the user may approve anything, but neither reaches
// past this: these paths are refused in every mode, every time.
//
// It is deliberately not a list of "important files". Backing up the whole of
// $HOME is not DigiCLI's job, and a denylist that tries to protect everything
// ends up protecting nothing a model would actually be steered into. What it
// covers is the small set of things a file tool can be talked into that the
// user cannot undo by reading the transcript afterwards:
//
//   - taking code execution out of DigiCLI, into a shell, a git hook or a
//     login item that runs later, as the user, with no prompt attached;
//   - taking credentials out of the building, which for a remote provider is
//     one read away;
//   - breaking the operating system itself.
//
// Refusals are per path, not per command, because write_file cannot delete,
// recurse or execute: the worst single call it can make is to replace one
// file's contents. Replacing the right file is enough, which is what this
// stops.

// systemDirs are the operating system's own trees. /tmp and /var are left out
// on purpose: they are scratch space by design, and on macOS the user's own
// temp directory lives under /private/var, so blocking them would refuse
// ordinary work while protecting nothing.
var systemDirs = []string{
	"/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys", "/usr",
	"/etc", "/private/etc",
	// macOS.
	"/System", "/Library", "/Applications",
	// The parts of /var that are not scratch space: cron tables and the
	// system databases behind sudo and the package manager.
	"/var/spool", "/private/var/spool", "/var/db", "/private/var/db",
	// Windows.
	`C:\Windows`, `C:\Program Files`, `C:\Program Files (x86)`,
}

// secretDirs hold credentials, relative to the user's home directory. These
// are refused for reading as well as writing: everything a tool returns goes
// into the model's context, and with a remote provider that means reading
// ~/.ssh/id_rsa would put a private key in somebody else's request logs.
var secretDirs = []string{
	".ssh", ".gnupg", ".aws", ".kube", ".docker", ".password-store",
	".config/gh", ".config/gcloud", ".config/op",
	"Library/Keychains",
}

// secretFiles are the same thing as a single file rather than a directory.
var secretFiles = []string{
	".netrc", ".git-credentials", ".npmrc", ".pypirc",
}

// startupFiles are read by a shell or login session, so writing one is
// arbitrary command execution with a delay on it — the closest a file tool can
// get to running a command, and a long way outside what "edit my code" means.
var startupFiles = []string{
	".zshrc", ".zshenv", ".zprofile", ".zlogin", ".zlogout",
	".bashrc", ".bash_profile", ".bash_login", ".bash_logout",
	".profile", ".inputrc",
	".config/fish/config.fish",
}

// persistenceDirs start things without being asked, and survive a reboot.
var persistenceDirs = []string{
	"Library/LaunchAgents", ".config/systemd", ".config/autostart",
}

// Guard reports why an operation on resolved must be refused, or "" when there
// is nothing to object to. resolved is absolute with symlinks already taken
// out, which is what makes the prefix comparisons below trustworthy: a link
// under the working directory pointing at ~/.ssh arrives here as ~/.ssh.
//
// mutating separates the two halves. Writing is refused everywhere in the
// list; reading is refused only where the content itself is the danger.
func Guard(resolved string, mutating bool) string {
	home := homeDir()

	// Credentials: refused in both directions.
	for _, dir := range secretDirs {
		if home != "" && under(resolved, filepath.Join(home, dir)) {
			return fmt.Sprintf("%s holds credentials, which DigiCLI never reads or writes, "+
				"in any mode", tildeify(filepath.Join(home, dir), home))
		}
	}
	for _, name := range secretFiles {
		if home != "" && same(resolved, filepath.Join(home, name)) {
			return fmt.Sprintf("%s holds credentials, which DigiCLI never reads or writes, "+
				"in any mode", tildeify(filepath.Join(home, name), home))
		}
	}

	if !mutating {
		return ""
	}

	// A file written straight into the filesystem root, which is both a
	// mistake and the only shape of "write to /" there is.
	if parent := filepath.Dir(resolved); parent == resolved || isRoot(parent) {
		return "the filesystem root is not somewhere DigiCLI writes"
	}

	for _, dir := range systemDirs {
		if under(resolved, dir) {
			return fmt.Sprintf("%s belongs to the operating system, and DigiCLI does not "+
				"write there in any mode", dir)
		}
	}

	for _, name := range startupFiles {
		if home != "" && same(resolved, filepath.Join(home, name)) {
			return fmt.Sprintf("%s runs as you the next time a shell starts, so writing it "+
				"would be running a command rather than editing a file",
				tildeify(filepath.Join(home, name), home))
		}
	}

	for _, dir := range persistenceDirs {
		if home != "" && under(resolved, filepath.Join(home, dir)) {
			return fmt.Sprintf("%s starts programs automatically, which is not something "+
				"DigiCLI sets up on your behalf", tildeify(filepath.Join(home, dir), home))
		}
	}

	// Git's own directory, wherever it is — including inside the working
	// directory, which is the only entry here that a normal session will
	// actually run into. .git/config sets core.hooksPath and .git/hooks holds
	// scripts git runs on commit, so a write here is again command execution;
	// the rest of it is the history, which is not editable by hand.
	if component(resolved, ".git") {
		return "the .git directory is git's own storage — its hooks and config run " +
			"commands, and its objects are not editable by hand. Change the files in " +
			"the working tree instead"
	}

	// DigiCLI's own state, which includes the mode this policy is read from.
	// A model that can set mode to auto has granted itself auto.
	if dir, err := config.Dir(); err == nil && under(resolved, dir) {
		return fmt.Sprintf("%s is DigiCLI's own configuration, including the mode that "+
			"decides what you are allowed to do", tildeify(dir, home))
	}

	// The running binary. Replacing it is what /update is for.
	if exe, err := os.Executable(); err == nil {
		if resolvedExe, err := filepath.EvalSymlinks(exe); err == nil && same(resolved, resolvedExe) {
			return "that is the running DigiCLI binary — /update replaces it"
		}
	}

	return ""
}

// homeDir is the user's home directory with symlinks resolved, so it compares
// like-for-like against a resolved path. It is looked up on every call rather
// than cached: the lookup is an environment variable, and a cached one could
// not be pointed somewhere else by a test.
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		return home
	}
	return resolved
}

// under reports whether path is dir or sits beneath it. The separator stops
// /Librarian matching a /Library entry.
func under(path, dir string) bool {
	if same(path, dir) {
		return true
	}
	return hasPrefix(path, dir+string(filepath.Separator))
}

// component reports whether name appears as a whole element of path, which is
// how ".git" is found wherever in the tree it sits.
func component(path, name string) bool {
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if equal(part, name) {
			return true
		}
	}
	return false
}

func isRoot(path string) bool {
	return path == filepath.Dir(path)
}

// Windows paths are case-insensitive, so a denylist compared case-sensitively
// there would be bypassed by typing C:\WINDOWS.
func equal(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func same(a, b string) bool { return equal(filepath.Clean(a), filepath.Clean(b)) }

func hasPrefix(path, prefix string) bool {
	if runtime.GOOS == "windows" {
		return len(path) >= len(prefix) && strings.EqualFold(path[:len(prefix)], prefix)
	}
	return strings.HasPrefix(path, prefix)
}

// tildeify shortens a path under the home directory for display, so a refusal
// reads as ~/.ssh rather than /Users/somebody/.ssh.
func tildeify(path, home string) string {
	if home == "" || !under(path, home) {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil {
		return path
	}
	if rel == "." {
		return "~"
	}
	return "~" + string(filepath.Separator) + rel
}
