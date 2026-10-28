import { Loader2, ShieldAlert, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import { api, type Plugin } from "../api";
import { useStore } from "../store";
import { NativeSelect } from "./Field";
import { Modal } from "./Modal";

// Plugins (hive plugin list): what is installed, who stands behind each
// plugin (its tier and publisher, and the key that signed its release),
// what confines it, and any release the registry revoked, in red. The
// window only reads; `hive plugin` changes plugins.

// tierClass colors a tier's badge: official and verified stand out,
// community and unlisted do not.
function tierClass(tier: string): string {
  switch (tier) {
    case "official":
      return "border-primary/40 bg-primary/10 text-primary";
    case "verified":
      return "border-working/40 bg-working/10 text-working";
    default:
      return "border-border text-muted-foreground";
  }
}

export function TierBadge({ p }: { p: Plugin }) {
  const tier = p.tier || "unchecked";
  return (
    <span className={cn("rounded border px-1.5 py-px text-xs", tierClass(tier))} title={p.tier_meaning}>
      {tier}
    </span>
  );
}

function sandboxLabel(s: string): string {
  return s && s !== "none" ? `sandboxed (${s})` : "unsandboxed";
}

function PluginItem({ p }: { p: Plugin }) {
  return (
    <li className={cn("flex flex-col gap-1 rounded-md border border-border/60 p-2.5", p.revoked && "border-destructive/60")}>
      <div className="flex items-center gap-2">
        {p.revoked ? <ShieldAlert className="size-4 text-destructive" aria-hidden /> : <ShieldCheck className="size-4 text-muted-foreground" aria-hidden />}
        <span className="font-medium">{p.name}</span>
        <span className="text-xs text-muted-foreground">
          {p.id} {p.version}
        </span>
        <span className="flex-1" />
        <TierBadge p={p} />
      </div>
      {p.revoked && (
        <p role="alert" className="text-sm font-medium text-destructive">
          Revoked: {p.revoked}
          {p.advisory && (
            <>
              {" "}
              · <span className="font-normal">{p.advisory}</span>
            </>
          )}
        </p>
      )}
      <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-0.5 text-xs">
        <dt className="text-muted-foreground">Tier</dt>
        <dd>{p.tier_meaning}</dd>
        {p.publisher && (
          <>
            <dt className="text-muted-foreground">Publisher</dt>
            <dd>{p.publisher}</dd>
          </>
        )}
        {p.registry && (
          <>
            <dt className="text-muted-foreground">Signed by</dt>
            <dd className="truncate font-mono" title={p.signed_by}>
              {p.signed_by || "nobody (not signed)"}
            </dd>
          </>
        )}
        {p.provenance && (
          <>
            <dt className="text-muted-foreground">Built by</dt>
            <dd className="truncate">{p.provenance}</dd>
          </>
        )}
        <dt className="text-muted-foreground">Sandbox</dt>
        <dd>{sandboxLabel(p.sandbox)}</dd>
        <dt className="text-muted-foreground">State</dt>
        <dd className={cn(p.problem && "text-destructive")}>{p.problem || (p.enabled ? p.state || "ready" : "disabled")}</dd>
      </dl>
    </li>
  );
}

export function PluginsDialog() {
  const saved = useStore((s) => s.machines);
  const machines = saved.filter((m) => m.connection?.state === "connected");
  const [machine, setMachine] = useState("");
  const [plugins, setPlugins] = useState<Plugin[]>();
  const [err, setErr] = useState("");

  useEffect(() => {
    let live = true;
    api
      .plugins(machine)
      .then((list) => {
        if (live) {
          setPlugins(list ?? []);
          setErr("");
        }
      })
      .catch((e: unknown) => live && setErr(String(e)));
    return () => {
      live = false;
    };
  }, [machine]);

  return (
    <Modal title="Plugins" description="Official plugins are maintained by Hive's maintainers, verified publishers had their identity reviewed, community plugins passed automated checks only." className="sm:max-w-2xl">
      {machines.length > 0 && (
        <NativeSelect
          aria-label="Machine"
          value={machine}
          onChange={(e) => {
            setPlugins(undefined);
            setMachine(e.target.value);
          }}
        >
          <option value="">This computer</option>
          {machines.map((m) => (
            <option key={m.name} value={m.name}>
              {m.name}
            </option>
          ))}
        </NativeSelect>
      )}
      {err && (
        <p role="alert" className="text-sm text-destructive">
          {err}
        </p>
      )}
      {!plugins && !err && (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> Reading plugins…
        </p>
      )}
      {plugins &&
        (plugins.length === 0 ? (
          <p className="text-sm text-muted-foreground">No plugins installed. Find some with `hive plugin search`.</p>
        ) : (
          <ul className="flex max-h-[65vh] flex-col gap-2 overflow-y-auto text-sm">
            {plugins.map((p) => (
              <PluginItem key={p.id} p={p} />
            ))}
          </ul>
        ))}
    </Modal>
  );
}
