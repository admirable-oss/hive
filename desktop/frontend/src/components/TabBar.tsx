import { motion } from "framer-motion";
import { Columns2, ExternalLink, Maximize2, MessageSquareText, Minimize2, PanelLeft, Plus, Rows2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { api } from "../api";
import { agentOfPane, currentEnv, currentPane, currentTab, store, useStore } from "../store";
import { StateDot } from "./StateDot";

function IconAction({ label, onClick, children, pressed }: { label: string; onClick: () => void; children: React.ReactNode; pressed?: boolean }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label={label} aria-pressed={pressed} onClick={onClick} className="no-drag text-muted-foreground hover:text-foreground">
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

// TabBar shows the environment's tabs and acts on the focused pane.
export function TabBar({ mac }: { mac: boolean }) {
  const env = useStore(currentEnv);
  const tabs = useStore((s) => s.workspace.tabs).filter((t) => t.environment_id === env);
  const shown = useStore((s) => currentTab(s)?.id);
  const pane = useStore(currentPane);
  const agent = useStore((s) => agentOfPane(s, currentPane(s)?.id));
  const sidebar = useStore((s) => s.ui.sidebar);
  return (
    <header className={cn("drag flex h-13 shrink-0 items-center gap-1 border-b px-2", mac && !sidebar && "pl-[88px]")}>
      {!sidebar && (
        <IconAction label="Show sidebar" onClick={() => store.setUI({ sidebar: true })}>
          <PanelLeft />
        </IconAction>
      )}
      <div className="flex min-w-0 items-center gap-0.5 overflow-x-auto" role="tablist" aria-label="Tabs">
        {tabs.map((t) => {
          const current = t.id === shown;
          return (
            <button
              key={t.id}
              role="tab"
              aria-selected={current}
              className={cn(
                "no-drag relative flex h-8 shrink-0 items-center gap-2 rounded-md px-3 text-sm transition-colors",
                current ? "text-foreground" : "text-muted-foreground hover:text-foreground",
              )}
              onClick={() => {
                store.setUI({ tab: t.id, pane: undefined });
                api.focusTab(t.id).catch(store.fail);
              }}
            >
              {current && <motion.span layoutId="tab-current" className="absolute inset-0 rounded-md bg-accent" transition={{ type: "spring", stiffness: 500, damping: 40 }} />}
              <span className="relative flex items-center gap-2">
                <StateDot state={t.state} />
                {t.name || "shell"}
              </span>
            </button>
          );
        })}
      </div>
      {env && (
        <IconAction label="New tab" onClick={() => api.newTab(env).then((p) => store.reveal(p)).catch(store.fail)}>
          <Plus />
        </IconAction>
      )}
      <span className="flex-1" />
      <div className="flex items-center gap-0.5">
        {agent && (
          <Button variant="secondary" size="sm" className="no-drag mr-1" onClick={() => store.setUI({ dialog: { kind: "prompt", agent: agent.id } })}>
            <MessageSquareText /> Prompt
          </Button>
        )}
        {pane && (
          <>
            <IconAction label="Split right" onClick={() => api.splitPane(pane.id, "right").catch(store.fail)}>
              <Columns2 />
            </IconAction>
            <IconAction label="Split down" onClick={() => api.splitPane(pane.id, "down").catch(store.fail)}>
              <Rows2 />
            </IconAction>
            <IconAction label="Zoom pane" pressed={pane.zoomed} onClick={() => api.zoomPane(pane.id).catch(store.fail)}>
              {pane.zoomed ? <Minimize2 /> : <Maximize2 />}
            </IconAction>
            <IconAction label="Open in a new window" onClick={() => api.popOut(pane.id).catch(store.fail)}>
              <ExternalLink />
            </IconAction>
          </>
        )}
      </div>
    </header>
  );
}
