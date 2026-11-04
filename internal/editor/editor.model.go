// Package editor is the editor bridge: `hive editor serve`, which every
// editor extension (VS Code, the JetBrains IDEs, Neovim, Zed) speaks, and
// `hive editor lsp`, the same bridge as a language server (M31, RFC 0010).
//
// The bridge is a client of the daemon, over pkg/hiveapi, and the editor's
// server: JSON-RPC 2.0 on standard input and output. It does in one place
// what every extension would otherwise do itself: order agents with the
// blocked ones first, turn a selection or a diagnostic into a prompt with
// its path and lines, draw terminals as the desktop app does (termview),
// find path:line links in them, parse a branch's diff into hunks with
// their IDs, read gate output as diagnostics, and say which files agents
// are changing. Extensions stay thin, and all of them behave alike.
//
// The protocol is frozen in api/v1/editor.txt: version 1 may gain
// methods, notifications and fields, never lose or change them.
package editor

import (
	"time"

	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/termview"
)

// ProtocolVersion is the editor protocol's version, which initialize
// returns and extensions check.
const ProtocolVersion = 1

// Notifications the bridge sends.
const (
	NotifyAgentChanged   = "agent.changed"
	NotifyTaskChanged    = "task.changed"
	NotifyBranchChanged  = "branch.changed"
	NotifyFileChanged    = "file.changed"
	NotifyTerminalScreen = "terminal.screen"
	NotifyTerminalEnded  = "terminal.ended"
	NotifyNotify         = "notify"
	NotifyDaemonState    = "daemon.state"
)

// Notifications lists every notification, for the frozen protocol.
var Notifications = []string{
	NotifyAgentChanged, NotifyTaskChanged, NotifyBranchChanged, NotifyFileChanged,
	NotifyTerminalScreen, NotifyTerminalEnded, NotifyNotify, NotifyDaemonState,
}

// Features a daemon may be too old for (InitializeResult.Missing).
const (
	FeatureFiles  = "files"  // agent.files: API level 19
	FeatureHunks  = "hunks"  // branch.hunk: API level 19
	FeatureReview = "review" // branch.*: API level 13
)

// Bounds.
const (
	// MaxAttachmentBytes bounds the attachments of one prompt, in all.
	MaxAttachmentBytes = 256 << 10
	// MaxFileBytes bounds review.file.
	MaxFileBytes = 4 << 20
	// MaxViews bounds the terminals one editor has open.
	MaxViews = 32
	// FrameBudget paces terminal.screen per view, as the desktop app does.
	FrameBudget = 16 * time.Millisecond
	// MaxLinksPerLine bounds the links found in one line.
	MaxLinksPerLine = 8
)

// Client names the editor.
type Client struct {
	Name    string `json:"name" desc:"the editor: vscode, jetbrains, nvim, zed, …"`
	Version string `json:"version,omitempty" desc:"the extension's version"`
}

// InitializeParams is initialize's request: who the editor is and what it
// has open.
type InitializeParams struct {
	Client Client `json:"client"`
	// Folders are the editor's workspace folders, absolute. Lists default
	// to what concerns them.
	Folders []string `json:"folders,omitempty" desc:"the editor's workspace folders (absolute)"`
}

// ServerInfo names the bridge.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// DaemonInfo is the daemon the bridge talks to.
type DaemonInfo struct {
	Version  string `json:"version"`
	APILevel int    `json:"api_level"`
}

// InitializeResult is what the bridge speaks.
type InitializeResult struct {
	Protocol int        `json:"protocol" desc:"the editor protocol's version (1)"`
	Server   ServerInfo `json:"server"`
	Daemon   DaemonInfo `json:"daemon"`
	// Missing are the features the daemon is too old for: files, hunks,
	// review. The editor hides them.
	Missing []string `json:"missing" desc:"features the daemon is too old for (files, hunks, review)"`
}

// FoldersParams changes the editor's workspace folders.
type FoldersParams struct {
	Folders []string `json:"folders" desc:"the editor's workspace folders (absolute)"`
}

// Empty is a request or result with nothing in it.
type Empty struct{}

// TaskRef names the task an agent works on.
type TaskRef struct {
	Plan  string `json:"plan"`
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	State string `json:"state,omitempty"`
}

