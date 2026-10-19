import { useState } from "react";
import { api, type Discovery, type IdentityCheck, type MachineStatus, type Nearby, type PairRequest } from "../api";
import { store, useStore, type Dialog } from "../store";
import { close, Modal } from "./Modal";

// Machines: other computers running Hive, saved (M7), found nearby (M17).
// Nothing is saved or trusted without showing the identity it proved first
// (IdentitySheet), and the confirmation names that exact identity.

const stateText: Record<string, string> = {
  trusted: "trusted",
  configured: "saved without an identity",
  changed: "identity changed",
  disabled: "disabled",
};

const connText: Record<string, string> = {
  connected: "connected",
  connecting: "connecting…",
  offline: "unreachable",
  outdated: "hive there is too old",
  off: "not connected",
};

function busyRun(setBusy: (b: boolean) => void, p: Promise<unknown>) {
  setBusy(true);
  p.catch(store.fail).finally(() => setBusy(false));
}

// review proves a machine's identity and shows it.
function review(check: Promise<IdentityCheck>, target: Partial<Extract<Dialog, { kind: "identity" }>>) {
  return check.then((c) => store.setUI({ dialog: { kind: "identity", check: c, ...target } }));
}

function SavedRow({ m }: { m: MachineStatus }) {
  const [busy, setBusy] = useState(false);
  const blocked = m.counts?.blocked ?? 0;
  const conn = m.connection?.state ?? "off";
  return (
    <li className="machine-row">
      <div className="machine-main">
        <span className={`conn-dot conn-${conn}`} aria-hidden />
        <strong>{m.name}</strong>
        <span className="item-meta">{m.host}</span>
      </div>
      <div className="machine-meta">
        <span className={m.state === "changed" ? "warn" : "quiet"}>{stateText[m.state] ?? m.state}</span>
        <span className="quiet">· {connText[conn] ?? conn}</span>
        {m.connection?.message && conn !== "connected" && <span className="quiet"> · {m.connection.message}</span>}
        {blocked > 0 && <span className="warn"> · {blocked} need you</span>}
      </div>
      <div className="machine-actions">
        {(m.state === "changed" || m.state === "configured") && (
          <button className={m.state === "changed" ? "primary" : undefined} disabled={busy} onClick={() => busyRun(setBusy, review(api.checkTrust(m.name), { machine: m.name }))}>
            {m.state === "changed" ? "Review identity…" : "Pin identity…"}
          </button>
        )}
        <button disabled={busy} onClick={() => busyRun(setBusy, api.machineAction(m.name, m.disabled ? "enable" : "disable"))}>
          {m.disabled ? "Enable" : "Disable"}
        </button>
        <button
          className="danger-quiet"
          disabled={busy}
          onClick={() =>
            store.setUI({
              dialog: {
                kind: "confirm",
                title: `Forget ${m.name}?`,
                body: "Hive stops connecting to it. Its daemon and agents keep running there.",
                action: "Forget",
                run: () => void api.machineAction(m.name, "remove").catch(store.fail),
              },
            })
          }
        >
          Forget
        </button>
      </div>
    </li>
  );
}

function NearbyRow({ r }: { r: Nearby }) {
  const [busy, setBusy] = useState(false);
  const [as, setAs] = useState("");
  const saved = r.state !== "discovered";
  return (
    <li className="machine-row">
      <div className="machine-main">
        <strong>{r.name}</strong>
        <code className="item-meta">{r.id.slice(0, 8)}</code>
        <span className="item-meta">{(r.addresses ?? [])[0] ?? ""}</span>
      </div>
      <div className="machine-meta">
        {saved ? <span className="quiet">saved as {r.machine}</span> : <span className="quiet">not paired</span>}
        {r.duplicate_name && <span className="warn"> · another runtime has this name</span>}
        {r.note && <span className="quiet"> · {r.note}</span>}
      </div>
      {!saved && (
        <div className="machine-actions">
          <input aria-label={`Name for ${r.name}`} placeholder="save as (optional)" value={as} onChange={(e) => setAs(e.target.value)} />
          <button className="primary" disabled={busy} onClick={() => busyRun(setBusy, review(api.checkPair({ id: r.id, as: as.trim() || undefined }), { pair: { id: r.id, as: as.trim() || undefined } }))}>
            Pair…
          </button>
        </div>
      )}
    </li>
  );
}

