// Package config loads and saves ~/.digicli/config.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/10txn/digicli/internal/types"
)

// Default values used for a fresh config and for any field left empty.
const (
	DefaultProvider      = "ollama"
	DefaultModel         = "qwen2.5-coder:7b"
	DefaultEndpoint      = "http://localhost:11434"
	DefaultMode          = types.ModeManual
	DefaultContextWindow = 8192
)

// Providers are the backends DigiCLI knows about.
var Providers = []string{"ollama", "claude", "openai"}

// Config is the on-disk configuration. API keys are stored in plaintext for
// now (the file is written 0600); encrypting them is an open question.
type Config struct {
	// Provider is the active LLM backend: ollama, claude or openai.
	Provider string `json:"provider"`
	// Model is the model name passed to that provider.
	Model string `json:"model"`
	// Mode is the interaction mode the session starts in.
	Mode types.Mode `json:"mode"`
	// OllamaEndpoint is the base URL of the local Ollama server.
	OllamaEndpoint string `json:"ollamaEndpoint"`
	// ContextWindow is the model's context size in tokens, used for the
	// usage readout in the status bar.
	ContextWindow int `json:"contextWindow"`
	// APIKeys is keyed by provider name ("claude", "openai").
	APIKeys map[string]string `json:"apiKeys"`
	// UpdateCheck turns on the startup check for a newer release. It is the
	// one thing DigiCLI contacts that is not the model endpoint, so it stays
	// off until the user says otherwise.
	UpdateCheck bool `json:"updateCheck"`
	// UpdatePrompted records that the question has been answered, in the
	// first-run prompt or in the settings pane. A fresh config has both of
	// these false, which is what makes DigiCLI ask rather than assume — and
	// which asks existing users too, since their config predates the field.
	UpdatePrompted bool `json:"updatePrompted"`
}

// New returns a Config populated with defaults.
func New() *Config {
	return &Config{
		Provider:       DefaultProvider,
		Model:          DefaultModel,
		Mode:           DefaultMode,
		OllamaEndpoint: DefaultEndpoint,
		ContextWindow:  DefaultContextWindow,
		APIKeys:        map[string]string{},
	}
}

// applyDefaults fills in anything a hand-edited or older config left empty, so
// a partial file never produces a half-working session.
func (c *Config) applyDefaults() {
	if c.Provider == "" {
		c.Provider = DefaultProvider
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.OllamaEndpoint == "" {
		c.OllamaEndpoint = DefaultEndpoint
	}
	if c.ContextWindow <= 0 {
		c.ContextWindow = DefaultContextWindow
	}
	if !c.Mode.Valid() {
		c.Mode = DefaultMode
	}
	if c.APIKeys == nil {
		c.APIKeys = map[string]string{}
	}
}

// Load reads ~/.digicli/config.json. A missing file is not an error: defaults
// are returned and written to disk so the user has something to edit.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, fmt.Errorf("locating config: %w", err)
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		cfg := New()
		if err := cfg.Save(); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	cfg := New()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

// Save writes the config atomically with 0600 permissions, since it may hold
// API keys.
func (c *Config) Save() error {
	path, err := Path()
	if err != nil {
		return fmt.Errorf("locating config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	data = append(data, '\n')

	// Write to a temp file in the same directory, then rename, so an
	// interrupted write can't truncate a good config.
	tmp, err := os.CreateTemp(dir, fileName+".*")
	if err != nil {
		return fmt.Errorf("creating temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing temp config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("saving %s: %w", path, err)
	}
	return nil
}

// HasKey reports whether an API key is configured for the given provider.
func (c *Config) HasKey(provider string) bool {
	return c.APIKeys[provider] != ""
}
