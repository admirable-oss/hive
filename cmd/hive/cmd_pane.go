package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/protocol"
)

// paneTimeout bounds pane commands that do not wait for output.
const paneTimeout = 15 * time.Second

func newPaneCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pane",
		Short: "Split, arrange, type into and read panes",
		Long: `Panes are the places in a tab where processes run. Commands that take an
optional [pane] act on $HIVE_PANE_ID when none is given, so a program inside
a pane can drive its own pane (hive pane split, hive pane read).`,
	}
	cmd.AddCommand(
		newPaneListCmd(a),
		paneCmd(a, &cobra.Command{Use: "get [pane]", Short: "Show a pane"}, func(ctx context.Context, w client.Workspace, id string, _ []string) error {
			p, err := w.PaneGet(ctx, id)
			if err != nil {
				return err
			}
			return a.emit(p, func() error { return printPane(a, p) })
		}),
		newPaneSplitCmd(a),
		newPanePopupCmd(a),
		newPaneFocusCmd(a),
		newPaneResizeCmd(a),
		newPaneZoomCmd(a),
		newPaneSwapCmd(a),
		newPaneMoveCmd(a),
		paneCmd(a, &cobra.Command{Use: "close [pane]", Short: "Close a pane and stop its process"}, func(ctx context.Context, w client.Workspace, id string, _ []string) error {
			if err := w.PaneClose(ctx, id); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "closed pane %s\n", id)
			return nil
		}),
		newPaneRenameCmd(a),
		paneCmd(a, &cobra.Command{Use: "input [pane]", Short: "Send standard input to a pane, byte for byte"}, func(ctx context.Context, w client.Workspace, id string, _ []string) error {
			data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
			if err != nil {
				return err
			}
			return w.PaneInput(ctx, id, data)
		}),
		newPaneSendTextCmd(a),
		newPaneSendKeysCmd(a),
		newPaneRunCmd(a),
		newPaneReadCmd(a),
		newPaneWaitCmd(a),
	)
	return cmd
}

// paneCmd completes cmd as a command on an optional [pane] argument
// (default $HIVE_PANE_ID) followed by nothing else.
func paneCmd(a *app, cmd *cobra.Command, run func(ctx context.Context, w client.Workspace, id string, rest []string) error) *cobra.Command {
	cmd.Args = rangeArgs(0, 1)
	cmd.ValidArgsFunction = completePanes(a)
	cmd.RunE = withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
		id, err := a.paneArg(args)
		if err != nil {
			return err
		}
		return run(ctx, client.NewWorkspace(a.client), id, nil)
	})
	return needsDaemon(cmd)
}

// paneArg is args[0], else $HIVE_PANE_ID.
func (a *app) paneArg(args []string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return args[0], nil
	}
	if id := a.getenv("HIVE_PANE_ID"); id != "" {
		return id, nil
	}
	return "", &usageError{errors.New("no pane given (and $HIVE_PANE_ID is not set)")}
}

func newPaneListCmd(a *app) *cobra.Command {
	var envID, tabID string
	cmd := needsDaemon(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List panes",
		Args:    noArgs,
		RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			panes, err := client.NewWorkspace(a.client).PaneList(ctx, envID, tabID)
			if err != nil {
				return err
			}
			return a.emit(panes, func() error {
				w := a.table()
				fmt.Fprintln(w, "ID\tENV\tTAB\tNAME\tSIZE\tSTATUS\tCOMMAND")
				for _, p := range panes {
					fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, paneMarks(p), p.EnvironmentID, p.TabID, cmpStr(p.Name, "-"), paneSize(p), paneStatus(p), paneCommand(p))
				}
				return w.Flush()
			})
		}),
	})
	cmd.Flags().StringVar(&envID, "env", "", "only this environment's panes")
	cmd.Flags().StringVar(&tabID, "tab", "", "only this tab's panes")
	return cmd
}

