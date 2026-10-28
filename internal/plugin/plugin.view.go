package plugin

// Pane views (plugin API 2): a plugin draws a pane from data, never from
// code a client runs. A view document is a tree of a few kinds of node,
// which the TUI draws in cells and the desktop app and the web observer
// as their own widgets. Every string is text; a link is https and opened
// only by a person's click; a button runs only one of the plugin's
// declared actions.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// View node kinds.
const (
	NodeText     = "text"
	NodeRow      = "row"
	NodeColumn   = "column"
	NodeList     = "list"
	NodeTable    = "table"
	NodeProgress = "progress"
	NodeBadge    = "badge"
	NodeDivider  = "divider"
	NodeButton   = "button"
	NodeLink     = "link"
)

// NodeKinds are every view node kind.
var NodeKinds = []string{NodeText, NodeRow, NodeColumn, NodeList, NodeTable, NodeProgress, NodeBadge, NodeDivider, NodeButton, NodeLink}

// TextStyles are a text node's styles; badges take Tones.
var TextStyles = []string{"", "plain", "muted", "bold", "code", "ok", "warn", "error"}

// Bounds on a view document.
const (
	MaxViewNodes      = 2000
	MaxViewDepth      = 32
	MaxViewText       = 4096
	MaxViewTableCells = 64
	MaxViewBytes      = 256 << 10
)

// EventView announces that a plugin's view changed (set or cleared).
const EventView = "plugin.view"

