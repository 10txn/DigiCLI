package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/10txn/digicli/internal/types"
)

// useTempHome points DIGICLI_HOME at a temp directory for the test's duration.
func useTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DIGICLI_HOME", dir)
	return dir
}

func TestLoadCreatesDefaultsWhenMissing(t *testing.T) {
	dir := useTempHome(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider != DefaultProvider || cfg.Model != DefaultModel {
		t.Errorf("got provider %q model %q, want defaults", cfg.Provider, cfg.Model)
	}
	if cfg.Mode != DefaultMode {
		t.Errorf("got mode %v, want %v", cfg.Mode, DefaultMode)
	}

	// The defaults should have been written out for the user to edit.
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Errorf("config was not created: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	useTempHome(t)

	cfg := New()
	cfg.Mode = types.ModePlan
	cfg.Model = "llama3.1:8b"
	cfg.APIKeys["claude"] = "sk-ant-test"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Mode != types.ModePlan {
		t.Errorf("mode: got %v, want plan", got.Mode)
	}
	if got.Model != "llama3.1:8b" {
		t.Errorf("model: got %q", got.Model)
	}
	if got.APIKeys["claude"] != "sk-ant-test" {
		t.Errorf("api key was not persisted")
	}
}

func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	dir := useTempHome(t)

	if err := New().Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The file may hold API keys, so nothing but the owner should read it.
	info, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions: got %o, want 600", perm)
	}
}

func TestLoadFillsGapsInPartialConfig(t *testing.T) {
	dir := useTempHome(t)

	partial := []byte(`{"model":"mistral:7b"}`)
	if err := os.WriteFile(filepath.Join(dir, fileName), partial, 0o600); err != nil {
		t.Fatalf("seeding config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model != "mistral:7b" {
		t.Errorf("model: got %q, want the value from disk", cfg.Model)
	}
	if cfg.Provider != DefaultProvider {
		t.Errorf("provider: got %q, want default", cfg.Provider)
	}
	if cfg.OllamaEndpoint != DefaultEndpoint {
		t.Errorf("endpoint: got %q, want default", cfg.OllamaEndpoint)
	}
	if cfg.APIKeys == nil {
		t.Error("APIKeys should never be nil after Load")
	}
}

func TestLoadReportsInvalidJSON(t *testing.T) {
	dir := useTempHome(t)

	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seeding config: %v", err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected an error for a corrupt config")
	}
}

func TestSaveLeavesNoTempFiles(t *testing.T) {
	dir := useTempHome(t)

	if err := New().Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) != 1 || entries[0].Name() != fileName {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only %s, got %v", fileName, names)
	}
}
