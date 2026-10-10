package platform

import "context"

// WatchShutdown calls prepare when the host is about to shut down or go to
// sleep, and holds the shutdown back until prepare returns (within the
// system's limit: logind's InhibitDelayMaxSec, 5 s by default). It blocks
// until ctx ends. It returns ErrUnsupported where the OS gives no such
// notice; on macOS launchd sends SIGTERM at shutdown instead, which the
// daemon handles as any stop.
func WatchShutdown(ctx context.Context, prepare func(ctx context.Context)) error {
	return watchShutdown(ctx, prepare)
}
