import { AnimatePresence, motion } from "framer-motion";
import { Fingerprint, Loader2, Radar, Server, ShieldAlert, ShieldCheck } from "lucide-react";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { api, type Discovery, type IdentityCheck, type MachineStatus, type Nearby, type PairRequest } from "../api";
import { store, useStore, type Dialog } from "../store";
import { close, Modal } from "./Modal";

// Machines: other computers running Hive, saved (M7), found nearby (M17).
// Nothing is saved or trusted without showing the identity it proved first
// (IdentitySheet), and the confirmation names that exact identity.

const stateText: Record<string, string> = {
  trusted: "trusted",
  configured: "no identity pinned",
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

const connDot: Record<string, string> = {
  connected: "bg-done",
  connecting: "bg-working animate-pulse",
  offline: "bg-destructive",
  outdated: "bg-destructive",
};

function busyRun(setBusy: (b: boolean) => void, p: Promise<unknown>) {
  setBusy(true);
  p.catch(store.fail).finally(() => setBusy(false));
}

// review proves a machine's identity and shows it.
function review(check: Promise<IdentityCheck>, target: Partial<Extract<Dialog, { kind: "identity" }>>) {
  return check.then((c) => store.setUI({ dialog: { kind: "identity", check: c, ...target } }));
}

function Row({ children }: { children: React.ReactNode }) {
  return (
    <motion.li layout initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }} className="rounded-lg border bg-card/50 p-3">
      {children}
    </motion.li>
  );
}

function SavedRow({ m }: { m: MachineStatus }) {
  const [busy, setBusy] = useState(false);
  const blocked = m.counts?.blocked ?? 0;
  const conn = m.connection?.state ?? "off";
  return (
    <Row>
      <div className="flex items-center gap-2">
        <span aria-hidden className={cn("size-2 rounded-full ring-1 ring-idle", connDot[conn] ?? "bg-transparent")} />
        <span className="font-medium">{m.name}</span>
        <span className="truncate font-mono text-xs text-muted-foreground">{m.host}</span>
        <span className="flex-1" />
        <Badge variant={m.state === "changed" ? "destructive" : m.state === "trusted" ? "secondary" : "outline"}>
          {m.state === "trusted" ? <ShieldCheck /> : m.state === "changed" ? <ShieldAlert /> : null}
          {stateText[m.state] ?? m.state}
        </Badge>
      </div>
      <p className="mt-1 ml-4 text-xs text-muted-foreground">
        {connText[conn] ?? conn}
        {m.connection?.message && conn !== "connected" ? ` · ${m.connection.message}` : ""}
        {blocked > 0 && <span className="text-blocked"> · {blocked} need you</span>}
      </p>
      <div className="mt-2 ml-4 flex flex-wrap gap-1.5">
        {(m.state === "changed" || m.state === "configured") && (
          <Button size="xs" variant={m.state === "changed" ? "default" : "outline"} disabled={busy} onClick={() => busyRun(setBusy, review(api.checkTrust(m.name), { machine: m.name }))}>
            {busy && <Loader2 className="animate-spin" />}
            {m.state === "changed" ? "Review identity…" : "Pin identity…"}
          </Button>
        )}
        <Button size="xs" variant="outline" disabled={busy} onClick={() => busyRun(setBusy, api.machineAction(m.name, m.disabled ? "enable" : "disable"))}>
          {m.disabled ? "Enable" : "Disable"}
        </Button>
        <Button
          size="xs"
          variant="ghost"
          className="text-destructive hover:text-destructive"
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
        </Button>
      </div>
    </Row>
  );
}

function NearbyRow({ r }: { r: Nearby }) {
  const [busy, setBusy] = useState(false);
  const [as, setAs] = useState("");
  const saved = r.state !== "discovered";
  return (
    <Row>
      <div className="flex items-center gap-2">
        <Server className="size-3.5 text-muted-foreground" />
        <span className="font-medium">{r.name}</span>
        <code className="text-xs text-muted-foreground">{r.id.slice(0, 8)}</code>
        <span className="font-mono text-xs text-muted-foreground">{(r.addresses ?? [])[0] ?? ""}</span>
        <span className="flex-1" />
        <Badge variant="outline">{saved ? `saved as ${r.machine}` : "not paired"}</Badge>
      </div>
      {(r.duplicate_name || r.note) && (
        <p className="mt-1 ml-5 text-xs text-muted-foreground">
          {r.duplicate_name && <span className="text-blocked">another runtime has this name</span>}
          {r.note && <span> {r.note}</span>}
        </p>
      )}
      {!saved && (
        <div className="mt-2 ml-5 flex gap-1.5">
          <Input className="h-7 text-xs" aria-label={`Name for ${r.name}`} placeholder="save as (optional)" value={as} onChange={(e) => setAs(e.target.value)} />
          <Button size="sm" className="h-7" disabled={busy} onClick={() => busyRun(setBusy, review(api.checkPair({ id: r.id, as: as.trim() || undefined }), { pair: { id: r.id, as: as.trim() || undefined } }))}>
            {busy && <Loader2 className="animate-spin" />}
            Pair…
          </Button>
        </div>
      )}
    </Row>
  );
}

