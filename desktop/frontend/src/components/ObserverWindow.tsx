import { AnimatePresence, motion } from "framer-motion";
import { Eye, Loader2, ShieldOff } from "lucide-react";
import { memo, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import { listenObserved, observerApi, type ObservedScreen, type ObservedView, type Pane } from "../api";
import { Bee } from "./Bee";
import { StateDot, stateText } from "./StateDot";
import { useApplyScheme } from "./theme";

// The observer window: what an observer credential allows, live, on a
// shared screen. There is no way to type here; its page can call only
// ObserverService, which reads.

const MAX_WATCHED = 12;

interface Text {
  rows: string[];
  ended: boolean;
}

function apply(prev: Text | undefined, s: ObservedScreen): Text {
  const rows = s.keyframe || !prev ? Array.from({ length: s.height }, () => "") : prev.rows.slice(0, s.height);
  while (rows.length < s.height) rows.push("");
  for (const l of s.lines ?? []) if (l.y >= 0 && l.y < rows.length) rows[l.y] = l.text;
  return { rows, ended: !!s.ended };
}

const ScreenText = memo(function ScreenText({ text }: { text?: Text }) {
  if (!text)
    return (
      <div className="flex items-center gap-2 border-t bg-term-bg px-3 py-4 text-xs text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" /> Waiting for the screen…
      </div>
    );
  return (
    <pre className="max-h-80 overflow-hidden border-t bg-term-bg px-3 py-2 font-mono text-xs leading-[1.3] text-term-fg" aria-label="Screen">
      {text.rows.join("\n")}
    </pre>
  );
});

export function ObserverWindow() {
  useApplyScheme();
  const [view, setView] = useState<ObservedView>();
  const [screens, setScreens] = useState<Record<string, Text>>({});
  const [shells, setShells] = useState(false);

  useEffect(() => {
    observerApi.view().then(setView).catch(() => {});
    return listenObserved(setView, (s) => setScreens((all) => ({ ...all, [s.pane]: apply(all[s.pane], s) })));
  }, []);

  const ws = view?.workspace;
  const shown = useMemo<Pane[]>(() => {
    if (!ws) return [];
    const agents = ws.panes.filter((p) => p.agent);
    return shells ? [...agents, ...ws.panes.filter((p) => !p.agent)] : agents;
  }, [ws, shells]);
  const watching = view?.observer.screens ? shown.slice(0, MAX_WATCHED).map((p) => p.id) : [];
  const key = watching.join(",");

  useEffect(() => {
    const panes = key ? key.split(",") : [];
    panes.forEach((p) => void observerApi.watch(p).catch(() => {}));
    return () => panes.forEach((p) => void observerApi.unwatch(p).catch(() => {}));
  }, [key]);

  const obs = view?.observer;
  const until = obs ? new Date(obs.expires_at).toLocaleString() : "";
  return (
    <div className="flex h-full flex-col bg-background text-foreground">
      <header className="drag flex h-13 shrink-0 items-center gap-3 border-b pr-4 pl-[88px]">
        <span className="flex items-center gap-2 text-sm leading-none font-semibold">
          <Bee className="text-[18px]" /> Hive
        </span>
        <Badge variant="secondary" role="status">
          <Eye /> Read-only
        </Badge>
        {obs && (
          <span className="truncate text-xs text-muted-foreground">
            {obs.scope} · {obs.screens ? "screens" : "states only"} · until {until}
          </span>
        )}
        <span className="flex-1" />
        <Label className="no-drag text-xs font-normal text-muted-foreground">
          <Switch checked={shells} onCheckedChange={setShells} aria-label="Shells too" /> Shells too
        </Label>
      </header>
      <AnimatePresence>
        {view && view.state !== "connected" && (
          <motion.div
            role="alert"
            initial={{ height: 0 }}
            animate={{ height: "auto" }}
            exit={{ height: 0 }}
            className={cn("overflow-hidden border-b text-sm", view.state === "ended" ? "bg-destructive/10 text-destructive" : "bg-working/10")}
          >
            <div className="flex items-center gap-2 px-4 py-2">
              {view.state === "ended" ? <ShieldOff className="size-4" /> : <Loader2 className="size-4 animate-spin" />}
              {view.state === "ended" ? `${view.message}. Close this window.` : (view.message ?? "Connecting…")}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
      <div className="grid min-h-0 flex-1 auto-rows-min grid-cols-[repeat(auto-fill,minmax(420px,1fr))] gap-3 overflow-auto p-4">
        <AnimatePresence initial={false}>
          {shown.map((p) => {
            const a = ws?.agents.find((x) => x.id === p.agent);
            return (
              <motion.section
                layout
                key={p.id}
                initial={{ opacity: 0, scale: 0.98 }}
                animate={{ opacity: 1, scale: 1 }}
                exit={{ opacity: 0, scale: 0.98 }}
                aria-label={a?.name ?? p.name ?? "shell"}
                className={cn("overflow-hidden rounded-xl border bg-card shadow-sm", a?.state === "blocked" && "border-blocked/60 ring-1 ring-blocked/30")}
              >
                <div className="flex items-center gap-2 px-3 py-2.5">
                  <StateDot state={a?.state} />
                  <strong className="text-sm">{a?.name ?? p.name ?? "shell"}</strong>
                  <span className="truncate text-xs text-muted-foreground">{ws?.environments.find((e) => e.id === p.environment_id)?.name}</span>
                  <span className="flex-1" />
                  {a && <span className={cn("text-xs font-medium", stateText[a.state])}>{a.state}</span>}
                </div>
                {a?.reason && <div className="px-3 pb-2 text-xs text-muted-foreground">{a.reason}</div>}
                {obs?.screens && watching.includes(p.id) && <ScreenText text={screens[p.id]} />}
              </motion.section>
            );
          })}
        </AnimatePresence>
        {ws && shown.length === 0 && <div className="col-span-full grid place-items-center py-16 text-sm text-muted-foreground">No agents to show.</div>}
      </div>
    </div>
  );
}
