package plugin

// Plugin issue trackers (plugin API 2): a tracker's command gets one
// operation (get, search, comment, close) as JSON on stdin and answers on
// stdout. Hive names its issues <tracker>:<key>.

import (
	"context"
	"maps"
	"slices"
	"time"
)

// TrackerTimeout bounds one tracker operation.
const TrackerTimeout = time.Minute

// Tracker operations.
const (
	TrackerOpGet     = "get"
	TrackerOpSearch  = "search"
	TrackerOpComment = "comment"
	TrackerOpClose   = "close"
)

// PluginTracker is an issue tracker a loaded plugin provides.
type PluginTracker struct {
	Plugin string
	Decl   TrackerDecl
}

// TrackerFor returns the plugin tracker of that name: of the plugins that
// can run and declare it, the first by ID.
func (h *Host) TrackerFor(name string) (PluginTracker, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range slices.Sorted(maps.Keys(h.plugins)) {
		l := h.plugins[id]
		if l.Manifest == nil || l.problem != "" {
			continue
		}
		for _, t := range l.Manifest.Contributes.Trackers {
			if t.Name == name {
				return PluginTracker{Plugin: id, Decl: t}, true
			}
		}
	}
	return PluginTracker{}, false
}

// TrackerNames lists the trackers the loaded plugins provide.
func (h *Host) TrackerNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, l := range h.plugins {
		if l.Manifest == nil || l.problem != "" {
			continue
		}
		for _, t := range l.Manifest.Contributes.Trackers {
			if !slices.Contains(out, t.Name) {
				out = append(out, t.Name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// TrackerRequest is what a tracker's command reads on stdin.
type TrackerRequest struct {
	Op      string `json:"op"`
	Tracker string `json:"tracker"`
	Key     string `json:"key,omitempty"`
	Query   string `json:"query,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Body    string `json:"body,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// CallTracker runs one operation of a plugin's tracker and returns its
// answer: an issue for get and close, {"issues": […]} for search, anything
// for comment.
func (h *Host) CallTracker(ctx context.Context, t PluginTracker, req TrackerRequest) ([]byte, error) {
	req.Tracker = t.Decl.Name
	return h.Invoke(ctx, t.Plugin, ContribTracker, t.Decl.Command, req, TrackerTimeout)
}
