# API reference

<!-- Generated from the daemon's api.schema; do not edit by hand. Rewrite it
with: HIVE_UPDATE_SURFACE=1 go test ./internal/runtime/tests -run Frozen -->

API level 17. Clients speak newline-delimited JSON over the daemon's unix
socket (`hive api schema` prints this as JSON Schema; `pkg/hiveapi` is the Go SDK;
the README's "Wire protocol" section has the framing). A protocol-2 client
opens with `{"type":"hello","version":"2","params":{"client":"my-tool"}}`, then
sends requests such as
`{"version":"2","type":"request","id":"1","method":"agent.list","params":{"all":true}}`,
each answered by a `response` with the same `id` and a `result` or an `error`
(`code`, `message`). Pipe methods answer, then carry a byte stream on the
connection. Every name below is stable within 1.x (the compatibility policy).

## access

### `access.audit`

| Param | Type | |
|---|---|---|
| `limit` | `integer` |  |
| `member` | `string` |  |

Result: [\[\]access.AuditEntry](#accessauditentry)

### `access.doctor`

| Param | Type | |
|---|---|---|
| `fix` | `boolean` |  |

Result: [runtime.AccessDoctor](#runtimeaccessdoctor)

### `access.get`

| Param | Type | |
|---|---|---|
| `member` | `string` |  |

Result: [runtime.AccessMember](#runtimeaccessmember)

### `access.grant`

| Param | Type | |
|---|---|---|
| `environment` | `string` |  |
| `member` | `string` |  |
| `role` | `string` | observer, operator, owner, or none to take the role away |

Result: [access.Member](#accessmember)

### `access.invite`

| Param | Type | |
|---|---|---|
| `grants` | [\[\]access.Grant](#accessgrant) |  |
| `key` | `string` |  |
| `name` | `string` |  |
| `print` | `boolean` |  |
| `ttl_seconds` | `integer` |  |

Result: [runtime.AccessInvited](#runtimeaccessinvited)

### `access.list`

No params.

Result: [runtime.AccessList](#runtimeaccesslist)

### `access.renew`

| Param | Type | |
|---|---|---|
| `member` | `string` |  |
| `ttl_seconds` | `integer` |  |

Result: [access.Member](#accessmember)

### `access.revoke`

| Param | Type | |
|---|---|---|
| `member` | `string` |  |

Result: [access.Member](#accessmember)

### `access.whoami`

No params.

Result: [runtime.WhoAmI](#runtimewhoami)

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
| `isolate` | `boolean` |  |
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

## branch

### `branch.check`

| Param | Type | |
|---|---|---|
| `ref` | `string` | read this branch's repository |
| `repo` | `string` | read this repository (its main working tree); neither: every repository |

Result: [\[\]branch.Branch](#branchbranch)

### `branch.clean`

| Param | Type | |
|---|---|---|
| `delete_branch` | `boolean` | also delete the local branch once git agrees it has merged |
| `force` | `boolean` | remove it even with uncommitted changes (they are saved under refs/hive/saved/ first) |
| `merged` | `boolean` | every merged branch, instead of one |
| `ref` | `string` | the branch |

Result: [\[\]branch.Branch](#branchbranch)

### `branch.comment`

| Param | Type | |
|---|---|---|
| `body` | `string` | the comment |
| `by` | `string` | who wrote it |
| `line` | `integer` | the line it is about, in the branch's version of the file |
| `path` | `string` | the file it is about |
| `ref` | `string` | the branch |
| `send` | `boolean` | send it (and every comment not sent yet) to the branch's agent now |

Result: [branch.Branch](#branchbranch)

### `branch.conflicts`

| Param | Type | |
|---|---|---|
| `ref` | `string` | the branch |
| `with` | `string` | another branch to merge with (default: its base) |

Result: [branch.Conflicts](#branchconflicts)

### `branch.diff`

| Param | Type | |
|---|---|---|
| `context` | `integer` | lines of context around each change (default 3) |
| `limit` | `integer` | the most bytes of patch to return (default 512 KiB, at most 4 MiB) |
| `paths` | `[]string` | only these files |
| `ref` | `string` | the branch: its ID, environment, agent, plan/task or name |
| `stat` | `boolean` | only the files and their line counts, no patch |

Result: [branch.Diff](#branchdiff)

### `branch.get`

| Param | Type | |
|---|---|---|
| `ref` | `string` | the branch: its ID, environment, agent, plan/task or name |

Result: [branch.Branch](#branchbranch)

### `branch.list`

| Param | Type | |
|---|---|---|
| `all` | `boolean` | include cleaned and removed branches |
| `repo` | `string` | only this repository's branches (its main working tree) |

Result: [\[\]branch.Branch](#branchbranch)

### `branch.pr`

| Param | Type | |
|---|---|---|
| `base` | `string` | the branch it would merge into (default: the branch's base) |
| `body` | `string` | the description (default: its commits and gates) |
| `draft` | `boolean` | open it as a draft |
| `push` | `boolean` | push the branch first if the remote lacks its commits |
| `ref` | `string` | the branch |
| `remote` | `string` | the remote (default: its upstream's, else origin) |
| `title` | `string` | the title (default: its first commit's subject) |

Result: [branch.Branch](#branchbranch)

### `branch.push`

| Param | Type | |
|---|---|---|
| `ref` | `string` | the branch |
| `remote` | `string` | the remote to push to (default: its upstream's, else origin) |

Result: [branch.Branch](#branchbranch)

### `branch.ready`

| Param | Type | |
|---|---|---|
| `gates` | [\[\]branch.Gate](#branchgate) | gates to run instead of the configured ones |
| `ref` | `string` | the branch |

Result: [branch.Branch](#branchbranch)

### `branch.send`

| Param | Type | |
|---|---|---|
| `ref` | `string` | the branch: its ID, environment, agent, plan/task or name |

Result: [branch.Branch](#branchbranch)

### `branch.update`

| Param | Type | |
|---|---|---|
| `abort` | `boolean` | undo a merge or rebase stopped on a conflict |
| `rebase` | `boolean` | rebase onto the base instead of merging it |
| `ref` | `string` | the branch |

Result: [branch.UpdateResult](#branchupdateresult)

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

## insights

### `insights.asks`

| Param | Type | |
|---|---|---|
| `by` | `string` | group rows by kind (default), agent, environment, task, day or model |
| `environment` | `string` | only this environment |
| `from` | `string` | from this time (default: the start of today, local time) |
| `kind` | `string` | only this agent kind |
| `limit` | `integer` | at most this many (default and most 1000) |
| `to` | `string` | until this time (default: now) |

Result: [\[\]insights.Ask](#insightsask)

### `insights.budgets`

No params.

Result: [\[\]insights.BudgetStatus](#insightsbudgetstatus)

### `insights.export`

| Param | Type | |
|---|---|---|
| `by` | `string` | group rows by kind (default), agent, environment, task, day or model |
| `environment` | `string` | only this environment |
| `from` | `string` | from this time (default: the start of today, local time) |
| `kind` | `string` | only this agent kind |
| `to` | `string` | until this time (default: now) |
| `what` | `string` | spans, usage or alerts |

Result: [\[\]insights.Record](#insightsrecord)

### `insights.resume`

| Param | Type | |
|---|---|---|
| `budget` | `string` | the budget's name |
| `key` | `string` | one key of it (a plan, environment or kind; default: every held one) |

Result: [\[\]insights.BudgetStatus](#insightsbudgetstatus)

### `insights.summary`

| Param | Type | |
|---|---|---|
| `by` | `string` | group rows by kind (default), agent, environment, task, day or model |
| `environment` | `string` | only this environment |
| `from` | `string` | from this time (default: the start of today, local time) |
| `kind` | `string` | only this agent kind |
| `to` | `string` | until this time (default: now) |

Result: [insights.Summary](#insightssummary)

### `insights.usage`

| Param | Type | |
|---|---|---|
| `cache_read` | `integer` | input tokens read from a cache |
| `cache_write` | `integer` | input tokens written to a cache |
| `cost_usd` | `number` | cost in US dollars, as the agent or provider stated it |
| `id` | `string` | the agent's pane or process ID |
| `input` | `integer` | input tokens (not cached) |
| `model` | `string` | the model the usage is for |
| `output` | `integer` | output tokens |
| `session` | `string` | the agent's session (default: the agent): totals are per session |
| `source` | `string` | where the numbers come from: report (default), transcript, statusline, or a name of the reporter's |
| `total` | `boolean` | the numbers are the session's totals so far (what grew is recorded), not an increment |

Result: [insights.UsageResult](#insightsusageresult)

## issue

### `issue.close`

| Param | Type | |
|---|---|---|
| `comment` | `string` | a comment to add as it closes |
| `ref` | `string` | tracker:key |

Result: [issue.Issue](#issueissue)

### `issue.comment`

| Param | Type | |
|---|---|---|
| `body` | `string` | the comment, as the tracker formats it (Markdown on GitHub) |
| `ref` | `string` | tracker:key |

Result: `object`

### `issue.get`

| Param | Type | |
|---|---|---|
| `ref` | `string` | tracker:key (linear:HIV-42, github:owner/repo#42) |

Result: [issue.Issue](#issueissue)

### `issue.search`

| Param | Type | |
|---|---|---|
| `limit` | `integer` | at most this many (default 20, at most 100) |
| `query` | `string` | what to search for, in the tracker's own syntax |
| `tracker` | `string` | only this tracker (default: every one) |

Result: [\[\]issue.Issue](#issueissue)

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

## observer

### `observer.activity`

No params.

Result: [observer.Activity](#observeractivity)

### `observer.audit`

| Param | Type | |
|---|---|---|
| `credential` | `string` |  |
| `limit` | `integer` |  |

Result: [\[\]observer.AuditEntry](#observerauditentry)

### `observer.create`

| Param | Type | |
|---|---|---|
| `name` | `string` |  |
| `scope` | [observer.Scope](#observerscope) |  |
| `screens` | `boolean` |  |
| `ttl_seconds` | `integer` |  |

Result: [runtime.ObserverCreated](#runtimeobservercreated)

### `observer.get`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [runtime.ObserverInfo](#runtimeobserverinfo)

### `observer.list`

No params.

Result: [runtime.ObserverList](#runtimeobserverlist)

### `observer.revoke`

| Param | Type | |
|---|---|---|
| `id` | `string` |  |

Result: [observer.Credential](#observercredential)

### `observer.validate`

| Param | Type | |
|---|---|---|
| `token` | `string` |  |

Result: [runtime.ObserverValidation](#runtimeobservervalidation)

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

### `plugin.cancel`

| Param | Type | |
|---|---|---|
| `plugin` | `string` |  |
| `run` | `string` |  |

Result: [protocol.Empty](#protocolempty)

### `plugin.capabilities`

No params.

Result: [plugin.Discovery](#plugindiscovery)

### `plugin.card`

| Param | Type | |
|---|---|---|
| `card` | `string` |  |
| `environment` | `string` |  |
| `links` | [\[\]plugin.Link](#pluginlink) |  |
| `pane` | `string` |  |
| `plugin` | `string` |  |
| `rows` | [\[\]plugin.CardRow](#plugincardrow) |  |
| `ttl` | `integer` | nanoseconds |

Result: [plugin.CardContent](#plugincardcontent)

### `plugin.config`

| Param | Type | |
|---|---|---|
| `plugin` | `string` |  |

Result: [plugin.ConfigView](#pluginconfigview)

### `plugin.config.set`

| Param | Type | |
|---|---|---|
| `plugin` | `string` |  |
| `set` | `map[string]any` |  |
| `unset` | `[]string` |  |

Result: [plugin.ConfigView](#pluginconfigview)

### `plugin.contributions`

No params.

Result: [plugin.Contributions](#plugincontributions)

### `plugin.list`

No params.

Result: [\[\]plugin.Status](#pluginstatus)

### `plugin.ready`

| Param | Type | |
|---|---|---|
| `plugin` | `string` |  |

Result: [protocol.Empty](#protocolempty)

### `plugin.reload`

| Param | Type | |
|---|---|---|
| `plugin` | `string` |  |

Result: [\[\]plugin.Status](#pluginstatus)

### `plugin.request`

| Param | Type | |
|---|---|---|
| `capability` | `string` |  |
| `plugin` | `string` |  |
| `reason` | `string` |  |

Result: [plugin.PermissionRequest](#pluginpermissionrequest)

### `plugin.run`

| Param | Type | |
|---|---|---|
| `action` | `string` |  |
| `environment` | `string` |  |
| `pane` | `string` |  |
| `plugin` | `string` |  |
| `tab` | `string` |  |

Result: [plugin.Result](#pluginresult)

### `plugin.status`

| Param | Type | |
|---|---|---|
| `indicator` | `string` |  |
| `plugin` | `string` |  |
| `text` | `string` |  |
| `tone` | `string` |  |
| `ttl` | `integer` | nanoseconds |

Result: [plugin.StatusContent](#pluginstatuscontent)

### `plugin.view.clear`

| Param | Type | |
|---|---|---|
| `doc` | [plugin.ViewDoc](#pluginviewdoc) |  |
| `plugin` | `string` |  |
| `view` | `string` |  |

Result: [plugin.ViewContent](#pluginviewcontent)

### `plugin.view.click`

| Param | Type | |
|---|---|---|
| `environment` | `string` |  |
| `node` | `string` |  |
| `pane` | `string` |  |
| `plugin` | `string` |  |
| `tab` | `string` |  |
| `view` | `string` |  |

Result: [plugin.Result](#pluginresult)

### `plugin.view.set`

| Param | Type | |
|---|---|---|
| `doc` | [plugin.ViewDoc](#pluginviewdoc) |  |
| `plugin` | `string` |  |
| `view` | `string` |  |

Result: [plugin.ViewContent](#pluginviewcontent)

### `plugin.views`

No params.

Result: [\[\]plugin.ViewContent](#pluginviewcontent)

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

## task

### `task.approve`

| Param | Type | |
|---|---|---|
| `by` | `string` | who decides, for the task's log |
| `deny` | `boolean` | refuse: cancel the task instead of starting it |
| `id` | `string` | the plan's ID, or a unique prefix of it |
| `note` | `string` | why, for the task's log |
| `task` | `string` | the task (may be left out when one task waits) |

Result: [task.Plan](#taskplan)

### `task.cancel`

| Param | Type | |
|---|---|---|
| `id` | `string` | the plan's ID, or a unique prefix of it |
| `task` | `string` | one of its tasks |

Result: [task.Plan](#taskplan)

### `task.get`

| Param | Type | |
|---|---|---|
| `id` | `string` | the plan's ID, or a unique prefix of it |
| `task` | `string` | one of its tasks |

Result: [task.Plan](#taskplan)

### `task.list`

No params.

Result: [\[\]task.Summary](#tasksummary)

### `task.retry`

| Param | Type | |
|---|---|---|
| `id` | `string` | the plan's ID, or a unique prefix of it |
| `task` | `string` | one of its tasks |

Result: [task.Plan](#taskplan)

### `task.run`

| Param | Type | |
|---|---|---|
| `plan` | [task.PlanSpec](#taskplanspec) | the plan, as data |
| `source` | `string` | the plan file's name, for errors |
| `toml` | `string` | the plan, as the text of a plan file (instead of plan) |

Result: [task.Plan](#taskplan)

### `task.wait`

| Param | Type | |
|---|---|---|
| `after` | `integer` | with until=change: the plan revision already seen |
| `id` | `string` | the plan's ID, or a unique prefix of it |
| `task` | `string` | wait for this task rather than the whole plan |
| `timeout_ms` | `integer` | give up after this many milliseconds (0: never) |
| `until` | `string` | finished (default), attention (also a task waiting for a person) or change |

Result: [task.WaitResult](#taskwaitresult)

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

### `terminal.graphics` (pipe)

| Param | Type | |
|---|---|---|
| `height` | `integer` |  |
| `process_id` | `string` |  |
| `width` | `integer` |  |

Result: [protocol.Empty](#protocolempty)

### `terminal.image` (pipe)

| Param | Type | |
|---|---|---|
| `format` | `string` |  |
| `process_id` | `string` |  |
| `sha256` | `string` |  |
| `size` | `integer` |  |
| `transfer_id` | `string` |  |
| `view_id` | `string` |  |

Result: [terminal.ImageReceipt](#terminalimagereceipt)

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

## workflow

### `workflow.cancel`

| Param | Type | |
|---|---|---|
| `by` | `string` | who asks, for a rerun's record |
| `id` | `string` | the run's ID, or a unique prefix of it |

Result: [workflow.Run](#workflowrun)

### `workflow.fire`

| Param | Type | |
|---|---|---|
| `data` | `any` | the payload, which expressions read as trigger.data |
| `webhook` | `string` | the webhook's name, as workflows' [[on]] webhook says |

Result: [\[\]workflow.RunSummary](#workflowrunsummary)

### `workflow.get`

| Param | Type | |
|---|---|---|
| `name` | `string` | the workflow's name (hive workflow list) |

Result: [workflow.Workflow](#workflowworkflow)

### `workflow.list`

No params.

Result: [\[\]workflow.Workflow](#workflowworkflow)

### `workflow.reload`

No params.

Result: [\[\]workflow.Workflow](#workflowworkflow)

### `workflow.rerun`

| Param | Type | |
|---|---|---|
| `by` | `string` | who asks, for a rerun's record |
| `id` | `string` | the run's ID, or a unique prefix of it |

Result: [workflow.Run](#workflowrun)

### `workflow.run`

| Param | Type | |
|---|---|---|
| `by` | `string` | who asked, for the run's record |
| `dir` | `string` | the directory steps run in unless they name an environment (absolute) |
| `inputs` | `map[string]string` | the workflow's inputs, by name |
| `name` | `string` | a known workflow's name |
| `source` | `string` | the file's name, for errors |
| `toml` | `string` | the text of a workflow file (instead of name) |

Result: [workflow.Run](#workflowrun)

### `workflow.runs`

| Param | Type | |
|---|---|---|
| `limit` | `integer` | at most this many (0: every one kept) |
| `workflow` | `string` | only this workflow's runs |

Result: [\[\]workflow.RunSummary](#workflowrunsummary)

### `workflow.status`

| Param | Type | |
|---|---|---|
| `by` | `string` | who asks, for a rerun's record |
| `id` | `string` | the run's ID, or a unique prefix of it |

Result: [workflow.Run](#workflowrun)

### `workflow.trigger`

| Param | Type | |
|---|---|---|
| `name` | `string` | the trigger's name, as the plugin's [[contributes.triggers]] says |
| `payload` | `map[string]any` | what the workflows' match filters, and expressions read as trigger.data |
| `plugin` | `string` | the plugin whose trigger fires (a plugin's own call says it) |

Result: [runtime.workflowTriggerResult](#runtimeworkflowtriggerresult)

### `workflow.trust`

| Param | Type | |
|---|---|---|
| `name` | `string` | the workflow's name |
| `revoke` | `boolean` | stop its triggers instead |

Result: [workflow.Workflow](#workflowworkflow)

### `workflow.validate`

| Param | Type | |
|---|---|---|
| `source` | `string` | the file's name, for errors (and the workflow's name when it gives none) |
| `toml` | `string` | the text of a workflow file |

Result: [workflow.Spec](#workflowspec)

### `workflow.wait`

| Param | Type | |
|---|---|---|
| `id` | `string` | the run's ID, or a unique prefix of it |
| `timeout_ms` | `integer` | give up after this many milliseconds (0: never) |

Result: [workflow.WaitResult](#workflowwaitresult)

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

### access.AuditEntry

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `client` | `string` |  |
| `detail` | `string` |  |
| `from` | `string` |  |
| `member` | `string` |  |
| `method` | `string` |  |
| `name` | `string` |  |
| `time` | `string` |  |

### access.Grant

| Field | Type | |
|---|---|---|
| `environment` | `string` |  |
| `role` | `string` |  |

### access.Member

| Field | Type | |
|---|---|---|
| `created_at` | `string` |  |
| `expires_at` | `string` |  |
| `fingerprint` | `string` |  |
| `grants` | [\[\]access.Grant](#accessgrant) |  |
| `id` | `string` |  |
| `key` | `string` |  |
| `last_seen_at` | `string` |  |
| `managed` | `boolean` |  |
| `name` | `string` |  |
| `revoked_at` | `string` |  |
| `state` | `string` |  |

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

### agent.Input

| Field | Type | |
|---|---|---|
| `image` | `string` |  |

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
| `input` | [agent.Input](#agentinput) |  |
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

### branch.Branch

| Field | Type | |
|---|---|---|
| `added` | `integer` | lines added |
| `ahead` | `integer` | commits on the branch that its base lacks |
| `base` | `string` | what the branch merges into: a branch, or a commit |
| `base_commit` | `string` | the commit it started from |
| `behind` | `integer` | commits on its base that the branch lacks |
| `changed` | `[]string` | the changed files (at most 200) |
| `checking` | `boolean` | its gates are running |
| `cleanup` | `string` | why a merged branch's worktree was not removed |
| `closed_at` | `string` | when its worktree was removed |
| `comments` | [\[\]branch.Comment](#branchcomment) | review comments, sent or waiting to be sent to its agent |
| `committed` | `boolean` | it has been seen with commits of its own |
| `created_at` | `string` |  |
| `deleted` | `integer` | lines deleted |
| `dirty` | `boolean` | uncommitted changes in the worktree |
| `environment_id` | `string` | its worktree's environment, while there is one |
| `error` | `string` | why it could not be read last time |
| `files` | `integer` | files changed against its base, uncommitted changes included |
| `gates` | [\[\]branch.GateResult](#branchgateresult) | the last run of its gates |
| `head` | `string` | the commit checked out |
| `id` | `string` | the branch's ID in Hive: its environment's ID when it was made |
| `in_progress` | `boolean` | a merge or rebase is stopped there, for a person to resolve |
| `merged_at` | `string` |  |
| `name` | `string` | the branch's name |
| `overlaps` | [\[\]branch.Overlap](#branchoverlap) | other branches (or its base) changing the same files |
| `owner` | [branch.Owner](#branchowner) | the agent or task it was made for |
| `path` | `string` | its worktree, while there is one |
| `pull_request` | [branch.PullRequest](#branchpullrequest) | the pull request opened for it, on request |
| `pushed` | [branch.Push](#branchpush) | its last push, made on request |
| `ready_at` | `string` | when its gates passed |
| `ready_tree` | `string` | the files its gates passed on (a git tree) |
| `repo` | `string` | the repository (its main working tree) |
| `rev` | `integer` |  |
| `saved` | `string` | the ref keeping uncommitted work discarded with its worktree |
| `started` | `boolean` | an agent has worked there |
| `state` | `string` | created, working, ready, merged, cleaned or removed |
| `updated_at` | `string` |  |

### branch.CheckParams

| Field | Type | |
|---|---|---|
| `ref` | `string` | read this branch's repository |
| `repo` | `string` | read this repository (its main working tree); neither: every repository |

### branch.CleanRequest

| Field | Type | |
|---|---|---|
| `delete_branch` | `boolean` | also delete the local branch once git agrees it has merged |
| `force` | `boolean` | remove it even with uncommitted changes (they are saved under refs/hive/saved/ first) |
| `merged` | `boolean` | every merged branch, instead of one |
| `ref` | `string` | the branch |

### branch.Comment

| Field | Type | |
|---|---|---|
| `at` | `string` |  |
| `body` | `string` |  |
| `by` | `string` |  |
| `id` | `integer` |  |
| `line` | `integer` |  |
| `path` | `string` |  |
| `sent_at` | `string` |  |
| `sent_to` | `string` |  |

### branch.CommentRequest

| Field | Type | |
|---|---|---|
| `body` | `string` | the comment |
| `by` | `string` | who wrote it |
| `line` | `integer` | the line it is about, in the branch's version of the file |
| `path` | `string` | the file it is about |
| `ref` | `string` | the branch |
| `send` | `boolean` | send it (and every comment not sent yet) to the branch's agent now |

### branch.ConflictFile

| Field | Type | |
|---|---|---|
| `content` | `string` |  |
| `path` | `string` |  |
| `truncated` | `boolean` |  |

### branch.Conflicts

| Field | Type | |
|---|---|---|
| `branch` | `string` |  |
| `clean` | `boolean` |  |
| `files` | [\[\]branch.ConflictFile](#branchconflictfile) |  |
| `with` | `string` |  |

### branch.ConflictsRequest

| Field | Type | |
|---|---|---|
| `ref` | `string` | the branch |
| `with` | `string` | another branch to merge with (default: its base) |

### branch.Diff

| Field | Type | |
|---|---|---|
| `base` | `string` | what it merges into |
| `branch` | `string` | the branch's name |
| `dirty` | `boolean` |  |
| `files` | [\[\]git.FileChange](#gitfilechange) |  |
| `from` | `string` |  |
| `head` | `string` |  |
| `patch` | `string` |  |
| `to` | `string` |  |
| `truncated` | `boolean` |  |

### branch.DiffRequest

| Field | Type | |
|---|---|---|
| `context` | `integer` | lines of context around each change (default 3) |
| `limit` | `integer` | the most bytes of patch to return (default 512 KiB, at most 4 MiB) |
| `paths` | `[]string` | only these files |
| `ref` | `string` | the branch: its ID, environment, agent, plan/task or name |
| `stat` | `boolean` | only the files and their line counts, no patch |

### branch.Gate

| Field | Type | |
|---|---|---|
| `command` | `[]string` | the command, as an argument vector |
| `name` | `string` | what it checks |
| `repo` | `string` | only for repositories in this directory (empty: every one) |
| `timeout_ms` | `integer` | how long it may take, in milliseconds (default 30 minutes) |

### branch.GateResult

| Field | Type | |
|---|---|---|
| `at` | `string` |  |
| `command` | `[]string` |  |
| `duration_ms` | `integer` |  |
| `error` | `string` |  |
| `exit_code` | `integer` |  |
| `name` | `string` |  |
| `output` | `[]string` |  |
| `passed` | `boolean` |  |
| `skipped` | `boolean` |  |

### branch.ListRequest

| Field | Type | |
|---|---|---|
| `all` | `boolean` | include cleaned and removed branches |
| `repo` | `string` | only this repository's branches (its main working tree) |

### branch.Overlap

| Field | Type | |
|---|---|---|
| `branch` | `string` | the other branch's name, or the base's |
| `conflicts` | `[]string` | files a merge of the two would conflict on |
| `files` | `[]string` | files both change |
| `unknown` | `boolean` | conflicts are unknown: git is too old to compute them |
| `with` | `string` | the other branch's ID; empty when it is the base |

### branch.Owner

| Field | Type | |
|---|---|---|
| `agent` | `string` | the agent's ID |
| `kind` | `string` | the agent's kind |
| `plan` | `string` | the plan of the task it was made for |
| `task` | `string` | the task it was made for |

### branch.PullRequest

| Field | Type | |
|---|---|---|
| `at` | `string` |  |
| `draft` | `boolean` |  |
| `forge` | `string` |  |
| `number` | `integer` |  |
| `url` | `string` |  |

### branch.PullRequestRequest

| Field | Type | |
|---|---|---|
| `base` | `string` | the branch it would merge into (default: the branch's base) |
| `body` | `string` | the description (default: its commits and gates) |
| `draft` | `boolean` | open it as a draft |
| `push` | `boolean` | push the branch first if the remote lacks its commits |
| `ref` | `string` | the branch |
| `remote` | `string` | the remote (default: its upstream's, else origin) |
| `title` | `string` | the title (default: its first commit's subject) |

### branch.Push

| Field | Type | |
|---|---|---|
| `at` | `string` |  |
| `commit` | `string` |  |
| `remote` | `string` |  |

### branch.PushRequest

| Field | Type | |
|---|---|---|
| `ref` | `string` | the branch |
| `remote` | `string` | the remote to push to (default: its upstream's, else origin) |

### branch.ReadyRequest

| Field | Type | |
|---|---|---|
| `gates` | [\[\]branch.Gate](#branchgate) | gates to run instead of the configured ones |
| `ref` | `string` | the branch |

### branch.Ref

| Field | Type | |
|---|---|---|
| `ref` | `string` | the branch: its ID, environment, agent, plan/task or name |

### branch.UpdateRequest

| Field | Type | |
|---|---|---|
| `abort` | `boolean` | undo a merge or rebase stopped on a conflict |
| `rebase` | `boolean` | rebase onto the base instead of merging it |
| `ref` | `string` | the branch |

### branch.UpdateResult

| Field | Type | |
|---|---|---|
| `branch` | [branch.Branch](#branchbranch) |  |
| `conflicts` | `[]string` |  |

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

### git.FileChange

| Field | Type | |
|---|---|---|
| `added` | `integer` |  |
| `deleted` | `integer` |  |
| `from` | `string` |  |
| `path` | `string` |  |
| `status` | `string` |  |

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

### insights.Alert

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `budget` | `string` |  |
| `environment` | `string` |  |
| `held` | `boolean` |  |
| `key` | `string` |  |
| `level` | `string` |  |
| `limit` | [insights.Limits](#insightslimits) |  |
| `message` | `string` |  |
| `per` | `string` |  |
| `time` | `string` |  |
| `used` | [insights.Amounts](#insightsamounts) |  |
| `window` | `string` |  |

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

### insights.AsksParams

| Field | Type | |
|---|---|---|
| `by` | `string` | group rows by kind (default), agent, environment, task, day or model |
| `environment` | `string` | only this environment |
| `from` | `string` | from this time (default: the start of today, local time) |
| `kind` | `string` | only this agent kind |
| `limit` | `integer` | at most this many (default and most 1000) |
| `to` | `string` | until this time (default: now) |

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

### insights.ExportParams

| Field | Type | |
|---|---|---|
| `by` | `string` | group rows by kind (default), agent, environment, task, day or model |
| `environment` | `string` | only this environment |
| `from` | `string` | from this time (default: the start of today, local time) |
| `kind` | `string` | only this agent kind |
| `to` | `string` | until this time (default: now) |
| `what` | `string` | spans, usage or alerts |

### insights.Limits

| Field | Type | |
|---|---|---|
| `cost_usd` | `number` |  |
| `tokens` | `integer` |  |
| `working_ms` | `integer` |  |

### insights.QueryParams

| Field | Type | |
|---|---|---|
| `by` | `string` | group rows by kind (default), agent, environment, task, day or model |
| `environment` | `string` | only this environment |
| `from` | `string` | from this time (default: the start of today, local time) |
| `kind` | `string` | only this agent kind |
| `to` | `string` | until this time (default: now) |

### insights.Record

| Field | Type | |
|---|---|---|
| `alert` | [insights.Alert](#insightsalert) |  |
| `span` | [insights.Span](#insightsspan) |  |
| `type` | `string` |  |
| `usage` | [insights.Usage](#insightsusage) |  |
| `v` | `integer` |  |

### insights.ResumeParams

| Field | Type | |
|---|---|---|
| `budget` | `string` | the budget's name |
| `key` | `string` | one key of it (a plan, environment or kind; default: every held one) |

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

### insights.Span

| Field | Type | |
|---|---|---|
| `agent` | `string` |  |
| `end` | `string` |  |
| `environment` | `string` |  |
| `kind` | `string` |  |
| `name` | `string` |  |
| `pane` | `string` |  |
| `plan` | `string` |  |
| `reason` | `string` |  |
| `start` | `string` |  |
| `state` | `string` |  |
| `task` | `string` |  |

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

### insights.Usage

| Field | Type | |
|---|---|---|
| `agent` | `string` |  |
| `cost_usd` | `number` |  |
| `environment` | `string` |  |
| `kind` | `string` |  |
| `model` | `string` |  |
| `name` | `string` |  |
| `pane` | `string` |  |
| `plan` | `string` |  |
| `session` | `string` |  |
| `source` | `string` |  |
| `task` | `string` |  |
| `time` | `string` |  |
| `tokens` | [insights.Tokens](#insightstokens) |  |

### insights.UsageParams

| Field | Type | |
|---|---|---|
| `cache_read` | `integer` | input tokens read from a cache |
| `cache_write` | `integer` | input tokens written to a cache |
| `cost_usd` | `number` | cost in US dollars, as the agent or provider stated it |
| `id` | `string` | the agent's pane or process ID |
| `input` | `integer` | input tokens (not cached) |
| `model` | `string` | the model the usage is for |
| `output` | `integer` | output tokens |
| `session` | `string` | the agent's session (default: the agent): totals are per session |
| `source` | `string` | where the numbers come from: report (default), transcript, statusline, or a name of the reporter's |
| `total` | `boolean` | the numbers are the session's totals so far (what grew is recorded), not an increment |

### insights.UsageResult

| Field | Type | |
|---|---|---|
| `agent` | [insights.Who](#insightswho) |  |
| `recorded` | [insights.Usage](#insightsusage) |  |

### insights.Who

| Field | Type | |
|---|---|---|
| `agent` | `string` |  |
| `environment` | `string` |  |
| `kind` | `string` |  |
| `name` | `string` |  |
| `pane` | `string` |  |
| `plan` | `string` |  |
| `task` | `string` |  |

### issue.CloseParams

| Field | Type | |
|---|---|---|
| `comment` | `string` | a comment to add as it closes |
| `ref` | `string` | tracker:key |

### issue.CommentParams

| Field | Type | |
|---|---|---|
| `body` | `string` | the comment, as the tracker formats it (Markdown on GitHub) |
| `ref` | `string` | tracker:key |

### issue.Issue

| Field | Type | |
|---|---|---|
| `assignees` | `[]string` |  |
| `body` | `string` |  |
| `key` | `string` | the tracker's own name for it: HIV-42, owner/repo#42 |
| `labels` | `[]string` |  |
| `ref` | `string` | tracker:key, as Hive names it |
| `state` | `string` | open or closed, as the tracker says |
| `title` | `string` |  |
| `tracker` | `string` | the tracker: github, or a plugin's |
| `updated_at` | `string` |  |
| `url` | `string` |  |

### issue.RefParams

| Field | Type | |
|---|---|---|
| `ref` | `string` | tracker:key (linear:HIV-42, github:owner/repo#42) |

### issue.SearchParams

| Field | Type | |
|---|---|---|
| `limit` | `integer` | at most this many (default 20, at most 100) |
| `query` | `string` | what to search for, in the tracker's own syntax |
| `tracker` | `string` | only this tracker (default: every one) |

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

### observer.ActiveObserver

| Field | Type | |
|---|---|---|
| `connections` | `integer` |  |
| `id` | `string` |  |
| `name` | `string` |  |

### observer.Activity

| Field | Type | |
|---|---|---|
| `observers` | [\[\]observer.ActiveObserver](#observeractiveobserver) |  |
| `watched` | `map[string]integer` |  |

### observer.AuditEntry

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `client` | `string` |  |
| `credential` | `string` |  |
| `detail` | `string` |  |
| `name` | `string` |  |
| `time` | `string` |  |

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

### observer.Scope

| Field | Type | |
|---|---|---|
| `all` | `boolean` |  |
| `environments` | `[]string` |  |
| `panes` | `[]string` |  |

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
| `view` | `string` |  |
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
| `view` | `string` |  |

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
| `async` | `boolean` |  |
| `command` | `[]string` |  |
| `id` | `string` |  |
| `timeout` | `string` |  |
| `title` | `string` |  |

### plugin.ActionStats

| Field | Type | |
|---|---|---|
| `canceled` | `integer` |  |
| `failed` | `integer` |  |
| `run` | `integer` |  |
| `timed_out` | `integer` |  |

### plugin.CapabilityInfo

| Field | Type | |
|---|---|---|
| `description` | `string` |  |
| `enforced` | `string` |  |
| `name` | `string` |  |

### plugin.Card

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `scope` | `string` |  |
| `title` | `string` |  |

### plugin.CardContent

| Field | Type | |
|---|---|---|
| `card` | `string` |  |
| `environment` | `string` |  |
| `links` | [\[\]plugin.Link](#pluginlink) |  |
| `pane` | `string` |  |
| `plugin` | `string` |  |
| `rows` | [\[\]plugin.CardRow](#plugincardrow) |  |
| `title` | `string` |  |
| `until` | `string` |  |

### plugin.CardRow

| Field | Type | |
|---|---|---|
| `label` | `string` |  |
| `text` | `string` |  |
| `tone` | `string` |  |

### plugin.CardUpdate

| Field | Type | |
|---|---|---|
| `card` | `string` |  |
| `environment` | `string` |  |
| `links` | [\[\]plugin.Link](#pluginlink) |  |
| `pane` | `string` |  |
| `plugin` | `string` |  |
| `rows` | [\[\]plugin.CardRow](#plugincardrow) |  |
| `ttl` | `integer` | nanoseconds |

### plugin.ConfigView

| Field | Type | |
|---|---|---|
| `error` | `string` |  |
| `name` | `string` |  |
| `path` | `string` |  |
| `plugin` | `string` |  |
| `schema_version` | `integer` |  |
| `settings` | [\[\]plugin.Setting](#pluginsetting) |  |
| `values` | `map[string]any` |  |
| `warnings` | `[]string` |  |

### plugin.Configuration

| Field | Type | |
|---|---|---|
| `directory` | `string` |  |
| `settings` | [\[\]plugin.Setting](#pluginsetting) |  |
| `version` | `integer` |  |

### plugin.ContextAction

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `contexts` | `[]string` |  |
| `title` | `string` |  |

### plugin.Contributes

| Field | Type | |
|---|---|---|
| `actions` | [\[\]plugin.ContextAction](#plugincontextaction) |  |
| `cards` | [\[\]plugin.Card](#plugincard) |  |
| `forges` | [\[\]plugin.ForgeDecl](#pluginforgedecl) |  |
| `links` | [\[\]plugin.Link](#pluginlink) |  |
| `palette` | [\[\]plugin.PaletteEntry](#pluginpaletteentry) |  |
| `status` | [\[\]plugin.StatusIndicator](#pluginstatusindicator) |  |
| `steps` | [\[\]plugin.StepDecl](#pluginstepdecl) |  |
| `trackers` | [\[\]plugin.TrackerDecl](#plugintrackerdecl) |  |
| `triggers` | [\[\]plugin.TriggerDecl](#plugintriggerdecl) |  |
| `views` | [\[\]plugin.ViewDecl](#pluginviewdecl) |  |

### plugin.Contribution

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `available` | `boolean` |  |
| `contexts` | `[]string` |  |
| `hidden` | `boolean` |  |
| `id` | `string` |  |
| `key` | `string` |  |
| `kind` | `string` |  |
| `plugin` | `string` |  |
| `scope` | `string` |  |
| `title` | `string` |  |
| `url` | `string` |  |

### plugin.Contributions

| Field | Type | |
|---|---|---|
| `cards` | [\[\]plugin.CardContent](#plugincardcontent) |  |
| `declared` | [\[\]plugin.Contribution](#plugincontribution) |  |
| `status` | [\[\]plugin.StatusContent](#pluginstatuscontent) |  |

### plugin.Discovery

| Field | Type | |
|---|---|---|
| `api` | `integer` |  |
| `capabilities` | [\[\]plugin.CapabilityInfo](#plugincapabilityinfo) |  |
| `contributions` | `[]string` |  |
| `events` | `[]string` |  |
| `granted` | `[]string` |  |
| `hive_version` | `string` |  |
| `isolation` | `[]string` |  |
| `methods` | `[]string` |  |
| `plugin` | `string` |  |
| `supported_apis` | `[]integer` |  |

### plugin.Event

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `on` | `string` |  |
| `timeout` | `string` |  |

### plugin.EventStats

| Field | Type | |
|---|---|---|
| `delivered` | `integer` |  |
| `dropped` | `integer` |  |
| `failed` | `integer` |  |
| `queued` | `integer` |  |

### plugin.ForgeDecl

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `hosts` | `[]string` |  |
| `name` | `string` |  |

### plugin.Grant

| Field | Type | |
|---|---|---|
| `denied` | `[]string` |  |
| `effective` | `[]string` |  |
| `missing` | `[]string` |  |
| `ungranted` | `[]string` |  |

### plugin.Health

| Field | Type | |
|---|---|---|
| `actions` | [plugin.ActionStats](#pluginactionstats) |  |
| `crashes` | `integer` |  |
| `events` | [plugin.EventStats](#plugineventstats) |  |
| `last_error` | `string` |  |
| `last_error_at` | `string` |  |
| `loaded_at` | `string` |  |
| `ready_at` | `string` |  |
| `requests` | [\[\]plugin.PermissionRequest](#pluginpermissionrequest) |  |
| `restarts` | `integer` |  |
| `since` | `string` |  |
| `state` | `string` |  |
| `warnings` | `[]string` |  |

### plugin.Link

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `title` | `string` |  |
| `url` | `string` |  |

### plugin.Manifest

| Field | Type | |
|---|---|---|
| `actions` | [\[\]plugin.Action](#pluginaction) |  |
| `api` | `integer` |  |
| `configuration` | [plugin.Configuration](#pluginconfiguration) |  |
| `contributes` | [plugin.Contributes](#plugincontributes) |  |
| `data_directory` | `string` |  |
| `description` | `string` |  |
| `event_concurrency` | `integer` |  |
| `events` | [\[\]plugin.Event](#pluginevent) |  |
| `id` | `string` |  |
| `isolation` | `string` |  |
| `isolation_reason` | `string` |  |
| `max_hive_version` | `string` |  |
| `migrations` | [\[\]plugin.Migration](#pluginmigration) |  |
| `min_hive_version` | `string` |  |
| `name` | `string` |  |
| `permissions` | [plugin.Permissions](#pluginpermissions) |  |
| `platforms` | `[]string` |  |
| `runtime` | [\[\]plugin.Runtime](#pluginruntime) |  |
| `startup` | [\[\]plugin.Startup](#pluginstartup) |  |
| `test` | [plugin.TestSpec](#plugintestspec) |  |
| `version` | `string` |  |

### plugin.Migration

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `from` | `integer` |  |
| `to` | `integer` |  |

### plugin.NetworkScope

| Field | Type | |
|---|---|---|
| `hosts` | `[]string` |  |

### plugin.PaletteEntry

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `title` | `string` |  |

### plugin.ParamSpec

| Field | Type | |
|---|---|---|
| `default` | `any` |  |
| `description` | `string` |  |
| `required` | `boolean` |  |
| `type` | `string` |  |

### plugin.PermissionRequest

| Field | Type | |
|---|---|---|
| `at` | `string` |  |
| `capability` | `string` |  |
| `plugin` | `string` |  |
| `reason` | `string` |  |

### plugin.Permissions

| Field | Type | |
|---|---|---|
| `network` | [plugin.NetworkScope](#pluginnetworkscope) |  |
| `optional` | `[]string` |  |
| `process` | [plugin.ProcessScope](#pluginprocessscope) |  |
| `reasons` | `map[string]string` |  |
| `required` | `[]string` |  |

### plugin.ProcessScope

| Field | Type | |
|---|---|---|
| `exec` | `[]string` |  |

### plugin.Provenance

| Field | Type | |
|---|---|---|
| `attestation` | `string` |  |
| `builder` | `string` |  |
| `repository` | `string` |  |
| `run` | `string` |  |
| `workflow` | `string` |  |

### plugin.Result

| Field | Type | |
|---|---|---|
| `async` | `boolean` |  |
| `exit_code` | `integer` |  |
| `output` | `string` |  |
| `run` | `string` |  |

### plugin.Revocation

| Field | Type | |
|---|---|---|
| `advisory` | `string` |  |
| `id` | `string` |  |
| `reason` | `string` |  |
| `revoked_at` | `string` |  |
| `version` | `string` |  |

### plugin.RunInfo

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `async` | `boolean` |  |
| `id` | `string` |  |
| `started` | `string` |  |

### plugin.Runtime

| Field | Type | |
|---|---|---|
| `command` | `string` |  |
| `kind` | `string` |  |
| `min_version` | `string` |  |

### plugin.Setting

| Field | Type | |
|---|---|---|
| `default` | `any` |  |
| `description` | `string` |  |
| `name` | `string` |  |
| `required` | `boolean` |  |
| `type` | `string` |  |

### plugin.Startup

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `restart` | `boolean` |  |

### plugin.Status

| Field | Type | |
|---|---|---|
| `api` | `integer` |  |
| `checked_at` | `string` |  |
| `checksum` | `string` |  |
| `commit` | `string` |  |
| `dir` | `string` |  |
| `enabled` | `boolean` |  |
| `grant` | [plugin.Grant](#plugingrant) |  |
| `granted` | `[]string` |  |
| `health` | [plugin.Health](#pluginhealth) |  |
| `id` | `string` |  |
| `installed_at` | `string` |  |
| `isolation` | `string` |  |
| `last_error` | `string` |  |
| `linked` | `boolean` |  |
| `manifest` | [plugin.Manifest](#pluginmanifest) |  |
| `previous_checksum` | `string` |  |
| `previous_commit` | `string` |  |
| `previous_ref` | `string` |  |
| `previous_trust` | [plugin.Trust](#plugintrust) |  |
| `previous_version` | `string` |  |
| `problem` | `string` |  |
| `provenance` | [plugin.Provenance](#pluginprovenance) |  |
| `publisher` | `string` |  |
| `ref` | `string` |  |
| `registry` | `boolean` |  |
| `revoked` | [plugin.Revocation](#pluginrevocation) |  |
| `running` | `integer` |  |
| `runs` | [\[\]plugin.RunInfo](#pluginruninfo) |  |
| `sandbox` | `string` |  |
| `signed_by` | `string` |  |
| `source` | `string` |  |
| `tier` | `string` |  |
| `version` | `string` |  |

### plugin.StatusContent

| Field | Type | |
|---|---|---|
| `indicator` | `string` |  |
| `plugin` | `string` |  |
| `text` | `string` |  |
| `tone` | `string` |  |
| `until` | `string` |  |

### plugin.StatusIndicator

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `title` | `string` |  |

### plugin.StatusUpdate

| Field | Type | |
|---|---|---|
| `indicator` | `string` |  |
| `plugin` | `string` |  |
| `text` | `string` |  |
| `tone` | `string` |  |
| `ttl` | `integer` | nanoseconds |

### plugin.StepDecl

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `description` | `string` |  |
| `inputs` | [map[string]plugin.ParamSpec](#pluginparamspec) |  |
| `name` | `string` |  |
| `outputs` | [map[string]plugin.ParamSpec](#pluginparamspec) |  |
| `timeout` | `string` |  |

### plugin.TestSpec

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `timeout` | `string` |  |

### plugin.TrackerDecl

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `name` | `string` |  |

### plugin.TriggerDecl

| Field | Type | |
|---|---|---|
| `description` | `string` |  |
| `name` | `string` |  |

### plugin.Trust

| Field | Type | |
|---|---|---|
| `checked_at` | `string` |  |
| `provenance` | [plugin.Provenance](#pluginprovenance) |  |
| `publisher` | `string` |  |
| `revoked` | [plugin.Revocation](#pluginrevocation) |  |
| `signed_by` | `string` |  |
| `tier` | `string` |  |

### plugin.ViewClick

| Field | Type | |
|---|---|---|
| `environment` | `string` |  |
| `node` | `string` |  |
| `pane` | `string` |  |
| `plugin` | `string` |  |
| `tab` | `string` |  |
| `view` | `string` |  |

### plugin.ViewContent

| Field | Type | |
|---|---|---|
| `available` | `boolean` |  |
| `doc` | [plugin.ViewDoc](#pluginviewdoc) |  |
| `plugin` | `string` |  |
| `title` | `string` |  |
| `updated` | `string` |  |
| `view` | `string` |  |

### plugin.ViewDecl

| Field | Type | |
|---|---|---|
| `command` | `[]string` |  |
| `id` | `string` |  |
| `title` | `string` |  |

### plugin.ViewDoc

| Field | Type | |
|---|---|---|
| `root` | [plugin.ViewNode](#pluginviewnode) |  |

### plugin.ViewNode

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `children` | [\[\]plugin.ViewNode](#pluginviewnode) |  |
| `columns` | `[]string` |  |
| `id` | `string` |  |
| `kind` | `string` |  |
| `rows` | `[][]string` |  |
| `style` | `string` |  |
| `text` | `string` |  |
| `url` | `string` |  |
| `value` | `number` |  |

### plugin.ViewUpdate

| Field | Type | |
|---|---|---|
| `doc` | [plugin.ViewDoc](#pluginviewdoc) |  |
| `plugin` | `string` |  |
| `view` | `string` |  |

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
| `log_dir` | `string` |  |
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

### runtime.AccessDoctor

| Field | Type | |
|---|---|---|
| `authorized_keys` | `string` |  |
| `fixed` | `boolean` |  |
| `problems` | `[]string` |  |

### runtime.AccessInviteParams

| Field | Type | |
|---|---|---|
| `grants` | [\[\]access.Grant](#accessgrant) |  |
| `key` | `string` |  |
| `name` | `string` |  |
| `print` | `boolean` |  |
| `ttl_seconds` | `integer` |  |

### runtime.AccessInvited

| Field | Type | |
|---|---|---|
| `authorized_keys` | `string` |  |
| `line` | `string` |  |
| `member` | [access.Member](#accessmember) |  |
| `socket` | `string` |  |

### runtime.AccessList

| Field | Type | |
|---|---|---|
| `authorized_keys` | `string` |  |
| `enabled` | `boolean` |  |
| `members` | [\[\]runtime.AccessMember](#runtimeaccessmember) |  |
| `socket` | `string` |  |

### runtime.AccessMember

| Field | Type | |
|---|---|---|
| `connections` | `integer` |  |
| `created_at` | `string` |  |
| `expires_at` | `string` |  |
| `fingerprint` | `string` |  |
| `grants` | [\[\]access.Grant](#accessgrant) |  |
| `id` | `string` |  |
| `key` | `string` |  |
| `last_seen_at` | `string` |  |
| `managed` | `boolean` |  |
| `name` | `string` |  |
| `revoked_at` | `string` |  |
| `state` | `string` |  |

### runtime.AgentStartRequest

| Field | Type | |
|---|---|---|
| `args` | `[]string` |  |
| `base` | `string` |  |
| `env` | `map[string]string` |  |
| `environment_id` | `string` |  |
| `isolate` | `boolean` |  |
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

### runtime.ObserverCreateParams

| Field | Type | |
|---|---|---|
| `name` | `string` |  |
| `scope` | [observer.Scope](#observerscope) |  |
| `screens` | `boolean` |  |
| `ttl_seconds` | `integer` |  |

### runtime.ObserverCreated

| Field | Type | |
|---|---|---|
| `credential` | [observer.Credential](#observercredential) |  |
| `socket` | `string` |  |
| `token` | `string` |  |

### runtime.ObserverInfo

| Field | Type | |
|---|---|---|
| `connections` | `integer` |  |
| `created_at` | `string` |  |
| `expires_at` | `string` |  |
| `id` | `string` |  |
| `last_used_at` | `string` |  |
| `name` | `string` |  |
| `revoked_at` | `string` |  |
| `scope` | [observer.Scope](#observerscope) |  |
| `screens` | `boolean` |  |
| `state` | `string` |  |

### runtime.ObserverList

| Field | Type | |
|---|---|---|
| `credentials` | [\[\]runtime.ObserverInfo](#runtimeobserverinfo) |  |
| `enabled` | `boolean` |  |
| `socket` | `string` |  |

### runtime.ObserverValidation

| Field | Type | |
|---|---|---|
| `credential` | [observer.Credential](#observercredential) |  |
| `state` | `string` |  |
| `valid` | `boolean` |  |

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

### runtime.WhoAmI

| Field | Type | |
|---|---|---|
| `from` | `string` |  |
| `member` | [access.Member](#accessmember) |  |
| `owner` | `boolean` |  |

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

### runtime.accessAuditParams

| Field | Type | |
|---|---|---|
| `limit` | `integer` |  |
| `member` | `string` |  |

### runtime.accessDoctorParams

| Field | Type | |
|---|---|---|
| `fix` | `boolean` |  |

### runtime.accessGrantParams

| Field | Type | |
|---|---|---|
| `environment` | `string` |  |
| `member` | `string` |  |
| `role` | `string` | observer, operator, owner, or none to take the role away |

### runtime.accessRef

| Field | Type | |
|---|---|---|
| `member` | `string` |  |

### runtime.accessRenewParams

| Field | Type | |
|---|---|---|
| `member` | `string` |  |
| `ttl_seconds` | `integer` |  |

### runtime.agentIDParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |

### runtime.agentRenameParams

| Field | Type | |
|---|---|---|
| `id` | `string` |  |
| `name` | `string` |  |

### runtime.observerAuditParams

| Field | Type | |
|---|---|---|
| `credential` | `string` |  |
| `limit` | `integer` |  |

### runtime.observerRef

| Field | Type | |
|---|---|---|
| `id` | `string` |  |

### runtime.observerValidateParams

| Field | Type | |
|---|---|---|
| `token` | `string` |  |

### runtime.pluginConfigSet

| Field | Type | |
|---|---|---|
| `plugin` | `string` |  |
| `set` | `map[string]any` |  |
| `unset` | `[]string` |  |

### runtime.pluginParams

| Field | Type | |
|---|---|---|
| `plugin` | `string` |  |

### runtime.pluginRequestParams

| Field | Type | |
|---|---|---|
| `capability` | `string` |  |
| `plugin` | `string` |  |
| `reason` | `string` |  |

### runtime.pluginRunRef

| Field | Type | |
|---|---|---|
| `plugin` | `string` |  |
| `run` | `string` |  |

### runtime.workflowTriggerParams

| Field | Type | |
|---|---|---|
| `name` | `string` | the trigger's name, as the plugin's [[contributes.triggers]] says |
| `payload` | `map[string]any` | what the workflows' match filters, and expressions read as trigger.data |
| `plugin` | `string` | the plugin whose trigger fires (a plugin's own call says it) |

### runtime.workflowTriggerResult

| Field | Type | |
|---|---|---|
| `runs` | `[]string` |  |

### runtime.worktreeListParams

| Field | Type | |
|---|---|---|
| `repo` | `string` |  |

### task.Approval

| Field | Type | |
|---|---|---|
| `approved` | `boolean` |  |
| `by` | `string` |  |
| `decided_at` | `string` |  |
| `note` | `string` |  |
| `requested_at` | `string` |  |

### task.ApproveParams

| Field | Type | |
|---|---|---|
| `by` | `string` | who decides, for the task's log |
| `deny` | `boolean` | refuse: cancel the task instead of starting it |
| `id` | `string` | the plan's ID, or a unique prefix of it |
| `note` | `string` | why, for the task's log |
| `task` | `string` | the task (may be left out when one task waits) |

### task.LogEntry

| Field | Type | |
|---|---|---|
| `message` | `string` |  |
| `time` | `string` |  |

### task.Plan

| Field | Type | |
|---|---|---|
| `cancelled` | `boolean` |  |
| `created_at` | `string` |  |
| `ended_at` | `string` |  |
| `id` | `string` |  |
| `kinds` | `map[string]integer` |  |
| `max_parallel` | `integer` |  |
| `name` | `string` |  |
| `reason` | `string` |  |
| `rev` | `integer` |  |
| `schema` | `integer` |  |
| `state` | `string` |  |
| `tasks` | [\[\]task.Task](#tasktask) |  |
| `timeout_ms` | `integer` |  |

### task.PlanSpec

| Field | Type | |
|---|---|---|
| `defaults` | [task.Spec](#taskspec) | values for what a task leaves out (id, prompt and needs are never taken from here) |
| `kinds` | `map[string]integer` | per agent kind, how many of its tasks may run at once |
| `max_parallel` | `integer` | how many of its tasks may run at once (0: as many as the machines allow) |
| `name` | `string` | what the plan is for |
| `tasks` | [\[\]task.Spec](#taskspec) | the tasks, in any order; needs orders them |
| `timeout_ms` | `integer` | the plan's time budget in milliseconds; unfinished tasks are cancelled when it runs out (0: none) |

### task.Ref

| Field | Type | |
|---|---|---|
| `id` | `string` | the plan's ID, or a unique prefix of it |
| `task` | `string` | one of its tasks |

### task.Result

| Field | Type | |
|---|---|---|
| `changed` | `[]string` |  |
| `changed_more` | `integer` |  |
| `exit_code` | `integer` |  |
| `outcome` | `string` |  |
| `output` | `[]string` |  |
| `screen` | `[]string` |  |
| `summary` | `string` |  |

### task.RunParams

| Field | Type | |
|---|---|---|
| `plan` | [task.PlanSpec](#taskplanspec) | the plan, as data |
| `source` | `string` | the plan file's name, for errors |
| `toml` | `string` | the plan, as the text of a plan file (instead of plan) |

### task.Spec

| Field | Type | |
|---|---|---|
| `approve` | `boolean` | wait for a person's approval before starting |
| `args` | `[]string` | extra command-line arguments for the agent |
| `base` | `string` | where a new worktree branch starts (default HEAD) |
| `close_issue` | `boolean` | close the issue when the task's branch merges |
| `command` | `[]string` | with until=command: the command whose exit status decides |
| `env` | `map[string]string` | extra environment variables for the agent |
| `environment_id` | `string` | the environment to run in, or to make a worktree of |
| `id` | `string` | the task's name within its plan (letters, digits, '.', '_', '-') |
| `isolate` | `boolean` | run in a new worktree on hive/<plan>/<task> unless worktree names a branch |
| `issue` | `string` | the issue it works on, as tracker:key (linear:HIV-42, github:owner/repo#42) |
| `keep_agent` | `boolean` | leave the agent running when the task ends |
| `kind` | `string` | the agent: claude, codex, … (see agent.manifests) |
| `labels` | `[]string` | run on a machine that has all of these labels |
| `machine` | `string` | the saved machine to run on (local: this one) |
| `match` | `string` | with until=match: a regular expression the agent's screen must match |
| `needs` | `[]string` | tasks that must succeed first |
| `no_summary` | `boolean` | do not ask the agent to write a summary |
| `prompt` | `string` | what the agent is asked to do |
| `retries` | `integer` | how many times a failed attempt is retried automatically |
| `timeout_ms` | `integer` | how long the agent may work on it, in milliseconds (0: no bound) |
| `title` | `string` | a one-line description |
| `until` | `string` | done (default), idle, match or command |
| `worktree` | `string` | a branch: run in a new worktree on it |

### task.Summary

| Field | Type | |
|---|---|---|
| `attention` | [\[\]task.TaskView](#tasktaskview) |  |
| `counts` | `map[string]integer` |  |
| `created_at` | `string` |  |
| `ended_at` | `string` |  |
| `id` | `string` |  |
| `name` | `string` |  |
| `reason` | `string` |  |
| `rev` | `integer` |  |
| `state` | `string` |  |
| `total` | `integer` |  |

### task.Task

| Field | Type | |
|---|---|---|
| `agent_id` | `string` |  |
| `approval` | [task.Approval](#taskapproval) |  |
| `attempt` | `integer` |  |
| `base_commit` | `string` |  |
| `ended_at` | `string` |  |
| `environment_id` | `string` |  |
| `log` | [\[\]task.LogEntry](#tasklogentry) |  |
| `machine` | `string` |  |
| `pane_id` | `string` |  |
| `path` | `string` |  |
| `phase` | `string` |  |
| `ready_at` | `string` |  |
| `reason` | `string` |  |
| `remote` | `string` |  |
| `result` | [task.Result](#taskresult) |  |
| `retried` | `integer` |  |
| `spec` | [task.Spec](#taskspec) |  |
| `started_at` | `string` |  |
| `state` | `string` |  |
| `worked_ms` | `integer` |  |

### task.TaskView

| Field | Type | |
|---|---|---|
| `agent_id` | `string` |  |
| `attempt` | `integer` |  |
| `ended_at` | `string` |  |
| `environment` | `string` |  |
| `id` | `string` |  |
| `kind` | `string` |  |
| `machine` | `string` |  |
| `needs` | `[]string` |  |
| `pane_id` | `string` |  |
| `plan_id` | `string` |  |
| `reason` | `string` |  |
| `rev` | `integer` |  |
| `started_at` | `string` |  |
| `state` | `string` |  |
| `title` | `string` |  |

### task.WaitParams

| Field | Type | |
|---|---|---|
| `after` | `integer` | with until=change: the plan revision already seen |
| `id` | `string` | the plan's ID, or a unique prefix of it |
| `task` | `string` | wait for this task rather than the whole plan |
| `timeout_ms` | `integer` | give up after this many milliseconds (0: never) |
| `until` | `string` | finished (default), attention (also a task waiting for a person) or change |

### task.WaitResult

| Field | Type | |
|---|---|---|
| `outcome` | `string` |  |
| `plan` | [task.Plan](#taskplan) |  |

### terminal.ImageReceipt

| Field | Type | |
|---|---|---|
| `delivery` | `string` |  |
| `done` | `boolean` |  |
| `path` | `string` |  |

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

### terminal.imageParams

| Field | Type | |
|---|---|---|
| `format` | `string` |  |
| `process_id` | `string` |  |
| `sha256` | `string` |  |
| `size` | `integer` |  |
| `transfer_id` | `string` |  |
| `view_id` | `string` |  |

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

### workflow.FireParams

| Field | Type | |
|---|---|---|
| `data` | `any` | the payload, which expressions read as trigger.data |
| `webhook` | `string` | the webhook's name, as workflows' [[on]] webhook says |

### workflow.InputSpec

| Field | Type | |
|---|---|---|
| `default` | `string` |  |
| `description` | `string` |  |
| `name` | `string` |  |
| `required` | `boolean` |  |

### workflow.LogEntry

| Field | Type | |
|---|---|---|
| `message` | `string` |  |
| `time` | `string` |  |

### workflow.NameParams

| Field | Type | |
|---|---|---|
| `name` | `string` | the workflow's name (hive workflow list) |

### workflow.Repeat

| Field | Type | |
|---|---|---|
| `max` | `integer` |  |
| `until` | `string` |  |

### workflow.Resolved

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `dir` | `string` |  |
| `env` | `map[string]string` |  |
| `environment_id` | `string` |  |
| `kind` | `string` |  |
| `prompt` | `string` |  |
| `run` | `[]string` |  |
| `shell` | `string` |  |
| `uses` | `string` |  |
| `with` | `map[string]string` |  |
| `worktree` | `string` |  |

### workflow.Result

| Field | Type | |
|---|---|---|
| `attempt` | `integer` |  |
| `changed` | `[]string` |  |
| `changed_more` | `integer` |  |
| `ended_at` | `string` |  |
| `exit_code` | `integer` |  |
| `index` | `integer` |  |
| `item` | `string` |  |
| `kind` | `string` |  |
| `outcome` | `string` |  |
| `output` | `[]string` |  |
| `outputs` | `map[string]string` |  |
| `plan` | `string` |  |
| `reason` | `string` |  |
| `resolved` | [workflow.Resolved](#workflowresolved) |  |
| `screen` | `[]string` |  |
| `started_at` | `string` |  |
| `summary` | `string` |  |

### workflow.Run

| Field | Type | |
|---|---|---|
| `cancelled` | `boolean` |  |
| `created_at` | `string` |  |
| `digest` | `string` |  |
| `dir` | `string` |  |
| `ended_at` | `string` |  |
| `id` | `string` |  |
| `inputs` | `map[string]string` |  |
| `outputs` | `map[string]string` |  |
| `path` | `string` |  |
| `reason` | `string` |  |
| `rev` | `integer` |  |
| `schema` | `integer` |  |
| `source` | `string` |  |
| `spec` | [workflow.Spec](#workflowspec) |  |
| `started_at` | `string` |  |
| `state` | `string` |  |
| `steps` | [\[\]workflow.StepRun](#workflowsteprun) |  |
| `trigger` | [workflow.Trigger](#workflowtrigger) |  |
| `workflow` | `string` |  |

### workflow.RunParams

| Field | Type | |
|---|---|---|
| `by` | `string` | who asked, for the run's record |
| `dir` | `string` | the directory steps run in unless they name an environment (absolute) |
| `inputs` | `map[string]string` | the workflow's inputs, by name |
| `name` | `string` | a known workflow's name |
| `source` | `string` | the file's name, for errors |
| `toml` | `string` | the text of a workflow file (instead of name) |

### workflow.RunRef

| Field | Type | |
|---|---|---|
| `by` | `string` | who asks, for a rerun's record |
| `id` | `string` | the run's ID, or a unique prefix of it |

### workflow.RunSummary

| Field | Type | |
|---|---|---|
| `counts` | `map[string]integer` |  |
| `created_at` | `string` |  |
| `current` | `[]string` |  |
| `ended_at` | `string` |  |
| `id` | `string` |  |
| `reason` | `string` |  |
| `rev` | `integer` |  |
| `started_at` | `string` |  |
| `state` | `string` |  |
| `total` | `integer` |  |
| `trigger` | `string` |  |
| `workflow` | `string` |  |

### workflow.RunsParams

| Field | Type | |
|---|---|---|
| `limit` | `integer` | at most this many (0: every one kept) |
| `workflow` | `string` | only this workflow's runs |

### workflow.Spec

| Field | Type | |
|---|---|---|
| `concurrency` | `string` |  |
| `description` | `string` |  |
| `environment` | `string` |  |
| `inputs` | [\[\]workflow.InputSpec](#workflowinputspec) |  |
| `name` | `string` |  |
| `outputs` | `map[string]string` |  |
| `steps` | [\[\]workflow.StepSpec](#workflowstepspec) |  |
| `timeout_ms` | `integer` |  |
| `triggers` | [\[\]workflow.TriggerSpec](#workflowtriggerspec) |  |
| `version` | `integer` |  |

### workflow.StepRun

| Field | Type | |
|---|---|---|
| `attempt` | `integer` |  |
| `attempts` | `integer` |  |
| `current` | [workflow.Result](#workflowresult) |  |
| `ended_at` | `string` |  |
| `id` | `string` |  |
| `items` | `[]string` |  |
| `iteration` | `integer` |  |
| `kind` | `string` |  |
| `log` | [\[\]workflow.LogEntry](#workflowlogentry) |  |
| `next_at` | `string` |  |
| `reason` | `string` |  |
| `results` | [\[\]workflow.Result](#workflowresult) |  |
| `started_at` | `string` |  |
| `state` | `string` |  |

### workflow.StepSpec

| Field | Type | |
|---|---|---|
| `action` | `string` |  |
| `agent` | `[]string` |  |
| `backoff_ms` | `integer` |  |
| `base` | `string` |  |
| `check` | `[]string` |  |
| `dir` | `string` |  |
| `env` | `map[string]string` |  |
| `environment` | `string` |  |
| `foreach` | `[]string` |  |
| `foreach_expr` | `string` |  |
| `id` | `string` |  |
| `if` | `string` |  |
| `isolate` | `boolean` |  |
| `keep_agent` | `boolean` |  |
| `labels` | `[]string` |  |
| `machine` | `string` |  |
| `match` | `string` |  |
| `name` | `string` |  |
| `needs` | `[]string` |  |
| `no_summary` | `boolean` |  |
| `on_failure` | `string` |  |
| `prompt` | `string` |  |
| `repeat` | [workflow.Repeat](#workflowrepeat) |  |
| `retries` | `integer` |  |
| `run` | `[]string` |  |
| `shell` | `string` |  |
| `timeout_ms` | `integer` |  |
| `until` | `string` |  |
| `uses` | `string` |  |
| `with` | `map[string]string` |  |
| `worktree` | `string` |  |

### workflow.Trigger

| Field | Type | |
|---|---|---|
| `by` | `string` |  |
| `data` | `map[string]any` |  |
| `from` | `string` |  |
| `index` | `integer` |  |
| `time` | `string` |  |
| `type` | `string` |  |

### workflow.TriggerInfo

| Field | Type | |
|---|---|---|
| `description` | `string` |  |
| `type` | `string` |  |

### workflow.TriggerSpec

| Field | Type | |
|---|---|---|
| `branches` | `[]string` |  |
| `event` | `string` |  |
| `every_ms` | `integer` |  |
| `git` | `string` |  |
| `inputs` | `map[string]string` |  |
| `match` | `map[string]string` |  |
| `plugin` | `string` |  |
| `remote` | `string` |  |
| `repo` | `string` |  |
| `schedule` | `string` |  |
| `webhook` | `string` |  |

### workflow.TrustParams

| Field | Type | |
|---|---|---|
| `name` | `string` | the workflow's name |
| `revoke` | `boolean` | stop its triggers instead |

### workflow.ValidateParams

| Field | Type | |
|---|---|---|
| `source` | `string` | the file's name, for errors (and the workflow's name when it gives none) |
| `toml` | `string` | the text of a workflow file |

### workflow.WaitParams

| Field | Type | |
|---|---|---|
| `id` | `string` | the run's ID, or a unique prefix of it |
| `timeout_ms` | `integer` | give up after this many milliseconds (0: never) |

### workflow.WaitResult

| Field | Type | |
|---|---|---|
| `outcome` | `string` |  |
| `run` | [workflow.Run](#workflowrun) |  |

### workflow.Workflow

| Field | Type | |
|---|---|---|
| `description` | `string` |  |
| `digest` | `string` |  |
| `error` | `string` |  |
| `inputs` | [\[\]workflow.InputSpec](#workflowinputspec) |  |
| `last` | [workflow.RunSummary](#workflowrunsummary) |  |
| `name` | `string` |  |
| `next` | `string` |  |
| `own` | `boolean` |  |
| `path` | `string` |  |
| `problems` | `[]string` |  |
| `spec` | [workflow.Spec](#workflowspec) |  |
| `steps` | `integer` |  |
| `text` | `string` |  |
| `triggers` | [\[\]workflow.TriggerInfo](#workflowtriggerinfo) |  |
| `trusted` | `boolean` |  |
