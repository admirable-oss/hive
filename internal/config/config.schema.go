package config

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
)

// setter validates one raw TOML value and stores it.
type setter func(raw any) error

// schema maps section → key → setter, binding each key to its field in c.
// It is the single list of accepted keys; Render documents the same keys.
func (c *Config) schema() map[string]map[string]setter {
	return map[string]map[string]setter{
		"daemon": {
			"autostart":        boolean(&c.Daemon.Autostart),
			"shutdown_timeout": duration(&c.Daemon.ShutdownTimeout, time.Second, 10*time.Minute),
		},
		"log": {
			"level": func(raw any) error { return setString(raw, func(s string) error { return setLevel(&c.Log.Level, s) }) },
			"format": func(raw any) error {
				return setString(raw, func(s string) error { return setFormat(&c.Log.Format, s) })
			},
			"max_size_mb": integer(&c.Log.MaxSizeMB, 1, 1024),
			"max_backups": integer(&c.Log.MaxBackups, 0, 100),
		},
		"process": {
			"stop_grace": duration(&c.Process.StopGrace, 0, 5*time.Minute),
		},
		"terminal": {
			"default_width":  integer(&c.Terminal.DefaultWidth, 20, 1000),
			"default_height": integer(&c.Terminal.DefaultHeight, 5, 500),
			"scrollback_mb":  integer(&c.Terminal.ScrollbackMB, 0, 1024),
			"shell": func(raw any) error {
				return setString(raw, func(s string) error { c.Terminal.Shell = strings.TrimSpace(s); return nil })
			},
			"history_kb": deprecated("attaching now repaints the screen; set terminal.scrollback_mb for history instead"),
		},
		"git": {
			"refresh_interval": duration(&c.Git.RefreshInterval, time.Second, 10*time.Minute),
		},
		"ui": {
			"sidebar":       boolean(&c.UI.Sidebar),
			"sidebar_width": integer(&c.UI.SidebarWidth, 16, 120),
			"mouse":         boolean(&c.UI.Mouse),
			"clipboard": func(raw any) error {
				return setString(raw, func(s string) error {
					s = strings.ToLower(strings.TrimSpace(s))
					if !slices.Contains(clipboardModes, s) {
						return fmt.Errorf("%q is not one of %s", s, strings.Join(clipboardModes, ", "))
					}
					c.UI.Clipboard = s
					return nil
				})
			},
		},
		"theme": {
			"name": func(raw any) error {
				return setString(raw, func(s string) error {
					if s = strings.ToLower(strings.TrimSpace(s)); s == "" {
						return fmt.Errorf("is empty")
					}
					c.Theme.Name = s
					return nil
				})
			},
			"custom": stringTable(&c.Theme.Custom),
		},
		"keys": c.keysSchema(),
		"worktrees": {
			"directory": func(raw any) error {
				return setString(raw, func(s string) error {
					if s != "" && s != "~" && !strings.HasPrefix(s, "~/") && !filepath.IsAbs(s) {
						return fmt.Errorf("%q must be absolute or start with ~/", s)
					}
					c.Worktrees.Directory = s
					return nil
				})
			},
		},
	}
}

// keysSchema reads [keys]: prefix_keys, and a table per mode.
func (c *Config) keysSchema() map[string]setter {
	keys := map[string]setter{
		"prefix_keys": func(raw any) error {
			list, err := keyList(raw)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				return fmt.Errorf("needs at least one key")
			}
			c.Keys.Prefix = list
			return nil
		},
	}
	for _, mode := range KeyModes {
		keys[mode] = func(raw any) error {
			table, ok := raw.(map[string]any)
			if !ok && mode == "prefix" {
				return fmt.Errorf("is the table of prefix-mode bindings; set the prefix key itself with keys.prefix_keys")
			}
			if !ok {
				return typeError("a table of action = key(s)", raw)
			}
			bindings := map[string][]string{}
			var bad []string
			for _, action := range slices.Sorted(maps.Keys(table)) {
				list, err := keyList(table[action])
				if err != nil {
					bad = append(bad, action+" "+err.Error())
					continue
				}
				bindings[action] = list
			}
			if len(bindings) > 0 {
				if c.Keys.Modes == nil {
					c.Keys.Modes = map[string]map[string][]string{}
				}
				c.Keys.Modes[mode] = bindings
			}
			if len(bad) > 0 {
				return fmt.Errorf("%s (ignored)", strings.Join(bad, "; "))
			}
			return nil
		}
	}
	return keys
}