func newPaneSplitCmd(a *app) *cobra.Command {
	var (
		dir     string
		ratio   float64
		spec    paneSpecFlags
		noFocus bool
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "split [pane] [-- command [args...]]",
		Short: "Split a pane and start a process in the new one (prints its ID)",
		Example: `  hive pane split                           # split my pane, run a shell to the right
  hive pane split p1a2b3 -d down -r 0.3 -- npm test --watch`,
		Args:              argsBeforeDash(0, 1),
		ValidArgsFunction: completePanes(a),
		RunE: withTimeout(paneTimeout, func(ctx context.Context, cmd *cobra.Command, args []string) error {
			before, command := splitAtDash(cmd, args)
			id, err := a.paneArg(before)
			if err != nil {
				return err
			}
			d, err := parseDirection(dir)
			if err != nil {
				return err
			}
			s, err := spec.spec(command)
			if err != nil {
				return err
			}
			req := pane.SplitRequest{Pane: id, Direction: d, Ratio: ratio, Spec: s}
			if noFocus {
				f := false
				req.Focus = &f
			}
			p, err := client.NewWorkspace(a.client).PaneSplit(ctx, req)
			if err != nil {
				return err
			}
			return a.emit(p, func() error { fmt.Fprintln(a.out, p.ID); return nil })
		}),
	})
	cmd.Flags().StringVarP(&dir, "direction", "d", "right", "where the new pane goes: right, left, down or up")
	cmd.Flags().Float64VarP(&ratio, "ratio", "r", 0.5, "the new pane's share of the space (0-1)")
	cmd.Flags().BoolVar(&noFocus, "no-focus", false, "keep the focus on the split pane")
	spec.register(cmd, "name")
	return cmd
}

func newPanePopupCmd(a *app) *cobra.Command {
	var (
		tabID         string
		width, height int
		spec          paneSpecFlags
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "popup [-- command [args...]]",
		Short: "Open a floating pane over a tab; it closes when its command exits",
		Example: `  hive pane popup -- lazygit
  hive pane popup --tab t1a2b3 --width 60 --height 40 -- htop`,
		Args: argsBeforeDash(0, 0),
		RunE: withTimeout(paneTimeout, func(ctx context.Context, cmd *cobra.Command, args []string) error {
			_, command := splitAtDash(cmd, args)
			w := client.NewWorkspace(a.client)
			if tabID == "" {
				id, err := a.paneArg(nil)
				if err != nil {
					return &usageError{errors.New("give --tab (or run inside a pane)")}
				}
				p, err := w.PaneGet(ctx, id)
				if err != nil {
					return err
				}
				tabID = p.TabID
			}
			s, err := spec.spec(command)
			if err != nil {
				return err
			}
			p, err := w.PanePopup(ctx, pane.PopupRequest{TabID: tabID, Spec: s, WidthPct: width, HeightPct: height})
			if err != nil {
				return err
			}
			return a.emit(p, func() error { fmt.Fprintln(a.out, p.ID); return nil })
		}),
	})
	cmd.Flags().StringVar(&tabID, "tab", "", "the tab to open it over (default: the tab of $HIVE_PANE_ID)")
	cmd.Flags().IntVar(&width, "width", 80, "width in percent of the tab")
	cmd.Flags().IntVar(&height, "height", 80, "height in percent of the tab")
	spec.register(cmd, "name")
	return cmd
}

func newPaneFocusCmd(a *app) *cobra.Command {
	var dir string
	cmd := paneCmd(a, &cobra.Command{
		Use:   "focus [pane]",
		Short: "Focus a pane, or with -d its neighbour in that direction",
	}, func(ctx context.Context, w client.Workspace, id string, _ []string) error {
		var d layout.Direction
		if dir != "" {
			var err error
			if d, err = parseDirection(dir); err != nil {
				return err
			}
		}
		p, err := w.PaneFocus(ctx, id, d)
		if err != nil {
			return err
		}
		return a.emit(p, func() error { fmt.Fprintln(a.out, p.ID); return nil })
	})
	cmd.Flags().StringVarP(&dir, "direction", "d", "", "focus the neighbour to the right, left, down or up")
	return cmd
}

