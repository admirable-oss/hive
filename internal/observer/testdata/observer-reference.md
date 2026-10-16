# API reference

<!-- Generated from the daemon's api.schema; do not edit by hand. Rewrite it
with: HIVE_UPDATE_SURFACE=1 go test ./internal/runtime/tests -run Frozen -->

API level 11. Clients speak newline-delimited JSON over the daemon's unix
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
