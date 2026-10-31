package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/runtime"
)

func newProxyCmd(a *app) *cobra.Command {
	var (
		toObserver bool
		member     string
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "proxy",
		Short: "Bridge standard input and output to the daemon (what remote clients run over SSH)",
		Long: `Connect standard input and output to this machine's daemon, starting it when
it is not running. ` + "`hive --remote <host>`" + ` and saved machines run
` + "`ssh <host> hive proxy`" + ` and talk to the daemon through it.

With --observer it bridges to the observer socket instead, where only
observer tokens work and every method only reads: what remote observers
(` + "`hive --remote <host> observer …`" + `, dashboards) connect through.

With --member it bridges to the member socket, as that member: what the
forced command of a member's key in authorized_keys runs
(` + "`hive access invite`" + ` writes it). Over ssh it serves only clients asking
for ` + "`hive proxy`" + `, and refuses logins and every other command.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.remote != nil {
				return &usageError{errors.New("proxy runs on the machine whose daemon it bridges; drop --remote/--machine")}
			}
			path := a.socket
			var binding []byte
			switch {
			case member != "" && toObserver:
				return &usageError{errors.New("give --observer or --member, not both")}
			case member != "":
				if !access.ValidID(member) {
					return &usageError{fmt.Errorf("--member %q is not a member ID", member)}
				}
				// ssh replaced what the member's side asked for with this
				// command; serve only the clients that asked for a proxy.
				if a.getenv("SSH_CONNECTION") != "" {
					if err := access.CheckOriginal(a.getenv("SSH_ORIGINAL_COMMAND")); err != nil {
						return err
					}
				}
				path = runtime.MemberSocketPath(a.root)
				binding, _ = json.Marshal(access.Binding{Member: member, From: sshFrom(a.getenv("SSH_CONNECTION"))})
				binding = append(binding, '\n')
			case toObserver:
				path = runtime.ObserverSocketPath(a.root)
			}
			conn, err := dialSocket(cmd.Context(), path)
			if err != nil {
				return err
			}
			defer conn.Close()
			if binding != nil {
				if _, err := conn.Write(binding); err != nil {
					return err
				}
			}
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
	cmd.Flags().StringVar(&member, "member", "", "bridge to the member socket as this member `id` (what a member's ssh key runs)")
	return cmd
}

// sshFrom is where an ssh connection comes from: the client's address and
// port in SSH_CONNECTION ("client port server port").
func sshFrom(conn string) string {
	f := strings.Fields(conn)
	if len(f) < 2 {
		return ""
	}
	return f[0] + " " + f[1]
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