func newPaneResizeCmd(a *app) *cobra.Command {
	var (
		dir   string
		cells int
	)
	cmd := paneCmd(a, &cobra.Command{
		Use:     "resize [pane]",
		Short:   "Grow a pane by moving its border (a negative count shrinks it)",
		Example: `  hive pane resize -d right -n 10`,
	}, func(ctx context.Context, w client.Workspace, id string, _ []string) error {
		d, err := parseDirection(dir)
		if err != nil {
			return err
		}
		p, err := w.PaneResize(ctx, id, d, cells)
		if err != nil {
			return err
		}
		return a.emit(p, func() error { fmt.Fprintf(a.out, "pane %s is now %s\n", p.ID, paneSize(p)); return nil })
	})
	cmd.Flags().StringVarP(&dir, "direction", "d", "right", "the border to move: right, left, down or up")
	cmd.Flags().IntVarP(&cells, "cells", "n", 5, "how many cells to move it")
	return cmd
}

func newPaneZoomCmd(a *app) *cobra.Command {
	var on, off bool
	cmd := paneCmd(a, &cobra.Command{
		Use:   "zoom [pane]",
		Short: "Toggle a pane filling its whole tab",
	}, func(ctx context.Context, w client.Workspace, id string, _ []string) error {
		var set *bool
		switch {
		case on && off:
			return &usageError{errors.New("--on and --off exclude each other")}
		case on, off:
			set = &on
		}
		p, err := w.PaneZoom(ctx, id, set)
		if err != nil {
			return err
		}
		return a.emit(p, func() error {
			state := "unzoomed"
			if p.Zoomed {
				state = "zoomed"
			}
			fmt.Fprintf(a.out, "pane %s %s\n", p.ID, state)
			return nil
		})
	})
	cmd.Flags().BoolVar(&on, "on", false, "zoom (instead of toggling)")
	cmd.Flags().BoolVar(&off, "off", false, "unzoom (instead of toggling)")
	return cmd
}

func newPaneSwapCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:               "swap <pane> <other>",
		Short:             "Swap two panes' places",
		Args:              exactArgs(2),
		ValidArgsFunction: completePanes(a),
		RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			if err := client.NewWorkspace(a.client).PaneSwap(ctx, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "swapped %s and %s\n", args[0], args[1])
			return nil
		}),
	})
}

func newPaneMoveCmd(a *app) *cobra.Command {
	var (
		req pane.MoveRequest
		dir string
	)
	cmd := paneCmd(a, &cobra.Command{
		Use:   "move [pane]",
		Short: "Move a pane to another tab or environment; its process keeps running",
		Example: `  hive pane move p1a2b3 --tab t4c5d6 -d down
  hive pane move p1a2b3 --env review        # into a new tab there`,
	}, func(ctx context.Context, w client.Workspace, id string, _ []string) error {
		if req.TabID == "" && req.EnvironmentID == "" {
			return &usageError{errors.New("give --tab or --env")}
		}
		r := req
		r.Pane = id
		if dir != "" {
			d, err := parseDirection(dir)
			if err != nil {
				return err
			}
			r.Direction = d
		}
		p, err := w.PaneMove(ctx, r)
		if err != nil {
			return err
		}
		return a.emit(p, func() error {
			fmt.Fprintf(a.out, "moved pane %s to tab %s (environment %s)\n", p.ID, p.TabID, p.EnvironmentID)
			return nil
		})
	})
	cmd.Flags().StringVar(&req.TabID, "tab", "", "the destination tab")
	cmd.Flags().StringVar(&req.EnvironmentID, "env", "", "the destination environment (a new tab there, unless --tab)")
	cmd.Flags().StringVar(&req.Target, "next-to", "", "the pane of the destination to split (default: its focused pane)")
	cmd.Flags().StringVarP(&dir, "direction", "d", "", "the side of that pane to go to (default right)")
	return cmd
}

func newPaneRenameCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:               "rename [pane] <name>",
		Short:             "Name a pane",
		Args:              rangeArgs(1, 2),
		ValidArgsFunction: completePanes(a),
		RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			id, err := a.paneArg(args[:len(args)-1])
			if err != nil {
				return err
			}
			p, err := client.NewWorkspace(a.client).PaneRename(ctx, id, args[len(args)-1])
			if err != nil {
				return err
			}
			return a.emit(p, func() error { fmt.Fprintf(a.out, "renamed pane %s to %q\n", p.ID, p.Name); return nil })
		}),
	})
}

