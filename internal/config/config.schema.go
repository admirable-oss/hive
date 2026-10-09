package config

import (
	"fmt"
	"maps"
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
			"history_kb":     deprecated("attaching now repaints the screen; set terminal.scrollback_mb for history instead"),
		},
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
