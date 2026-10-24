import { motion } from "framer-motion";
import { AlertTriangle, CheckCircle2, GitPullRequest, Loader2, MessageSquare, Play, Send, Trash2, Upload } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { api, type Branch, type BranchDiff } from "../api";
import { store, useStore } from "../store";
import { close, Modal } from "./Modal";
import { StateDot } from "./StateDot";

// branchState is the agent state a branch's dot shows: blocked when it
// needs a person (a conflict, a stopped merge), working while it changes,
// done when it is ready or merged.
function branchState(b: Branch): string {
  if (b.in_progress || (b.overlaps ?? []).some((o) => (o.conflicts ?? []).length > 0)) return "blocked";
  switch (b.state) {
    case "ready":
    case "merged":
      return "done";
    case "working":
      return "working";
    default:
      return "idle";
  }
}

// branchNote is what a branch's second line says.
function branchNote(b: Branch): string {
  const conflicts = (b.overlaps ?? []).filter((o) => (o.conflicts ?? []).length > 0);
  if (b.in_progress) return "a merge stopped: resolve it";
  if (conflicts.length > 0) return `conflicts with ${conflicts.map((o) => o.branch).join(", ")}`;
  if ((b.overlaps ?? []).length > 0) return `overlaps ${(b.overlaps ?? []).map((o) => o.branch).join(", ")}`;
  const parts: string[] = [b.state];
  if (b.files > 0) parts.push(`${b.files} files +${b.added} -${b.deleted}`);
  if (b.pending > 0) parts.push(`${b.pending} comments to send`);
  return parts.join(" · ");
}

// BranchItem is one branch in the sidebar; it opens the review.
export function BranchItem({ b }: { b: Branch }) {
  return (
    <motion.li layout initial={{ opacity: 0, x: -8 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: -8 }} transition={{ duration: 0.18 }}>
      <button
        className="flex w-full flex-col rounded-md px-2 py-1.5 text-left hover:bg-sidebar-accent"
        aria-label={`Review ${b.name}`}
        onClick={() => store.setUI({ dialog: { kind: "review", branch: b.id } })}
      >
        <span className="flex w-full items-center gap-2.5 text-sm">
          <StateDot state={branchState(b)} />
          <span className="min-w-0 flex-1 truncate font-mono text-xs font-medium">{b.name}</span>
        </span>
        <span className={cn("truncate pl-5 text-xs text-muted-foreground", branchState(b) === "blocked" && "text-blocked")}>
          {b.machine ? `${b.machine} · ` : ""}
          {branchNote(b)}
        </span>
      </button>
    </motion.li>
  );
}

// diffLine is a line of a patch, with where a comment on it would go.
interface DiffLine {
  text: string;
  kind: "file" | "hunk" | "add" | "del" | "ctx" | "meta";
  path: string;
  line: number;
}