func newPaneSendTextCmd(a *app) *cobra.Command {
	var paste bool
	cmd := needsDaemon(&cobra.Command{
		Use:               "send-text <pane> <text>",
		Short:             "Type text into a pane (no Enter; see send-keys and run)",
		Args:              exactArgs(2),
		ValidArgsFunction: completePanes(a),
		RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			return client.NewWorkspace(a.client).PaneSendText(ctx, args[0], args[1], paste)
		}),
	})
	cmd.Flags().BoolVar(&paste, "paste", false, "send it as one bracketed paste (multi-line prompts)")
	return cmd
}

func newPaneSendKeysCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "send-keys <pane> <key>...",
		Short: "Press keys in a pane",
		Long: `Press keys in a pane: Enter, Tab, Space, Escape, Backspace, Delete, Insert,
Home, End, PageUp, PageDown, Up, Down, Left, Right, BTab and F1-F12 (any
case), single characters, and Ctrl/Alt combinations (C-c, M-x, C-M-a).
Use send-text to type text.`,
		Example: `  hive pane send-keys "$P" C-c
  hive pane send-keys "$P" Escape : w q Enter`,
		Args:              minArgs(2),
		ValidArgsFunction: completePanes(a),
		RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			return client.NewWorkspace(a.client).PaneSendKeys(ctx, args[0], args[1:])
		}),
	})
}

func newPaneRunCmd(a *app) *cobra.Command {
	cmd := needsDaemon(&cobra.Command{
		Use:               "run <pane> <command line>...",
		Short:             "Type a command line into a pane's shell and press Enter",
		Example:           `  hive pane run p1a2b3 make test`,
		Args:              minArgs(2),
		ValidArgsFunction: completePanes(a),
		RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			return client.NewWorkspace(a.client).PaneRun(ctx, args[0], strings.Join(args[1:], " "))
		}),
	})
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newPaneReadCmd(a *app) *cobra.Command {
	var req client.ReadRequest
	cmd := paneCmd(a, &cobra.Command{
		Use:   "read [pane]",
		Short: "Print what a pane shows",
		Long: `Print what a pane shows. --source picks what:
  visible           the screen (default)
  recent            the screen and the lines that scrolled off above it
  recent-unwrapped  the same, with lines the terminal wrapped joined again
  history           only the lines that scrolled off`,
	}, func(ctx context.Context, w client.Workspace, id string, _ []string) error {
		lines, err := w.PaneRead(ctx, id, req)
		if err != nil {
			return err
		}
		return a.emit(lines, func() error {
			for _, l := range lines {
				fmt.Fprintln(a.out, l)
			}
			return nil
		})
	})
	cmd.Flags().StringVarP(&req.Source, "source", "s", "visible", "visible, recent, recent-unwrapped or history")
	cmd.Flags().IntVarP(&req.Lines, "lines", "n", 0, "only the last n lines")
	cmd.Flags().BoolVar(&req.ANSI, "ansi", false, "keep colours as escape sequences")
	return cmd
}

func newPaneWaitCmd(a *app) *cobra.Command {
	var (
		pattern, text string
		anywhere      bool
		timeout       time.Duration
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "wait-output [pane]",
		Short: "Wait until a pane prints a matching line, and print it",
		Long: `Wait until a pane prints a line matching --regex (or containing --text) and
print that line. Only output that appears after the command starts counts,
unless --anywhere also lets what is already on screen match. Exits with
status 4 when --timeout passes first.`,
		Example: `  hive pane run "$P" npm test
  hive pane wait-output "$P" --regex '(passed|failed)' --timeout 5m`,
		Args:              rangeArgs(0, 1),
		ValidArgsFunction: completePanes(a),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.paneArg(args)
			if err != nil {
				return err
			}
			switch {
			case pattern != "" && text != "":
				return &usageError{errors.New("--regex and --text exclude each other")}
			case text != "":
				pattern = regexp.QuoteMeta(text)
			case pattern == "":
				return &usageError{errors.New("give --regex or --text")}
			}
			ctx := cmd.Context()
			if timeout > 0 { // the daemon enforces it; this is a backstop
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout+10*time.Second)
				defer cancel()
			}
			line, err := client.NewWorkspace(a.client).PaneWaitOutput(ctx, id, client.WaitRequest{Pattern: pattern, Anywhere: anywhere}, timeout)
			if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodeTimeout {
				return &codedError{code: exitTimeout, msg: fmt.Sprintf("no matching output within %s", timeout)}
			}
			if err != nil {
				return err
			}
			return a.emit(map[string]string{"line": line}, func() error { fmt.Fprintln(a.out, line); return nil })
		},
	})
	cmd.Flags().StringVar(&pattern, "regex", "", "a regular expression (Go syntax) a line must match")
	cmd.Flags().StringVar(&text, "text", "", "text a line must contain")
	cmd.Flags().BoolVar(&anywhere, "anywhere", false, "let output already on screen match too")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long (exit status 4); 0 waits forever")
	return cmd
}

