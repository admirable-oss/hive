package runtime

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/admirable-oss/hive/internal/persist"
	"github.com/admirable-oss/hive/internal/process"
)

// shimGrace is how long a shim's directory without a live process is left
// alone: the shim may still be starting, or its result being collected.
const shimGrace = 10 * time.Minute

// gc removes what is no longer needed: ended processes that no pane shows
// and that ended longer ago than the retention, shim directories of
// processes that are gone, history of panes that no longer exist (all of
// it when replay is off), and unreadable files moved aside long ago.
func (k *keeper) gc(ctx context.Context, req persist.GCRequest) (persist.GCReport, error) {
	report := persist.GCReport{DryRun: req.DryRun, Processes: []string{}, Files: []string{}}
	olderThan := req.OlderThan
	if olderThan <= 0 {
		olderThan = k.retention.ExitedProcesses
	}
	now := k.now()
	cutoff := now.Add(-olderThan)

	panes, err := k.panes.Panes(ctx, "", "")
	if err != nil {
		return report, err
	}
	shown, paneIDs := map[string]bool{}, map[string]bool{}
	for _, p := range panes {
		shown[p.ProcessID], paneIDs[p.ID] = true, true
	}
	procs, err := k.procs.List(ctx, "")
	if err != nil {
		return report, err
	}
	var errs []error
	records := map[string]process.Process{}
	for _, p := range procs {
		records[p.ID] = p
		ended := p.StartedAt
		if p.EndedAt != nil {
			ended = *p.EndedAt
		}
		if p.Active() || shown[p.ID] || ended.After(cutoff) {
			continue
		}
		size := diskUsage(filepath.Join(k.root, "environments", p.EnvironmentID, "processes", p.ID))
		if !req.DryRun {
			if err := k.procs.Remove(ctx, p.ID); err != nil {
				if !errors.Is(err, process.ErrActive) && !errors.Is(err, process.ErrNotFound) {
					errs = append(errs, err)
				}
				continue
			}
		}
		report.Processes = append(report.Processes, p.ID)
		report.Bytes += size
	}

	// Shim directories whose process is gone or ended.
	runDir := filepath.Join(k.root, "run")
	entries, err := os.ReadDir(runDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !e.IsDir() || now.Sub(info.ModTime()) < shimGrace {
			continue
		}
		if p, ok := records[e.Name()]; ok && p.Active() {
			continue
		}
		errs = append(errs, k.remove(&report, filepath.Join(runDir, e.Name())))
	}

	// Pane history nobody will replay.
	keep := func(id string) bool { return k.cfg != nil && k.cfg.ReplayHistory && paneIDs[id] }
	for _, f := range k.historyFiles(keep) {
		errs = append(errs, k.remove(&report, f))
	}

	// Unreadable files moved aside, once they are as old as ended processes.
	for _, pattern := range []string{
		filepath.Join(k.root, "*"+persist.CorruptSuffix+"*"),
		filepath.Join(k.root, "snapshots", "*"+persist.CorruptSuffix+"*"),
		filepath.Join(k.root, "environments", "*", "*"+persist.CorruptSuffix+"*"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, f := range matches {
			if info, err := os.Stat(f); err == nil && info.ModTime().Before(cutoff) {
				errs = append(errs, k.remove(&report, f))
			}
		}
	}

	if n := len(report.Processes) + len(report.Files); n > 0 && !req.DryRun {
		k.log.Info("cleaned up", "processes", len(report.Processes), "files", len(report.Files), "bytes", report.Bytes)
	}
	return report, errors.Join(errs...)
}

// historyFiles lists kept pane history that keep rejects, without removing it.
func (k *keeper) historyFiles(keep func(paneID string) bool) []string {
	files, _ := k.history.Prune(keep, true)
	return files
}

// remove deletes path (unless dry-running) and records it.
func (k *keeper) remove(report *persist.GCReport, path string) error {
	size := diskUsage(path)
	if !report.DryRun {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	report.Files = append(report.Files, path)
	report.Bytes += size
	return nil
}

// runGC cleans up every retention.gc_interval until ctx ends.
func (k *keeper) runGC(ctx context.Context) {
	if k.retention.GCInterval <= 0 {
		return
	}
	t := time.NewTicker(k.retention.GCInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := k.gc(ctx, persist.GCRequest{}); err != nil && ctx.Err() == nil {
				k.log.Warn("clean-up failed", "err", err)
			}
		}
	}
}

// diskUsage is the size of the files under path.
func diskUsage(path string) int64 {
	var n int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
