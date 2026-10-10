//go:build linux

package platform

import (
	"context"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	login1       = "org.freedesktop.login1"
	login1Path   = "/org/freedesktop/login1"
	login1Mgr    = "org.freedesktop.login1.Manager"
	prepareLimit = 4 * time.Second // under logind's default 5 s delay
)

// watchShutdown takes a logind "delay" inhibitor lock: logind announces a
// shutdown or sleep with PrepareForShutdown/PrepareForSleep(true) and waits
// until every delay lock is released (or the delay runs out).
func watchShutdown(ctx context.Context, prepare func(ctx context.Context)) error {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, member := range []string{"PrepareForShutdown", "PrepareForSleep"} {
		if err := conn.AddMatchSignalContext(ctx, dbus.WithMatchInterface(login1Mgr), dbus.WithMatchMember(member)); err != nil {
			return err
		}
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)

	obj := conn.Object(login1, login1Path)
	inhibit := func() (*os.File, error) {
		var fd dbus.UnixFD
		err := obj.CallWithContext(ctx, login1Mgr+".Inhibit", 0, "shutdown:sleep", "Hive", "Saving the agent session", "delay").Store(&fd)
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd), "logind-inhibitor"), nil
	}
	lock, err := inhibit()
	if err != nil {
		return err
	}
	release := func() {
		if lock != nil {
			_ = lock.Close()
			lock = nil
		}
	}
	defer release()

	for {
		select {
		case <-ctx.Done():
			return nil
		case sig, ok := <-signals:
			if !ok {
				return nil
			}
			if len(sig.Body) != 1 {
				continue
			}
			starting, _ := sig.Body[0].(bool)
			switch {
			case starting:
				pctx, cancel := context.WithTimeout(ctx, prepareLimit)
				prepare(pctx)
				cancel()
				release()
			case lock == nil:
				// Woken from sleep, or the shutdown was cancelled: hold a
				// lock for the next one.
				if lock, err = inhibit(); err != nil {
					return err
				}
			}
		}
	}
}
