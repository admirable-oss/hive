import { api, type Agent } from "../api";
import { attention, currentTab, show, store, useStore } from "../store";

export function StateDot({ state, label }: { state?: string; label?: boolean }) {
  if (!state) return null;
  return (
    <span className={`dot state-${state}`} role="img" aria-label={state}>
      {label && <span className="dot-label">{state}</span>}
    </span>
  );
}

function AgentItem({ a }: { a: Agent }) {
  return (
    <li>
      <button
        className="item"
        onClick={() => {
          if (a.pane_id) {
            show(store, a.pane_id);
            api.focusPane(a.pane_id).catch(store.fail);
          }
        }}
        title={a.reason || a.state}
      >
        <StateDot state={a.state} />
        <span className="item-name">{a.name}</span>
        <span className="item-meta">{a.machine ? `${a.machine} · ` : ""}{a.environment_id.split("/").pop()}</span>
      </button>
    </li>
  );
}

// Sidebar lists what needs you, then every environment with its tabs.
export function Sidebar({ mac }: { mac: boolean }) {
  const ws = useStore((s) => s.workspace);
  const shown = useStore((s) => currentTab(s)?.id);
  const conn = useStore((s) => s.connection);
  const needs = attention(ws);
  const machines = useStore((s) => s.machines);
  const names = ["", ...new Set(ws.environments.map((e) => e.machine ?? "").filter(Boolean))];
  const groups = names.map((machine) => ({ machine, envs: ws.environments.filter((e) => (e.machine ?? "") === machine) })).filter((g) => g.envs.length > 0);
  return (
    <nav className="sidebar" aria-label="Environments and agents">
      <div className={"sidebar-head" + (mac ? " mac" : "")}>
        <span className="brand">Hive</span>
        <span className={`conn conn-${conn.state}`} title={conn.message || conn.state} role="status">
          {conn.state === "connected" ? `${conn.session}` : conn.state}
        </span>
      </div>
      <div className="sidebar-body">
        <section aria-labelledby="needs-you">
          <h2 id="needs-you">Needs you</h2>
          {needs.length === 0 ? (
            <p className="quiet">No agent is waiting.</p>
          ) : (
            <ul>
              {needs.map((a) => (
                <AgentItem key={a.id} a={a} />
              ))}
            </ul>
          )}
        </section>
        <section aria-labelledby="envs">
          <h2 id="envs">
            Environments
            <button className="icon" aria-label="New environment" title="New environment" onClick={() => store.setUI({ dialog: { kind: "new-environment" } })}>
              +
            </button>
          </h2>
          {ws.environments.length === 0 && <p className="quiet">No environments yet.</p>}
          {groups.map((g) => (
            <div key={g.machine} className="machine-group">
              {groups.length > 1 && <h3 className="machine-head">{g.machine || "This computer"}</h3>}
              <ul>
                {g.envs.map((e) => (
                  <li key={e.id} className="env">
                    <div className="env-head">
                      <StateDot state={e.state} />
                      <span className="item-name" title={e.path}>
                        {e.name}
                      </span>
                      {e.branch && (
                        <span className="branch">
                          {e.branch}
                          {e.dirty ? "*" : ""}
                        </span>
                      )}
                    </div>
                    <ul>
                      {ws.tabs
                        .filter((t) => t.environment_id === e.id)
                        .map((t) => {
                          const agents = ws.agents.filter((a) => a.tab_id === t.id);
                          return (
                            <li key={t.id}>
                              <button
                                className={"item tab-item" + (t.id === shown ? " current" : "")}
                                aria-current={t.id === shown ? "page" : undefined}
                                onClick={() => {
                                  store.setUI({ env: e.id, tab: t.id, pane: undefined });
                                  api.focusTab(t.id).catch(store.fail);
                                }}
                              >
                                <StateDot state={t.state} />
                                <span className="item-name">{t.name || agents[0]?.name || "shell"}</span>
                                {agents.length > 1 && <span className="item-meta">{agents.length} agents</span>}
                              </button>
                            </li>
                          );
                        })}
                    </ul>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </section>
        <section aria-labelledby="machines">
          <h2 id="machines">
            Machines
            <button className="icon" aria-label="Manage machines" title="Manage machines" onClick={() => store.setUI({ dialog: { kind: "machines" } })}>
              ⋯
            </button>
          </h2>
          {machines.length === 0 ? (
            <p className="quiet">
              <button className="link-button" onClick={() => store.setUI({ dialog: { kind: "machines" } })}>
                Add a machine
              </button>{" "}
              to see its agents here.
            </p>
          ) : (
            <ul>
              {machines.map((m) => (
                <li key={m.name}>
                  <button className="item" onClick={() => store.setUI({ dialog: { kind: "machines" } })} title={m.connection?.message || m.host}>
                    <span className={`conn-dot conn-${m.connection?.state ?? "off"}`} aria-hidden />
                    <span className="item-name">{m.name}</span>
                    {m.state === "changed" ? (
                      <span className="warn item-meta">identity changed</span>
                    ) : (
                      <span className="item-meta">{(m.counts?.blocked ?? 0) > 0 ? `${m.counts.blocked} need you` : m.connection?.state === "connected" ? "" : (m.connection?.state ?? "off")}</span>
                    )}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
      <div className="sidebar-foot">
        <button className="primary" onClick={() => store.setUI({ dialog: { kind: "new-agent" } })}>
          New agent
        </button>
      </div>
    </nav>
  );
}
