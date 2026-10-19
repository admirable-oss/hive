import { memo, useEffect, useMemo, useState } from "react";
import { listenObserved, observerApi, type ObservedScreen, type ObservedView, type Pane } from "../api";
import { StateDot } from "./Sidebar";
import { useScheme } from "./theme";

// The observer window: what an observer credential allows, live, on a
// shared screen. There is no input here at all; its page can call only
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
  if (!text) return <div className="obs-screen quiet">Waiting for the screen…</div>;
  return (
    <pre className="obs-screen" aria-label="Screen">
      {text.rows.join("\n")}
    </pre>
  );
});

export function ObserverWindow() {
  const scheme = useScheme();
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
    <div className={`app single observer scheme-${scheme}`} data-scheme={scheme}>
      <main className="main">
        <header className="obs-head">
          <span className="brand">Hive</span>
          <span className="obs-badge" role="status">
            Read-only
          </span>
          {obs && (
            <span className="quiet">
              {obs.scope} · {obs.screens ? "screens" : "states only"} · until {until}
            </span>
          )}
          <span className="tabbar-gap" />
          <label className="quiet obs-toggle">
            <input type="checkbox" checked={shells} onChange={(e) => setShells(e.target.checked)} /> Shells too
          </label>
        </header>
        {view?.state !== "connected" && (
          <div className={`banner banner-${view?.state === "ended" ? "offline" : "connecting"}`} role="alert">
            {view?.state === "ended" ? `${view.message}. Close this window.` : (view?.message ?? "Connecting…")}
          </div>
        )}
        <div className="obs-grid">
          {shown.map((p) => {
            const a = ws?.agents.find((x) => x.id === p.agent);
            return (
              <section key={p.id} className={`obs-card state-card-${a?.state ?? "none"}`} aria-label={a?.name ?? p.name ?? "shell"}>
                <div className="obs-card-head">
                  <StateDot state={a?.state} />
                  <strong>{a?.name ?? p.name ?? "shell"}</strong>
                  <span className="item-meta">{ws?.environments.find((e) => e.id === p.environment_id)?.name}</span>
                  <span className="tabbar-gap" />
                  {a && <span className={`obs-state state-text-${a.state}`}>{a.state}</span>}
                </div>
                {a?.reason && <div className="quiet obs-reason">{a.reason}</div>}
                {obs?.screens && watching.includes(p.id) && <ScreenText text={screens[p.id]} />}
              </section>
            );
          })}
          {ws && shown.length === 0 && <div className="empty">No agents to show.</div>}
        </div>
      </main>
    </div>
  );
}
