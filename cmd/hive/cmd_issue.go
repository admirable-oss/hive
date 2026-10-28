package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/issue"
)

const issueTimeout = 2 * time.Minute

func newIssueCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "issue",
		Aliases: []string{"issues"},
		Short:   "Read, search, comment on and close issues in GitHub and plugins' trackers",
		Long: `An issue is named tracker:key: github:owner/repo#42 (through the gh tool and
your login), or a tracker a plugin provides, such as linear:HIV-42.
` + "`hive task run --from-issue <ref>`" + ` starts a task from one.`,
		Example: `  hive issue get github:admirable-oss/hive#42
  hive issue search "flaky test" --tracker linear
  hive issue comment linear:HIV-42 "Fixed on main."
  hive task run --from-issue linear:HIV-42 --kind claude --env api --isolate --close-issue`,
	}
	cmd.AddCommand(newIssueGetCmd(a), newIssueSearchCmd(a), newIssueCommentCmd(a), newIssueCloseCmd(a))
	return cmd
}

func newIssueGetCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "get <tracker:key>",
		Short: "Show an issue",
		Args:  exactArgs(1),
		RunE: withTimeout(issueTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			i, err := client.NewIssues(a.client).Get(ctx, args[0])
			if err != nil {
				return err
			}
			return a.emit(i, func() error { printIssue(a, i); return nil })
		}),
	})
}

func printIssue(a *app, i issue.Issue) {
	fmt.Fprintf(a.out, "%s  %s\n", i.Ref, i.Title)
	w := a.table()
	if i.State != "" {
		fmt.Fprintf(w, "State\t%s\n", i.State)
	}
	if i.URL != "" {
		fmt.Fprintf(w, "URL\t%s\n", i.URL)
	}
	if len(i.Labels) > 0 {
		fmt.Fprintf(w, "Labels\t%s\n", strings.Join(i.Labels, ", "))
	}
	if len(i.Assignees) > 0 {
		fmt.Fprintf(w, "Assignees\t%s\n", strings.Join(i.Assignees, ", "))
	}
	_ = w.Flush()
	if body := strings.TrimSpace(i.Body); body != "" {
		fmt.Fprintf(a.out, "\n%s\n", body)
	}
}

func newIssueSearchCmd(a *app) *cobra.Command {
	var (
		tracker string
		limit   int
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "search <query>",
		Short: "Search one tracker, or every one",
		Args:  exactArgs(1),
		RunE: withTimeout(issueTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			found, err := client.NewIssues(a.client).Search(ctx, tracker, args[0], limit)
			if err != nil {
				return err
			}
			return a.emit(found, func() error {
				if len(found) == 0 {
					fmt.Fprintln(a.errOut, "No issues found.")
					return nil
				}
				w := a.table()
				fmt.Fprintln(w, "ISSUE\tSTATE\tTITLE")
				for _, i := range found {
					fmt.Fprintf(w, "%s\t%s\t%s\n", i.Ref, cmpStr(i.State, "-"), i.Title)
				}
				return w.Flush()
			})
		}),
	})
	cmd.Flags().StringVar(&tracker, "tracker", "", "only this tracker (github, or a plugin's)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "at most this many (up to 100)")
	return cmd
}

// textArg is a command's text argument, or standard input for "-".
func textArg(arg string) (string, error) {
	if arg != "-" {
		return arg, nil
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, issue.MaxComment+1))
	if err != nil {
		return "", &usageError{err}
	}
	return string(b), nil
}

func newIssueCommentCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "comment <tracker:key> <text | ->",
		Short: "Comment on an issue (- reads the comment from standard input)",
		Args:  exactArgs(2),
		RunE: withTimeout(issueTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			body, err := textArg(args[1])
			if err != nil {
				return err
			}
			if err := client.NewIssues(a.client).Comment(ctx, args[0], body); err != nil {
				return err
			}
			return a.done(map[string]any{"ref": args[0]}, "commented on %s", args[0])
		}),
	})
}

func newIssueCloseCmd(a *app) *cobra.Command {
	var comment string
	cmd := needsDaemon(&cobra.Command{
		Use:   "close <tracker:key>",
		Short: "Close an issue",
		Args:  exactArgs(1),
		RunE: withTimeout(issueTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			i, err := client.NewIssues(a.client).Close(ctx, args[0], comment)
			if err != nil {
				return err
			}
			return a.done(map[string]any{"issue": i}, "closed %s (%s)", i.Ref, cmpStr(i.State, "closed"))
		}),
	})
	cmd.Flags().StringVar(&comment, "comment", "", "a comment to add as it closes")
	return cmd
}

// issuePrompt is the prompt of a task started from an issue.
func issuePrompt(i issue.Issue, extra string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Resolve %s: %s\n", i.Ref, i.Title)
	if i.URL != "" {
		fmt.Fprintf(&b, "%s\n", i.URL)
	}
	if body := strings.TrimSpace(i.Body); body != "" {
		fmt.Fprintf(&b, "\n%s\n", body)
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		fmt.Fprintf(&b, "\n%s\n", extra)
	}
	return b.String()
}

var errIssueAndFile = errors.New("give a plan file or --from-issue, not both")
