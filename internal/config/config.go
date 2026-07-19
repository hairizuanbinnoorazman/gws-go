// Package config resolves and creates the application's configuration directory.
package config

import (
	"errors"
	"os"
	"path/filepath"
)

// DirEnv overrides the default configuration directory.
const DirEnv = "GWS_GO_CONFIG_DIR"

// Dir returns the configured application directory without creating it.
func Dir() (string, error) {
	if dir := os.Getenv(DirEnv); dir != "" {
		return dir, nil
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "gws-go"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot determine config directory: set " + DirEnv)
	}
	return filepath.Join(home, ".config", "gws-go"), nil
}

// EnsureDir creates and returns the owner-only application directory.
func EnsureDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
