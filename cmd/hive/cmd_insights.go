package main

import (
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/protocol"
)

// insightsTimeout bounds insights commands; reports from agents' hooks
// and status lines have tighter bounds of their own.
const insightsTimeout = 30 * time.Second

// insightsAsksShown is how many asks the summary lists under its table.
const insightsAsksShown = 5

func newInsightsCmd(a *app) *cobra.Command {
	var r insightsRange
	var by string
	cmd := needsDaemon(&cobra.Command{
		Use:     "insights",
		Aliases: []string{"insight"},
		Short:   "Show where agents' time went and what they used, on this machine",
		Long: `Hive follows every agent's state, so it knows how long each one worked,
sat blocked waiting for you (and what it asked), or idled. Where an agent
reports usage (its hooks' transcripts, Claude Code's status line, or
` + "`hive insights report`" + `), Hive adds its tokens and cost, with where each number
came from. An agent that reports nothing shows its usage as unknown: Hive
estimates nothing.

Budgets ([[insights.budget]] in the configuration) warn as they near their
limits, and a pausing one holds new tasks of plans until its window ends or
you run ` + "`hive insights resume`" + `.

Everything stays in ~/.hive/insights, kept for [retention] insights_days.
Nothing is sent anywhere: ` + "`hive insights export`" + ` writes it out when you ask.`,
		Example: `  hive insights
  hive insights --since 7d --by day
  hive insights asks
  hive insights budgets
  hive insights resume daily
  hive insights export --format csv --what spans --since 2026-10-01 -o spans.csv`,
		Args: noArgs,
		RunE: withTimeout(insightsTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			q, err := r.query(time.Now())
			if err != nil {
				return err
			}
			q.By = by
			in := client.NewInsights(a.client)
			sum, err := in.Summary(ctx, q)
			if err != nil {
				return insightsErr(err)
			}
			if a.json || a.template != "" {
				return a.emit(sum, nil)
			}
			asks, err := in.Asks(ctx, q, insightsAsksShown)
			if err != nil {
				return insightsErr(err)
			}
			budgets, err := in.Budgets(ctx)
			if err != nil {
				return insightsErr(err)
			}
			return printInsights(a.out, sum, asks, budgets, time.Now())
		}),
	})
	r.flags(cmd)
	cmd.Flags().StringVar(&by, "by", "", "group rows by kind (default), agent, environment, task, day or model")
	_ = cmd.RegisterFlagCompletionFunc("by", cobra.FixedCompletions(insights.Bys, cobra.ShellCompDirectiveNoFileComp))
	cmd.AddCommand(newInsightsAsksCmd(a), newInsightsBudgetsCmd(a), newInsightsResumeCmd(a), newInsightsExportCmd(a),
		newInsightsReportCmd(a), newInsightsStatuslineCmd(a))
	return cmd
}

// insightsRange is the --since and --until flags.
type insightsRange struct {
	since, until string
}

func (r *insightsRange) flags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.since, "since", "", "from: 7d (this day and the 6 before), 12h, 2026-10-01, a time, or today (default)")
	cmd.Flags().StringVar(&r.until, "until", "", "until: a date (its end), a time, or a duration ago (default: now)")
}

// query is the range's wire form.
func (r insightsRange) query(now time.Time) (insights.QueryParams, error) {
	var q insights.QueryParams
	var err error
	if q.From, err = parseSince(r.since, now); err != nil {
		return q, &usageError{fmt.Errorf("--since: %w", err)}
	}
	if q.To, err = parseUntil(r.until, now); err != nil {
		return q, &usageError{fmt.Errorf("--until: %w", err)}
	}
	if !q.From.IsZero() && !q.To.IsZero() && q.To.Before(q.From) {
		return q, &usageError{errors.New("--until is before --since")}
	}
	return q, nil
}

func startOfLocalDay(t time.Time) time.Time {
	y, m, d := t.Local().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// parseSince reads --since: "" and "today" are the start of today; Nd and
// Nw count whole days back, today included; a Go duration is that long
// ago; a date is its local midnight; RFC 3339 is that moment.
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "":
		return time.Time{}, nil // the daemon's default: today
	case "today":
		return startOfLocalDay(now), nil
	case "yesterday":
		return startOfLocalDay(now).AddDate(0, 0, -1), nil
	}
	if n, unit, ok := dayCount(s); ok {
		return startOfLocalDay(now).AddDate(0, 0, -(n*unit - 1)), nil
	}
	return parseMoment(s, now, false)
}