function Heading({ id, children, action }: { id: string; children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <h3 id={id} className="mb-2 flex items-center justify-between text-xs font-semibold tracking-wider text-muted-foreground uppercase">
      {children}
      {action}
    </h3>
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
    <Modal title="Machines" description="Other computers running Hive. Their agents appear in the sidebar once they are trusted." className="sm:max-w-2xl">
      <div className="grid max-h-[62vh] gap-5 overflow-y-auto pr-1">
        <section aria-labelledby="saved-machines">
          <Heading id="saved-machines">Saved</Heading>
          {machines.length === 0 ? (
            <p className="text-sm text-muted-foreground">No other machines yet. Find one nearby, or pair one by its ssh address.</p>
          ) : (
            <ul className="grid gap-2">
              <AnimatePresence initial={false}>
                {machines.map((m) => (
                  <SavedRow key={m.name} m={m} />
                ))}
              </AnimatePresence>
            </ul>
          )}
        </section>
        <section aria-labelledby="nearby-machines">
          <Heading
            id="nearby-machines"
            action={
              <Button size="xs" variant="outline" onClick={discover} disabled={looking}>
                {looking ? <Loader2 className="animate-spin" /> : <Radar />}
                {looking ? "Looking…" : found ? "Look again" : "Find machines nearby"}
              </Button>
            }
          >
            Nearby
          </Heading>
          {found && found.runtimes.length === 0 && (
            <p className="text-sm text-muted-foreground">
              None answered{found.interfaces.length > 0 ? ` on ${found.interfaces.join(", ")}` : ""}. Machines announce themselves only with{" "}
              <code className="text-xs">discovery.advertise = true</code>, and multicast must reach them; otherwise pair by address below.
            </p>
          )}
          {found && found.runtimes.length > 0 && (
            <ul className="grid gap-2">
              {found.runtimes.map((r) => (
                <NearbyRow key={r.id} r={r} />
              ))}
            </ul>
          )}
        </section>
        <section aria-labelledby="pair-host">
          <Heading id="pair-host">Pair by ssh</Heading>
          <form className="flex gap-2" onSubmit={pairHost}>
            <Input className="flex-1 font-mono" aria-label="ssh destination" placeholder="me@10.8.0.12" value={host} onChange={(e) => setHost(e.target.value)} required />
            <Input className="w-40" aria-label="Save as" placeholder="save as (optional)" value={as} onChange={(e) => setAs(e.target.value)} />
            <Button type="submit" disabled={busy || !host.trim()}>
              {busy && <Loader2 className="animate-spin" />}
              Check…
            </Button>
          </form>
          <p className="mt-1.5 text-xs text-muted-foreground">Hive connects with your ssh keys and config, and shows the identity it proves before saving anything.</p>
        </section>
      </div>
      <DialogFooter>
        <Button variant="outline" onClick={close}>
          Done
        </Button>
      </DialogFooter>
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
  const rows: [string, React.ReactNode][] = [
    ["Address", <span className="font-mono">{check.machine.host}</span>],
    ["Name", <>{check.proof.name} {check.proof.hostname && <span className="text-muted-foreground">(host {check.proof.hostname})</span>}</>],
    ["Platform", `${check.proof.platform}, hive ${check.proof.version}`],
    ["Identity", <code>{check.proof.id}</code>],
  ];
  return (
    <Modal title={changed ? `${d.machine}: identity changed` : d.pair ? "Pair with this machine?" : `${d.machine}: identity`} onClose={back}>
      {changed && (
        <div role="alert" className="flex gap-2.5 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          <ShieldAlert className="mt-0.5 size-4 shrink-0" />
          <p>
            This machine proved a different identity than the one it was paired with. If Hive was not reinstalled there and its home not removed, another
            computer may be answering at its address. Trust it only if you know why it changed.
          </p>
        </div>
      )}
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="min-w-0 break-all">{v}</dd>
          </div>
        ))}
      </dl>
      <div className="rounded-lg border bg-muted/40 p-3">
        <div className="mb-1 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
          <Fingerprint className="size-3.5" /> Fingerprint
        </div>
        <code className="block font-mono text-sm break-all">{check.fingerprint}</code>
        {changed && check.pinned && (
          <>
            <div className="mt-2 mb-1 text-xs font-medium text-muted-foreground">Was</div>
            <code className="block font-mono text-sm break-all text-muted-foreground line-through">{check.pinned}</code>
          </>
        )}
      </div>
      <p className="text-xs text-muted-foreground">
        To compare, run <code>hive machine identity</code> on that machine.
      </p>
      <DialogFooter>
        <Button variant="outline" onClick={back}>
          {verified ? "Close" : "Cancel"}
        </Button>
        {!verified && (
          <Button variant={changed ? "destructive" : "default"} disabled={busy} onClick={confirm}>
            {busy && <Loader2 className="animate-spin" />}
            {changed ? "Trust the new identity" : d.pair ? `Trust and save as ${check.machine.name}` : "Pin this identity"}
          </Button>
        )}
      </DialogFooter>
    </Modal>
  );
}
