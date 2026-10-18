import { useEffect, useId, useRef, useState } from "react";
import { api, type Manifest } from "../api";
import { currentEnv, store, useStore, type Dialog as DialogState } from "../store";

function Modal({ title, children, onClose }: { title: string; children: React.ReactNode; onClose: () => void }) {
  const id = useId();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>("[autofocus], input, textarea, select, button")?.focus();
    return () => prev?.focus?.();
  }, []);
  return (
    <div className="overlay" onMouseDown={onClose}>
      <div
        className="dialog"
        role="dialog"
        aria-modal
        aria-labelledby={id}
        ref={ref}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.key === "Escape" && onClose()}
      >
        <h2 id={id}>{title}</h2>
        {children}
      </div>
    </div>
  );
}

const close = () => store.setUI({ dialog: undefined });

function NewAgent({ env: initial }: { env?: string }) {
  const envs = useStore((s) => s.workspace.environments);
  const fallback = useStore(currentEnv);
  const [manifests, setManifests] = useState<Manifest[]>([]);
  const [kind, setKind] = useState("");
  const [env, setEnv] = useState(initial ?? fallback ?? "");
  const [worktree, setWorktree] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api
      .manifests()
      .then((ms) => {
        setManifests(ms);
        setKind((k) => k || ms.find((m) => m.id === "claude")?.id || ms[0]?.id || "");
      })
      .catch(store.fail);
  }, []);
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    api
      .startAgent({ kind, environment_id: env, worktree: worktree.trim() || undefined, name: name.trim() || undefined })
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
    <Modal title="New agent" onClose={close}>
      <form onSubmit={submit}>
        <label>
          Agent
          <select value={kind} onChange={(e) => setKind(e.target.value)} required>
            {manifests.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Environment
          <select value={env} onChange={(e) => setEnv(e.target.value)} required>
            {envs.map((e) => (
              <option key={e.id} value={e.id}>
                {e.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Worktree branch <span className="quiet">(optional: a new git worktree for this agent)</span>
          <input value={worktree} onChange={(e) => setWorktree(e.target.value)} placeholder="fix/login" />
        </label>
        <label>
          Name <span className="quiet">(optional)</span>
          <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        {envs.length === 0 && <p className="quiet">Make an environment first: agents work in one.</p>}
        <div className="buttons">
          <button type="button" onClick={close}>
            Cancel
          </button>
          <button className="primary" type="submit" disabled={busy || !kind || !env}>
            Start
          </button>
        </div>
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
    <Modal title="Prompt an agent" onClose={close}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          send();
        }}
      >
        <label>
          Agent
          <select value={agent} onChange={(e) => setAgent(e.target.value)}>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({a.state})
              </option>
            ))}
          </select>
        </label>
        {target?.state === "blocked" && <p className="warn">This agent is blocked: answer it in its pane first.</p>}
        <label>
          Prompt
          <textarea
            autoFocus
            rows={6}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                send();
              }
            }}
          />
        </label>
        <div className="buttons">
          <span className="quiet">⌘/Ctrl + Enter sends</span>
          <button type="button" onClick={close}>
            Cancel
          </button>
          <button className="primary" type="submit" disabled={busy || !agent || !text.trim()}>
            Send
          </button>
        </div>
      </form>
    </Modal>
  );
}

function NewEnvironment() {
  const [id, setId] = useState("");
  const [dir, setDir] = useState("");
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    api
      .createEnvironment(id.trim(), dir.trim())
      .then(() => {
        close();
        store.setUI({ env: id.trim(), tab: undefined });
      })
      .catch(store.fail);
  };
  return (
    <Modal title="New environment" onClose={close}>
      <form onSubmit={submit}>
        <label>
          Name
          <input autoFocus value={id} onChange={(e) => setId(e.target.value)} pattern="[A-Za-z0-9][A-Za-z0-9._-]*" required />
        </label>
        <label>
          Directory <span className="quiet">(an absolute path; empty makes a workspace Hive manages)</span>
          <input value={dir} onChange={(e) => setDir(e.target.value)} placeholder="/Users/me/code/api" />
        </label>
        <div className="buttons">
          <button type="button" onClick={close}>
            Cancel
          </button>
          <button className="primary" type="submit" disabled={!id.trim()}>
            Create
          </button>
        </div>
      </form>
    </Modal>
  );
}

function Confirm({ d }: { d: Extract<DialogState, { kind: "confirm" }> }) {
  return (
    <Modal title={d.title} onClose={close}>
      <p>{d.body}</p>
      <div className="buttons">
        <button onClick={close}>Cancel</button>
        <button
          className="danger"
          autoFocus
          onClick={() => {
            close();
            d.run();
          }}
        >
          {d.action}
        </button>
      </div>
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
  }
}

export function Toasts() {
  const toasts = useStore((s) => s.ui.toasts);
  return (
    <div className="toasts" role="status" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className={"toast" + (t.error ? " error" : "")}>
          {t.text}
        </div>
      ))}
    </div>
  );
}
