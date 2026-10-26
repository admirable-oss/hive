import { Loader2, Play } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { api, type BudgetStatus, type Insights as InsightsData, type InsightsRow } from "../api";
import { store, useStore } from "../store";
import { NativeSelect } from "./Field";
import { Modal } from "./Modal";

// Insights (hive insights): today's time in each state and usage by agent
// kind, the times agents waited on you and what they asked, and the
// budgets, with Resume for one that holds new tasks. Time grows by itself,
// so the window reads them again while it is open.

// insightsEvery is how often an open window reads insights again.
const insightsEvery = 15_000;

// duration is milliseconds, roughly: 1h 20m, 12m, 45s, 0.
export function duration(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s <= 0) return "0";
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}

// count is 950, 12.3k or 4.5M.
function count(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1e6) return `${(n / 1e3).toFixed(1).replace(/\.0$/, "")}k`;
  return `${(n / 1e6).toFixed(1).replace(/\.0$/, "")}M`;
}

function tokens(r: InsightsRow): string {
  return r.tokens === null || r.tokens === undefined ? "unknown" : count(r.tokens);
}

function cost(r: InsightsRow): string {
  return r.cost_usd === null || r.cost_usd === undefined ? "unknown" : `$${r.cost_usd.toFixed(2)}`;
}

function budgetName(b: BudgetStatus): string {
  return b.per === "day" ? b.name : `${b.name} · ${b.key}`;
}

function Row({ r, total }: { r: InsightsRow; total?: boolean }) {
  return (
    <tr className={cn("border-t border-border/60", total && "font-medium")}>
      <th scope="row" className="py-1.5 pr-3 text-left font-medium">
        {total ? "Total" : r.key}
      </th>
      <td className="pr-3 text-working">{duration(r.working_ms)}</td>
      <td className="pr-3 text-blocked">{duration(r.blocked_ms)}</td>
      <td className="pr-3 text-muted-foreground">{duration(r.idle_ms)}</td>
      <td className="pr-3">
        {duration(r.waiting_ms)}
        {r.asks > 0 && <span className="text-muted-foreground"> ({r.asks})</span>}
      </td>
      <td className={cn("pr-3", r.tokens == null && "text-muted-foreground")}>{tokens(r)}</td>
      <td className={cn("pr-3", r.cost_usd == null && "text-muted-foreground")}>{cost(r)}</td>
      <td className="text-xs text-muted-foreground">{(r.sources ?? []).join(", ") || "-"}</td>
    </tr>
  );
}

export function InsightsDialog({ machine: first = "" }: { machine?: string }) {
  const saved = useStore((s) => s.machines);
  const machines = saved.filter((m) => m.connection?.state === "connected");
  const [machine, setMachine] = useState(first);
  const [data, setData] = useState<InsightsData>();
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let live = true;
    api
      .insights(machine)
      .then((d) => {
        if (live) {
          setData(d);
          setErr("");
        }
      })
      .catch((e: unknown) => live && setErr(String(e)));
    const t = setTimeout(() => setTick((n) => n + 1), insightsEvery);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [machine, tick]);

  const resume = (b: BudgetStatus) => {
    setBusy(b.name + "/" + b.key);
    api
      .resumeBudget(machine, b.name, b.per === "day" ? "" : b.key)
      .then(() => setTick((n) => n + 1))
      .catch(store.fail)
      .finally(() => setBusy(""));
  };

  return (
    <Modal title="Insights" description="Where agents' time went today and what they reported using. Kept on this machine only." className="sm:max-w-3xl">
      {machines.length > 0 && (
        <NativeSelect aria-label="Machine" value={machine} onChange={(e) => setMachine(e.target.value)}>
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
      {!data && !err && (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> Reading insights…
        </p>
      )}
      {data && (
        <div className="flex max-h-[65vh] flex-col gap-5 overflow-y-auto text-sm">
          <section aria-label="Today by kind">
            {data.rows.length === 0 ? (
              <p className="text-muted-foreground">Nothing recorded today yet.</p>
            ) : (
              <table className="w-full tabular-nums">
                <thead className="text-xs text-muted-foreground">
                  <tr>
                    <th scope="col" className="pb-1 text-left font-normal">Kind</th>
                    <th scope="col" className="pb-1 text-left font-normal">Working</th>
                    <th scope="col" className="pb-1 text-left font-normal">Blocked</th>
                    <th scope="col" className="pb-1 text-left font-normal">Idle</th>
                    <th scope="col" className="pb-1 text-left font-normal">Waiting on you</th>
                    <th scope="col" className="pb-1 text-left font-normal">Tokens</th>
                    <th scope="col" className="pb-1 text-left font-normal">Cost</th>
                    <th scope="col" className="pb-1 text-left font-normal">Source</th>
                  </tr>
                </thead>
                <tbody>
                  {data.rows.map((r) => (
                    <Row key={r.key} r={r} />
                  ))}
                  {data.rows.length > 1 && <Row r={data.total} total />}
                </tbody>
              </table>
            )}
          </section>

          <section aria-label="Budgets">
            <h3 className="mb-1.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase">Budgets</h3>
            {data.budgets.length === 0 ? (
              <p className="text-muted-foreground">No budgets: add [[insights.budget]] to Hive's configuration.</p>
            ) : (
              <ul className="flex flex-col gap-1.5">
                {data.budgets.map((b) => (
                  <li key={b.name + "/" + b.key} className="flex items-center gap-3">
                    <span className="min-w-0 flex-1 truncate">
                      <span className="font-medium">{budgetName(b)}</span>
                      <span className="text-muted-foreground"> · {b.used || "no limits"}</span>
                    </span>
                    <span className={cn("tabular-nums", b.level === "exceeded" ? "text-destructive" : b.level === "warning" ? "text-blocked" : "text-muted-foreground")}>
                      {Math.round(b.fraction * 100)}%{b.level ? ` ${b.level}` : ""}
                    </span>
                    {b.held ? (
                      <Button size="xs" variant="secondary" aria-label={`Resume ${budgetName(b)}`} disabled={busy === b.name + "/" + b.key} onClick={() => resume(b)}>
                        <Play /> Resume
                      </Button>
                    ) : (
                      b.resumed && <span className="text-xs text-muted-foreground">resumed</span>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </section>

          <section aria-label="Asks">
            <h3 className="mb-1.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase">Waiting on you</h3>
            {data.asks.length === 0 ? (
              <p className="text-muted-foreground">No agent waited on you today.</p>
            ) : (
              <ul className="flex flex-col gap-1">
                {data.asks.map((a, i) => (
                  <li key={i} className="flex gap-3">
                    <span className="w-14 shrink-0 text-muted-foreground tabular-nums">{a.start ? new Date(a.start).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : ""}</span>
                    <span className="min-w-0 flex-1">
                      <span className="font-medium">{a.who}</span>
                      <span className="text-muted-foreground">
                        {" "}
                        · {a.environment}
                        {a.task ? ` · ${a.task}` : ""} · {a.open ? `waiting ${duration(a.waited_ms)}` : `waited ${duration(a.waited_ms)}`}
                      </span>
                      {a.reason && <span className="block truncate text-muted-foreground" title={a.reason}>“{a.reason}”</span>}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>
      )}
    </Modal>
  );
}
