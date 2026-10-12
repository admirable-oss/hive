package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/runtime"
)

func newDebugCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Look inside the daemon: profiles and resource use",
	}
	var (
		req runtime.PprofRequest
		out string
	)
	pprofCmd := needsDaemon(&cobra.Command{
		Use:   "pprof",
		Short: "Profile the daemon (cpu for --seconds, heap, goroutine, …) into a file for go tool pprof",
		Example: `  hive debug pprof                      # 30 s of CPU
  hive debug pprof --profile heap -o heap.pprof
  go tool pprof -http :8080 hive-cpu-*.pprof`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !slices.Contains(runtime.Profiles, req.Profile) {
				return &usageError{fmt.Errorf("--profile %q: one of %s", req.Profile, strings.Join(runtime.Profiles, ", "))}
			}
			if out == "" {
				out = fmt.Sprintf("hive-%s-%s.pprof", req.Profile, time.Now().Format("20060102-150405"))
				if req.Profile == "trace" {
					out = strings.TrimSuffix(out, ".pprof") + ".trace"
				}
			}
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), time.Duration(max(req.Seconds, 30)+30)*time.Second)
			defer cancel()
			if req.Profile == "cpu" || req.Profile == "trace" {
				secs := req.Seconds
				if secs <= 0 {
					secs = 30
				}
				fmt.Fprintf(a.errOut, "hive: profiling the daemon for %ds…\n", secs)
			}
			if err := a.client.Stream(ctx, "debug.pprof", req, f); err != nil {
				_ = f.Close()
				_ = os.Remove(out)
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			tool := "go tool pprof -http :8080 " + out
			if req.Profile == "trace" {
				tool = "go tool trace " + out
			}
			return a.done(map[string]any{"path": out, "profile": req.Profile}, "wrote %s; inspect it with `%s`", out, tool)
		},
	})
	pprofCmd.Flags().StringVar(&req.Profile, "profile", "cpu", "cpu, trace, heap, allocs, goroutine, block, mutex or threadcreate")
	pprofCmd.Flags().IntVar(&req.Seconds, "seconds", 0, "how long a cpu profile or trace runs (default 30)")
	pprofCmd.Flags().StringVarP(&out, "output", "o", "", "the file to write (default hive-<profile>-<time>.pprof)")

	stats := needsDaemon(&cobra.Command{
		Use:   "stats",
		Short: "Show the daemon's goroutines, open files and memory",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var st runtime.Stats
			if err := a.client.Call(ctx, "debug.stats", struct{}{}, &st); err != nil {
				return err
			}
			return a.emit(st, func() error {
				w := a.table()
				fmt.Fprintf(w, "goroutines\t%d\nopen files\t%d\nheap in use\t%s\nheap from the OS\t%s\ngarbage collections\t%d\nuptime\t%s\n",
					st.Goroutines, st.OpenFDs, formatBytes(int64(st.HeapAlloc)), formatBytes(int64(st.HeapSys)), st.NumGC,
					(time.Duration(st.Uptime) * time.Second).String())
				return w.Flush()
			})
		}),
	})
	cmd.AddCommand(pprofCmd, stats)
	return cmd
}
