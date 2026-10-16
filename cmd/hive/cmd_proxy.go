package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/runtime"
)

func newProxyCmd(a *app) *cobra.Command {
	var toObserver bool
	cmd := needsDaemon(&cobra.Command{
		Use:   "proxy",
		Short: "Bridge standard input and output to the daemon (what remote clients run over SSH)",
		Long: `Connect standard input and output to this machine's daemon, starting it when
it is not running. ` + "`hive --remote <host>`" + ` and saved machines run
` + "`ssh <host> hive proxy`" + ` and talk to the daemon through it.

With --observer it bridges to the observer socket instead, where only
observer tokens work and every method only reads: what remote observers
(` + "`hive --remote <host> observer …`" + `, dashboards) connect through.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.remote != nil {
				return &usageError{errors.New("proxy runs on the machine whose daemon it bridges; drop --remote/--machine")}
			}
			path := a.socket
			if toObserver {
				path = runtime.ObserverSocketPath(a.root)
			}
			conn, err := dialSocket(cmd.Context(), path)
			if err != nil {
				return err
			}
			defer conn.Close()
			go func() {
				_, _ = io.Copy(conn, os.Stdin)
				// The client is done sending; the daemon then ends the
				// connection, which ends the copy below.
				if uc, ok := conn.(*net.UnixConn); ok {
					_ = uc.CloseWrite()
				}
			}()
			_, err = io.Copy(a.out, conn)
			if errors.Is(err, net.ErrClosed) {
				err = nil
			}
			return err
		},
	})
	cmd.Flags().BoolVar(&toObserver, "observer", false, "bridge to the observer socket (read-only, token-authenticated)")
	return cmd
}

// dialSocket connects to a daemon socket, waiting briefly for one the
// daemon is still opening (the observer socket, right after a start).
func dialSocket(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := d.DialContext(ctx, "unix", path)
		if err == nil || !platform.NotListening(err) || time.Now().After(deadline) {
			return conn, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
