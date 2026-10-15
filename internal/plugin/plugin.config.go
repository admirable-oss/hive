package plugin

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

const pluginConfigFile = "settings.json"

// ConfigurationFile is the JSON file named by HIVE_PLUGIN_CONFIG.
type ConfigurationFile struct {
	SchemaVersion int            `json:"schema_version"`
	Values        map[string]any `json:"values"`
}

// ConfigDir is the host-owned persistent configuration root for a plugin.
// Unlike DataDir, it is intended for settings and is kept separate from
// plugin-created application state.
func (s *Store) ConfigDir(id string) string { return filepath.Join(s.dir, ".config", id) }

// prepareConfiguration creates defaults, validates existing settings, and
// executes each declared version migration before plugin code can start.
// Settings the schema no longer declares are kept, and reported as
// warnings: a plugin release may drop a setting without breaking.
func (h *Host) prepareConfiguration(ctx context.Context, l *loaded) ([]string, error) {
	if h.cfg.Store == nil {
		return nil, errors.New("plugin host has no store")
	}
	base := h.cfg.Store.ConfigDir(l.ID)
	if l.Manifest.Configuration.Directory != "" && l.Manifest.Configuration.Directory != "." {
		base = filepath.Join(base, filepath.FromSlash(l.Manifest.Configuration.Directory))
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, fmt.Errorf("create configuration directory: %w", err)
	}
	configPath := filepath.Join(base, pluginConfigFile)
	config := ConfigurationFile{SchemaVersion: l.Manifest.Configuration.Version, Values: defaults(l.Manifest.Configuration.Settings)}
	if err := jsonfile.Read(configPath, &config); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read plugin settings: %w", err)
		}
		// A retained previous release means this is an upgrade from a version
		// that may not have created a settings file. Start from schema zero so
		// declared data/config migrations are not silently skipped.
		if l.PreviousVersion != "" && len(l.Manifest.Migrations) > 0 {
			config.SchemaVersion = 0
		}
		if len(l.Manifest.Configuration.Settings) > 0 || config.SchemaVersion > 0 || len(l.Manifest.Migrations) > 0 {
			if err := jsonfile.Write(configPath, config); err != nil {
				return nil, fmt.Errorf("write default plugin settings: %w", err)
			}
		}
	}
	if config.Values == nil {
		config.Values = map[string]any{}
	}
	for config.SchemaVersion < l.Manifest.Configuration.Version {
		migration, ok := l.Manifest.Migration(config.SchemaVersion)
		if !ok {
			return nil, fmt.Errorf("settings for plugin %s need migration %d to %d, but the manifest does not declare it", l.ID, config.SchemaVersion, config.SchemaVersion+1)
		}
		migrated, err := h.runMigration(ctx, l, migration, configPath, config)
		if err != nil {
			return nil, fmt.Errorf("migrate plugin settings %d to %d: %w", migration.From, migration.To, err)
		}
		config.SchemaVersion = migration.To
		config.Values = migrated.Values
		if err := jsonfile.Write(configPath, config); err != nil {
			return nil, fmt.Errorf("stage migrated plugin settings: %w", err)
		}
	}
	return ValidateSettings(config, l.Manifest.Configuration)
}

func (h *Host) runMigration(ctx context.Context, l *loaded, migration Migration, configPath string, config ConfigurationFile) (ConfigurationFile, error) {
	stage := configPath + ".migrate"
	if err := jsonfile.Write(stage, config); err != nil {
		return ConfigurationFile{}, err
	}
	defer os.Remove(stage)
	commandCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	_, err := h.runWithEnvironment(commandCtx, l, migration.Command, Context{Trigger: "migration"}, nil, map[string]string{
		"HIVE_PLUGIN_CONFIG":              stage,
		"HIVE_PLUGIN_CONFIG_FROM_VERSION": fmt.Sprint(migration.From),
		"HIVE_PLUGIN_CONFIG_TO_VERSION":   fmt.Sprint(migration.To),
	})
	if err != nil {
		return ConfigurationFile{}, err
	}
	var migrated ConfigurationFile
	if err := jsonfile.Read(stage, &migrated); err != nil {
		return ConfigurationFile{}, fmt.Errorf("migration did not leave valid JSON settings: %w", err)
	}
	if migrated.Values == nil {
		migrated.Values = map[string]any{}
	}
	if migrated.SchemaVersion != config.SchemaVersion {
		return ConfigurationFile{}, fmt.Errorf("migration must leave schema_version at %d; Hive advances it after success", config.SchemaVersion)
	}
	return migrated, nil
}

func defaults(settings []Setting) map[string]any {
	values := make(map[string]any, len(settings))
	for _, setting := range settings {
		if setting.Default != nil {
			values[setting.Name] = setting.Default
		}
	}
	return values
}

// ValidateSettings checks settings against their schema. Wrong types,
// missing required settings and a schema older than the plugin's are
// errors; settings the schema does not declare are warnings (obsolete, or
// from a newer release the user rolled back from) and are kept.
func ValidateSettings(config ConfigurationFile, spec Configuration) ([]string, error) {
	if config.SchemaVersion < spec.Version {
		return nil, fmt.Errorf("plugin settings schema_version %d is older than required version %d", config.SchemaVersion, spec.Version)
	}
	var warnings []string
	byName := make(map[string]Setting, len(spec.Settings))
	for _, setting := range spec.Settings {
		byName[setting.Name] = setting
	}
	for name, value := range config.Values {
		setting, ok := byName[name]
		if !ok {
			// Kept, not deleted: a newer release (rolled back from) may own
			// it, and the user may want it back.
			if config.SchemaVersion <= spec.Version {
				warnings = append(warnings, fmt.Sprintf("setting %q is no longer declared; it is kept but unused", name))
			}
			continue
		}
		if !validSettingValue(setting.Type, value) {
			return warnings, fmt.Errorf("plugin setting %q must have type %s", name, setting.Type)
		}
	}
	for _, setting := range spec.Settings {
		if setting.Required {
			if _, ok := config.Values[setting.Name]; !ok {
				return warnings, fmt.Errorf("required plugin setting %q is missing from HIVE_PLUGIN_CONFIG", setting.Name)
			}
		}
	}
	slices.Sort(warnings)
	return warnings, nil
}

