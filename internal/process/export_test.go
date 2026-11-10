package process

import "time"

// SetAdoptRetry shortens the unresponsive-shim retry for a test.
func SetAdoptRetry(first, maxWait time.Duration) (restore func()) {
	f, m := adoptRetryInterval, adoptRetryMax
	adoptRetryInterval, adoptRetryMax = first, maxWait
	return func() { adoptRetryInterval, adoptRetryMax = f, m }
}
