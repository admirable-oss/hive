# API reference

<!-- Generated from the daemon's api.schema; do not edit by hand. Rewrite it
with: HIVE_UPDATE_SURFACE=1 go test ./internal/runtime/tests -run Frozen -->

API level 9. Clients speak newline-delimited JSON over the daemon's unix
socket (`hive api schema` prints this as JSON Schema; `pkg/hiveapi` is the Go SDK;
the README's "Wire protocol" section has the framing). A protocol-2 client
opens with `{"type":"hello","version":"2","params":{"client":"my-tool"}}`, then
sends requests such as
`{"version":"2","type":"request","id":"1","method":"agent.list","params":{"all":true}}`,
each answered by a `response` with the same `id` and a `result` or an `error`
(`code`, `message`). Pipe methods answer, then carry a byte stream on the
connection. Every name below is stable within 1.x (the compatibility policy).

## agent

### `agent.explain`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [agent.Explanation](#agentexplanation)

### `agent.focus`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [agent.Agent](#agentagent)

### `agent.get`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [agent.Agent](#agentagent)

### `agent.list`

| Param | Type | |
|---|---|---|
| `all` | `boolean` |  |

Result: [agent.ListResult](#agentlistresult)

### `agent.manifests`

No params.

Result: [\[\]agent.Manifest](#agentmanifest)

### `agent.prompt`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `read` | `integer` |  |
| `stall_timeout_ms` | `integer` |  |
| `start_timeout_ms` | `integer` |  |
| `text` | `string` |  |
| `timeout_ms` | `integer` |  |
| `until` | `string` |  |
| `wait` | `boolean` |  |

Result: [agent.PromptResult](#agentpromptresult)

### `agent.read`

| Param | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `id` | `string` |  |
| `lines` | `integer` |  |
| `source` | `string` |  |

Result: `[]string`

### `agent.reload`

No params.

Result: [agent.ReloadResult](#agentreloadresult)

### `agent.rename`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `name` | `string` |  |

Result: [agent.Agent](#agentagent)

### `agent.report`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `message` | `string` |  |
| `session_id` | `string` |  |
| `state` | `string` |  |
| `ttl_ms` | `integer` |  |

Result: [agent.Agent](#agentagent)

### `agent.seen`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: `map[string]boolean`

### `agent.send_keys`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `keys` | `[]string` |  |

Result: `map[string]boolean`

### `agent.start`

| Param | Type | |
|---|---|---|
| `args` | `[]string` |  |
| `base` | `string` |  |
| `env` | `map[string]string` |  |
| `environment_id` | `string` |  |
| `kind` | `string` |  |
| `name` | `string` |  |
| `worktree` | `string` |  |

Result: [runtime.AgentStarted](#runtimeagentstarted)

### `agent.stop`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [agent.Agent](#agentagent)

### `agent.wait`

| Param | Type | |
|---|---|---|
| `after` | `integer` |  |
| `id` | `string` |  |
| `stall_ms` | `integer` |  |
| `timeout_ms` | `integer` |  |
| `until` | `string` |  |

Result: [agent.WaitResult](#agentwaitresult)

## api

### `api.schema`

No params.

Result: [protocol.Schema](#protocolschema)

### `api.snapshot`

No params.

Result: [runtime.APISnapshot](#runtimeapisnapshot)

## client

### `client.report`

| Param | Type | |
|---|---|---|
| `budget` | `integer` |  |
| `rtt_us` | `integer` |  |

Result: [protocol.Empty](#protocolempty)

## debug

### `debug.pprof` (pipe)

| Param | Type | |
|---|---|---|
| `profile` | `string` |  |
| `seconds` | `integer` |  |

Result: [protocol.Empty](#protocolempty)

### `debug.stats`

No params.

Result: [runtime.Stats](#runtimestats)

## environment

### `environment.create`

| Param | Type | |
|---|---|---|
| `env` | `map[string]string` |  |
| `id` | `string` |  |
| `root` | `string` |  |
| `worktree` | [environment.Worktree](#environmentworktree) |  |

Result: [environment.Environment](#environmentenvironment)

### `environment.get`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [environment.Environment](#environmentenvironment)

### `environment.list`

No params.

Result: [\[\]environment.Environment](#environmentenvironment)

### `environment.remove`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [protocol.Empty](#protocolempty)

### `environment.update`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `set` | `map[string]string` |  |
| `unset` | `[]string` |  |

Result: [environment.Environment](#environmentenvironment)

## events

### `events.subscribe` (pipe)

| Param | Type | |
|---|---|---|
| `types` | `[]string` |  |

Result: [event.subscribeResult](#eventsubscriberesult)

## layout

### `layout.apply`

| Param | Type | |
|---|---|---|
| `environments` | [\[\]pane.EnvironmentSpec](#paneenvironmentspec) |  |
| `version` | `integer` |  |

Result: [pane.ApplyResult](#paneapplyresult)

### `layout.export`

| Param | Type | |
|---|---|---|
| `environments` | `[]string` |  |

Result: [pane.LayoutSpec](#panelayoutspec)

## metadata

### `metadata.list`

No params.

Result: [\[\]metadata.Entry](#metadataentry)

### `metadata.report`

| Param | Type | |
|---|---|---|
| `clear` | `boolean` |  |
| `environment` | `string` |  |
| `pane` | `string` |  |
| `source` | `string` |  |
| `title` | `string` |  |
| `tokens` | `map[string]string` |  |
| `ttl` | `integer` | nanoseconds |

Result: [metadata.Entry](#metadataentry)

## pane

### `pane.close`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [protocol.Empty](#protocolempty)

### `pane.focus`

| Param | Type | |
|---|---|---|
| `direction` | `string` |  |
| `id` | `string` |  |

Result: [pane.Pane](#panepane)

### `pane.get`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [pane.Pane](#panepane)

### `pane.input`

| Param | Type | |
|---|---|---|
| `data` | `string` |  |
| `id` | `string` |  |

Result: [protocol.Empty](#protocolempty)

### `pane.list`

| Param | Type | |
|---|---|---|
| `environment_id` | `string` |  |
| `tab_id` | `string` |  |

Result: [\[\]pane.Pane](#panepane)

### `pane.move`

| Param | Type | |
|---|---|---|
| `direction` | `string` |  |
| `environment_id` | `string` |  |
| `pane` | `string` |  |
| `tab_id` | `string` |  |
| `target` | `string` |  |

Result: [pane.Pane](#panepane)

### `pane.popup`

| Param | Type | |
|---|---|---|
| `height_pct` | `integer` |  |
| `spec` | [pane.Spec](#panespec) |  |
| `tab_id` | `string` |  |
| `width_pct` | `integer` |  |

Result: [pane.Pane](#panepane)

### `pane.read`

| Param | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `id` | `string` |  |
| `lines` | `integer` |  |
| `source` | `string` |  |

Result: `[]string`

### `pane.rename`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `name` | `string` |  |

Result: [pane.Pane](#panepane)

### `pane.resize`

| Param | Type | |
|---|---|---|
| `cells` | `integer` |  |
| `direction` | `string` |  |
| `id` | `string` |  |

Result: [pane.Pane](#panepane)

### `pane.run`

| Param | Type | |
|---|---|---|
| `command` | `string` |  |
| `id` | `string` |  |

Result: [protocol.Empty](#protocolempty)

### `pane.send_keys`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `keys` | `[]string` |  |

Result: [protocol.Empty](#protocolempty)

### `pane.send_text`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `paste` | `boolean` |  |
| `text` | `string` |  |

Result: [protocol.Empty](#protocolempty)

### `pane.split`

| Param | Type | |
|---|---|---|
| `direction` | `string` |  |
| `focus` | `boolean` |  |
| `pane` | `string` |  |
| `ratio` | `number` |  |
| `spec` | [pane.Spec](#panespec) |  |
| `tab_id` | `string` |  |

Result: [pane.Pane](#panepane)

### `pane.swap`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `with` | `string` |  |

Result: [protocol.Empty](#protocolempty)

### `pane.wait_output`

| Param | Type | |
|---|---|---|
| `anywhere` | `boolean` |  |
| `id` | `string` |  |
| `pattern` | `string` |  |
| `timeout_ms` | `integer` |  |

Result: `map[string]string`

### `pane.zoom`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `on` | `boolean` |  |

Result: [pane.Pane](#panepane)

## plugin

### `plugin.list`

No params.

Result: [\[\]plugin.Status](#pluginstatus)

### `plugin.reload`

No params.

Result: [\[\]plugin.Status](#pluginstatus)

### `plugin.run`

| Param | Type | |
|---|---|---|
| `action` | `string` |  |
| `environment` | `string` |  |
| `pane` | `string` |  |
| `plugin` | `string` |  |
| `tab` | `string` |  |

Result: [plugin.Result](#pluginresult)

## process

### `process.get`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [process.Process](#processprocess)

### `process.list`

| Param | Type | |
|---|---|---|
| `environment_id` | `string` |  |

Result: [\[\]process.Process](#processprocess)

### `process.logs`

| Param | Type | |
|---|---|---|
| `follow` | `boolean` |  |
| `id` | `string` |  |
| `stream` | `string` |  |
| `tail` | `integer` |  |

Result: [process.logsResult](#processlogsresult)

### `process.logs.stream`

No params.


### `process.start`

| Param | Type | |
|---|---|---|
| `args` | `[]string` |  |
| `command` | `string` |  |
| `cwd` | `string` |  |
| `env` | `map[string]string` |  |
| `environment_id` | `string` |  |
| `height` | `integer` |  |
| `terminal` | `boolean` |  |
| `width` | `integer` |  |

Result: [process.Process](#processprocess)

### `process.stop`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [protocol.Empty](#protocolempty)

## runtime

### `runtime.gc`

| Param | Type | |
|---|---|---|
| `dry_run` | `boolean` |  |
| `older_than` | `integer` | nanoseconds |

Result: [persist.GCReport](#persistgcreport)

### `runtime.ping`

No params.

Result: `map[string]boolean`

### `runtime.shutdown`

No params.


### `runtime.status`

No params.

Result: [runtime.Snapshot](#runtimesnapshot)

## session

### `session.restore`

| Param | Type | |
|---|---|---|
| `resume_agents` | `boolean` |  |

Result: [persist.RestoreReport](#persistrestorereport)

### `session.save`

No params.

Result: [persist.SaveResult](#persistsaveresult)

## tab

### `tab.close`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [protocol.Empty](#protocolempty)

### `tab.create`

| Param | Type | |
|---|---|---|
| `environment_id` | `string` |  |
| `name` | `string` |  |
| `pane` | [pane.Spec](#panespec) |  |

Result: `map[string]any`

### `tab.focus`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [pane.Tab](#panetab)

### `tab.get`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [pane.Tab](#panetab)

### `tab.list`

| Param | Type | |
|---|---|---|
| `environment_id` | `string` |  |
| `tab_id` | `string` |  |

Result: [\[\]pane.Tab](#panetab)

### `tab.rename`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |
| `name` | `string` |  |

Result: [pane.Tab](#panetab)

### `tab.resize`

| Param | Type | |
|---|---|---|
| `height` | `integer` |  |
| `id` | `string` |  |
| `width` | `integer` |  |

Result: [pane.Tab](#panetab)

## terminal

### `terminal.attach` (pipe)

| Param | Type | |
|---|---|---|
| `height` | `integer` |  |
| `process_id` | `string` |  |
| `width` | `integer` |  |

Result: [terminal.viewResult](#terminalviewresult)

### `terminal.frames` (pipe)

| Param | Type | |
|---|---|---|
| `height` | `integer` |  |
| `process_id` | `string` |  |
| `width` | `integer` |  |

Result: [terminal.viewResult](#terminalviewresult)

### `terminal.input`

| Param | Type | |
|---|---|---|
| `data` | `string` |  |
| `process_id` | `string` |  |
| `view_id` | `string` |  |

Result: [protocol.Empty](#protocolempty)

### `terminal.resize`

| Param | Type | |
|---|---|---|
| `height` | `integer` |  |
| `process_id` | `string` |  |
| `view_id` | `string` |  |
| `width` | `integer` |  |

Result: [protocol.Empty](#protocolempty)

### `terminal.snapshot`

| Param | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `process_id` | `string` |  |
| `scrollback` | `integer` |  |

Result: [terminal.SnapshotResult](#terminalsnapshotresult)

## worktree

### `worktree.create`

| Param | Type | |
|---|---|---|
| `base` | `string` |  |
| `branch` | `string` |  |
| `env` | `map[string]string` |  |
| `id` | `string` |  |
| `repo` | `string` |  |

Result: [environment.Environment](#environmentenvironment)

### `worktree.list`

| Param | Type | |
|---|---|---|
| `repo` | `string` |  |

Result: [\[\]runtime.WorktreeInfo](#runtimeworktreeinfo)

### `worktree.open`

| Param | Type | |
|---|---|---|
| `env` | `map[string]string` |  |
| `id` | `string` |  |
| `path` | `string` |  |

Result: [environment.Environment](#environmentenvironment)

### `worktree.remove`

| Param | Type | |
|---|---|---|
| `force` | `boolean` |  |
| `id` | `string` |  |
| `path` | `string` |  |

Result: [protocol.Empty](#protocolempty)

## Types

### agent.Agent

| Field | Type | |
|---|---|---|
| `completion_seq` | `integer` |  |
| `environment_id` | `string` |  |
| `id` | `string` |  |
| `kind` | `string` |  |
| `last_output` | `string` |  |
| `name` | `string` |  |
| `pane_id` | `string` |  |
| `pid` | `integer` |  |
| `reason` | `string` |  |
| `session_id` | `string` |  |
| `since` | `string` |  |
| `source` | `string` |  |
| `state` | `string` |  |
| `state_seq` | `integer` |  |
| `tab_id` | `string` |  |

### agent.Detect

| Field | Type | |
|---|---|---|
| `args_regex` | `string` |  |
| `process` | `[]string` |  |

### agent.Explanation

| Field | Type | |
|---|---|---|
| `agent` | [agent.Agent](#agentagent) |  |
| `detected` | `string` |  |
| `manifest` | `string` |  |
| `manifest_source` | `string` |  |
| `quiet_ns` | `integer` | nanoseconds |
| `report` | [agent.ReportState](#agentreportstate) |  |
| `screen` | `[]string` |  |
| `verdict` | [agent.Verdict](#agentverdict) |  |
| `worked_since_seen` | `boolean` |  |

### agent.KeysParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `keys` | `[]string` |  |

### agent.ListResult

| Field | Type | |
|---|---|---|
| `agents` | [\[\]agent.Agent](#agentagent) |  |
| `rollups` | [agent.Rollups](#agentrollups) |  |

### agent.Manifest

| Field | Type | |
|---|---|---|
| `activity` | `boolean` |  |
| `detect` | [agent.Detect](#agentdetect) |  |
| `id` | `string` |  |
| `name` | `string` |  |
| `priority` | `integer` |  |
| `resume` | [agent.Resume](#agentresume) |  |
| `rules` | [\[\]agent.Rule](#agentrule) |  |
| `source` | `string` |  |
| `start` | [agent.Start](#agentstart) |  |
| `synthetic` | `boolean` |  |

### agent.Matcher

| Field | Type | |
|---|---|---|
| `all` | [\[\]agent.Matcher](#agentmatcher) |  |
| `any` | [\[\]agent.Matcher](#agentmatcher) |  |
| `contains` | `string` |  |
| `ignore_case` | `boolean` |  |
| `line_regex` | `string` |  |
| `not` | [agent.Matcher](#agentmatcher) |  |
| `regex` | `string` |  |

### agent.PromptParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `read` | `integer` |  |
| `stall_timeout_ms` | `integer` |  |
| `start_timeout_ms` | `integer` |  |
| `text` | `string` |  |
| `timeout_ms` | `integer` |  |
| `until` | `string` |  |
| `wait` | `boolean` |  |

### agent.PromptResult

| Field | Type | |
|---|---|---|
| `agent` | [agent.Agent](#agentagent) |  |
| `outcome` | `string` |  |
| `output` | `[]string` |  |

### agent.ReadParams

| Field | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `id` | `string` |  |
| `lines` | `integer` |  |
| `source` | `string` |  |

### agent.ReloadResult

| Field | Type | |
|---|---|---|
| `manifests` | `[]string` |  |
| `warnings` | `[]string` |  |

### agent.ReportState

| Field | Type | |
|---|---|---|
| `message` | `string` |  |
| `session_id` | `string` |  |
| `state` | `string` |  |
| `ttl` | `integer` | nanoseconds |
| `until` | `string` |  |

### agent.Resume

| Field | Type | |
|---|---|---|
| `args` | `[]string` |  |
| `last` | `[]string` |  |

### agent.Rollups

| Field | Type | |
|---|---|---|
| `environments` | `map[string]string` |  |
| `session` | `string` |  |
| `tabs` | `map[string]string` |  |

### agent.Rule

| Field | Type | |
|---|---|---|
| `description` | `string` |  |
| `match` | [agent.Matcher](#agentmatcher) |  |
| `priority` | `integer` |  |
| `region` | `string` |  |
| `state` | `string` |  |

### agent.Start

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `submit` | `string` |  |
| `submit_delay_ms` | `integer` |  |

### agent.Verdict

| Field | Type | |
|---|---|---|
| `description` | `string` |  |
| `region` | `string` |  |
| `rule` | `integer` |  |
| `state` | `string` |  |
| `text` | `string` |  |

### agent.WaitParams

| Field | Type | |
|---|---|---|
| `after` | `integer` |  |
| `id` | `string` |  |
| `stall_ms` | `integer` |  |
| `timeout_ms` | `integer` |  |
| `until` | `string` |  |

### agent.WaitResult

| Field | Type | |
|---|---|---|
| `agent` | [agent.Agent](#agentagent) |  |
| `outcome` | `string` |  |

### agent.idParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |

### agent.listParams

| Field | Type | |
|---|---|---|
| `all` | `boolean` |  |

### agent.reportParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `message` | `string` |  |
| `session_id` | `string` |  |
| `state` | `string` |  |
| `ttl_ms` | `integer` |  |

### environment.CreateRequest

| Field | Type | |
|---|---|---|
| `env` | `map[string]string` |  |
| `id` | `string` |  |
| `root` | `string` |  |
| `worktree` | [environment.Worktree](#environmentworktree) |  |

### environment.Environment

| Field | Type | |
|---|---|---|
| `created_at` | `string` |  |
| `env` | `map[string]string` |  |
| `git` | [git.Status](#gitstatus) |  |
| `id` | `string` |  |
| `managed` | `boolean` |  |
| `name` | `string` |  |
| `path` | `string` |  |
| `schema` | `integer` |  |
| `status` | `string` |  |
| `worktree` | [environment.Worktree](#environmentworktree) |  |

### environment.UpdateRequest

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `set` | `map[string]string` |  |
| `unset` | `[]string` |  |

### environment.Worktree

| Field | Type | |
|---|---|---|
| `branch` | `string` |  |
| `repo` | `string` |  |

### environment.idParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |

### event.subscribeParams

| Field | Type | |
|---|---|---|
| `types` | `[]string` |  |

### event.subscribeResult

| Field | Type | |
|---|---|---|
| `seq` | `integer` |  |

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

### layout.Node

| Field | Type | |
|---|---|---|
| `first` | [layout.Node](#layoutnode) |  |
| `pane` | `string` |  |
| `ratio` | `number` |  |
| `second` | [layout.Node](#layoutnode) |  |
| `split` | `string` |  |

### layout.Rect

| Field | Type | |
|---|---|---|
| `H` | `integer` |  |
| `W` | `integer` |  |
| `X` | `integer` |  |
| `Y` | `integer` |  |

### metadata.Entry

| Field | Type | |
|---|---|---|
| `environment` | `string` |  |
| `pane` | `string` |  |
| `title` | `string` |  |
| `title_until` | `string` |  |
| `tokens` | [\[\]metadata.Token](#metadatatoken) |  |

### metadata.Report

| Field | Type | |
|---|---|---|
| `clear` | `boolean` |  |
| `environment` | `string` |  |
| `pane` | `string` |  |
| `source` | `string` |  |
| `title` | `string` |  |
| `tokens` | `map[string]string` |  |
| `ttl` | `integer` | nanoseconds |

### metadata.Token

| Field | Type | |
|---|---|---|
| `key` | `string` |  |
| `source` | `string` |  |
| `text` | `string` |  |
| `until` | `string` |  |

### pane.ApplyResult

| Field | Type | |
|---|---|---|
| `created_environments` | `[]string` |  |
| `panes` | `integer` |  |
| `tabs` | [\[\]pane.Tab](#panetab) |  |

### pane.CreateTabRequest

| Field | Type | |
|---|---|---|
| `environment_id` | `string` |  |
| `name` | `string` |  |
| `pane` | [pane.Spec](#panespec) |  |

### pane.EnvironmentSpec

| Field | Type | |
|---|---|---|
| `env` | `map[string]string` |  |
| `id` | `string` |  |
| `root` | `string` |  |
| `tabs` | [\[\]pane.TabSpec](#panetabspec) |  |

### pane.LayoutSpec

| Field | Type | |
|---|---|---|
| `environments` | [\[\]pane.EnvironmentSpec](#paneenvironmentspec) |  |
| `version` | `integer` |  |

### pane.MoveRequest

| Field | Type | |
|---|---|---|
| `direction` | `string` |  |
| `environment_id` | `string` |  |
| `pane` | `string` |  |
| `tab_id` | `string` |  |
| `target` | `string` |  |

### pane.NodeSpec

| Field | Type | |
|---|---|---|
| `first` | [pane.NodeSpec](#panenodespec) |  |
| `pane` | [pane.Spec](#panespec) |  |
| `ratio` | `number` |  |
| `second` | [pane.NodeSpec](#panenodespec) |  |
| `split` | `string` |  |

### pane.Pane

| Field | Type | |
|---|---|---|
| `created_at` | `string` |  |
| `environment_id` | `string` |  |
| `focused` | `boolean` |  |
| `id` | `string` |  |
| `name` | `string` |  |
| `popup` | `boolean` |  |
| `process` | [process.Process](#processprocess) |  |
| `process_id` | `string` |  |
| `rect` | [layout.Rect](#layoutrect) |  |
| `tab_id` | `string` |  |
| `zoomed` | `boolean` |  |

### pane.Popup

| Field | Type | |
|---|---|---|
| `height_pct` | `integer` |  |
| `pane` | `string` |  |
| `width_pct` | `integer` |  |

### pane.PopupRequest

| Field | Type | |
|---|---|---|
| `height_pct` | `integer` |  |
| `spec` | [pane.Spec](#panespec) |  |
| `tab_id` | `string` |  |
| `width_pct` | `integer` |  |

### pane.Spec

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `cwd` | `string` |  |
| `env` | `map[string]string` |  |
| `name` | `string` |  |

### pane.SplitRequest

| Field | Type | |
|---|---|---|
| `direction` | `string` |  |
| `focus` | `boolean` |  |
| `pane` | `string` |  |
| `ratio` | `number` |  |
| `spec` | [pane.Spec](#panespec) |  |
| `tab_id` | `string` |  |

### pane.Tab

| Field | Type | |
|---|---|---|
| `active` | `boolean` |  |
| `created_at` | `string` |  |
| `environment_id` | `string` |  |
| `focused` | `string` |  |
| `height` | `integer` |  |
| `id` | `string` |  |
| `layout` | [layout.Node](#layoutnode) |  |
| `name` | `string` |  |
| `popups` | [\[\]pane.Popup](#panepopup) |  |
| `width` | `integer` |  |
| `zoomed` | `string` |  |

### pane.TabSpec

| Field | Type | |
|---|---|---|
| `layout` | [pane.NodeSpec](#panenodespec) |  |
| `name` | `string` |  |

### pane.exportParams

| Field | Type | |
|---|---|---|
| `environments` | `[]string` |  |

### pane.focusParams

| Field | Type | |
|---|---|---|
| `direction` | `string` |  |
| `id` | `string` |  |

### pane.idParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |

### pane.inputParams

| Field | Type | |
|---|---|---|
| `data` | `string` |  |
| `id` | `string` |  |

### pane.keysParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `keys` | `[]string` |  |

### pane.listParams

| Field | Type | |
|---|---|---|
| `environment_id` | `string` |  |
| `tab_id` | `string` |  |

### pane.paneResizeParams

| Field | Type | |
|---|---|---|
| `cells` | `integer` |  |
| `direction` | `string` |  |
| `id` | `string` |  |

### pane.readParams

| Field | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `id` | `string` |  |
| `lines` | `integer` |  |
| `source` | `string` |  |

### pane.renameParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `name` | `string` |  |

### pane.runParams

| Field | Type | |
|---|---|---|
| `command` | `string` |  |
| `id` | `string` |  |

### pane.swapParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `with` | `string` |  |

### pane.tabResizeParams

| Field | Type | |
|---|---|---|
| `height` | `integer` |  |
| `id` | `string` |  |
| `width` | `integer` |  |

### pane.textParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `paste` | `boolean` |  |
| `text` | `string` |  |

### pane.waitParams

| Field | Type | |
|---|---|---|
| `anywhere` | `boolean` |  |
| `id` | `string` |  |
| `pattern` | `string` |  |
| `timeout_ms` | `integer` |  |

### pane.zoomParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `on` | `boolean` |  |

### persist.GCReport

| Field | Type | |
|---|---|---|
| `bytes` | `integer` |  |
| `dry_run` | `boolean` |  |
| `files` | `[]string` |  |
| `processes` | `[]string` |  |

### persist.GCRequest

| Field | Type | |
|---|---|---|
| `dry_run` | `boolean` |  |
| `older_than` | `integer` | nanoseconds |

### persist.RestoreReport

| Field | Type | |
|---|---|---|
| `panes` | [\[\]persist.RestoredPane](#persistrestoredpane) |  |

### persist.RestoreRequest

| Field | Type | |
|---|---|---|
| `resume_agents` | `boolean` |  |

### persist.RestoredPane

| Field | Type | |
|---|---|---|
| `agent` | `string` |  |
| `command` | `[]string` |  |
| `cwd` | `string` |  |
| `environment` | `string` |  |
| `error` | `string` |  |
| `pane` | `string` |  |
| `process_id` | `string` |  |
| `resumed` | `string` |  |
| `typed` | `string` |  |

### persist.SaveResult

| Field | Type | |
|---|---|---|
| `path` | `string` |  |
| `written` | `boolean` |  |

### plugin.Action

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `id` | `string` |  |
| `timeout` | `string` |  |
| `title` | `string` |  |

### plugin.Event

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `on` | `string` |  |
| `timeout` | `string` |  |

### plugin.Manifest

| Field | Type | |
|---|---|---|
| `actions` | [\[\]plugin.Action](#pluginaction) |  |
| `description` | `string` |  |
| `events` | [\[\]plugin.Event](#pluginevent) |  |
| `id` | `string` |  |
| `min_hive_version` | `string` |  |
| `name` | `string` |  |
| `platforms` | `[]string` |  |
| `runtime` | [\[\]plugin.Runtime](#pluginruntime) |  |
| `startup` | [\[\]plugin.Startup](#pluginstartup) |  |
| `version` | `string` |  |

### plugin.Result

| Field | Type | |
|---|---|---|
| `exit_code` | `integer` |  |
| `output` | `string` |  |

### plugin.Runtime

| Field | Type | |
|---|---|---|
| `command` | `string` |  |
| `min_version` | `string` |  |

### plugin.Startup

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `restart` | `boolean` |  |

### plugin.Status

| Field | Type | |
|---|---|---|
| `commit` | `string` |  |
| `dir` | `string` |  |
| `enabled` | `boolean` |  |
| `id` | `string` |  |
| `installed_at` | `string` |  |
| `last_error` | `string` |  |
| `linked` | `boolean` |  |
| `manifest` | [plugin.Manifest](#pluginmanifest) |  |
| `problem` | `string` |  |
| `ref` | `string` |  |
| `running` | `integer` |  |
| `source` | `string` |  |

### process.LogsRequest

| Field | Type | |
|---|---|---|
| `follow` | `boolean` |  |
| `id` | `string` |  |
| `stream` | `string` |  |
| `tail` | `integer` |  |

### process.Process

| Field | Type | |
|---|---|---|
| `args` | `[]string` |  |
| `command` | `string` |  |
| `ended_at` | `string` |  |
| `env` | `map[string]string` |  |
| `environment_id` | `string` |  |
| `exit_code` | `integer` |  |
| `id` | `string` |  |
| `lost` | `boolean` |  |
| `pid` | `integer` |  |
| `started_at` | `string` |  |
| `status` | `string` |  |
| `terminal` | `boolean` |  |
| `working_dir` | `string` |  |

### process.StartRequest

| Field | Type | |
|---|---|---|
| `args` | `[]string` |  |
| `command` | `string` |  |
| `cwd` | `string` |  |
| `env` | `map[string]string` |  |
| `environment_id` | `string` |  |
| `height` | `integer` |  |
| `terminal` | `boolean` |  |
| `width` | `integer` |  |

### process.idParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |

### process.listParams

| Field | Type | |
|---|---|---|
| `environment_id` | `string` |  |

### process.logsResult

| Field | Type | |
|---|---|---|
| `logs` | `string` |  |
| `truncated` | `boolean` |  |

### protocol.Empty

An object.

### protocol.LinkReport

| Field | Type | |
|---|---|---|
| `budget` | `integer` |  |
| `rtt_us` | `integer` |  |

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

### runtime.APISnapshot

| Field | Type | |
|---|---|---|
| `agents` | [\[\]agent.Agent](#agentagent) |  |
| `daemon` | [runtime.Snapshot](#runtimesnapshot) |  |
| `environments` | [\[\]environment.Environment](#environmentenvironment) |  |
| `panes` | [\[\]pane.Pane](#panepane) |  |
| `processes` | [\[\]process.Process](#processprocess) |  |
| `rollups` | [agent.Rollups](#agentrollups) |  |
| `tabs` | [\[\]pane.Tab](#panetab) |  |

### runtime.AgentStartRequest

| Field | Type | |
|---|---|---|
| `args` | `[]string` |  |
| `base` | `string` |  |
| `env` | `map[string]string` |  |
| `environment_id` | `string` |  |
| `kind` | `string` |  |
| `name` | `string` |  |
| `worktree` | `string` |  |

### runtime.AgentStarted

| Field | Type | |
|---|---|---|
| `agent` | [agent.Agent](#agentagent) |  |
| `environment` | [environment.Environment](#environmentenvironment) |  |
| `pane` | [pane.Pane](#panepane) |  |
| `tab` | [pane.Tab](#panetab) |  |

### runtime.PluginRunRequest

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `environment` | `string` |  |
| `pane` | `string` |  |
| `plugin` | `string` |  |
| `tab` | `string` |  |

### runtime.PprofRequest

| Field | Type | |
|---|---|---|
| `profile` | `string` |  |
| `seconds` | `integer` |  |

### runtime.Snapshot

| Field | Type | |
|---|---|---|
| `agents_survive_restart` | `boolean` |  |
| `api_level` | `integer` |  |
| `pid` | `integer` |  |
| `protocol_version` | `string` |  |
| `session` | `string` |  |
| `socket` | `string` |  |
| `started_at` | `string` |  |
| `status` | `string` |  |
| `version` | `string` |  |

### runtime.Stats

| Field | Type | |
|---|---|---|
| `goroutines` | `integer` |  |
| `heap_alloc` | `integer` |  |
| `heap_sys` | `integer` |  |
| `num_gc` | `integer` |  |
| `open_fds` | `integer` |  |
| `started_at` | `string` |  |
| `uptime_seconds` | `number` |  |

### runtime.WorktreeCreateRequest

| Field | Type | |
|---|---|---|
| `base` | `string` |  |
| `branch` | `string` |  |
| `env` | `map[string]string` |  |
| `id` | `string` |  |
| `repo` | `string` |  |

### runtime.WorktreeInfo

| Field | Type | |
|---|---|---|
| `bare` | `boolean` |  |
| `branch` | `string` |  |
| `detached` | `boolean` |  |
| `environments` | `[]string` |  |
| `head` | `string` |  |
| `locked` | `boolean` |  |
| `main` | `boolean` |  |
| `path` | `string` |  |

### runtime.WorktreeOpenRequest

| Field | Type | |
|---|---|---|
| `env` | `map[string]string` |  |
| `id` | `string` |  |
| `path` | `string` |  |

### runtime.WorktreeRemoveRequest

| Field | Type | |
|---|---|---|
| `force` | `boolean` |  |
| `id` | `string` |  |
| `path` | `string` |  |

### runtime.agentIDParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |

### runtime.agentRenameParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `name` | `string` |  |

### runtime.worktreeListParams

| Field | Type | |
|---|---|---|
| `repo` | `string` |  |

### terminal.SnapshotResult

| Field | Type | |
|---|---|---|
| `alt_screen` | `boolean` |  |
| `cursor` | [vt.Cursor](#vtcursor) |  |
| `height` | `integer` |  |
| `lines` | `[]string` |  |
| `scrollback` | `[]string` |  |
| `title` | `string` |  |
| `width` | `integer` |  |

### terminal.inputParams

| Field | Type | |
|---|---|---|
| `data` | `string` |  |
| `process_id` | `string` |  |
| `view_id` | `string` |  |

### terminal.resizeParams

| Field | Type | |
|---|---|---|
| `height` | `integer` |  |
| `process_id` | `string` |  |
| `view_id` | `string` |  |
| `width` | `integer` |  |

### terminal.snapshotParams

| Field | Type | |
|---|---|---|
| `ansi` | `boolean` |  |
| `process_id` | `string` |  |
| `scrollback` | `integer` |  |

### terminal.viewParams

| Field | Type | |
|---|---|---|
| `height` | `integer` |  |
| `process_id` | `string` |  |
| `width` | `integer` |  |

### terminal.viewResult

| Field | Type | |
|---|---|---|
| `height` | `integer` |  |
| `view_id` | `string` |  |
| `width` | `integer` |  |

### vt.Cursor

| Field | Type | |
|---|---|---|
| `Blink` | `boolean` |  |
| `Hidden` | `boolean` |  |
| `Shape` | `integer` |  |
| `X` | `integer` |  |
| `Y` | `integer` |  |