// Migration returns the sequential migration starting at version.
func (m *Manifest) Migration(version int) (Migration, bool) {
	for _, migration := range m.Migrations {
		if migration.From == version {
			return migration, true
		}
	}
	return Migration{}, false
}

func migrationFrom(migrations []Migration, version int) bool {
	for _, migration := range migrations {
		if migration.From == version {
			return true
		}
	}
	return false
}

// ConfigView is a plugin's settings with their schema: what `hive plugin
// config` shows and the UI's settings form is built from.
type ConfigView struct {
	Plugin        string         `json:"plugin"`
	Name          string         `json:"name"`
	SchemaVersion int            `json:"schema_version"`
	Settings      []Setting      `json:"settings"`
	Values        map[string]any `json:"values"`
	// Warnings and Error are what validation says about the values.
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
	Path     string   `json:"path"`
}

// SettingsPath is where plugin m keeps its settings.
func (s *Store) SettingsPath(m *Manifest) string {
	base := s.ConfigDir(m.ID)
	if m.Configuration.Directory != "" && m.Configuration.Directory != "." {
		base = filepath.Join(base, filepath.FromSlash(m.Configuration.Directory))
	}
	return filepath.Join(base, pluginConfigFile)
}

// Config reads plugin id's settings and checks them against its schema.
// Missing settings read as their defaults.
func (s *Store) Config(ctx context.Context, id, hiveVersion string) (ConfigView, error) {
	p, err := s.Get(ctx, id, hiveVersion)
	if err != nil {
		return ConfigView{}, err
	}
	if p.Manifest == nil {
		return ConfigView{}, fmt.Errorf("plugin %s: %s", id, p.Problem)
	}
	m := p.Manifest
	view := ConfigView{Plugin: id, Name: m.Name, Settings: slices.Clone(m.Configuration.Settings), Path: s.SettingsPath(m)}
	if view.Settings == nil {
		view.Settings = []Setting{}
	}
	file := ConfigurationFile{SchemaVersion: m.Configuration.Version, Values: defaults(m.Configuration.Settings)}
	if err := jsonfile.Read(view.Path, &file); err != nil && !errors.Is(err, os.ErrNotExist) {
		view.Error = "read settings: " + err.Error()
	}
	if file.Values == nil {
		file.Values = map[string]any{}
	}
	view.SchemaVersion, view.Values = file.SchemaVersion, file.Values
	if view.Error == "" {
		warnings, err := ValidateSettings(file, m.Configuration)
		view.Warnings = warnings
		if err != nil {
			view.Error = err.Error()
		}
	}
	return view, nil
}

// SetConfig changes plugin id's settings: set gives new values (checked
// against their declared types), unset removes values (back to defaults,
// or absent). Settings the schema does not declare cannot be set. The
// file is replaced atomically.
func (s *Store) SetConfig(ctx context.Context, id, hiveVersion string, set map[string]any, unset []string) (ConfigView, error) {
	view, err := s.Config(ctx, id, hiveVersion)
	if err != nil {
		return ConfigView{}, err
	}
	byName := map[string]Setting{}
	for _, st := range view.Settings {
		byName[st.Name] = st
	}
	values := maps.Clone(view.Values)
	for _, name := range unset {
		st, ok := byName[name]
		switch {
		case !ok && values[name] == nil:
			return view, fmt.Errorf("%s has no setting %q", id, name)
		case ok && st.Default != nil:
			values[name] = st.Default
		default:
			delete(values, name)
		}
	}
	for name, v := range set {
		st, ok := byName[name]
		if !ok {
			return view, fmt.Errorf("%s has no setting %q (have %s)", id, name, settingNames(view.Settings))
		}
		if !validSettingValue(st.Type, v) {
			return view, fmt.Errorf("setting %q must be a %s", name, st.Type)
		}
		values[name] = v
	}
	p, err := s.Get(ctx, id, hiveVersion)
	if err != nil {
		return view, err
	}
	file := ConfigurationFile{SchemaVersion: view.SchemaVersion, Values: values}
	if file.SchemaVersion == 0 {
		file.SchemaVersion = p.Manifest.Configuration.Version
	}
	if _, err := ValidateSettings(file, p.Manifest.Configuration); err != nil {
		return view, err
	}
	if err := os.MkdirAll(filepath.Dir(view.Path), 0o700); err != nil {
		return view, err
	}
	if err := jsonfile.Write(view.Path, file); err != nil {
		return view, err
	}
	return s.Config(ctx, id, hiveVersion)
}

func settingNames(settings []Setting) string {
	names := make([]string, len(settings))
	for i, s := range settings {
		names[i] = s.Name
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// ParseSetting reads a value typed by the user for a setting of typ.
func ParseSetting(typ, raw string) (any, error) {
	switch typ {
	case "string":
		return raw, nil
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true", "yes", "on", "1":
			return true, nil
		case "false", "no", "off", "0":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not true or false", raw)
	case "integer":
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", raw)
		}
		return n, nil
	case "number":
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return f, nil
	}
	return nil, fmt.Errorf("unknown setting type %q", typ)
}