// paneSpecFlags are the flags describing what a new pane runs.
type paneSpecFlags struct {
	name, cwd string
	env       []string
}

// register adds the flags; nameFlag is the pane name's flag.
func (f *paneSpecFlags) register(cmd *cobra.Command, nameFlag string) {
	cmd.Flags().StringVar(&f.name, nameFlag, "", "a name for the pane")
	cmd.Flags().StringVar(&f.cwd, "cwd", "", "its directory, relative to the environment's (default: the environment's)")
	cmd.Flags().StringArrayVarP(&f.env, "env", "e", nil, "a variable for its process, as KEY=VALUE (repeatable)")
}

func (f *paneSpecFlags) spec(command []string) (pane.Spec, error) {
	env, err := parseVars(f.env)
	if err != nil {
		return pane.Spec{}, err
	}
	return pane.Spec{Name: f.name, Command: command, Cwd: f.cwd, Env: env}, nil
}

// argsBeforeDash accepts lo..hi arguments before "--" and any after.
func argsBeforeDash(lo, hi int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		before, _ := splitAtDash(cmd, args)
		if len(before) < lo || len(before) > hi {
			return &usageError{fmt.Errorf("expected %d to %d arguments before --, got %d (put the command after --)", lo, hi, len(before))}
		}
		return nil
	}
}

func splitAtDash(cmd *cobra.Command, args []string) (before, after []string) {
	if i := cmd.ArgsLenAtDash(); i >= 0 {
		return args[:i], args[i:]
	}
	return args, nil
}

func parseDirection(s string) (layout.Direction, error) {
	switch d := layout.Direction(strings.ToLower(s)); d {
	case layout.Left, layout.Right, layout.Up, layout.Down:
		return d, nil
	}
	return "", &usageError{fmt.Errorf("direction %q: want right, left, down or up", s)}
}

func printPane(a *app, p pane.Pane) error {
	w := a.table()
	fmt.Fprintf(w, "ID\t%s%s\n", p.ID, paneMarks(p))
	if p.Name != "" {
		fmt.Fprintf(w, "Name\t%s\n", p.Name)
	}
	fmt.Fprintf(w, "Environment\t%s\n", p.EnvironmentID)
	fmt.Fprintf(w, "Tab\t%s\n", p.TabID)
	fmt.Fprintf(w, "Size\t%s\n", paneSize(p))
	fmt.Fprintf(w, "Process\t%s (%s)\n", p.ProcessID, paneStatus(p))
	fmt.Fprintf(w, "Command\t%s\n", paneCommand(p))
	return w.Flush()
}

// paneMarks flags the focused (*), zoomed (Z) and popup (^) panes.
func paneMarks(p pane.Pane) string {
	m := ""
	if p.Focused {
		m += "*"
	}
	if p.Zoomed {
		m += "Z"
	}
	if p.Popup {
		m += "^"
	}
	if m != "" {
		m = " " + m
	}
	return m
}

func paneSize(p pane.Pane) string {
	if p.Rect == nil {
		return "hidden"
	}
	return fmt.Sprintf("%dx%d", p.Rect.W, p.Rect.H)
}

func paneStatus(p pane.Pane) string {
	if p.Process == nil {
		return "-"
	}
	return string(p.Process.Status)
}

func paneCommand(p pane.Pane) string {
	if p.Process == nil {
		return "-"
	}
	return summary(*p.Process)
}

func completePanes(a *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), completeTimeout)
		defer cancel()
		panes, err := client.NewWorkspace(a.client).PaneList(ctx, "", "")
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := make([]cobra.Completion, 0, len(panes))
		for _, p := range panes {
			out = append(out, cobra.CompletionWithDesc(p.ID, p.EnvironmentID+" · "+cmpStr(p.Name, paneCommand(p))))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
