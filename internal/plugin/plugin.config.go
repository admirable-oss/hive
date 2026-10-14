package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
func (h *Host) prepareConfiguration(ctx context.Context, l *loaded) error {
	if h.cfg.Store == nil {
		return errors.New("plugin host has no store")
	}
	base := h.cfg.Store.ConfigDir(l.ID)
	if l.Manifest.Configuration.Directory != "" && l.Manifest.Configuration.Directory != "." {
		base = filepath.Join(base, filepath.FromSlash(l.Manifest.Configuration.Directory))
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}
	configPath := filepath.Join(base, pluginConfigFile)
	config := ConfigurationFile{SchemaVersion: l.Manifest.Configuration.Version, Values: defaults(l.Manifest.Configuration.Settings)}
	if err := jsonfile.Read(configPath, &config); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read plugin settings: %w", err)
		}
		// A retained previous release means this is an upgrade from a version
		// that may not have created a settings file. Start from schema zero so
		// declared data/config migrations are not silently skipped.
		if l.PreviousVersion != "" && len(l.Manifest.Migrations) > 0 {
			config.SchemaVersion = 0
		}
		if len(l.Manifest.Configuration.Settings) > 0 || config.SchemaVersion > 0 || len(l.Manifest.Migrations) > 0 {
			if err := jsonfile.Write(configPath, config); err != nil {
				return fmt.Errorf("write default plugin settings: %w", err)
			}
		}
	}
	if config.Values == nil {
		config.Values = map[string]any{}
	}
	for config.SchemaVersion < l.Manifest.Configuration.Version {
		migration, ok := l.Manifest.Migration(config.SchemaVersion)
		if !ok {
			return fmt.Errorf("settings for plugin %s need migration %d to %d, but the manifest does not declare it", l.ID, config.SchemaVersion, config.SchemaVersion+1)
		}
		migrated, err := h.runMigration(ctx, l, migration, configPath, config)
		if err != nil {
			return fmt.Errorf("migrate plugin settings %d to %d: %w", migration.From, migration.To, err)
		}
		config.SchemaVersion = migration.To
		config.Values = migrated.Values
		if err := jsonfile.Write(configPath, config); err != nil {
			return fmt.Errorf("stage migrated plugin settings: %w", err)
		}
	}
	return validateConfiguration(config, l.Manifest.Configuration)
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

func validateConfiguration(config ConfigurationFile, spec Configuration) error {
	if config.SchemaVersion < spec.Version {
		return fmt.Errorf("plugin settings schema_version %d is older than required version %d", config.SchemaVersion, spec.Version)
	}
	byName := make(map[string]Setting, len(spec.Settings))
	for _, setting := range spec.Settings {
		byName[setting.Name] = setting
	}
	for name, value := range config.Values {
		setting, ok := byName[name]
		if !ok {
			// A previous release may be running after rollback. Keep unknown
			// fields from a newer schema intact so that rolling forward again
			// does not lose user settings.
			if config.SchemaVersion > spec.Version {
				continue
			}
			return fmt.Errorf("plugin settings contain undeclared key %q", name)
		}
		if !validSettingValue(setting.Type, value) {
			return fmt.Errorf("plugin setting %q must have type %s", name, setting.Type)
		}
	}
	for _, setting := range spec.Settings {
		if setting.Required {
			if _, ok := config.Values[setting.Name]; !ok {
				return fmt.Errorf("required plugin setting %q is missing from HIVE_PLUGIN_CONFIG", setting.Name)
			}
		}
	}
	return nil
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