// Agent is an agent as an editor shows it.
type Agent struct {
	ID     string `json:"id" desc:"the agent's ID (its process)"`
	PaneID string `json:"pane_id,omitempty"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	// State is working, blocked, done, idle, exited or unknown.
	State  string    `json:"state" desc:"working, blocked (waiting for a person), done, idle, exited or unknown"`
	Since  time.Time `json:"since,omitzero" desc:"when it entered its state"`
	Reason string    `json:"reason,omitempty" desc:"why it is in its state (what a blocked agent asks)"`
	// Attention says a person is needed: it is blocked.
	Attention     bool   `json:"attention,omitempty" desc:"it waits for a person"`
	EnvironmentID string `json:"environment_id"`
	// Dir is where it works: its environment's directory (its worktree,
	// when isolated).
	Dir string `json:"dir,omitempty" desc:"where it works (its worktree when isolated)"`
	// Branch is the branch of its worktree, when Hive made one for it.
	Branch string   `json:"branch,omitempty" desc:"its worktree's branch, when it has one of its own"`
	Repo   string   `json:"repo,omitempty" desc:"the repository its worktree belongs to"`
	Task   *TaskRef `json:"task,omitempty" desc:"the task it works on"`
}

// AgentListParams is agent.list's request.
type AgentListParams struct {
	// Folder keeps the agents of one folder; empty means the editor's
	// folders.
	Folder string `json:"folder,omitempty" desc:"only this folder's agents (default: the editor's folders)"`
	All    bool   `json:"all,omitempty" desc:"every agent, whatever its folder"`
}

// AgentList is agent.list's result.
type AgentList struct {
	Agents []Agent `json:"agents" desc:"blocked first, then working, done, idle, unknown and exited"`
}

// Kind is an agent kind Hive knows.
type Kind struct {
	Kind string `json:"kind" desc:"its ID: claude, codex, …"`
	Name string `json:"name"`
	// Installed says its command is on this machine's PATH.
	Installed bool `json:"installed" desc:"its command is found on the bridge's PATH"`
}

// KindList is agent.kinds's result.
type KindList struct {
	Kinds []Kind `json:"kinds"`
}

// Attachment kinds.
const (
	AttachSelection  = "selection"
	AttachFile       = "file"
	AttachDiagnostic = "diagnostic"
	AttachTest       = "test"
)

// Attachment is code the editor sends with a prompt.
type Attachment struct {
	Kind string `json:"kind" desc:"selection, file, diagnostic or test"`
	// Path is the file, absolute.
	Path      string `json:"path,omitempty" desc:"the file (absolute)"`
	StartLine int    `json:"start_line,omitempty" desc:"the first line, from 1"`
	EndLine   int    `json:"end_line,omitempty" desc:"the last line, from 1"`
	// Language is the editor's language ID, for the code fence.
	Language string `json:"language,omitempty" desc:"the editor's language ID (go, typescript, …)"`
	// Text is the selection, or a file's unsaved text.
	Text string `json:"text,omitempty" desc:"the selected text, or a file's text when the editor has unsaved changes"`
	// Message, Severity and Source describe a diagnostic.
	Message  string `json:"message,omitempty" desc:"a diagnostic's message"`
	Severity string `json:"severity,omitempty" desc:"error, warning, information or hint"`
	Source   string `json:"source,omitempty" desc:"what reported the diagnostic"`
	// Name and Output describe a failing test.
	Name   string `json:"name,omitempty" desc:"a test's name"`
	Output string `json:"output,omitempty" desc:"a failing test's output"`
}

// Prompt is text and code from the editor, which the bridge turns into one
// prompt.
type Prompt struct {
	Text        string       `json:"text,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// StartParams is agent.start's request.
type StartParams struct {
	Kind string `json:"kind" desc:"the agent kind (agent.kinds)"`
	// Folder is where it works: an environment of that directory is used,
	// or made.
	Folder   string `json:"folder" desc:"where it works (absolute): an environment of that directory is used, or made"`
	Isolate  bool   `json:"isolate,omitempty" desc:"in a new worktree on a branch Hive names"`
	Worktree string `json:"worktree,omitempty" desc:"in a new worktree on this branch"`
	Base     string `json:"base,omitempty" desc:"where a new worktree's branch starts (default HEAD)"`
	Name     string `json:"name,omitempty" desc:"a name for its tab and pane"`
	// Prompt is sent once it is ready.
	Prompt *Prompt `json:"prompt,omitempty" desc:"sent once it is ready for a prompt"`
}

// Started is agent.start's result.
type Started struct {
	Agent Agent `json:"agent"`
	// Outcome is how sending the prompt went, when there was one.
	Outcome string `json:"outcome,omitempty"`
}

// PromptParams is agent.prompt's request.
type PromptParams struct {
	ID     string `json:"id" desc:"the agent"`
	Prompt Prompt `json:"prompt"`
}

// PromptResult is how sending a prompt went.
type PromptResult struct {
	Outcome string `json:"outcome" desc:"started (the agent took it), blocked, exited or timeout"`
	// Text is the prompt the agent got.
	Text string `json:"text" desc:"the prompt as the agent got it"`
}

// KeysParams is agent.keys's request.
type KeysParams struct {
	ID   string   `json:"id"`
	Keys []string `json:"keys" desc:"named keys: Enter, Escape, Up, Down, Tab, C-c, y, 1, …"`
}

// IDParams names an agent.
type IDParams struct {
	ID string `json:"id"`
}

// ReadParams is agent.read's request.
type ReadParams struct {
	ID    string `json:"id"`
	Lines int    `json:"lines,omitempty" desc:"only the last n lines"`
}

// ReadResult is an agent's screen as text.
type ReadResult struct {
	Lines []string `json:"lines"`
	Links []Link   `json:"links,omitempty" desc:"path:line links in the lines, with y counted in them"`
}

// TaskView is a task as an editor shows it.
type TaskView struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	Kind        string `json:"kind,omitempty"`
	State       string `json:"state" desc:"waiting, approval, queued, running, blocked, succeeded, failed, cancelled or skipped"`
	AgentID     string `json:"agent_id,omitempty"`
	PaneID      string `json:"pane_id,omitempty"`
	Environment string `json:"environment,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Attention   bool   `json:"attention,omitempty" desc:"it waits for a person: an approval, or a blocked agent"`
}

// PlanView is a plan and its tasks.
type PlanView struct {
	ID        string     `json:"id"`
	Name      string     `json:"name,omitempty"`
	State     string     `json:"state" desc:"running, succeeded, failed or cancelled"`
	Attention bool       `json:"attention,omitempty" desc:"a task waits for a person"`
	CreatedAt time.Time  `json:"created_at"`
	Tasks     []TaskView `json:"tasks" desc:"attention first, then in the plan's order"`
}

// TaskListParams is task.list's request.
type TaskListParams struct {
	Folder string `json:"folder,omitempty" desc:"unused in protocol 1; plans are listed whatever their folder"`
}

// TaskList is task.list's result.
type TaskList struct {
	Plans []PlanView `json:"plans" desc:"running plans first (those needing a person before them), then the most recent"`
}

// ApproveParams is task.approve's request.
type ApproveParams struct {
	Plan string `json:"plan"`
	Task string `json:"task,omitempty"`
	Deny bool   `json:"deny,omitempty"`
	Note string `json:"note,omitempty"`
}

// OpenParams is terminal.open's request.
type OpenParams struct {
	Agent string `json:"agent" desc:"the agent (or its pane)"`
	Cols  int    `json:"cols" desc:"the panel's width in cells"`
	Rows  int    `json:"rows" desc:"the panel's height in cells"`
}

// Opened is terminal.open's result; terminal.screen notifications follow.
type Opened struct {
	View  string `json:"view" desc:"names the view in terminal.* requests and notifications"`
	Agent string `json:"agent"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
}

