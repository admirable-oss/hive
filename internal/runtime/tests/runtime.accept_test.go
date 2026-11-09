//go:build !windows

package runtime_test

import (
	"context"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/runtime"
)

// starvedFactory's listeners fail their first Accepts as a process out of
// file descriptors does.
type starvedFactory struct{ fails int32 }

func (f starvedFactory) Listen(network, address string) (net.Listener, error) {
	ln, err := runtime.NetListenerFactory{}.Listen(network, address)
	if err != nil {
		return nil, err
	}
	l := &starvedListener{Listener: ln}
	l.left.Store(f.fails)
	return l, nil
}

type starvedListener struct {
	net.Listener
	left atomic.Int32
}

func (l *starvedListener) Accept() (net.Conn, error) {
	if l.left.Add(-1) >= 0 {
		return nil, &net.OpError{Op: "accept", Net: "unix", Err: os.NewSyscallError("accept4", syscall.EMFILE)}
	}
	return l.Listener.Accept()
}

// Running out of file descriptors fails some Accepts; the daemon must keep
// accepting once they are free, not stop serving for good.
func TestRuntime_AcceptSurvivesRunningOutOfDescriptors(t *testing.T) {
	path := shortSock(t)
	mod := runtime.NewModule(runtime.Config{SocketPath: path, Listener: starvedFactory{fails: 5}})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mod.Service.Stop(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.NewService(client.Config{SocketPath: path}).Ping(ctx); err != nil {
		t.Fatalf("daemon unreachable after transient accept failures: %v", err)
	}
}
