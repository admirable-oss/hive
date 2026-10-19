import { AnimatePresence, motion } from "framer-motion";
import { FolderPlus, Loader2, Plus, RotateCw, WifiOff } from "lucide-react";
import { useEffect } from "react";
import { Button } from "@/components/ui/button";
import { Kbd } from "@/components/ui/kbd";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { api, isMac } from "../api";
import { runCommand } from "../commands";
import { currentTab, store, useStore } from "../store";
import { Bee } from "./Bee";
import { Dialogs } from "./Dialogs";
import { History } from "./History";
import { Palette } from "./Palette";
import { PaneGrid, SinglePane } from "./PaneGrid";
import { Sidebar } from "./Sidebar";
import { TabBar } from "./TabBar";
import { useApplyScheme } from "./theme";

// Banner says when the daemon cannot be reached, and why.
export function Banner() {
  const c = useStore((s) => s.connection);
  const text: Record<string, string> = {
    connecting: c.message || "Connecting to the hive daemon…",
    offline: `Cannot reach the hive daemon: ${c.message ?? "it does not answer"}. Agents keep running; retrying.`,
    outdated: c.message ?? "The hive daemon is older than this app.",
  };
  return (
    <AnimatePresence initial={false}>
      {c.state !== "connected" && (
        <motion.div
          key="banner"
          role="alert"
          initial={{ height: 0, opacity: 0 }}
          animate={{ height: "auto", opacity: 1 }}
          exit={{ height: 0, opacity: 0 }}
          className={cn("overflow-hidden border-b text-sm", c.state === "connecting" ? "bg-working/10" : "bg-destructive/10 text-destructive")}
        >
          <div className="flex items-center gap-3 px-4 py-2">
            {c.state === "connecting" ? <Loader2 className="size-4 animate-spin" /> : <WifiOff className="size-4" />}
            <span className="flex-1">{text[c.state] ?? c.state}</span>
            {c.state !== "connecting" && (
              <Button size="xs" variant="outline" onClick={() => api.retry().catch(store.fail)}>
                <RotateCw /> Retry now
              </Button>
            )}
          </div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}

function Empty({ mac }: { mac: boolean }) {
  const envs = useStore((s) => s.workspace.environments.length);
  const connected = useStore((s) => s.connection.state === "connected");
  if (!connected) return <div className="flex-1" />;
  return (
    <div className="grid flex-1 place-items-center p-8">
      <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.3 }} className="flex max-w-md flex-col items-center gap-4 text-center">
        <motion.div animate={{ y: [0, -4, 0] }} transition={{ duration: 2.4, repeat: Infinity, ease: "easeInOut" }}>
          <Bee className="text-[56px]" />
        </motion.div>
        <h1 className="text-xl font-semibold tracking-tight">{envs === 0 ? "Welcome to Hive" : "Nothing open here"}</h1>
        <p className="text-sm text-muted-foreground">
          {envs === 0
            ? "Make an environment for a project, then start an agent in it. Agents keep working when you close this window."
            : "Start an agent, or open a tab with a shell."}
        </p>
        <div className="flex gap-2">
          {envs === 0 ? (
            <Button onClick={() => runCommand("new-environment")}>
              <FolderPlus /> New environment
            </Button>
          ) : (
            <>
              <Button onClick={() => runCommand("new-agent")}>
                <Plus /> New agent
              </Button>
              <Button variant="outline" onClick={() => runCommand("new-tab")}>
                New tab
              </Button>
            </>
          )}
        </div>
        <p className="text-xs text-muted-foreground">
          <Kbd>{mac ? "⌘" : "Ctrl+Shift+"}K</Kbd> opens the command palette
        </p>
      </motion.div>
    </div>
  );
}

// useShortcuts adds the palette's own key, which webviews do not give a
// menu accelerator for when the menu is hidden (popped-out windows).
function useShortcuts(mac: boolean) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = mac ? e.metaKey : e.ctrlKey && e.shiftKey;
      if (mod && e.key.toLowerCase() === "k") {
        e.preventDefault();
        runCommand("palette");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mac]);
}

export function App() {
  const mac = isMac();
  const scheme = useApplyScheme();
  const tab = useStore(currentTab);
  const sidebar = useStore((s) => s.ui.sidebar);
  const palette = useStore((s) => s.ui.palette);
  useShortcuts(mac);
  return (
    <TooltipProvider delayDuration={400}>
      <div className={cn("grid h-full grid-rows-[100%] bg-background text-foreground", sidebar ? "grid-cols-[260px_1fr]" : "grid-cols-1")} data-scheme={scheme}>
        {sidebar && <Sidebar mac={mac} />}
        <main className="flex min-h-0 min-w-0 flex-col">
          <TabBar mac={mac} />
          <Banner />
          {tab ? <PaneGrid key={tab.id} tab={tab} /> : <Empty mac={mac} />}
        </main>
      </div>
      {palette && <Palette />}
      <History />
      <Dialogs />
      <Toaster position="bottom-right" richColors closeButton />
    </TooltipProvider>
  );
}

// PaneWindow is a window with one pane (#/pane/<id>).
export function PaneWindow({ pane: id }: { pane: string }) {
  const scheme = useApplyScheme();
  const pane = useStore((s) => s.workspace.panes.find((p) => p.id === id));
  useShortcuts(isMac());
  return (
    <TooltipProvider>
      <div className="flex h-full flex-col bg-background text-foreground" data-scheme={scheme}>
        <div className="drag h-8 shrink-0 border-b" />
        <Banner />
        {pane ? <SinglePane pane={pane} /> : <div className="grid flex-1 place-items-center text-sm text-muted-foreground">This pane is closed.</div>}
        <Toaster position="bottom-right" richColors />
      </div>
    </TooltipProvider>
  );
}
