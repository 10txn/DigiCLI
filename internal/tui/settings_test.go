package tui

import (
	"strings"
	"testing"

	"github.com/10txn/digicli/internal/config"
	"github.com/10txn/digicli/internal/types"
)

func TestSettingsFieldsRoundTrip(t *testing.T) {
	cfg := config.New()
	for _, field := range settingsFields {
		// Every field must at least be readable and writable with its own
		// current value, which catches a Get/Set pair that disagree.
		current := field.Get(cfg)
		if err := field.Set(cfg, current); err != nil {
			t.Errorf("%s: setting its own value %q failed: %v", field.Label, current, err)
		}
		if got := field.Get(cfg); got != current {
			t.Errorf("%s: round trip gave %q, want %q", field.Label, got, current)
		}
	}
}

func TestSettingsFieldValidation(t *testing.T) {
	cfg := config.New()
	byLabel := map[string]settingField{}
	for _, field := range settingsFields {
		byLabel[field.Label] = field
	}

	tests := []struct {
		label   string
		value   string
		wantErr bool
	}{
		{"ollama endpoint", "http://localhost:11434", false},
		{"ollama endpoint", "localhost:11434", true}, // no scheme
		{"context window", "4096", false},
		{"context window", "0", true},
		{"context window", "lots", true},
		{"provider", "claude", false},
		{"provider", "gemini", true},
		{"mode", "plan", false},
		{"mode", "turbo", true},
		{"model", "", true},
	}

	for _, tt := range tests {
		field, ok := byLabel[tt.label]
		if !ok {
			t.Fatalf("no field labelled %q", tt.label)
		}
		err := field.Set(cfg, tt.value)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s = %q: error %v, wantErr %v", tt.label, tt.value, err, tt.wantErr)
		}
	}
}

// TestSettingsEditsReachConfig walks the pane the way a user would and checks
// the change lands on the live config.
func TestSettingsEditsReachConfig(t *testing.T) {
	cfg := config.New()
	s := newSettings(cfg)
	s.resize(96, 30)

	// Move to "mode" and cycle it forward.
	for s.cursor < len(settingsFields) && settingsFields[s.cursor].Label != "mode" {
		s.move(1)
	}
	s.cycle(1)

	if cfg.Mode != types.ModeAuto {
		t.Errorf("mode: got %v, want auto (manual cycled forward)", cfg.Mode)
	}
}

func TestSettingsCursorStaysInRange(t *testing.T) {
	s := newSettings(config.New())
	s.resize(96, 30)

	s.move(-10)
	if s.cursor != 0 {
		t.Errorf("cursor after moving up past the top: %d, want 0", s.cursor)
	}

	s.move(100)
	if want := len(settingsFields) - 1; s.cursor != want {
		t.Errorf("cursor after moving down past the end: %d, want %d", s.cursor, want)
	}
}

// TestSettingsScrollsToCursor covers the short-terminal case where the rows
// do not all fit at once.
func TestSettingsScrollsToCursor(t *testing.T) {
	s := newSettings(config.New())
	s.resize(96, 16) // too short for every field

	if s.viewport.Height >= len(settingsFields) {
		t.Skipf("all %d fields fit; nothing to scroll", len(settingsFields))
	}

	s.cursor = len(settingsFields) - 1
	s.ensureVisible()

	top := s.viewport.YOffset
	bottom := top + s.viewport.Height - 1
	if s.cursor < top || s.cursor > bottom {
		t.Errorf("cursor %d is outside the visible rows %d..%d", s.cursor, top, bottom)
	}
}

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "not set"},
		{"short", "•••••"},
		{"sk-ant-api03-abcdef", "sk-a••••••cdef"},
	}
	for _, tt := range tests {
		if got := maskSecret(tt.in); got != tt.want {
			t.Errorf("maskSecret(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A key must never appear in full in the rendered pane.
func TestSecretsAreNotRendered(t *testing.T) {
	const key = "sk-ant-api03-supersecretvalue"

	cfg := config.New()
	cfg.APIKeys["claude"] = key

	s := newSettings(cfg)
	s.resize(96, 30)

	if strings.Contains(s.View(), key) {
		t.Error("the settings pane rendered an API key in full")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"qwen2.5:3b", 20, "qwen2.5:3b"},
		{"qwen2.5-coder:7b", 8, "qwen2.5…"},
		{"abc", 1, "…"},
		{"abc", 0, ""},
		{"héllo wörld", 6, "héllo…"},
	}
	for _, tt := range tests {
		if got := truncate(tt.in, tt.width); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
	}
}
