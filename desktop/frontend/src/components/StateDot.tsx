import { motion } from "framer-motion";
import { cn } from "@/lib/utils";

const tone: Record<string, string> = {
  blocked: "bg-blocked",
  working: "bg-working",
  done: "bg-done",
  idle: "bg-idle",
  unknown: "bg-idle",
  exited: "bg-transparent ring-1 ring-idle",
};

// StateDot is an agent's state as a coloured dot; blocked agents pulse.
export function StateDot({ state, className }: { state?: string; className?: string }) {
  if (!state) return null;
  return (
    <span role="img" aria-label={state} className={cn("relative inline-flex size-2 shrink-0", className)}>
      {state === "blocked" && (
        <motion.span
          className="absolute inset-0 rounded-full bg-blocked"
          initial={{ opacity: 0.6, scale: 1 }}
          animate={{ opacity: 0, scale: 2.4 }}
          transition={{ duration: 1.6, repeat: Infinity, ease: "easeOut" }}
        />
      )}
      <span className={cn("relative inline-flex size-2 rounded-full", tone[state] ?? "bg-idle")} />
    </span>
  );
}

// stateText colours a state's name.
export const stateText: Record<string, string> = {
  blocked: "text-blocked",
  working: "text-working",
  done: "text-done",
  idle: "text-muted-foreground",
};