// parseUntil reads --until: "" is now, a date is the end of that day.
func parseUntil(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "now" {
		return time.Time{}, nil
	}
	return parseMoment(s, now, true)
}

// dayCount reads "7d" or "2w": the count and the days in its unit.
func dayCount(s string) (n, unit int, ok bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	switch s[len(s)-1] {
	case 'd':
		unit = 1
	case 'w':
		unit = 7
	default:
		return 0, 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 || n > 3650 {
		return 0, 0, false
	}
	return n, unit, true
}

func parseMoment(s string, now time.Time, endOfDay bool) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, time.Local); err == nil {
		if endOfDay {
			return t.AddDate(0, 0, 1), nil
		}
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, strings.ToUpper(s)); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%q is not 7d, 12h, a date (2026-10-01) or a time (RFC 3339)", s)
}

func insightsErr(err error) error {
	if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodeUnknownMethod {
		return fmt.Errorf("the hive daemon is too old for insights; restart it with `hive daemon restart` (%w)", err)
	}
	return err
}

// --- the summary ---

// insightsStates are the states the summary has columns for, in order;
// time waiting for approval counts as waiting.
var insightsStates = []string{insights.StateWorking, insights.StateBlocked, insights.StateIdle, insights.StateDone}

func printInsights(w io.Writer, sum insights.Summary, asks []insights.Ask, budgets []insights.BudgetStatus, now time.Time) error {
	fmt.Fprintf(w, "%s, by %s\n\n", rangeText(sum.From, sum.To, now), sum.By)
	if len(sum.Rows) == 0 {
		fmt.Fprintln(w, "nothing recorded in this range: agents' time is counted while the daemon runs")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		head := []string{strings.ToUpper(sum.By), "AGENTS"}
		for _, st := range insightsStates {
			head = append(head, strings.ToUpper(st))
		}
		head = append(head, "WAITING ON YOU", "TOKENS", "COST", "SOURCE")
		fmt.Fprintln(tw, strings.Join(head, "\t"))
		for _, row := range append(slices.Clone(sum.Rows), sum.Total) {
			cells := []string{row.Key, strconv.Itoa(row.Agents)}
			if sum.By == insights.ByModel {
				cells[1] = "-"
			}
			for _, st := range insightsStates {
				cells = append(cells, msText(row.StatesMS[st]))
			}
			waiting := msText(row.WaitingMS)
			if row.Asks > 0 {
				waiting += fmt.Sprintf(" (%d %s)", row.Asks, plural(row.Asks, "ask", "asks"))
			}
			cells = append(cells, waiting, tokensText(row.Tokens), costText(row.CostUSD), cmpStr(strings.Join(row.Sources, ", "), "-"))
			fmt.Fprintln(tw, strings.Join(cells, "\t"))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(asks) > 0 {
		fmt.Fprintf(w, "\nWaiting on you (latest %d):\n", len(asks))
		for _, ask := range asks {
			fmt.Fprintf(w, "  %s\n", askLine(ask, now))
		}
	}
	var near []insights.BudgetStatus
	for _, b := range budgets {
		if b.Level != "" || b.Held {
			near = append(near, b)
		}
	}
	if len(near) > 0 {
		fmt.Fprintln(w, "\nBudgets:")
		for _, b := range near {
			fmt.Fprintf(w, "  %s\n", budgetLine(b))
		}
	}
	return nil
}

// rangeText is "Today (00:00 – 14:32)" or "Oct 19 00:00 – Oct 25 14:32".
func rangeText(from, to, now time.Time) string {
	from, to = from.Local(), to.Local()
	if from.Equal(startOfLocalDay(now)) && sameDay(to, now) {
		return fmt.Sprintf("Today (%s – %s)", from.Format("15:04"), to.Format("15:04"))
	}
	return fmt.Sprintf("%s – %s", from.Format("Jan 2 15:04"), to.Format("Jan 2 15:04"))
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Local().Date()
	by, bm, bd := b.Local().Date()
	return ay == by && am == bm && ad == bd
}

// askLine is one time an agent waited on a person.
func askLine(ask insights.Ask, now time.Time) string {
	who := cmpStr(ask.Name, cmpStr(ask.Kind, "agent"))
	if ask.Pane != "" {
		who += " (" + ask.Pane + ")"
	}
	if ask.Task != "" {
		who += " task " + ask.Plan + "/" + ask.Task
	}
	waited := msText(ask.WaitedMS)
	if ask.Open {
		waited += ", still waiting"
	}
	what := ""
	if ask.Reason != "" {
		what = ": " + strconvQuote(firstLine(ask.Reason))
	}
	return fmt.Sprintf("%s  %s waited %s%s", ask.Start.Local().Format(timeOrDate(ask.Start, now)), who, waited, what)
}

func timeOrDate(t, now time.Time) string {
	if sameDay(t, now) {
		return "15:04"
	}
	return "Jan 2 15:04"
}

// strconvQuote quotes s for one line, without escaping its non-ASCII.
func strconvQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

// budgetLine is "daily (day 2026-10-25): $21.30 of $25.00, 85%, warning".
func budgetLine(b insights.BudgetStatus) string {
	name := b.Budget.Name
	if b.Key != "" && b.Budget.Per != insights.PerDay {
		name += " · " + b.Key
	}
	parts := []string{fmt.Sprintf("%s: %s, %.0f%%", name, usedText(b.Budget.Limits, b.Used), b.Fraction*100)}
	if b.Level != "" {
		parts = append(parts, b.Level)
	}
	switch {
	case b.Held:
		parts = append(parts, "holding new tasks (hive insights resume "+resumeArgs(b)+")")
	case b.Resumed:
		parts = append(parts, "resumed until the window ends")
	}
	return strings.Join(parts, ", ")
}

func resumeArgs(b insights.BudgetStatus) string {
	args := shellWord(b.Budget.Name)
	if b.Key != "" && b.Budget.Per != insights.PerDay {
		args += " " + shellWord(b.Key)
	}
	return args
}

func shellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`;&|<>()*?[]#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// usedText is each limit's use: "$21.30 of $25.00 · 3.1M of 20M tokens".
func usedText(l insights.Limits, u insights.Amounts) string {
	var parts []string
	if l.CostUSD > 0 {
		used := "unknown"
		if u.CostKnown {
			used = fmt.Sprintf("$%.2f", u.CostUSD)
		}
		parts = append(parts, fmt.Sprintf("%s of $%.2f", used, l.CostUSD))
	}
	if l.Tokens > 0 {
		used := "unknown"
		if u.TokensKnown {
			used = compactCount(u.Tokens)
		}
		parts = append(parts, fmt.Sprintf("%s of %s tokens", used, compactCount(l.Tokens)))
	}
	if l.WorkingMS > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s working", msText(u.WorkingMS), msText(l.WorkingMS)))
	}
	return cmpStr(strings.Join(parts, " · "), "no limits")
}

// msText is a duration in milliseconds, roughly: 1h20m, 12m, 45s, 0.
func msText(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d <= 0:
		return "0"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	if m := int(d.Minutes()) % 60; m > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dh", h)
}

// tokensText is a row's tokens: "1.2M (in 900k · out 80k · cache 220k)",
// or unknown when nothing reported any.
func tokensText(t *insights.Tokens) string {
	if t == nil {
		return "unknown"
	}
	return fmt.Sprintf("%s (in %s · out %s · cache %s)", compactCount(t.Total()), compactCount(t.Input), compactCount(t.Output),
		compactCount(t.CacheRead+t.CacheWrite))
}

func costText(c *float64) string {
	if c == nil {
		return "unknown"
	}
	return fmt.Sprintf("$%.2f", *c)
}

// compactCount is 950, 12.3k, 4.5M or 1.2B.
func compactCount(n int64) string {
	f := float64(n)
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return trimZero(fmt.Sprintf("%.1f", f/1e3)) + "k"
	case n < 1_000_000_000:
		return trimZero(fmt.Sprintf("%.1f", f/1e6)) + "M"
	}
	return trimZero(fmt.Sprintf("%.1f", f/1e9)) + "B"
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// --- subcommands ---

func newInsightsAsksCmd(a *app) *cobra.Command {
	var (
		r     insightsRange
		kind  string
		env   string
		limit int
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "asks",
		Short: "List the times agents waited on you, and what they asked",
		Long: `Each time an agent was blocked (a permission prompt, a question) or a task
waited for your approval, with what was asked and how long it waited.
Newest first.`,
		Args: noArgs,
		RunE: withTimeout(insightsTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			q, err := r.query(time.Now())
			if err != nil {
				return err
			}
			q.Kind, q.Environment = kind, env
			asks, err := client.NewInsights(a.client).Asks(ctx, q, limit)
			if err != nil {
				return insightsErr(err)
			}
			if asks == nil {
				asks = []insights.Ask{}
			}
			return a.emit(asks, func() error {
				if len(asks) == 0 {
					fmt.Fprintln(a.out, "no agent waited on you in this range")
					return nil
				}
				w := a.table()
				fmt.Fprintln(w, "WHEN\tWAITED\tWHO\tENVIRONMENT\tTASK\tASKED")
				now := time.Now()
				for _, ask := range asks {
					waited := msText(ask.WaitedMS)
					if ask.Open {
						waited += " (waiting)"
					}
					who := cmpStr(ask.Name, cmpStr(ask.Kind, "-"))
					if ask.Pane != "" {
						who += " " + ask.Pane
					}
					tsk := "-"
					if ask.Task != "" {
						tsk = ask.Plan + "/" + ask.Task
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", ask.Start.Local().Format(timeOrDate(ask.Start, now)), waited, who,
						cmpStr(ask.Env, "-"), tsk, cmpStr(firstLine(ask.Reason), "-"))
				}
				return w.Flush()
			})
		}),
	})
	r.flags(cmd)
	cmd.Flags().StringVar(&kind, "kind", "", "only agents of this kind")
	cmd.Flags().StringVar(&env, "env", "", "only this environment")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "at most this many (default 1000)")
	return cmd
}

func newInsightsBudgetsCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:     "budgets",
		Aliases: []string{"budget"},
		Short:   "Show each budget's use of its limits, and what it holds",
		Long: `Each [[insights.budget]] of the configuration, for each key of its window
(today, or each plan, environment or kind), with what it used of its limits.
A budget warns at warn_at of any limit; one with action = "pause" holds new
tasks in its scope once a limit is reached, until its window ends or
` + "`hive insights resume`" + `.`,
		Args: noArgs,
		RunE: withTimeout(insightsTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			st, err := client.NewInsights(a.client).Budgets(ctx)
			if err != nil {
				return insightsErr(err)
			}
			if st == nil {
				st = []insights.BudgetStatus{}
			}
			return a.emit(st, func() error { return printBudgets(a, st) })
		}),
	})
}

func printBudgets(a *app, st []insights.BudgetStatus) error {
	if len(st) == 0 {
		fmt.Fprintf(a.out, "no budgets: add [[insights.budget]] to %s (hive config --help)\n", a.configPath)
		return nil
	}
	w := a.table()
	fmt.Fprintln(w, "BUDGET\tPER\tKEY\tWINDOW\tUSED\tSHARE\tLEVEL\tACTION")
	for _, b := range st {
		action := cmpStr(b.Budget.Action, insights.ActionWarn)
		switch {
		case b.Held:
			action += " (holding)"
		case b.Resumed:
			action += " (resumed)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%.0f%%\t%s\t%s\n", b.Budget.Name, b.Budget.Per, cmpStr(b.Key, "-"), b.Window,
			usedText(b.Budget.Limits, b.Used), b.Fraction*100, cmpStr(b.Level, "-"), action)
	}
	return w.Flush()
}

func newInsightsResumeCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "resume <budget> [key]",
		Short: "Let new tasks start again under a budget that holds them",
		Long: `A budget with action = "pause" holds new tasks in its scope once it is over a
limit. Resume lifts the hold until the budget's window ends (a new day; a
plan's budget lasts for the plan), for one key (a plan, environment or kind)
or, without one, for every key it holds. Agents already working are never
stopped by a budget.`,
		Example: `  hive insights resume daily
  hive insights resume per-env api`,
		Args:              rangeArgs(1, 2),
		ValidArgsFunction: completeBudgets(a),
		RunE: withTimeout(insightsTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			key := ""
			if len(args) > 1 {
				key = args[1]
			}
			st, err := client.NewInsights(a.client).Resume(ctx, args[0], key)
			if err != nil {
				return insightsErr(err)
			}
			return a.emit(st, func() error {
				for _, b := range st {
					fmt.Fprintf(a.out, "%s\n", budgetLine(b))
				}
				return nil
			})
		}),
	})
}

func completeBudgets(a *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), completeTimeout)
		defer cancel()
		st, err := client.NewInsights(a.client).Budgets(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		for _, b := range st {
			v := b.Budget.Name
			if len(args) == 1 {
				if b.Budget.Name != args[0] || b.Key == "" {
					continue
				}
				v = b.Key
			}
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// exportWhats are what `hive insights export` writes.
var exportWhats = []string{"spans", "usage", "alerts", "summary"}

func newInsightsExportCmd(a *app) *cobra.Command {
	var (
		r            insightsRange
		format, what string
		by, out      string
		kind, env    string
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "export",
		Short: "Write insights out as CSV or JSON",
		Long: `Write the records of a range: spans (time in each state), usage (tokens and
cost, with their sources), alerts (budgets' warnings), or the summary (as
` + "`hive insights`" + ` shows it). Nothing else ever leaves the machine.

Times are RFC 3339, durations in milliseconds, cost in US dollars; usage an
agent did not report is empty, not zero.`,
		Example: `  hive insights export --what spans --format csv -o spans.csv
  hive insights export --what summary --by day --since 30d --format json`,
		Args: noArgs,
		RunE: withTimeout(insightsTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			if !slices.Contains([]string{"csv", "json"}, format) {
				return &usageError{fmt.Errorf("--format %q: want csv or json", format)}
			}
			what = strings.ToLower(what)
			if what == "span" || what == "alert" {
				what += "s"
			}
			if !slices.Contains(exportWhats, what) {
				return &usageError{fmt.Errorf("--what %q: want one of %s", what, strings.Join(exportWhats, ", "))}
			}
			q, err := r.query(time.Now())
			if err != nil {
				return err
			}
			q.Kind, q.Environment, q.By = kind, env, by
			in := client.NewInsights(a.client)
			var write func(io.Writer) error
			if what == "summary" {
				sum, err := in.Summary(ctx, q)
				if err != nil {
					return insightsErr(err)
				}
				write = func(w io.Writer) error { return exportSummary(w, format, sum) }
			} else {
				recs, err := in.Export(ctx, q, what)
				if err != nil {
					return insightsErr(err)
				}
				write = func(w io.Writer) error { return exportRecords(w, format, insights.What(what), recs) }
			}
			if out == "" || out == "-" {
				return write(a.out)
			}
			return writeFileAtomic(out, write)
		}),
	})
	r.flags(cmd)
	cmd.Flags().StringVar(&format, "format", "csv", "csv or json")
	cmd.Flags().StringVar(&what, "what", "spans", "spans, usage, alerts or summary")
	cmd.Flags().StringVar(&by, "by", "", "the summary's grouping: kind (default), agent, environment, task, day or model")
	cmd.Flags().StringVarP(&out, "output", "o", "", "write to this file (default: standard output)")
	cmd.Flags().StringVar(&kind, "kind", "", "only agents of this kind")
	cmd.Flags().StringVar(&env, "env", "", "only this environment")
	_ = cmd.RegisterFlagCompletionFunc("format", cobra.FixedCompletions([]string{"csv", "json"}, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("what", cobra.FixedCompletions(exportWhats, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("by", cobra.FixedCompletions(insights.Bys, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// writeFileAtomic writes path through a temporary file beside it, so a
// failed export leaves no half-written file.
func writeFileAtomic(path string, write func(io.Writer) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := write(f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func exportJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// exportRecords writes spans, usage or alerts: their JSON objects, or a
// CSV row each.
func exportRecords(w io.Writer, format, what string, recs []insights.Record) error {
	if format == "json" {
		list := make([]any, 0, len(recs))
		for _, r := range recs {
			switch {
			case r.Span != nil:
				list = append(list, spanJSON{Span: *r.Span, DurationMS: r.Span.Duration().Milliseconds()})
			case r.Usage != nil:
				list = append(list, r.Usage)
			case r.Alert != nil:
				list = append(list, r.Alert)
			}
		}
		return exportJSON(w, list)
	}
	cw := csv.NewWriter(w)
	ts := func(t time.Time) string { return t.Format(time.RFC3339) }
	who := func(x insights.Who) []string { return []string{x.Agent, x.Pane, x.Kind, x.Name, x.Env, x.Plan, x.Task} }
	whoHead := []string{"agent", "pane", "kind", "name", "environment", "plan", "task"}
	switch what {
	case insights.RecordSpan:
		_ = cw.Write(append(append([]string{}, whoHead...), "state", "start", "end", "duration_ms", "reason"))
	case insights.RecordUsage:
		_ = cw.Write(append(append([]string{"time"}, whoHead...), "session", "model", "source", "input", "output", "cache_read", "cache_write", "cost_usd"))
	case insights.RecordAlert:
		_ = cw.Write([]string{"time", "budget", "per", "key", "window", "level", "action", "held", "cost_usd", "tokens", "working_ms", "message"})
	}
	for _, r := range recs {
		switch {
		case r.Span != nil:
			s := r.Span
			_ = cw.Write(append(who(s.Who), s.State, ts(s.Start), ts(s.End), strconv.FormatInt(s.Duration().Milliseconds(), 10), s.Reason))
		case r.Usage != nil:
			u := r.Usage
			in, out, cr, cwr := "", "", "", ""
			if t := u.Tokens; t != nil {
				in, out, cr, cwr = itoa(t.Input), itoa(t.Output), itoa(t.CacheRead), itoa(t.CacheWrite)
			}
			cost := ""
			if u.CostUSD != nil {
				cost = strconv.FormatFloat(*u.CostUSD, 'f', -1, 64)
			}
			_ = cw.Write(append(append([]string{ts(u.Time)}, who(u.Who)...), u.Session, u.Model, u.Source, in, out, cr, cwr, cost))
		case r.Alert != nil:
			al := r.Alert
			cost, tokens := "", ""
			if al.Used.CostKnown {
				cost = strconv.FormatFloat(al.Used.CostUSD, 'f', -1, 64)
			}
			if al.Used.TokensKnown {
				tokens = itoa(al.Used.Tokens)
			}
			_ = cw.Write([]string{
				ts(al.Time), al.Budget, al.Per, al.Key, al.Window, al.Level, al.Action, strconv.FormatBool(al.Held),
				cost, tokens, itoa(al.Used.WorkingMS), al.Message,
			})
		}
	}
	cw.Flush()
	return cw.Error()
}

// spanJSON is a span as exported: with its duration.
type spanJSON struct {
	insights.Span
	DurationMS int64 `json:"duration_ms"`
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// exportSummary writes the summary: as JSON, or a CSV row per group and
// one for the total.
func exportSummary(w io.Writer, format string, sum insights.Summary) error {
	if format == "json" {
		return exportJSON(w, sum)
	}
	states := []string{insights.StateWorking, insights.StateBlocked, insights.StateIdle, insights.StateDone, insights.StateUnknown, insights.StateApproval}
	cw := csv.NewWriter(w)
	head := []string{sum.By, "from", "to", "agents"}
	for _, st := range states {
		head = append(head, st+"_ms")
	}
	head = append(head, "waiting_ms", "asks", "input", "output", "cache_read", "cache_write", "tokens", "cost_usd", "sources", "models")
	_ = cw.Write(head)
	for _, row := range append(slices.Clone(sum.Rows), sum.Total) {
		cells := []string{row.Key, sum.From.Format(time.RFC3339), sum.To.Format(time.RFC3339), strconv.Itoa(row.Agents)}
		for _, st := range states {
			cells = append(cells, itoa(row.StatesMS[st]))
		}
		cells = append(cells, itoa(row.WaitingMS), strconv.Itoa(row.Asks))
		if t := row.Tokens; t != nil {
			cells = append(cells, itoa(t.Input), itoa(t.Output), itoa(t.CacheRead), itoa(t.CacheWrite), itoa(t.Total()))
		} else {
			cells = append(cells, "", "", "", "", "")
		}
		cost := ""
		if row.CostUSD != nil {
			cost = strconv.FormatFloat(*row.CostUSD, 'f', -1, 64)
		}
		cells = append(cells, cost, strings.Join(row.Sources, " "), strings.Join(row.Models, " "))
		_ = cw.Write(cells)
	}
	cw.Flush()
	return cw.Error()
}

// --- reporting usage ---

func newInsightsReportCmd(a *app) *cobra.Command {
	var (
		paneID, session, model, source string
		input, output, cacheR, cacheW  int64
		cost                           float64
		total                          bool
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "report",
		Short: "Report an agent's usage: tokens and cost (for scripts, hooks and plugins)",
		Long: `Tell Hive what the agent in a pane used. Run inside the pane, it reports for
$HIVE_PANE_ID. Give only what you know: what is not given stays unknown.

With --total the numbers are the session's totals so far, as agents report
them: Hive records only what grew, so reporting the same total again (or
from another source) never counts it twice, and a total that went down
starts a new session. Without it they are an increment.`,
		Example: `  hive insights report --input 1200 --output 340 --cost 0.02
  hive insights report --session "$SESSION" --total --cost 1.85 --model gpt-5`,
		Args: noArgs,
		RunE: withTimeout(insightsTimeout, func(ctx context.Context, cmd *cobra.Command, _ []string) error {
			id, err := a.paneArg([]string{paneID})
			if err != nil {
				return err
			}
			p := insights.UsageParams{ID: id, Session: session, Model: model, Source: source, Total: total}
			set := func(name string, v int64) *int64 {
				if !cmd.Flags().Changed(name) {
					return nil
				}
				return &v
			}
			p.Input, p.Output, p.CacheRead, p.CacheWrite = set("input", input), set("output", output), set("cache-read", cacheR), set("cache-write", cacheW)
			if cmd.Flags().Changed("cost") {
				p.CostUSD = &cost
			}
			if p.Input == nil && p.Output == nil && p.CacheRead == nil && p.CacheWrite == nil && p.CostUSD == nil {
				return &usageError{errors.New("report something: --input, --output, --cache-read, --cache-write or --cost")}
			}
			res, err := client.NewInsights(a.client).ReportUsage(ctx, p)
			if err != nil {
				return insightsErr(err)
			}
			return a.emit(res, func() error {
				who := cmpStr(res.Agent.Name, cmpStr(res.Agent.Kind, "the agent"))
				if res.Agent.Pane != "" {
					who += " in pane " + res.Agent.Pane
				}
				if res.Recorded == nil {
					fmt.Fprintf(a.out, "nothing new for %s: the totals did not grow\n", who)
					return nil
				}
				fmt.Fprintf(a.out, "recorded for %s: %s\n", who, usageText(*res.Recorded))
				return nil
			})
		}),
	})
	f := cmd.Flags()
	f.StringVar(&paneID, "pane", "", "the agent's pane or process (default $HIVE_PANE_ID)")
	f.StringVar(&session, "session", "", "the agent's session: totals are per session (default: the agent)")
	f.StringVar(&model, "model", "", "the model the usage is for")
	f.StringVar(&source, "source", "", "where the numbers come from (default report)")
	f.Int64Var(&input, "input", 0, "input tokens (not cached)")
	f.Int64Var(&output, "output", 0, "output tokens")
	f.Int64Var(&cacheR, "cache-read", 0, "input tokens read from a cache")
	f.Int64Var(&cacheW, "cache-write", 0, "input tokens written to a cache")
	f.Float64Var(&cost, "cost", 0, "cost in US dollars, as the agent or provider stated it")
	f.BoolVar(&total, "total", false, "the numbers are the session's totals so far, not an increment")
	return cmd
}

// usageText is what a usage record holds: "1.2k tokens (in 1k · out 200) · $0.02".
func usageText(u insights.Usage) string {
	var parts []string
	if t := u.Tokens; t != nil {
		parts = append(parts, fmt.Sprintf("%s tokens (in %s · out %s · cache %s)", compactCount(t.Total()), compactCount(t.Input),
			compactCount(t.Output), compactCount(t.CacheRead+t.CacheWrite)))
	}
	if u.CostUSD != nil {
		parts = append(parts, fmt.Sprintf("$%.4f", *u.CostUSD))
	}
	if u.Model != "" {
		parts = append(parts, u.Model)
	}
	return strings.Join(parts, " · ") + " (" + u.Source + ")"
}

// statuslineTimeout bounds a status line's report: Claude Code waits for
// the command before it draws.
const statuslineTimeout = 1500 * time.Millisecond

// statuslineInput is what Claude Code passes its status line command.
type statuslineInput struct {
	SessionID string `json:"session_id"`
	Model     struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Cost *struct {
		TotalCostUSD *float64 `json:"total_cost_usd"`
	} `json:"cost"`
}

func newInsightsStatuslineCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "statusline",
		Short: "Claude Code's status line command: report its cost to Hive, print a status line",
		Long: `Set as Claude Code's status line command, it reads what Claude Code passes it
(the session, its model and its cost so far), reports the cost for the pane it
runs in, and prints a short status line: the model, the session's cost, and a
budget near or over its limit.

It never starts the daemon, does nothing but print outside a Hive pane, and
always exits 0. In ~/.claude/settings.json:

  "statusLine": {"type": "command", "command": "hive insights statusline"}`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), statuslineTimeout)
			defer cancel()
			var in statuslineInput
			if data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20)); err == nil {
				_ = json.Unmarshal(data, &in)
			}
			var cost *float64
			if in.Cost != nil {
				cost = in.Cost.TotalCostUSD
			}
			var budgets []insights.BudgetStatus
			if id := a.getenv("HIVE_PANE_ID"); id != "" {
				ins := client.NewInsights(a.client)
				if cost != nil {
					_, err := ins.ReportUsage(ctx, insights.UsageParams{
						ID: id, Session: in.SessionID, Model: in.Model.ID, Source: insights.SourceStatusline, CostUSD: cost, Total: true,
					})
					if err != nil && a.getenv("HIVE_DEBUG_HOOKS") != "" {
						fmt.Fprintf(a.errOut, "hive: insights statusline: %v\n", err)
					}
				}
				budgets, _ = ins.Budgets(ctx)
			}
			fmt.Fprintln(a.out, statusline(in, cost, budgets))
			return nil
		},
	}
}

// statusline is "Opus · $1.24 · budget daily 85%".
func statusline(in statuslineInput, cost *float64, budgets []insights.BudgetStatus) string {
	parts := []string{cmpStr(in.Model.DisplayName, cmpStr(in.Model.ID, "claude"))}
	if cost != nil {
		parts = append(parts, fmt.Sprintf("$%.2f", *cost))
	}
	// The budget nearest its limit, if any is near.
	var worst *insights.BudgetStatus
	for i, b := range budgets {
		if b.Level == "" || b.Level == insights.LevelUnenforceable {
			continue
		}
		if worst == nil || b.Fraction > worst.Fraction {
			worst = &budgets[i]
		}
	}
	if worst != nil {
		mark := "budget"
		if worst.Held {
			mark = "held by budget"
		}
		parts = append(parts, fmt.Sprintf("%s %s %.0f%%", mark, worst.Budget.Name, worst.Fraction*100))
	}
	return strings.Join(parts, " · ")
}

// --- usage from agents' hooks ---

// hookTranscriptTimeout bounds reading a transcript and reporting it.
const hookTranscriptTimeout = 3 * time.Second

// reportTranscript reports the usage an agent's transcript records, as
// its session's totals: the hooks `hive integration install` sets up pass
// its path. Unknown formats report nothing.
func (a *app) reportTranscript(ctx context.Context, paneID string, in hookInput) error {
	if in.TranscriptPath == "" {
		return nil
	}
	f, err := os.Open(in.TranscriptPath)
	if err != nil {
		return err
	}
	defer f.Close()
	tr, ok := insights.ReadTranscript(f)
	if !ok {
		return nil
	}
	t := tr.Tokens
	_, err = client.NewInsights(a.client).ReportUsage(ctx, insights.UsageParams{
		ID: paneID, Session: cmp.Or(in.SessionID, in.TranscriptPath), Model: tr.Model, Source: insights.SourceTranscript,
		Input: &t.Input, Output: &t.Output, CacheRead: &t.CacheRead, CacheWrite: &t.CacheWrite, Total: true,
	})
	return err
}
