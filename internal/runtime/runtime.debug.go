package runtime

import (
	"context"
	"errors"
	"fmt"
	goruntime "runtime"
	"runtime/pprof"
	"runtime/trace"
	"slices"
	"time"

	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/protocol"
)

// Profiles debug.pprof can take.
var Profiles = []string{"cpu", "trace", "heap", "allocs", "goroutine", "block", "mutex", "threadcreate"}

// PprofRequest asks for a profile of the daemon (debug.pprof).
type PprofRequest struct {
	Profile string `json:"profile"`
	// Seconds is how long a cpu profile or trace runs (default 30).
	Seconds int `json:"seconds,omitempty"`
}

// Stats is the daemon's own resource use (debug.stats), for watching for
// leaks.
type Stats struct {
	Goroutines int       `json:"goroutines"`
	OpenFDs    int       `json:"open_fds"`
	HeapAlloc  uint64    `json:"heap_alloc"`
	HeapSys    uint64    `json:"heap_sys"`
	NumGC      uint32    `json:"num_gc"`
	Uptime     float64   `json:"uptime_seconds"`
	StartedAt  time.Time `json:"started_at"`
}

func registerDebug(r *protocol.Router, s *Server) {
	r.MustRegister("debug.stats", protocol.Method(func(context.Context, struct{}) (Stats, error) {
		var m goruntime.MemStats
		goruntime.ReadMemStats(&m)
		fds, _ := platform.OpenFDs()
		started := s.Snapshot().StartedAt
		return Stats{
			Goroutines: goruntime.NumGoroutine(), OpenFDs: fds,
			HeapAlloc: m.HeapAlloc, HeapSys: m.HeapSys, NumGC: m.NumGC,
			Uptime: time.Since(started).Seconds(), StartedAt: started,
		}, nil
	}))
	// debug.pprof: a pipe carrying the profile, in pprof's format (a trace
	// in runtime/trace's), to feed `go tool pprof` or `go tool trace`.
	r.MustRegister("debug.pprof", protocol.PipeMethod(func(_ context.Context, p PprofRequest) (protocol.Empty, protocol.PipeFunc, error) {
		if !slices.Contains(Profiles, p.Profile) {
			return protocol.Empty{}, nil, protocol.NewError(protocol.ErrorCodeInvalidParams, fmt.Errorf("profile %q (want one of %v)", p.Profile, Profiles))
		}
		secs := p.Seconds
		if secs <= 0 {
			secs = 30
		}
		if secs > 600 {
			return protocol.Empty{}, nil, protocol.NewError(protocol.ErrorCodeInvalidParams, errors.New("seconds: at most 600"))
		}
		return protocol.Empty{}, func(ctx context.Context, pipe protocol.Pipe) error {
			wait := func() {
				select {
				case <-ctx.Done():
				case <-time.After(time.Duration(secs) * time.Second):
				}
			}
			switch p.Profile {
			case "cpu":
				if err := pprof.StartCPUProfile(pipe); err != nil {
					return err // another profile runs
				}
				wait()
				pprof.StopCPUProfile()
			case "trace":
				if err := trace.Start(pipe); err != nil {
					return err
				}
				wait()
				trace.Stop()
			default:
				if p.Profile == "heap" || p.Profile == "allocs" {
					goruntime.GC() // up-to-date numbers
				}
				return pprof.Lookup(p.Profile).WriteTo(pipe, 0)
			}
			return nil
		}, nil
	}))
}
