// Package config loads Hive's user configuration: a TOML file at
// ~/.config/hive/config.toml (or $XDG_CONFIG_HOME/hive/config.toml, or
// $HIVE_CONFIG). Every key is optional.
//
// Loading is forgiving by design. A syntax error is reported, because the
// file cannot be understood at all, but an unknown key or an invalid value
// only produces a warning and keeps that key's default. A typo in one setting
// must never stop the daemon that keeps agents alive.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// EnvPath overrides the config file location.
const EnvPath = "HIVE_CONFIG"

// EnvLogLevel overrides log.level, e.g. HIVE_LOG=debug.
const EnvLogLevel = "HIVE_LOG"

// ResolvePath returns the config file location: $HIVE_CONFIG, else
// $XDG_CONFIG_HOME/hive/config.toml, else <home>/.config/hive/config.toml.
func ResolvePath(getenv func(string) string, home string) string {
	if p := getenv(EnvPath); p != "" {
		return p
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "hive", "config.toml")
	}
	return filepath.Join(home, ".config", "hive", "config.toml")
}

// Load reads the config file at path. A missing file yields the defaults.
// Warnings describe ignored keys and values that fell back to defaults.
func Load(path string) (Config, []string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Defaults(), nil, nil
	}
	if err != nil {
		return Defaults(), nil, fmt.Errorf("read config: %w", err)
	}
	cfg, warnings, err := Parse(data)
	if err != nil {
		return cfg, warnings, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, warnings, nil
}

// Parse decodes a TOML document on top of the defaults.
func Parse(data []byte) (Config, []string, error) {
	cfg := Defaults()
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		if de, ok := errors.AsType[*toml.DecodeError](err); ok {
			row, col := de.Position()
			return cfg, nil, fmt.Errorf("syntax error at line %d, column %d: %w", row, col, err)
		}
		return cfg, nil, err
	}
	warnings := cfg.apply(doc) // mutates cfg; evaluate before returning it
	return cfg, warnings, nil
}

// ApplyEnv applies environment overrides (HIVE_LOG) to cfg.
func ApplyEnv(cfg Config, getenv func(string) string) (Config, []string) {
	var warnings []string
	if v := getenv(EnvLogLevel); v != "" {
		if err := setLevel(&cfg.Log.Level, v); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v; keeping %q", EnvLogLevel, err, cfg.Log.Level))
		}
	}
	return cfg, warnings
}
