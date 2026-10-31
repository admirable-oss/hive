# Insights

Hive follows every agent's state, so it knows how long each one worked, sat
blocked waiting for you (and what it asked), or idled. Where an agent
reports usage, Hive adds its tokens and cost, with where each number came
from. An agent that reports nothing shows its usage as unknown: Hive
estimates nothing.

Everything stays in `~/.hive/insights`, kept for `[retention]
insights_days`. Nothing is sent anywhere: `hive insights export` writes it
out when you ask.

```sh
hive insights                      # today, by agent kind
hive insights --since 7d --by day  # the last week, day by day
hive insights asks                 # when agents waited on you, and what they asked
hive insights budgets              # each budget's use of its limits
hive insights resume daily         # let new tasks start again under a paused budget
hive insights export --format csv --what spans --since 2026-10-01 -o spans.csv
```

`--by` groups rows by `kind` (the default), `agent`, `environment`, `task`,
`day` or `model`.

## Usage

Usage comes from the agents themselves:
- their hooks' transcripts;
- Claude Code's status line: set its status line command to
  `hive insights statusline`, which reports the cost to Hive and prints a
  status line;
- `hive insights report`, for scripts, hooks and plugins.

Each number keeps its source, and the views show it.

## Budgets

Budgets limit cost, tokens or agents' working time:

```toml
[[insights.budget]]
name = "daily"
per = "day"          # day | plan | environment | kind
environment = "api"  # only what runs in this environment
kind = "claude"      # only this agent kind
cost = 25.0          # US dollars
tokens = 20000000
working = "10h"      # agents' working time
warn_at = 0.8        # warn at this share of a limit
action = "pause"     # warn | pause
```

Each key of `per` is counted separately: today; each plan over its life
([tasks](tasks.md)); or each environment or agent kind, today. A budget
warns at `warn_at` of any of its limits. Once over, a `pause` budget holds
new tasks in its scope until its window ends or you run
`hive insights resume <name>`. Agents already working are never stopped.
Cost and tokens count only what agents report.

## Where else it shows

- The TUI's Overview has an insights panel.
- The desktop app and the web observer have insights pages.
- Dashboards read it with a scoped observer credential (`observe.insights`,
  [observers](observers.md)): only what the credential sees is counted, and
  what agents asked needs the screens permission.
- Budget alerts are `insights.alert` events.
