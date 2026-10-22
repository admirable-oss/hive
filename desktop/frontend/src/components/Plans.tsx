import { motion } from "framer-motion";
import { Check, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { api, type Plan } from "../api";
import { show, store } from "../store";
import { StateDot } from "./StateDot";

// planState is the agent state a plan's dot shows: blocked while a task
// waits for someone, working while it runs, then how it ended.
function planState(p: Plan): string {
  if ((p.waiting ?? []).length > 0) return "blocked";
  switch (p.state) {
    case "running":
      return "working";
    case "succeeded":
      return "done";
    default:
      return "exited";
  }
}

function progress(p: Plan): string {
  const c = p.counts ?? {};
  const parts = [`${p.done}/${p.total}`];
  for (const s of ["running", "blocked", "queued", "failed"]) {
    if ((c[s] ?? 0) > 0) parts.push(`${c[s]} ${s}`);
  }
  return parts.join(" · ");
}

// PlanItem is one plan: its progress, and the tasks waiting for someone,
// which an approval starts (or a refusal cancels) and a blocked agent's
// pane answers.
export function PlanItem({ p }: { p: Plan }) {
  const label = p.name || p.id;
  return (
    <motion.li layout initial={{ opacity: 0, x: -8 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: -8 }} transition={{ duration: 0.18 }}>
      <div className="flex items-center gap-2.5 px-2 pt-1.5 text-sm" title={p.reason || p.state}>
        <StateDot state={planState(p)} />
        <span className="min-w-0 flex-1 truncate font-medium">{label}</span>
        {p.state === "running" && (
          <Button variant="ghost" size="icon-xs" aria-label={`Cancel plan ${label}`} title="Cancel the plan" onClick={() => api.cancelPlan(p.id).catch(store.fail)}>
            <X />
          </Button>
        )}
      </div>
      <p className={cn("pl-7.5 pr-2 text-xs text-muted-foreground", p.state === "failed" && "text-destructive")}>
        {p.machine ? `${p.machine} · ` : ""}
        {progress(p)}
      </p>
      {(p.waiting ?? []).length > 0 && (
        <ul className="mt-1 space-y-1 pl-5.5">
          {(p.waiting ?? []).map((t) =>
            t.state === "approval" ? (
              <li key={t.id} className="flex items-center gap-1.5 rounded-md bg-blocked/10 px-2 py-1 text-xs">
                <span className="min-w-0 flex-1 truncate" title={t.title || t.id}>
                  {t.id} waits for approval
                </span>
                <Button size="xs" aria-label={`Approve ${t.id}`} onClick={() => api.approveTask(p.id, t.id, false).catch(store.fail)}>
                  <Check /> Approve
                </Button>
                <Button size="xs" variant="ghost" aria-label={`Refuse ${t.id}`} onClick={() => api.approveTask(p.id, t.id, true).catch(store.fail)}>
                  Refuse
                </Button>
              </li>
            ) : (
              <li key={t.id}>
                <button
                  className="w-full truncate rounded-md bg-blocked/10 px-2 py-1 text-left text-xs hover:bg-blocked/20"
                  onClick={() => {
                    if (t.pane_id) {
                      show(store, t.pane_id);
                      api.focusPane(t.pane_id).catch(store.fail);
                    }
                  }}
                >
                  {t.id}: its agent needs you
                </button>
              </li>
            ),
          )}
        </ul>
      )}
    </motion.li>
  );
}
