package update

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	// GOBIN is what underGoBin reads first, so setting it pins the go-install
	// case to a path this test controls rather than the machine's home.
	t.Setenv("GOBIN", "/home/jack/go/bin")

	tests := []struct {
		path string
		want Method
	}{
		{"/opt/homebrew/Cellar/digicli/0.1.2/bin/digicli", Homebrew},
		{"/home/linuxbrew/.linuxbrew/Cellar/digicli/0.1.2/bin/digicli", Homebrew},
		{"/usr/local/lib/node_modules/@digicli/darwin-arm64/bin/digicli", NPM},
		{`C:/Users/jack/AppData/Roaming/npm/node_modules/@digicli/win32-x64/bin/digicli.exe`, NPM},
		{"/home/jack/go/bin/digicli", GoInstall},
		{"/home/jack/.local/bin/digicli", Manual},
		{"/usr/local/bin/digicli", Manual},
		{"/tmp/digicli", Manual},

		// A global npm prefix commonly lives inside Homebrew's tree. npm owns
		// that binary, and `brew upgrade digicli` would not touch it.
		{"/opt/homebrew/lib/node_modules/@digicli/darwin-arm64/bin/digicli", NPM},
	}

	for _, tt := range tests {
		if got := classify(tt.path); got != tt.want {
			t.Errorf("classify(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// A GOPATH without GOBIN is the more common setup of the two.
func TestClassifyUsesGopath(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", filepath.Join("/srv", "gopath"))

	if got := classify("/srv/gopath/bin/digicli"); got != GoInstall {
		t.Errorf("got %q, want %q", got, GoInstall)
	}
	// A sibling directory that merely starts with the same characters is not
	// inside it.
	if got := classify("/srv/gopath-old/bin/digicli"); got != Manual {
		t.Errorf("got %q, want %q", got, Manual)
	}
}

func TestCommands(t *testing.T) {
	for _, method := range []Method{Homebrew, NPM, GoInstall} {
		cmd := command(method)
		if len(cmd) == 0 {
			t.Errorf("%s: no upgrade command", method)
			continue
		}
		if strings.Contains(strings.Join(cmd, " "), "sudo") {
			t.Errorf("%s: an upgrade must not ask for root", method)
		}
	}

	// npm must install the scoped wrapper: the bare name is a different,
	// unrelated package.
	if got := strings.Join(command(NPM), " "); !strings.Contains(got, "@digicli/cli") {
		t.Errorf("npm command %q does not install @digicli/cli", got)
	}

	// A downloaded binary has nothing behind it to run.
	if cmd := command(Manual); cmd != nil {
		t.Errorf("manual install has command %v, want none", cmd)
	}
}

func TestRunRefusesWithoutACommand(t *testing.T) {
	if _, err := (Install{Method: Manual}).Run(context.Background()); err == nil {
		t.Error("expected an error for an install with no upgrade command")
	}
}

func TestRunReportsAMissingTool(t *testing.T) {
	install := Install{
		Method:  Homebrew,
		Command: []string{"digicli-no-such-package-manager", "upgrade"},
	}
	_, err := install.Run(context.Background())
	if err == nil {
		t.Fatal("expected an error when the command is not installed")
	}
	if !strings.Contains(err.Error(), "PATH") {
		t.Errorf("error %q does not explain that the tool is missing", err)
	}
}

// Run hands back what the command printed, which is what /update shows.
func TestRunReturnsOutput(t *testing.T) {
	install := Install{Method: NPM, Command: []string{"echo", "up to date"}}

	out, err := install.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "up to date" {
		t.Errorf("output: got %q, want %q", out, "up to date")
	}
}

func TestRunReportsAFailedCommand(t *testing.T) {
	install := Install{Method: NPM, Command: []string{"false"}}

	if _, err := install.Run(context.Background()); err == nil {
		t.Error("expected an error from a command that exited non-zero")
	}
}

func TestDetectAlwaysAnswers(t *testing.T) {
	// Whatever the test binary looks like, Detect has to return something
	// usable: /update reads Method and Command without checking for a zero
	// value first.
	install := Detect()
	if install.Method == "" {
		t.Error("Detect returned no method")
	}
	if install.String() == "" {
		t.Error("Detect returned a method with no description")
	}
}
