# API reference

<!-- Generated from the daemon's api.schema; do not edit by hand. Rewrite it
with: HIVE_UPDATE_SURFACE=1 go test ./internal/runtime/tests -run Frozen -->

API level 15. Clients speak newline-delimited JSON over the daemon's unix
socket (`hive api schema` prints this as JSON Schema; `pkg/hiveapi` is the Go SDK;
the README's "Wire protocol" section has the framing). A protocol-2 client
opens with `{"type":"hello","version":"2","params":{"client":"my-tool"}}`, then
sends requests such as
`{"version":"2","type":"request","id":"1","method":"agent.list","params":{"all":true}}`,
each answered by a `response` with the same `id` and a `result` or an `error`
(`code`, `message`). Pipe methods answer, then carry a byte stream on the
connection. Every name below is stable within 1.x (the compatibility policy).

## observe

### `observe.auth`

| Param | Type | |
|---|---|---|
| `token` | `string` |  |

Result: [observer.AuthResult](#observerauthresult)

### `observe.events` (pipe)

| Param | Type | |
|---|---|---|
| `epoch` | `string` |  |
| `since` | `integer` |  |

Result: [observer.EventsResult](#observereventsresult)

### `observe.history`

| Param | Type | |
|---|---|---|
| `epoch` | `string` |  |
| `limit` | `integer` |  |
| `since` | `integer` |  |

Result: [observer.Page](#observerpage)

### `observe.insights`

| Param | Type | |
|---|---|---|
| `asks` | `integer` | how many of the latest asks (default 20, at most 1000) |
| `by` | `string` | group rows by kind (default), agent, environment, task, day or model |
| `from` | `string` | from this time (default: the start of today, the daemon's local time) |
| `to` | `string` | until this time (default: now) |
| `within` | [observer.Scope](#observerscope) | count only what is also in this scope |

Result: [observer.Insights](#observerinsights)

### `observe.output`

| Param | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `lines` | `integer` |  |
| `pane` | `string` |  |

Result: [observer.OutputResult](#observeroutputresult)

### `observe.pane`

| Param | Type | |
|---|---|---|
| `pane` | `string` |  |

Result: [observer.PaneResult](#observerpaneresult)

### `observe.schema`

No params.

Result: [protocol.Schema](#protocolschema)

### `observe.screen`

| Param | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `pane` | `string` |  |
| `scrollback` | `integer` |  |

Result: [observer.Screen](#observerscreen)

### `observe.snapshot`

No params.

Result: [observer.Snapshot](#observersnapshot)

### `observe.status`

No params.

Result: [observer.Status](#observerstatus)

### `observe.watch` (pipe)

| Param | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `interval_ms` | `integer` |  |
| `pane` | `string` |  |

Result: [observer.WatchResult](#observerwatchresult)

## Types

### environment.Worktree

| Field | Type | |
|---|---|---|
| `branch` | `string` |  |
| `repo` | `string` |  |

### git.Status

| Field | Type | |
|---|---|---|
| `ahead` | `integer` |  |
| `behind` | `integer` |  |
| `branch` | `string` |  |
| `conflicts` | `integer` |  |
| `detached` | `boolean` |  |
| `head` | `string` |  |
| `modified` | `integer` |  |
| `root` | `string` |  |
| `staged` | `integer` |  |
| `untracked` | `integer` |  |
| `upstream` | `string` |  |

### insights.Amounts

| Field | Type | |
|---|---|---|
| `cost_known` | `boolean` |  |
| `cost_usd` | `number` |  |
| `tokens` | `integer` |  |
| `tokens_known` | `boolean` |  |
| `working_ms` | `integer` |  |

### insights.Ask

| Field | Type | |
|---|---|---|
| `agent` | `string` |  |
| `end` | `string` |  |
| `environment` | `string` |  |
| `kind` | `string` |  |
| `name` | `string` |  |
| `open` | `boolean` |  |
| `pane` | `string` |  |
| `plan` | `string` |  |
| `reason` | `string` |  |
| `start` | `string` |  |
| `state` | `string` |  |
| `task` | `string` |  |
| `waited_ms` | `integer` |  |

### insights.Budget

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `cost_usd` | `number` |  |
| `environment` | `string` |  |
| `kind` | `string` |  |
| `name` | `string` |  |
| `per` | `string` |  |
| `tokens` | `integer` |  |
| `warn_at` | `number` |  |
| `working_ms` | `integer` |  |

### insights.BudgetStatus

| Field | Type | |
|---|---|---|
| `budget` | [insights.Budget](#insightsbudget) |  |
| `fraction` | `number` |  |
| `held` | `boolean` |  |
| `key` | `string` |  |
| `level` | `string` |  |
| `resumed` | `boolean` |  |
| `used` | [insights.Amounts](#insightsamounts) |  |
| `window` | `string` |  |

### insights.Row

| Field | Type | |
|---|---|---|
| `agents` | `integer` |  |
| `asks` | `integer` |  |
| `cost_usd` | `number` |  |
| `key` | `string` |  |
| `models` | `[]string` |  |
| `sources` | `[]string` |  |
| `states_ms` | `map[string]integer` |  |
| `tokens` | [insights.Tokens](#insightstokens) |  |
| `waiting_ms` | `integer` |  |

### insights.Summary

| Field | Type | |
|---|---|---|
| `by` | `string` |  |
| `from` | `string` |  |
| `rows` | [\[\]insights.Row](#insightsrow) |  |
| `to` | `string` |  |
| `total` | [insights.Row](#insightsrow) |  |

### insights.Tokens

| Field | Type | |
|---|---|---|
| `cache_read` | `integer` |  |
| `cache_write` | `integer` |  |
| `input` | `integer` |  |
| `output` | `integer` |  |

### metadata.Token

| Field | Type | |
|---|---|---|
| `key` | `string` |  |
| `source` | `string` |  |
| `text` | `string` |  |
| `until` | `string` |  |

### observer.Agent

| Field | Type | |
|---|---|---|
| `completion_seq` | `integer` |  |
| `environment_id` | `string` |  |
| `id` | `string` |  |
| `kind` | `string` |  |
| `last_output` | `string` |  |
| `name` | `string` |  |
| `pane_id` | `string` |  |
| `reason` | `string` |  |
| `since` | `string` |  |
| `source` | `string` |  |
| `state` | `string` |  |
| `state_seq` | `integer` |  |
| `tab_id` | `string` |  |

### observer.AuthRequest

| Field | Type | |
|---|---|---|
| `token` | `string` |  |

### observer.AuthResult

| Field | Type | |
|---|---|---|
| `credential` | [observer.Credential](#observercredential) |  |
| `epoch` | `string` |  |
| `seq` | `integer` |  |

### observer.Credential

| Field | Type | |
|---|---|---|
| `created_at` | `string` |  |
| `expires_at` | `string` |  |
| `id` | `string` |  |
| `last_used_at` | `string` |  |
| `name` | `string` |  |
| `revoked_at` | `string` |  |
| `scope` | [observer.Scope](#observerscope) |  |
| `screens` | `boolean` |  |
| `state` | `string` |  |

### observer.Cursor

| Field | Type | |
|---|---|---|
| `visible` | `boolean` |  |
| `x` | `integer` |  |
| `y` | `integer` |  |

### observer.Environment

| Field | Type | |
|---|---|---|
| `created_at` | `string` |  |
| `git` | [git.Status](#gitstatus) |  |
| `id` | `string` |  |
| `name` | `string` |  |
| `path` | `string` |  |
| `status` | `string` |  |
| `worktree` | [environment.Worktree](#environmentworktree) |  |

### observer.Event

| Field | Type | |
|---|---|---|
| `data` | `any` |  |
| `seq` | `integer` |  |
| `time` | `string` |  |
| `type` | `string` |  |

### observer.EventsRequest

| Field | Type | |
|---|---|---|
| `epoch` | `string` |  |
| `since` | `integer` |  |

### observer.EventsResult

| Field | Type | |
|---|---|---|
| `epoch` | `string` |  |
| `seq` | `integer` |  |

### observer.HistoryRequest

| Field | Type | |
|---|---|---|
| `epoch` | `string` |  |
| `limit` | `integer` |  |
| `since` | `integer` |  |

### observer.Insights

| Field | Type | |
|---|---|---|
| `asks` | [\[\]insights.Ask](#insightsask) |  |
| `budgets` | [\[\]insights.BudgetStatus](#insightsbudgetstatus) |  |
| `summary` | [insights.Summary](#insightssummary) |  |

### observer.InsightsRequest

| Field | Type | |
|---|---|---|
| `asks` | `integer` | how many of the latest asks (default 20, at most 1000) |
| `by` | `string` | group rows by kind (default), agent, environment, task, day or model |
| `from` | `string` | from this time (default: the start of today, the daemon's local time) |
| `to` | `string` | until this time (default: now) |
| `within` | [observer.Scope](#observerscope) | count only what is also in this scope |

### observer.Metadata

| Field | Type | |
|---|---|---|
| `environment` | `string` |  |
| `pane` | `string` |  |
| `title` | `string` |  |
| `tokens` | [\[\]metadata.Token](#metadatatoken) |  |

### observer.OutputRequest

| Field | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `lines` | `integer` |  |
| `pane` | `string` |  |

### observer.OutputResult

| Field | Type | |
|---|---|---|
| `lines` | `[]string` |  |
| `pane` | `string` |  |

### observer.Page

| Field | Type | |
|---|---|---|
| `epoch` | `string` |  |
| `events` | [\[\]observer.Event](#observerevent) |  |
| `last` | `integer` |  |
| `more` | `boolean` |  |
| `next` | `integer` |  |
| `resync` | [observer.ResyncData](#observerresyncdata) |  |

### observer.Pane

| Field | Type | |
|---|---|---|
| `created_at` | `string` |  |
| `environment_id` | `string` |  |
| `focused` | `boolean` |  |
| `id` | `string` |  |
| `name` | `string` |  |
| `popup` | `boolean` |  |
| `process` | [observer.Process](#observerprocess) |  |
| `process_id` | `string` |  |
| `rect` | [observer.Rect](#observerrect) |  |
| `tab_id` | `string` |  |
| `zoomed` | `boolean` |  |

### observer.PaneRequest

| Field | Type | |
|---|---|---|
| `pane` | `string` |  |

### observer.PaneResult

| Field | Type | |
|---|---|---|
| `agent` | [observer.Agent](#observeragent) |  |
| `metadata` | [observer.Metadata](#observermetadata) |  |
| `pane` | [observer.Pane](#observerpane) |  |

### observer.Plan

| Field | Type | |
|---|---|---|
| `counts` | `map[string]integer` |  |
| `created_at` | `string` |  |
| `ended_at` | `string` |  |
| `id` | `string` |  |
| `name` | `string` |  |
| `rev` | `integer` |  |
| `state` | `string` |  |
| `tasks` | [\[\]observer.PlanTask](#observerplantask) |  |
| `total` | `integer` |  |

### observer.PlanTask

| Field | Type | |
|---|---|---|
| `attempt` | `integer` |  |
| `ended_at` | `string` |  |
| `environment_id` | `string` |  |
| `id` | `string` |  |
| `kind` | `string` |  |
| `machine` | `string` |  |
| `needs` | `[]string` |  |
| `pane_id` | `string` |  |
| `plan_id` | `string` |  |
| `rev` | `integer` |  |
| `started_at` | `string` |  |
| `state` | `string` |  |
| `title` | `string` |  |

### observer.Process

| Field | Type | |
|---|---|---|
| `ended_at` | `string` |  |
| `environment_id` | `string` |  |
| `exit_code` | `integer` |  |
| `id` | `string` |  |
| `pane_id` | `string` |  |
| `program` | `string` |  |
| `started_at` | `string` |  |
| `status` | `string` |  |
| `terminal` | `boolean` |  |

### observer.Rect

| Field | Type | |
|---|---|---|
| `h` | `integer` |  |
| `w` | `integer` |  |
| `x` | `integer` |  |
| `y` | `integer` |  |

### observer.ResyncData

| Field | Type | |
|---|---|---|
| `epoch` | `string` |  |
| `reason` | `string` |  |

### observer.Scope

| Field | Type | |
|---|---|---|
| `all` | `boolean` |  |
| `environments` | `[]string` |  |
| `panes` | `[]string` |  |

### observer.Screen

| Field | Type | |
|---|---|---|
| `alt_screen` | `boolean` |  |
| `cursor` | [observer.Cursor](#observercursor) |  |
| `height` | `integer` |  |
| `lines` | `[]string` |  |
| `pane` | `string` |  |
| `scrollback` | `[]string` |  |
| `title` | `string` |  |
| `width` | `integer` |  |

### observer.ScreenRequest

| Field | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `pane` | `string` |  |
| `scrollback` | `integer` |  |

### observer.Snapshot

| Field | Type | |
|---|---|---|
| `agents` | [\[\]observer.Agent](#observeragent) |  |
| `environments` | [\[\]observer.Environment](#observerenvironment) |  |
| `epoch` | `string` |  |
| `metadata` | [\[\]observer.Metadata](#observermetadata) |  |
| `panes` | [\[\]observer.Pane](#observerpane) |  |
| `plans` | [\[\]observer.Plan](#observerplan) |  |
| `seq` | `integer` |  |
| `tabs` | [\[\]observer.Tab](#observertab) |  |

### observer.Status

| Field | Type | |
|---|---|---|
| `agents` | `integer` |  |
| `agents_survive_restart` | `boolean` |  |
| `api_level` | `integer` |  |
| `credential` | [observer.Credential](#observercredential) |  |
| `environments` | `integer` |  |
| `epoch` | `string` |  |
| `panes` | `integer` |  |
| `seq` | `integer` |  |
| `session` | `string` |  |
| `started_at` | `string` |  |
| `version` | `string` |  |

### observer.Tab

| Field | Type | |
|---|---|---|
| `active` | `boolean` |  |
| `created_at` | `string` |  |
| `environment_id` | `string` |  |
| `focused` | `string` |  |
| `height` | `integer` |  |
| `id` | `string` |  |
| `name` | `string` |  |
| `panes` | `[]string` |  |
| `popups` | `[]string` |  |
| `width` | `integer` |  |
| `zoomed` | `string` |  |

### observer.WatchRequest

| Field | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `interval_ms` | `integer` |  |
| `pane` | `string` |  |

### observer.WatchResult

| Field | Type | |
|---|---|---|
| `pane` | `string` |  |
| `process_id` | `string` |  |

### protocol.Empty

An object.

### protocol.MethodSchema

| Field | Type | |
|---|---|---|
| `name` | `string` |  |
| `params` | `any` |  |
| `pipe` | `boolean` |  |
| `result` | `any` |  |

### protocol.Schema

| Field | Type | |
|---|---|---|
| `$defs` | `map[string]any` |  |
| `api_level` | `integer` |  |
| `methods` | [\[\]protocol.MethodSchema](#protocolmethodschema) |  |
