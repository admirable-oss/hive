package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/admirable-oss/hive/internal/apisurface"
)

// TestCLIFrozen holds the CLI to api/v1/cli.txt: every command (and its
// aliases) and every flag, with its type.
func TestCLIFrozen(t *testing.T) {
	a, err := newApp(func(string) string { return "" }, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	root := newRootCmd(a)
	var lines []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden || strings.HasPrefix(c.Name(), "__") {
			return
		}
		path := c.CommandPath()
		lines = append(lines, "command "+path)
		for _, alias := range c.Aliases {
			lines = append(lines, fmt.Sprintf("command %s (alias %s)", path, alias))
		}
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden || f.Name == "help" {
				return
			}
			line := fmt.Sprintf("flag %s --%s %s", path, f.Name, f.Value.Type())
			if f.Shorthand != "" {
				line += " -" + f.Shorthand
			}
			lines = append(lines, line)
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	lines = append(lines,
		fmt.Sprintf("exit code %d ok", exitOK), fmt.Sprintf("exit code %d error", exitError),
		fmt.Sprintf("exit code %d usage", exitUsage), fmt.Sprintf("exit code %d not running", exitNotRunning),
		fmt.Sprintf("exit code %d timeout", exitTimeout), fmt.Sprintf("exit code %d agent", exitAgent),
	)
	apisurface.Check(t, "../../api/v1/cli.txt", lines)
}