export function Machines() {
  const machines = useStore((s) => s.machines);
  const [found, setFound] = useState<Discovery>();
  const [looking, setLooking] = useState(false);
  const [host, setHost] = useState("");
  const [as, setAs] = useState("");
  const [busy, setBusy] = useState(false);
  const discover = () => {
    setLooking(true);
    api
      .discover()
      .then(setFound)
      .catch(store.fail)
      .finally(() => setLooking(false));
  };
  const pairHost = (e: React.FormEvent) => {
    e.preventDefault();
    const req: PairRequest = { host: host.trim(), as: as.trim() || undefined };
    busyRun(setBusy, review(api.checkPair(req), { pair: req }));
  };
  return (
    <Modal title="Machines" onClose={close}>
      <section aria-labelledby="saved-machines">
        <h3 id="saved-machines">Saved</h3>
        {machines.length === 0 ? (
          <p className="quiet">No other machines yet. Find one nearby, or pair one by its ssh address.</p>
        ) : (
          <ul className="machine-list">
            {machines.map((m) => (
              <SavedRow key={m.name} m={m} />
            ))}
          </ul>
        )}
      </section>
      <section aria-labelledby="nearby-machines">
        <h3 id="nearby-machines">
          Nearby
          <button onClick={discover} disabled={looking}>
            {looking ? "Looking…" : found ? "Look again" : "Find machines nearby"}
          </button>
        </h3>
        {found && found.runtimes.length === 0 && (
          <p className="quiet">
            None answered{found.interfaces.length > 0 ? ` on ${found.interfaces.join(", ")}` : ""}. Machines announce themselves only
            with <code>discovery.advertise = true</code>, and multicast must reach them; otherwise pair by address below.
          </p>
        )}
        {found && found.runtimes.length > 0 && (
          <ul className="machine-list">
            {found.runtimes.map((r) => (
              <NearbyRow key={r.id} r={r} />
            ))}
          </ul>
        )}
      </section>
      <section aria-labelledby="pair-host">
        <h3 id="pair-host">Pair by ssh</h3>
        <form className="inline-form" onSubmit={pairHost}>
          <input aria-label="ssh destination" placeholder="me@10.8.0.12" value={host} onChange={(e) => setHost(e.target.value)} required />
          <input aria-label="Save as" placeholder="save as (optional)" value={as} onChange={(e) => setAs(e.target.value)} />
          <button className="primary" type="submit" disabled={busy || !host.trim()}>
            Check…
          </button>
        </form>
        <p className="quiet">Hive connects with your ssh keys and config, and shows the identity it proves before saving anything.</p>
      </section>
      <div className="buttons">
        <button onClick={close}>Done</button>
      </div>
    </Modal>
  );
}

// IdentitySheet shows what a machine proved; trusting confirms exactly
// that identity (the CLI refuses any other).
export function IdentitySheet({ d }: { d: Extract<Dialog, { kind: "identity" }> }) {
  const { check } = d;
  const [busy, setBusy] = useState(false);
  const changed = check.trust === "changed";
  const verified = check.trust === "verified";
  const back = () => store.setUI({ dialog: { kind: "machines" } });
  const confirm = () => {
    setBusy(true);
    const done = d.pair ? api.pair(d.pair, check.proof.id) : api.trust(d.machine!, check.proof.id);
    done
      .then(() => {
        store.toast(d.pair ? `Paired with ${check.machine.name}.` : `${d.machine} is trusted.`);
        back();
      })
      .catch((err) => {
        setBusy(false);
        store.fail(err);
      });
  };
  return (
    <Modal title={changed ? `${d.machine}: identity changed` : d.pair ? "Pair with this machine?" : `${d.machine}: identity`} onClose={back}>
      {changed && (
        <p className="warn" role="alert">
          This machine proved a different identity than the one it was paired with. If Hive was not reinstalled there and its home
          not removed, another computer may be answering at its address. Trust it only if you know why it changed.
        </p>
      )}
      <dl className="identity">
        <dt>Address</dt>
        <dd>{check.machine.host}</dd>
        <dt>Name</dt>
        <dd>
          {check.proof.name} {check.proof.hostname && <span className="quiet">(host {check.proof.hostname})</span>}
        </dd>
        <dt>Platform</dt>
        <dd>
          {check.proof.platform}, hive {check.proof.version}
        </dd>
        <dt>Identity</dt>
        <dd>
          <code>{check.proof.id}</code>
        </dd>
        <dt>Fingerprint</dt>
        <dd>
          <code>{check.fingerprint}</code>
        </dd>
        {changed && check.pinned && (
          <>
            <dt>Was</dt>
            <dd>
              <code>{check.pinned}</code>
            </dd>
          </>
        )}
      </dl>
      <p className="quiet">To compare, run <code>hive machine identity</code> on that machine.</p>
      <div className="buttons">
        <button onClick={back}>{verified ? "Close" : "Cancel"}</button>
        {!verified && (
          <button className={changed ? "danger" : "primary"} disabled={busy} onClick={confirm}>
            {changed ? "Trust the new identity" : d.pair ? `Trust and save as ${check.machine.name}` : "Pin this identity"}
          </button>
        )}
      </div>
    </Modal>
  );
}
