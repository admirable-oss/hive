package runtime

import (
	"log/slog"
	"net"

	"github.com/admirable-oss/hive/internal/platform"
)

// keepListening keeps a daemon socket served when its file is removed
// (platform.KeepListening), re-opening it through f.
func keepListening(ln net.Listener, path string, f ListenerFactory, log *slog.Logger) net.Listener {
	return platform.KeepListening(ln, path, func() (net.Listener, error) { return f.Listen("unix", path) }, log)
}
