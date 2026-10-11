package main

import (
	"errors"
	"io"
	"net"
	"os"

	"github.com/spf13/cobra"
)

func newProxyCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "proxy",
		Short: "Bridge standard input and output to the daemon (what remote clients run over SSH)",
		Long: `Connect standard input and output to this machine's daemon, starting it when
it is not running. ` + "`hive --remote <host>`" + ` and saved machines run
` + "`ssh <host> hive proxy`" + ` and talk to the daemon through it.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.remote != nil {
				return &usageError{errors.New("proxy runs on the machine whose daemon it bridges; drop --remote/--machine")}
			}
			var d net.Dialer
			conn, err := d.DialContext(cmd.Context(), "unix", a.socket)
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
}
