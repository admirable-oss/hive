import { api } from "../api";
import { agentOfPane, currentEnv, currentPane, currentTab, store, useStore } from "../store";
import { StateDot } from "./Sidebar";

// TabBar shows the environment's tabs and acts on the focused pane.
export function TabBar({ mac }: { mac: boolean }) {
  const env = useStore(currentEnv);
  const tabs = useStore((s) => s.workspace.tabs).filter((t) => t.environment_id === env);
  const shown = useStore((s) => currentTab(s)?.id);
  const pane = useStore(currentPane);
  const agent = useStore((s) => agentOfPane(s, currentPane(s)?.id));
  const sidebar = useStore((s) => s.ui.sidebar);
  return (
    <header className={"tabbar" + (mac && !sidebar ? " mac" : "")}>
      <div className="tabs" role="tablist" aria-label="Tabs">
        {tabs.map((t) => (
          <button
            key={t.id}
            role="tab"
            aria-selected={t.id === shown}
            className={"tab" + (t.id === shown ? " current" : "")}
            onClick={() => {
              store.setUI({ tab: t.id, pane: undefined });
              api.focusTab(t.id).catch(store.fail);
            }}
          >
            <StateDot state={t.state} />
            {t.name || "shell"}
          </button>
        ))}
      </div>
      {env && (
        <button className="icon" aria-label="New tab" title="New tab" onClick={() => api.newTab(env).then((p) => store.reveal(p)).catch(store.fail)}>
          +
        </button>
      )}
      <span className="tabbar-gap" />
      <div className="actions">
        {agent && (
          <button onClick={() => store.setUI({ dialog: { kind: "prompt", agent: agent.id } })} title="Send a prompt to this agent">
            Prompt
          </button>
        )}
        {pane && (
          <>
            <button className="icon" aria-label="Split right" title="Split right" onClick={() => api.splitPane(pane.id, "right").catch(store.fail)}>
              ⬓
            </button>
            <button className="icon" aria-label="Split down" title="Split down" onClick={() => api.splitPane(pane.id, "down").catch(store.fail)}>
              ⬒
            </button>
            <button className="icon" aria-label="Zoom pane" title="Zoom pane" aria-pressed={pane.zoomed} onClick={() => api.zoomPane(pane.id).catch(store.fail)}>
              ⤢
            </button>
            <button className="icon" aria-label="Open in a new window" title="Open in a new window" onClick={() => api.popOut(pane.id).catch(store.fail)}>
              ⧉
            </button>
          </>
        )}
      </div>
    </header>
  );
}
