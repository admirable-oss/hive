package config_test

import (
	"testing"

	"github.com/admirable-oss/hive/internal/config"
)

// FuzzParse checks that no input panics the loader and that whatever it
// accepts stays inside the documented ranges.
func FuzzParse(f *testing.F) {
	f.Add(config.Render(config.Defaults()))
	f.Add([]byte("[daemon]\nautostart = false\nshutdown_timeout = \"1m\"\n"))
	f.Add([]byte("[terminal]\ndefault_width = 99999\nhistory_kb = -1\n"))
	f.Add([]byte("log = 3\n[log.level]\nx = 1\n"))
	f.Add([]byte("[[daemon]]\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, _, err := config.Parse(data)
		if err != nil {
			if cfg != config.Defaults() {
				t.Fatal("a rejected document must leave the defaults")
			}
			return
		}
		tm := cfg.Terminal
		switch {
		case tm.DefaultWidth < 20 || tm.DefaultWidth > 1000,
			tm.DefaultHeight < 5 || tm.DefaultHeight > 500,
			tm.HistoryKB < 4 || tm.HistoryKB > 64<<10,
			cfg.Log.MaxSizeMB < 1 || cfg.Log.MaxSizeMB > 1024,
			cfg.Log.MaxBackups < 0 || cfg.Log.MaxBackups > 100,
			cfg.Daemon.ShutdownTimeout <= 0,
			cfg.Process.StopGrace < 0:
			t.Fatalf("out-of-range value accepted: %+v", cfg)
		}
		// Whatever was accepted must survive a render/parse round trip.
		again, warnings, err := config.Parse(config.Render(cfg))
		if err != nil || len(warnings) != 0 || again != cfg {
			t.Fatalf("round trip failed: %v %v\n%+v\n%+v", err, warnings, cfg, again)
		}
	})
}
