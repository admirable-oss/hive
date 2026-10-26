import { GitBranch, Loader2, Send } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Kbd } from "@/components/ui/kbd";
import { Textarea } from "@/components/ui/textarea";
import { api, type Manifest } from "../api";
import { currentEnv, store, useStore, type Dialog as DialogState } from "../store";
import { Field, NativeSelect } from "./Field";
import { InsightsDialog } from "./Insights";
import { IdentitySheet, Machines } from "./Machines";
import { close, Modal } from "./Modal";
import { ObserveDialog } from "./ObserveDialog";
import { Review } from "./Branches";

function NewAgent({ env: initial }: { env?: string }) {
  const envs = useStore((s) => s.workspace.environments);
  const fallback = useStore(currentEnv);
  const [manifests, setManifests] = useState<Manifest[]>([]);
  const [kind, setKind] = useState("");
  const [env, setEnv] = useState(initial ?? fallback ?? "");
  const [worktree, setWorktree] = useState("");
  const [isolate, setIsolate] = useState(false);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const machine = envs.find((e) => e.id === env)?.machine ?? "";
  useEffect(() => {
    api
      .manifests(machine)
      .then((ms) => {
        setManifests(ms ?? []);
        setKind((k) => (ms ?? []).find((m) => m.id === k)?.id || (ms ?? []).find((m) => m.id === "claude")?.id || ms?.[0]?.id || "");
      })
      .catch(store.fail);
  }, [machine]);
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    api
      .startAgent({ kind, environment_id: env, worktree: worktree.trim() || undefined, isolate: isolate || undefined, name: name.trim() || undefined })
      .then((pane) => {
        close();
        store.reveal(pane);
      })
      .catch((err) => {
        setBusy(false);
        store.fail(err);
      });
  };
  return (
    <Modal title="New agent" description="Start a coding agent in an environment. It keeps working when you close Hive.">
      <form onSubmit={submit} className="grid gap-4">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Agent">
            <NativeSelect value={kind} onChange={(e) => setKind(e.target.value)} required>
              {manifests.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label="Environment">
            <NativeSelect value={env} onChange={(e) => setEnv(e.target.value)} required>
              {envs.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.machine ? `${e.name} (${e.machine})` : e.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
        </div>
        <Field label="Worktree branch" hint="optional: a new git worktree for this agent">
          <div className="relative">
            <GitBranch className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input className="pl-8 font-mono" value={worktree} onChange={(e) => setWorktree(e.target.value)} placeholder="fix/login" />
          </div>
        </Field>
        {!worktree.trim() && (
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" className="size-4 accent-primary" checked={isolate} onChange={(e) => setIsolate(e.target.checked)} />
            Its own worktree, on a branch Hive names
          </label>
        )}
        <Field label="Name" hint="optional">
          <Input value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        {envs.length === 0 && <p className="text-sm text-muted-foreground">Make an environment first: agents work in one.</p>}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button type="submit" disabled={busy || !kind || !env}>
            {busy && <Loader2 className="animate-spin" />}
            Start
          </Button>
        </DialogFooter>
      </form>
    </Modal>
  );
}

function Prompt({ agent: initial }: { agent?: string }) {
  const agents = useStore((s) => s.workspace.agents);
  const [agent, setAgent] = useState(initial ?? agents[0]?.id ?? "");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const target = agents.find((a) => a.id === agent);
  const send = () => {
    if (!text.trim()) return;
    setBusy(true);
    api
      .prompt(agent, text)
      .then((outcome) => {
        close();
        store.toast(`Prompt sent to ${target?.name ?? "the agent"} (${outcome}).`);
      })
      .catch((err) => {
        setBusy(false);
        store.fail(err);
      });
  };
  return (
    <Modal title="Prompt an agent" className="sm:max-w-xl">
      <form
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          send();
        }}
      >
        <Field label="Agent">
          <NativeSelect value={agent} onChange={(e) => setAgent(e.target.value)}>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({a.state})
              </option>
            ))}
          </NativeSelect>
        </Field>
        {target?.state === "blocked" && <p className="rounded-md bg-blocked/10 px-3 py-2 text-sm text-blocked">This agent is blocked: answer it in its pane first.</p>}
        <Field label="Prompt">
          <Textarea
            autoFocus
            rows={7}
            className="font-mono text-[13px]"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                send();
              }
            }}
          />
        </Field>
        <DialogFooter className="items-center sm:justify-between">
          <span className="text-xs text-muted-foreground">
            <Kbd>⌘</Kbd>/<Kbd>Ctrl</Kbd> + <Kbd>Enter</Kbd> sends
          </span>
          <div className="flex gap-2">
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !agent || !text.trim()}>
              {busy ? <Loader2 className="animate-spin" /> : <Send />}
              Send
            </Button>
          </div>
        </DialogFooter>
      </form>
    </Modal>
  );
}

function NewEnvironment() {
  const machines = useStore((s) => s.machines).filter((m) => m.connection.state === "connected");
  const [machine, setMachine] = useState("");
  const [id, setId] = useState("");
  const [dir, setDir] = useState("");
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    api
      .createEnvironment(machine, id.trim(), dir.trim())
      .then(() => {
        close();
        store.setUI({ env: machine ? `${machine}/${id.trim()}` : id.trim(), tab: undefined });
      })
      .catch(store.fail);
  };
  return (
    <Modal title="New environment" description="A place agents work in: a project directory, or a workspace Hive manages.">
      <form onSubmit={submit} className="grid gap-4">
        {machines.length > 0 && (
          <Field label="Machine">
            <NativeSelect value={machine} onChange={(e) => setMachine(e.target.value)}>
              <option value="">This computer</option>
              {machines.map((m) => (
                <option key={m.name} value={m.name}>
                  {m.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
        )}
        <Field label="Name">
          <Input autoFocus value={id} onChange={(e) => setId(e.target.value)} pattern="[A-Za-z0-9][A-Za-z0-9._-]*" required />
        </Field>
        <Field label="Directory" hint="an absolute path; empty makes a managed workspace">
          <Input className="font-mono" value={dir} onChange={(e) => setDir(e.target.value)} placeholder="/Users/me/code/api" />
        </Field>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button type="submit" disabled={!id.trim()}>
            Create
          </Button>
        </DialogFooter>
      </form>
    </Modal>
  );
}

function Confirm({ d }: { d: Extract<DialogState, { kind: "confirm" }> }) {
  return (
    <Modal title={d.title} description={d.body}>
      <DialogFooter>
        <Button variant="outline" onClick={close}>
          Cancel
        </Button>
        <Button
          variant="destructive"
          autoFocus
          onClick={() => {
            close();
            d.run();
          }}
        >
          {d.action}
        </Button>
      </DialogFooter>
    </Modal>
  );
}

export function Dialogs() {
  const d = useStore((s) => s.ui.dialog);
  if (!d) return null;
  switch (d.kind) {
    case "new-agent":
      return <NewAgent env={d.env} />;
    case "prompt":
      return <Prompt agent={d.agent} />;
    case "new-environment":
      return <NewEnvironment />;
    case "confirm":
      return <Confirm d={d} />;
    case "machines":
      return <Machines />;
    case "observe":
      return <ObserveDialog />;
    case "identity":
      return <IdentitySheet d={d} />;
    case "review":
      return <Review branch={d.branch} />;
    case "insights":
      return <InsightsDialog machine={d.machine} />;
  }
}