// ViewNode is one node of a view document.
type ViewNode struct {
	Kind string `json:"kind"`
	// ID names a button (sent to its action) or any node a plugin wants to
	// address.
	ID    string `json:"id,omitempty"`
	Text  string `json:"text,omitempty"`
	Style string `json:"style,omitempty"`
	// Children of a row, a column or a list.
	Children []ViewNode `json:"children,omitempty"`
	// Columns and Rows of a table.
	Columns []string   `json:"columns,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
	// Value of a progress node, from 0 to 1.
	Value float64 `json:"value,omitempty"`
	// Action a button runs; URL a link opens.
	Action string `json:"action,omitempty"`
	URL    string `json:"url,omitempty"`
}

// ViewDoc is what a view shows.
type ViewDoc struct {
	Root ViewNode `json:"root"`
}

// ViewContent is a plugin's view as clients draw it.
type ViewContent struct {
	Plugin  string    `json:"plugin"`
	View    string    `json:"view"`
	Title   string    `json:"title"`
	Doc     *ViewDoc  `json:"doc,omitempty"` // nil: not drawn yet
	Updated time.Time `json:"updated,omitzero"`
	// Available is false while the plugin cannot run.
	Available bool `json:"available"`
}

// ViewUpdate sets (plugin.view.set) or, with no doc, clears
// (plugin.view.clear) a view.
type ViewUpdate struct {
	Plugin string   `json:"plugin,omitempty"`
	View   string   `json:"view"`
	Doc    *ViewDoc `json:"doc,omitempty"`
}

// ViewClick is a person's click on a view's button (plugin.view.click).
type ViewClick struct {
	Plugin string `json:"plugin"`
	View   string `json:"view"`
	Node   string `json:"node"`
	// Where the click happened, for the action's context.
	Environment string `json:"environment,omitempty"`
	Tab         string `json:"tab,omitempty"`
	Pane        string `json:"pane,omitempty"`
}

// CheckViewDoc checks a view document against a plugin's manifest: its
// shape and bounds, and that every button runs one of its actions.
func CheckViewDoc(m *Manifest, doc ViewDoc) error {
	if raw, _ := json.Marshal(doc); len(raw) > MaxViewBytes {
		return fmt.Errorf("%w: a view document is at most %d KiB", ErrContribution, MaxViewBytes>>10)
	}
	nodes := 0
	buttons := map[string]bool{}
	var walk func(n ViewNode, depth int) error
	walk = func(n ViewNode, depth int) error {
		nodes++
		if nodes > MaxViewNodes {
			return fmt.Errorf("%w: a view has at most %d nodes", ErrContribution, MaxViewNodes)
		}
		if depth > MaxViewDepth {
			return fmt.Errorf("%w: a view nests at most %d deep", ErrContribution, MaxViewDepth)
		}
		bad := func(format string, args ...any) error {
			return fmt.Errorf("%w: %s node: %s", ErrContribution, n.Kind, fmt.Sprintf(format, args...))
		}
		if !slices.Contains(NodeKinds, n.Kind) {
			return fmt.Errorf("%w: node kind %q (want %s)", ErrContribution, n.Kind, strings.Join(NodeKinds, ", "))
		}
		for _, s := range append([]string{n.Text, n.ID}, n.Columns...) {
			if utf8.RuneCountInString(s) > MaxViewText || !utf8.ValidString(s) {
				return bad("text of at most %d characters", MaxViewText)
			}
		}
		if n.ID != "" && !contribID.MatchString(n.ID) {
			return bad("id %q: lower-case letters, digits and dashes", n.ID)
		}
		container := n.Kind == NodeRow || n.Kind == NodeColumn || n.Kind == NodeList
		if len(n.Children) > 0 && !container {
			return bad("only rows, columns and lists have children")
		}
		if (len(n.Columns) > 0 || len(n.Rows) > 0) && n.Kind != NodeTable {
			return bad("only tables have columns and rows")
		}
		if n.Action != "" && n.Kind != NodeButton {
			return bad("only buttons have an action")
		}
		if n.URL != "" && n.Kind != NodeLink {
			return bad("only links have a url")
		}
		switch n.Kind {
		case NodeText:
			if !slices.Contains(TextStyles, n.Style) {
				return bad("style %q (want %s)", n.Style, strings.Join(TextStyles[1:], ", "))
			}
		case NodeBadge:
			if !slices.Contains(Tones, n.Style) {
				return bad("tone %q", n.Style)
			}
		case NodeProgress:
			if n.Value < 0 || n.Value > 1 {
				return bad("value from 0 to 1")
			}
		case NodeTable:
			if len(n.Columns) > MaxViewTableCells {
				return bad("at most %d columns", MaxViewTableCells)
			}
			for _, r := range n.Rows {
				if len(r) > max(len(n.Columns), 1) && len(n.Columns) > 0 || len(r) > MaxViewTableCells {
					return bad("a row has more cells than columns")
				}
				for _, c := range r {
					if utf8.RuneCountInString(c) > MaxViewText || !utf8.ValidString(c) {
						return bad("cells of at most %d characters", MaxViewText)
					}
				}
			}
		case NodeButton:
			if n.ID == "" {
				return bad("a button needs an id")
			}
			if buttons[n.ID] {
				return bad("button id %q twice", n.ID)
			}
			buttons[n.ID] = true
			if _, ok := m.Action(n.Action); !ok {
				return bad("action %q is not one of the plugin's [[actions]]", n.Action)
			}
		case NodeLink:
			if !strings.HasPrefix(n.URL, "https://") || !SafeURL(n.URL) {
				return bad("url must be https")
			}
		}
		for _, c := range n.Children {
			if err := walk(c, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(doc.Root, 0)
}

// FindNode returns the node with id in doc.
func (doc ViewDoc) FindNode(id string) (ViewNode, bool) {
	var found ViewNode
	ok := false
	var walk func(n ViewNode)
	walk = func(n ViewNode) {
		if ok {
			return
		}
		if n.ID == id {
			found, ok = n, true
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(doc.Root)
	return found, ok
}

// viewStore keeps the documents plugins set.
type viewStore struct {
	mu   sync.Mutex
	docs map[[2]string]ViewContent
}

// declaredView is a plugin's view declaration.
func declaredView(m *Manifest, view string) (ViewDecl, bool) {
	if m == nil {
		return ViewDecl{}, false
	}
	i := slices.IndexFunc(m.Contributes.Views, func(v ViewDecl) bool { return v.ID == view })
	if i < 0 {
		return ViewDecl{}, false
	}
	return m.Contributes.Views[i], true
}

// SetView sets (or, without a doc, clears) one of a plugin's views.
func (h *Host) SetView(u ViewUpdate) (ViewContent, error) {
	h.mu.Lock()
	l := h.plugins[u.Plugin]
	var m *Manifest
	if l != nil {
		m = l.Manifest
	}
	h.mu.Unlock()
	if l == nil || m == nil {
		return ViewContent{}, fmt.Errorf("%w %q", ErrNotFound, u.Plugin)
	}
	decl, ok := declaredView(m, u.View)
	if !ok {
		return ViewContent{}, fmt.Errorf("%w: plugin %s declares no view %q ([[contributes.views]])", ErrContribution, u.Plugin, u.View)
	}
	c := ViewContent{Plugin: u.Plugin, View: u.View, Title: decl.Title, Available: true}
	if u.Doc != nil {
		if err := CheckViewDoc(m, *u.Doc); err != nil {
			return ViewContent{}, err
		}
		doc := *u.Doc
		c.Doc, c.Updated = &doc, time.Now().UTC()
	}
	h.views.mu.Lock()
	if h.views.docs == nil {
		h.views.docs = map[[2]string]ViewContent{}
	}
	if c.Doc == nil {
		delete(h.views.docs, [2]string{u.Plugin, u.View})
	} else {
		h.views.docs[[2]string{u.Plugin, u.View}] = c
	}
	h.views.mu.Unlock()
	h.cfg.Publish(EventView, map[string]string{"plugin": u.Plugin, "view": u.View})
	return c, nil
}

// Views lists every declared view, with the document its plugin set.
func (h *Host) Views() []ViewContent {
	h.mu.Lock()
	var out []ViewContent
	for _, id := range slices.Sorted(maps.Keys(h.plugins)) {
		l := h.plugins[id]
		if l.Manifest == nil {
			continue
		}
		for _, v := range l.Manifest.Contributes.Views {
			if h.isHidden(id + ":" + ContribView + ":" + v.ID) {
				continue
			}
			out = append(out, ViewContent{Plugin: id, View: v.ID, Title: v.Title, Available: l.problem == ""})
		}
	}
	h.mu.Unlock()
	h.views.mu.Lock()
	defer h.views.mu.Unlock()
	for i, v := range out {
		if c, ok := h.views.docs[[2]string{v.Plugin, v.View}]; ok {
			out[i].Doc, out[i].Updated = c.Doc, c.Updated
		}
	}
	if out == nil {
		out = []ViewContent{}
	}
	return out
}

// View returns one view.
func (h *Host) View(plugin, view string) (ViewContent, error) {
	for _, v := range h.Views() {
		if v.Plugin == plugin && v.View == view {
			return v, nil
		}
	}
	return ViewContent{}, fmt.Errorf("%w: no view %s/%s", ErrNotFound, plugin, view)
}

// forgetViews drops a plugin's documents (it was unloaded).
func (h *Host) forgetViews(id string) bool {
	h.views.mu.Lock()
	defer h.views.mu.Unlock()
	changed := false
	for k := range h.views.docs {
		if k[0] == id {
			delete(h.views.docs, k)
			changed = true
		}
	}
	return changed
}

// OpenView opens one of a plugin's views in a pane: it fails when the
// plugin does not declare it, and runs the view's command to draw it,
// when it declares one, in the background.
func (h *Host) OpenView(plugin, view string, c Context) error {
	h.mu.Lock()
	l := h.plugins[plugin]
	ctx := h.ctx
	h.mu.Unlock()
	var m *Manifest
	if l != nil {
		m = l.Manifest
	}
	decl, ok := declaredView(m, view)
	if !ok {
		return fmt.Errorf("%w: no plugin view %s/%s (hive plugin views)", ErrNotFound, plugin, view)
	}
	if len(decl.Command) == 0 || l.problem != "" || ctx == nil {
		return nil // it shows what the plugin set, or nothing yet
	}
	c.Trigger, c.View = ContribView, view
	l.wg.Go(func() {
		cctx, cancel := context.WithTimeout(l.ctx, DefaultTimeout)
		defer cancel()
		if _, err := h.run(cctx, l, decl.Command, c, nil); err != nil && cctx.Err() == nil {
			h.mu.Lock()
			h.failLocked(l, fmt.Sprintf("draw view %s: %v", view, err))
			h.mu.Unlock()
		}
	})
	return nil
}

// Click runs the action of a view's button: the plugin's own declared
// action, with the view and the node in its context.
func (h *Host) Click(ctx context.Context, k ViewClick) (Result, error) {
	v, err := h.View(k.Plugin, k.View)
	if err != nil {
		return Result{}, err
	}
	if v.Doc == nil {
		return Result{}, fmt.Errorf("%w: view %s/%s shows nothing yet", ErrNotFound, k.Plugin, k.View)
	}
	n, ok := v.Doc.FindNode(k.Node)
	if !ok || n.Kind != NodeButton {
		return Result{}, fmt.Errorf("%w: view %s/%s has no button %q", ErrNotFound, k.Plugin, k.View, k.Node)
	}
	return h.Action(ctx, k.Plugin, n.Action, Context{Environment: k.Environment, Tab: k.Tab, Pane: k.Pane, View: k.View, Node: k.Node})
}
