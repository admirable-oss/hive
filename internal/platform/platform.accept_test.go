package platform_test

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/admirable-oss/hive/internal/platform"
)

func TestAcceptRetry(t *testing.T) {
	var r platform.AcceptRetry
	wrapped := &net.OpError{Op: "accept", Err: fmt.Errorf("wrap: %w", syscall.EMFILE)}
	if !r.Retry(wrapped) || !r.First() {
		t.Fatal("EMFILE is temporary and starts a run")
	}
	if !r.Retry(syscall.ECONNABORTED) || r.First() {
		t.Fatal("ECONNABORTED is temporary; a second failure is not the first")
	}
	r.Reset()
	for _, err := range []error{net.ErrClosed, errors.New("other"), syscall.EBADF} {
		if r.Retry(err) {
			t.Fatalf("%v must end the loop", err)
		}
	}
}