// InputParams is terminal.input's request.
type InputParams struct {
	View string `json:"view"`
	Data string `json:"data" desc:"what is typed, as a terminal sends it (\r is Enter)"`
}

// ResizeParams is terminal.resize's request.
type ResizeParams struct {
	View string `json:"view"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// ViewParams names a view.
type ViewParams struct {
	View string `json:"view"`
	// Full asks terminal.refresh for every line again.
	Full bool `json:"full,omitempty"`
}

// Link is a path:line reference found in an agent's output, resolved to a
// file that exists.
type Link struct {
	Y    int    `json:"y" desc:"the screen line"`
	X    int    `json:"x" desc:"the first column"`
	W    int    `json:"w" desc:"the columns it covers"`
	Path string `json:"path" desc:"the file (absolute)"`
	Line int    `json:"line,omitempty"`
	Col  int    `json:"col,omitempty"`
}

// Screen is a terminal.screen notification.
type Screen struct {
	View   string                `json:"view"`
	Update termview.ScreenUpdate `json:"update"`
	Links  []Link                `json:"links,omitempty"`
}

// Ended is a terminal.ended notification.
type Ended struct {
	View   string `json:"view"`
	Reason string `json:"reason" desc:"exited (the agent's terminal ended), closed or lost (the daemon went away)"`
}

// ResolveParams is link.resolve's request.
type ResolveParams struct {
	Agent string `json:"agent,omitempty" desc:"resolve relative paths in this agent's directory"`
	Dir   string `json:"dir,omitempty" desc:"or in this directory (absolute)"`
	Text  string `json:"text"`
}

// LinkList is link.resolve's result.
type LinkList struct {
	Links []Link `json:"links" desc:"links in the text, y counting its lines from 0"`
}

// BranchView is a branch as an editor shows it.
type BranchView struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Base     string        `json:"base"`
	Repo     string        `json:"repo"`
	Path     string        `json:"path,omitempty" desc:"its worktree"`
	State    string        `json:"state"`
	Owner    *branch.Owner `json:"owner,omitempty"`
	Files    int           `json:"files,omitempty"`
	Added    int           `json:"added,omitempty"`
	Deleted  int           `json:"deleted,omitempty"`
	Checking bool          `json:"checking,omitempty"`
	// Gates says how its last gates went: passed, failed, or empty when
	// they never ran.
	Gates    string `json:"gates,omitempty" desc:"passed or failed: its last gates"`
	Pending  int    `json:"pending,omitempty" desc:"comments not sent yet"`
	Conflict bool   `json:"conflict,omitempty" desc:"it would conflict with another branch or its base"`
	Rev      uint64 `json:"rev"`
}

// BranchesParams is review.branches's request.
type BranchesParams struct {
	Folder string `json:"folder,omitempty" desc:"only branches of this folder's repository (default: the editor's folders)"`
	All    bool   `json:"all,omitempty" desc:"every branch"`
}

// BranchList is review.branches's result.
type BranchList struct {
	Branches []BranchView `json:"branches"`
}

// DiffParams is review.diff's request.
type DiffParams struct {
	Ref string `json:"ref" desc:"the branch: its ID, environment, agent, plan/task or name"`
	// Context is the lines of context around each change (default 3);
	// review.hunk must be given the same.
	Context int `json:"context,omitempty" desc:"lines of context around changes (default 3); give review.hunk the same"`
}

// Review is a branch's diff as hunks.
type Review struct {
	Branch BranchView `json:"branch"`
	// From and To are the commits compared: where it forked from its base,
	// and a snapshot of its worktree.
	From      string       `json:"from"`
	To        string       `json:"to"`
	Context   int          `json:"context"`
	Files     []ReviewFile `json:"files"`
	Truncated bool         `json:"truncated,omitempty" desc:"the diff was too long: files at its end are missing or cut"`
	Comments  []Comment    `json:"comments,omitempty"`
}

// ReviewFile is one file of a review.
type ReviewFile struct {
	Path    string `json:"path" desc:"relative to the repository, as in the branch"`
	OldPath string `json:"old_path,omitempty" desc:"its path at the base, when renamed"`
	// Abs is the file in the branch's worktree.
	Abs     string       `json:"abs,omitempty" desc:"the file in the branch's worktree (absolute)"`
	Status  string       `json:"status" desc:"A (added), M, D (deleted), R (renamed), C or T"`
	Added   int          `json:"added"`
	Deleted int          `json:"deleted"`
	Binary  bool         `json:"binary,omitempty"`
	Hunks   []ReviewHunk `json:"hunks"`
}

// ReviewHunk is a hunk and the verdict a person gave it.
type ReviewHunk struct {
	ID       string   `json:"id"`
	Header   string   `json:"header"`
	OldStart int      `json:"old_start"`
	OldLines int      `json:"old_lines"`
	NewStart int      `json:"new_start"`
	NewLines int      `json:"new_lines"`
	Lines    []string `json:"lines" desc:"each starts with ' ', '-', '+' or '\\'"`
	Verdict  string   `json:"verdict,omitempty" desc:"accepted or rejected, once a person said"`
}

// Comment is a review comment.
type Comment struct {
	ID   int       `json:"id"`
	Path string    `json:"path,omitempty"`
	Line int       `json:"line,omitempty"`
	Body string    `json:"body"`
	By   string    `json:"by,omitempty"`
	At   time.Time `json:"at"`
	Sent bool      `json:"sent,omitempty"`
}

// FileParams is review.file's request.
type FileParams struct {
	Ref  string `json:"ref"`
	Path string `json:"path" desc:"relative to the repository"`
	// Side is base (the file where the branch forked) or branch (as it is
	// in the worktree).
	Side string `json:"side" desc:"base (where the branch forked) or branch (in its worktree now)"`
}

// FileContent is a file's text.
type FileContent struct {
	Path      string `json:"path,omitempty" desc:"the file on disk, for the branch side"`
	Content   string `json:"content"`
	Missing   bool   `json:"missing,omitempty" desc:"the file does not exist on that side"`
	Binary    bool   `json:"binary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// HunkParams is review.hunk's request.
type HunkParams struct {
	Ref     string `json:"ref"`
	Path    string `json:"path"`
	Hunk    string `json:"hunk" desc:"the hunk's ID, from review.diff"`
	Action  string `json:"action" desc:"accept or reject"`
	Context int    `json:"context,omitempty" desc:"what review.diff was given (default 3)"`
	// Force rejects while an agent works in the worktree; the editor asks
	// the person first.
	Force bool   `json:"force,omitempty" desc:"reject even while an agent works there (ask the person first)"`
	By    string `json:"by,omitempty"`
}

// CommentParams is review.comment's request.
type CommentParams struct {
	Ref  string `json:"ref"`
	Path string `json:"path,omitempty" desc:"relative to the repository"`
	Line int    `json:"line,omitempty" desc:"in the branch's version of the file, from 1"`
	Body string `json:"body"`
	By   string `json:"by,omitempty"`
	Send bool   `json:"send,omitempty" desc:"send it, and every comment not sent yet, to the branch's agent now"`
}

// RefParams names a branch.
type RefParams struct {
	Ref string `json:"ref"`
}

// BranchResult is a branch after a change.
type BranchResult struct {
	Branch BranchView `json:"branch"`
}

// Diagnostic is a problem at a file and line, from a gate's output.
type Diagnostic struct {
	Path string `json:"path" desc:"the file (absolute, in the branch's worktree); empty for the branch as a whole"`
	Rel  string `json:"rel,omitempty" desc:"relative to the repository"`
	Line int    `json:"line,omitempty" desc:"from 1"`
	Col  int    `json:"col,omitempty" desc:"from 1"`
	// Severity is error or warning.
	Severity string `json:"severity" desc:"error or warning"`
	Message  string `json:"message"`
	// Source is the gate's name.
	Source string `json:"source" desc:"the gate"`
	Branch string `json:"branch" desc:"the branch's ID"`
}

// DiagnosticsParams is review.diagnostics's request.
type DiagnosticsParams struct {
	Ref    string `json:"ref,omitempty" desc:"one branch; empty: every branch of the folder's (or the editor's folders') repositories"`
	Folder string `json:"folder,omitempty"`
}

// DiagnosticList is review.diagnostics's result.
type DiagnosticList struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// FileEdit is a file an agent is changing, as an editor marks it.
type FileEdit struct {
	Path      string    `json:"path" desc:"the file (absolute)"`
	Rel       string    `json:"rel" desc:"its path in its working tree"`
	Worktree  string    `json:"worktree"`
	Deleted   bool      `json:"deleted,omitempty"`
	Agent     string    `json:"agent" desc:"the agent's ID"`
	AgentName string    `json:"agent_name,omitempty"`
	Kind      string    `json:"kind"`
	PaneID    string    `json:"pane_id,omitempty"`
	State     string    `json:"state" desc:"the agent's state"`
	Task      *TaskRef  `json:"task,omitempty"`
	Since     time.Time `json:"since"`
	Last      time.Time `json:"last"`
	// Active says the agent may still change it: it works, or waits for a
	// person. Editors warn before a person edits such a file.
	Active bool `json:"active" desc:"the agent works or waits on a person: warn before editing"`
}

// FilesParams is file.list's request.
type FilesParams struct {
	Folder string `json:"folder,omitempty" desc:"only this folder (default: the editor's folders)"`
}

// FileList is file.list's result, and the file.changed notification.
type FileList struct {
	Files []FileEdit `json:"files"`
}

// CheckParams is file.check's request.
type CheckParams struct {
	Path string `json:"path" desc:"the file (absolute)"`
}

// CheckResult says who is changing a file.
type CheckResult struct {
	Editing []FileEdit `json:"editing"`
	// Warn says the editor should ask before the person edits it.
	Warn bool `json:"warn" desc:"an agent may still change it: ask before the person edits it"`
	// Message says so in a sentence.
	Message string `json:"message,omitempty"`
}

// Notify kinds.
const (
	NotifyBlocked  = "blocked"
	NotifyDone     = "done"
	NotifyApproval = "approval"
	NotifyGates    = "gates"
)

// Notice is a notify notification: something a person may want to open.
type Notice struct {
	Kind    string `json:"kind" desc:"blocked, done, approval or gates"`
	Level   string `json:"level" desc:"info, warning or error"`
	Message string `json:"message"`
	// Agent opens its terminal; Plan and Task its task; Ref its review.
	Agent string `json:"agent,omitempty"`
	Plan  string `json:"plan,omitempty"`
	Task  string `json:"task,omitempty"`
	Ref   string `json:"ref,omitempty"`
}

// DaemonState is a daemon.state notification.
type DaemonState struct {
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}