// parsePatch lays a unified diff out as lines that know their file and
// their line in the branch's version of it.
export function parsePatch(patch: string): DiffLine[] {
  const out: DiffLine[] = [];
  let path = "";
  let line = 0;
  for (const raw of patch.replace(/\n$/, "").split("\n")) {
    if (raw.startsWith("diff --git ") || raw.startsWith("index ") || raw.startsWith("--- ") || raw.startsWith("new file") || raw.startsWith("deleted file")) continue;
    if (raw.startsWith("+++ ")) {
      path = raw.slice(4).replace(/^b\//, "");
      out.push({ text: path, kind: "file", path, line: 0 });
    } else if (raw.startsWith("@@")) {
      line = Number(/\+(\d+)/.exec(raw)?.[1] ?? 0);
      out.push({ text: raw, kind: "hunk", path, line });
    } else if (raw.startsWith("+")) {
      out.push({ text: raw, kind: "add", path, line: line++ });
    } else if (raw.startsWith("-")) {
      out.push({ text: raw, kind: "del", path, line: Math.max(line, 1) });
    } else if (raw.startsWith(" ")) {
      out.push({ text: raw, kind: "ctx", path, line: line++ });
    } else if (raw) {
      out.push({ text: raw, kind: "meta", path, line: 0 });
    }
  }
  return out;
}

const lineTone: Record<DiffLine["kind"], string> = {
  file: "mt-3 bg-muted/60 font-semibold text-foreground",
  hunk: "text-working",
  add: "bg-done/10 text-done",
  del: "bg-destructive/10 text-destructive",
  ctx: "",
  meta: "text-muted-foreground",
};

// Review shows a branch: its overlaps and gates, its diff, and comments
// that go back to its agent. Push and pull requests run only from here,
// when the person asks.
export function Review({ branch: id }: { branch: string }) {
  const b = useStore((s) => (s.workspace.branches ?? []).find((x) => x.id === id));
  const [diff, setDiff] = useState<BranchDiff | undefined>();
  const [error, setError] = useState("");
  const [at, setAt] = useState<number | undefined>();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState("");
  const lines = useMemo(() => parsePatch(diff?.patch ?? ""), [diff]);

  const load = useCallback(() => {
    api
      .branchDiff(id)
      .then((d) => {
        setDiff(d);
        setError("");
      })
      .catch((e) => setError(String(e?.message ?? e)));
  }, [id]);
  useEffect(load, [load]);

  const run = (what: string, f: () => Promise<unknown>, done?: (v: unknown) => void) => {
    setBusy(what);
    f()
      .then((v) => {
        done?.(v);
        load();
      })
      .catch(store.fail)
      .finally(() => setBusy(""));
  };

  if (!b) {
    return (
      <Modal title="Review" description="This branch is no longer followed: it was cleaned up or removed.">
        <Button onClick={close}>Close</Button>
      </Modal>
    );
  }
  const target = at !== undefined ? lines[at] : undefined;
  const comment = (send: boolean) => {
    const body = text.trim();
    if (!body) return;
    run("comment", () => api.commentBranch(b.id, target?.path ?? "", target && target.line > 0 ? target.line : 0, body, send), () => {
      setText("");
      setAt(undefined);
      store.toast(send ? "Sent to the agent." : "Comment added.");
    });
  };

  return (
    <Modal
      title={`Review · ${b.name}`}
      className="sm:max-w-5xl"
      description={
        <span>
          {b.state} against <span className="font-mono">{b.base}</span>
          {b.ahead + b.behind > 0 && ` · ${b.ahead} ahead, ${b.behind} behind`}
          {b.dirty && " · uncommitted changes included"}
        </span>
      }
    >
      <div className="flex flex-wrap gap-2">
        <Button size="sm" disabled={!!busy || b.checking} onClick={() => run("gates", () => api.readyBranch(b.id), (ready) => store.toast(ready ? `${b.name} is ready.` : `${b.name} is not ready: a gate failed.`))}>
          {busy === "gates" || b.checking ? <Loader2 className="animate-spin" /> : <Play />} Run gates
        </Button>
        <Button size="sm" variant="outline" disabled={!!busy || b.pending === 0} onClick={() => run("send", () => api.sendComments(b.id), () => store.toast("Comments sent to the agent."))}>
          <Send /> Send {b.pending > 0 ? b.pending : ""} comments
        </Button>
        <Button size="sm" variant="outline" disabled={!!busy} onClick={() => run("push", () => api.pushBranch(b.id), () => store.toast(`Pushed ${b.name}.`))}>
          <Upload /> Push
        </Button>
        {b.pull_request ? (
          <Button size="sm" variant="outline" onClick={() => api.openLink(b.pull_request!).catch(store.fail)}>
            <GitPullRequest /> Pull request
          </Button>
        ) : (
          <Button
            size="sm"
            variant="outline"
            disabled={!!busy}
            onClick={() =>
              store.setUI({
                dialog: {
                  kind: "confirm",
                  title: "Open a pull request?",
                  body: `Push ${b.name} if its remote lacks it, and open a pull request against ${b.base}.`,
                  action: "Open it",
                  run: () => api.openPullRequest(b.id, false, true).then((url) => store.toast(`Opened ${url}`)).catch(store.fail),
                },
              })
            }
          >
            <GitPullRequest /> Open pull request
          </Button>
        )}
        <Button
          size="sm"
          variant="ghost"
          disabled={!!busy}
          onClick={() =>
            store.setUI({
              dialog: {
                kind: "confirm",
                title: `Clean up ${b.name}?`,
                body: "Remove its worktree and stop the agents in it. The branch and its commits stay in the repository; uncommitted changes keep the worktree.",
                action: "Clean up",
                run: () => api.cleanBranch(b.id, false).catch(store.fail),
              },
            })
          }
        >
          <Trash2 /> Clean up
        </Button>
      </div>

      {(b.overlaps ?? []).length > 0 && (
        <ul className="space-y-1 text-sm" aria-label="Overlaps">
          {(b.overlaps ?? []).map((o) => (
            <li key={o.branch} className={cn("flex gap-2 rounded-md px-2 py-1", (o.conflicts ?? []).length > 0 ? "bg-destructive/10" : "bg-blocked/10")}>
              <AlertTriangle className="mt-0.5 size-4 shrink-0" />
              <span>
                <span className="font-mono">{o.branch}</span>{" "}
                {(o.conflicts ?? []).length > 0 ? `would conflict in ${o.conflicts.join(", ")}` : `also changes ${o.files.join(", ")}`}
              </span>
            </li>
          ))}
        </ul>
      )}
      {(b.gates ?? []).length > 0 && (
        <ul className="flex flex-wrap gap-2 text-xs" aria-label="Gates">
          {(b.gates ?? []).map((g) => (
            <li key={g.name} title={(g.output ?? []).join("\n") || g.error} className={cn("flex items-center gap-1 rounded-full border px-2 py-0.5", g.passed ? "text-done" : !g.skipped && "text-destructive")}>
              {g.passed ? <CheckCircle2 className="size-3" /> : <AlertTriangle className="size-3" />}
              {g.name}: {g.skipped ? "skipped" : g.passed ? "passed" : g.error || `exit ${g.exit_code}`}
            </li>
          ))}
        </ul>
      )}
      {b.cleanup && <p className="text-xs text-muted-foreground">Kept: {b.cleanup}</p>}

      <div className="max-h-[50vh] overflow-auto rounded-md border font-mono text-xs" role="region" aria-label="Diff">
        {error && <p className="p-3 text-destructive">{error}</p>}
        {!diff && !error && <p className="p-3 text-muted-foreground">Reading the diff…</p>}
        {diff && lines.length === 0 && <p className="p-3 text-muted-foreground">No changes against {diff.base}.</p>}
        {lines.map((l, i) => (
          <div key={i}>
            <button
              className={cn("block w-full px-3 text-left whitespace-pre hover:bg-accent/50", lineTone[l.kind], at === i && "ring-1 ring-primary")}
              title={l.path ? `Comment on ${l.path}${l.line > 0 ? `:${l.line}` : ""}` : undefined}
              onClick={() => setAt(at === i ? undefined : i)}
            >
              {l.text || " "}
            </button>
            {at === i && (
              <div className="flex gap-2 border-y bg-muted/40 p-2 font-sans">
                <Textarea
                  autoFocus
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  placeholder={`A comment on ${l.path}${l.line > 0 ? `:${l.line}` : ""} for the agent`}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) comment(false);
                  }}
                />
                <div className="flex flex-col gap-1">
                  <Button size="sm" disabled={!text.trim() || !!busy} onClick={() => comment(false)}>
                    <MessageSquare /> Add
                  </Button>
                  <Button size="sm" variant="outline" disabled={!text.trim() || !!busy} onClick={() => comment(true)}>
                    <Send /> Add and send
                  </Button>
                </div>
              </div>
            )}
          </div>
        ))}
        {diff?.truncated && <p className="p-3 text-muted-foreground">The diff was cut: hive branch diff shows it all.</p>}
      </div>
    </Modal>
  );
}
