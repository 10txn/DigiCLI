package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Method is how the running binary got onto the machine, which is what decides
// the command that upgrades it.
type Method string

const (
	Homebrew  Method = "homebrew"
	NPM       Method = "npm"
	GoInstall Method = "go install"
	Manual    Method = "manual"
)

// Install describes the running binary and the command that would upgrade it.
type Install struct {
	Method Method
	// Path is the executable, with symlinks resolved — Homebrew's entry in
	// bin is a link into the Cellar, and the target is what identifies it.
	Path string
	// Command is the upgrade command, argv style. It is empty for an install
	// DigiCLI will not upgrade for the user: a binary downloaded by hand has
	// no package manager behind it, and the only way to update it would be to
	// overwrite the file currently executing.
	Command []string
}

// Detect works out where the running binary came from. It never fails: an
// install it cannot place is Manual, which means /update explains the upgrade
// rather than running one.
func Detect() Install {
	path, err := os.Executable()
	if err != nil {
		return Install{Method: Manual}
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	method := classify(path)
	return Install{Method: method, Path: path, Command: command(method)}
}

func classify(path string) Method {
	// Compared with forward slashes throughout so one set of checks covers
	// Windows, and lowercased because macOS renders the Cellar capitalised.
	p := strings.ToLower(filepath.ToSlash(path))

	switch {
	// npm comes first: a global npm prefix often sits inside Homebrew's tree
	// (/opt/homebrew/lib/node_modules/...), and npm is what owns the binary
	// there, not brew.
	case strings.Contains(p, "/node_modules/"):
		return NPM
	case strings.Contains(p, "/cellar/digicli/"):
		return Homebrew
	case underGoBin(p):
		return GoInstall
	default:
		return Manual
	}
}

// underGoBin reports whether the path is in the directory `go install` writes
// to. GOBIN wins when set, as the go command has it; otherwise it is the bin
// directory under the first GOPATH entry, defaulting to ~/go.
func underGoBin(p string) bool {
	var dirs []string
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		dirs = append(dirs, gobin)
	} else if gopath := os.Getenv("GOPATH"); gopath != "" {
		for _, entry := range filepath.SplitList(gopath) {
			dirs = append(dirs, filepath.Join(entry, "bin"))
		}
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}

	for _, dir := range dirs {
		prefix := strings.ToLower(filepath.ToSlash(dir))
		if prefix != "" && strings.HasPrefix(p, strings.TrimSuffix(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

func command(method Method) []string {
	switch method {
	case Homebrew:
		// brew refreshes the tap itself before upgrading, so there is no
		// separate `brew update` step to run first.
		return []string{"brew", "upgrade", "digicli"}
	case NPM:
		// The wrapper is scoped; the bare name belongs to an unrelated
		// package. @latest is explicit so npm cannot resolve to a cached
		// older version.
		return []string{"npm", "install", "-g", "@digicli/cli@latest"}
	case GoInstall:
		return []string{"go", "install", "github.com/10txn/digicli/cmd/digicli@latest"}
	default:
		return nil
	}
}

// Run executes the upgrade command and returns everything it printed, which is
// worth showing whether or not it worked — a package manager explains its own
// failures better than anything this could say about them.
//
// The upgrade replaces the binary this process is running from. That is safe
// on every platform DigiCLI targets except Windows, which refuses to replace a
// file in use; npm handles that by writing a new file and swapping it, so the
// running session simply keeps using the old inode until it exits. Either way
// the new version applies at the next start, not to this one.
func (i Install) Run(ctx context.Context) (string, error) {
	if len(i.Command) == 0 {
		return "", fmt.Errorf("this install has no upgrade command")
	}
	if _, err := exec.LookPath(i.Command[0]); err != nil {
		return "", fmt.Errorf("%s is not on your PATH, so DigiCLI cannot run %q",
			i.Command[0], strings.Join(i.Command, " "))
	}

	cmd := exec.CommandContext(ctx, i.Command[0], i.Command[1:]...)
	// Progress bars and colour codes would land in the chat transcript as
	// escape sequences, so ask for plain output.
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb")
	// Nothing is on stdin: a command that stops to ask a question would hang
	// the update with no way to answer it, so let it read EOF and fail.
	cmd.Stdin = nil

	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if ctx.Err() != nil {
		return output, fmt.Errorf("the update took too long and was stopped")
	}
	if err != nil {
		return output, fmt.Errorf("%s exited with an error: %w", i.Command[0], err)
	}
	return output, nil
}

// String names the install method for a status line.
func (i Install) String() string {
	if i.Method == Manual {
		return "a downloaded binary"
	}
	return string(i.Method)
}