// keyList reads a key ("ctrl+a") or a list of keys; [] is an empty,
// non-nil list (unbind).
func keyList(raw any) ([]string, error) {
	switch v := raw.(type) {
	case string:
		return []string{v}, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, typeError("a key or a list of keys", item)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, typeError("a key or a list of keys", raw)
}

// stringTable reads a table of strings.
func stringTable(dst *map[string]string) setter {
	return func(raw any) error {
		table, ok := raw.(map[string]any)
		if !ok {
			return typeError("a table", raw)
		}
		out := map[string]string{}
		for k, v := range table {
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("%s %w", k, typeError("a string", v))
			}
			out[k] = s
		}
		if len(out) == 0 {
			out = nil // an empty table sets nothing
		}
		*dst = out
		return nil
	}
}

// apply sets every valid key in doc and returns a warning for each key it
// ignored. Sections and keys are visited in sorted order so warnings are
// deterministic.
func (c *Config) apply(doc map[string]any) []string {
	schema := c.schema()
	var warnings []string
	for _, section := range slices.Sorted(maps.Keys(doc)) {
		keys, known := schema[section]
		table, isTable := doc[section].(map[string]any)
		switch {
		case !known:
			warnings = append(warnings, fmt.Sprintf("unknown section or key %q ignored", section))
			continue
		case !isTable:
			warnings = append(warnings, fmt.Sprintf("%q must be a table; ignored", section))
			continue
		}
		for _, key := range slices.Sorted(maps.Keys(table)) {
			set, ok := keys[key]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("unknown key %s.%s ignored", section, key))
				continue
			}
			if err := set(table[key]); err != nil {
				warnings = append(warnings, fmt.Sprintf("%s.%s: %v; using the default", section, key, err))
			}
		}
	}
	return warnings
}

// deprecated accepts a removed key with a warning instead of failing.
func deprecated(why string) setter {
	return func(any) error { return fmt.Errorf("is no longer used (%s)", why) }
}

func boolean(dst *bool) setter {
	return func(raw any) error {
		v, ok := raw.(bool)
		if !ok {
			return typeError("a boolean", raw)
		}
		*dst = v
		return nil
	}
}

func integer(dst *int, lo, hi int) setter {
	return func(raw any) error {
		v, ok := raw.(int64)
		if !ok {
			return typeError("an integer", raw)
		}
		if v < int64(lo) || v > int64(hi) {
			return fmt.Errorf("%d is outside %d..%d", v, lo, hi)
		}
		*dst = int(v)
		return nil
	}
}

func duration(dst *time.Duration, lo, hi time.Duration) setter {
	return func(raw any) error {
		return setString(raw, func(s string) error {
			d, err := time.ParseDuration(s)
			if err != nil {
				return fmt.Errorf("invalid duration %q (use e.g. \"500ms\", \"15s\", \"2m\")", s)
			}
			if d < lo || d > hi {
				return fmt.Errorf("%s is outside %s..%s", d, lo, hi)
			}
			*dst = d
			return nil
		})
	}
}

func setString(raw any, set func(string) error) error {
	s, ok := raw.(string)
	if !ok {
		return typeError("a string", raw)
	}
	return set(s)
}

func setLevel(dst *string, s string) error {
	if _, err := logging.ParseLevel(s); err != nil {
		return err
	}
	*dst = strings.ToLower(strings.TrimSpace(s))
	if *dst == "warning" {
		*dst = "warn"
	}
	return nil
}

func setFormat(dst *string, s string) error {
	f, err := logging.ParseFormat(s)
	if err != nil {
		return err
	}
	*dst = string(f)
	return nil
}

func typeError(want string, raw any) error {
	return fmt.Errorf("must be %s, got %T", want, raw)
}
