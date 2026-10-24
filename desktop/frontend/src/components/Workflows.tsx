import { motion } from "framer-motion";
import { RotateCcw, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { api, type WorkflowRun } from "../api";
import { store } from "../store";
import { StateDot } from "./StateDot";

// runState is the agent state a run's dot shows: working while it runs,
// then how it ended.
function runState(r: WorkflowRun): string {
  switch (r.state) {
    case "queued":
      return "idle";
    case "running":
      return "working";
    case "succeeded":
      return "done";
    case "cancelled":
      return "unknown";
    default:
      return "exited";
  }
}

function progress(r: WorkflowRun): string {
  const parts = [`${r.done}/${r.total} ${r.state}`];
  if (r.failed > 0) parts.push(`${r.failed} failed`);
  if ((r.current ?? []).length > 0) parts.push(`now ${(r.current ?? []).join(", ")}`);
  return parts.join(" · ");
}

// finishedShown is how many finished runs the sidebar keeps besides the
// ones going on.
const finishedShown = 3;

// visibleRuns are the runs going on, then the latest that finished.
export function visibleRuns(runs: WorkflowRun[]): WorkflowRun[] {
  let finished = 0;
  return runs.filter((r) => {
    if (r.state === "queued" || r.state === "running") return true;
    finished++;
    return finished <= finishedShown;
  });
}

// WorkflowItem is one workflow run: what started it, its steps' progress,
// and Cancel while it goes on, Run again once it ended.
export function WorkflowItem({ r }: { r: WorkflowRun }) {
  const going = r.state === "queued" || r.state === "running";
  return (
    <motion.li layout initial={{ opacity: 0, x: -8 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: -8 }} transition={{ duration: 0.18 }}>
      <div className="flex items-center gap-2.5 px-2 pt-1.5 text-sm" title={r.reason || `${r.workflow} · ${r.trigger}`}>
        <StateDot state={runState(r)} />
        <span className="min-w-0 flex-1 truncate font-medium">{r.workflow}</span>
        <span className="text-xs text-muted-foreground">{r.trigger}</span>
        {going ? (
          <Button variant="ghost" size="icon-xs" aria-label={`Cancel run ${r.id}`} title="Cancel the run" onClick={() => api.cancelWorkflowRun(r.id).catch(store.fail)}>
            <X />
          </Button>
        ) : (
          <Button variant="ghost" size="icon-xs" aria-label={`Run ${r.id} again`} title="Run again, with the same inputs" onClick={() => api.rerunWorkflow(r.id).catch(store.fail)}>
            <RotateCcw />
          </Button>
        )}
      </div>
      <p className={cn("pl-7.5 pr-2 text-xs text-muted-foreground", r.state === "failed" && "text-destructive")} title={r.reason || undefined}>
        {r.machine ? `${r.machine} · ` : ""}
        {progress(r)}
      </p>
    </motion.li>
  );
}
