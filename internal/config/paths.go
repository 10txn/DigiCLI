package config

import (
	"os"
	"path/filepath"
)

const (
	dirName  = ".digicli"
	fileName = "config.json"
)

// Dir returns the directory DigiCLI keeps its state in (~/.digicli).
// DIGICLI_HOME overrides it, which keeps tests off the real home directory.
func Dir() (string, error) {
	if override := os.Getenv("DIGICLI_HOME"); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, dirName), nil
}

// Path returns the full path to config.json.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}
