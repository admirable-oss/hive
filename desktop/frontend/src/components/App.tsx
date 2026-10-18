import { useEffect } from "react";
import { api, isMac } from "../api";
import { runCommand } from "../commands";
import { currentTab, store, useStore } from "../store";
import { Dialogs, Toasts } from "./Dialogs";
import { Palette } from "./Palette";
import { PaneGrid, SinglePane } from "./PaneGrid";
import { Sidebar } from "./Sidebar";
import { TabBar } from "./TabBar";
import { useScheme } from "./theme";

// Banner says when the daemon cannot be reached, and why.
export function Banner() {
  const c = useStore((s) => s.connection);
  if (c.state === "connected") return null;
  const text: Record<string, string> = {
    connecting: c.message || "Connecting to the hive daemon…",
    offline: `Cannot reach the hive daemon: ${c.message ?? "it does not answer"}. Agents keep running; retrying.`,
    outdated: c.message ?? "The hive daemon is older than this app.",
  };
  return (
    <div className={`banner banner-${c.state}`} role="alert">
      <span>{text[c.state] ?? c.state}</span>
      {c.state !== "connecting" && <button onClick={() => api.retry().catch(store.fail)}>Retry now</button>}
    </div>
  );
}

function Empty() {
  const envs = useStore((s) => s.workspace.environments.length);
  const connected = useStore((s) => s.connection.state === "connected");
  if (!connected) return <div className="empty" />;
  return (
    <div className="empty">
      <h1>{envs === 0 ? "Welcome to Hive" : "Nothing open here"}</h1>
      <p>
        {envs === 0
          ? "Make an environment for a project, then start an agent in it. Agents keep working when you close this window."
          : "Start an agent, or open a tab with a shell."}
      </p>
      <div className="buttons">
        {envs === 0 ? (
          <button className="primary" onClick={() => runCommand("new-environment")}>
            New environment
          </button>
        ) : (
          <>
            <button className="primary" onClick={() => runCommand("new-agent")}>
              New agent
            </button>
            <button onClick={() => runCommand("new-tab")}>New tab</button>
          </>
        )}
      </div>
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
  const scheme = useScheme();
  const tab = useStore(currentTab);
  const sidebar = useStore((s) => s.ui.sidebar);
  const palette = useStore((s) => s.ui.palette);
  useShortcuts(mac);
  return (
    <div className={`app scheme-${scheme}` + (sidebar ? "" : " no-sidebar")} data-scheme={scheme}>
      {sidebar && <Sidebar mac={mac} />}
      <main className="main">
        <Banner />
        <TabBar mac={mac} />
        {tab ? <PaneGrid key={tab.id} tab={tab} /> : <Empty />}
      </main>
      {palette && <Palette />}
      <Dialogs />
      <Toasts />
    </div>
  );
}

// PaneWindow is a window with one pane (#/pane/<id>).
export function PaneWindow({ pane: id }: { pane: string }) {
  const scheme = useScheme();
  const pane = useStore((s) => s.workspace.panes.find((p) => p.id === id));
  useShortcuts(isMac());
  return (
    <div className={`app single scheme-${scheme}`} data-scheme={scheme}>
      <main className="main">
        <Banner />
        {pane ? <SinglePane pane={pane} /> : <div className="empty">This pane is closed.</div>}
      </main>
      <Toasts />
    </div>
  );
}
