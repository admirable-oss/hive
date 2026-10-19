import { MotionConfig } from "framer-motion";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { api, isWindows, listen } from "./api";
import { quotePaths } from "./keys";
import { runCommand } from "./commands";
import { App, PaneWindow } from "./components/App";
import { ObserverWindow } from "./components/ObserverWindow";
import { currentPane, show, store } from "./store";
import "./index.css";

// boot loads the current state and follows the app's events.
export function boot() {
  store.onLost = (pane) => void api.refreshView(pane).catch(() => {});
  listen({
    connection: (c) => store.set((s) => ({ ...s, connection: c })),
    workspace: (w) => store.setWorkspace(w),
    screen: (u) => store.applyScreen(u),
    image: (u) => store.applyImage(u),
    settings: (st) => store.set((s) => ({ ...s, settings: st })),
    machines: (m) => store.set((s) => ({ ...s, machines: m })),
    focus: (pane) => show(store, pane),
    command: (name) => runCommand(name),
    drop: (paths) => {
      const pane = currentPane(store.state);
      if (pane && paths.length > 0) api.paste(pane.id, quotePaths(paths, isWindows()) + " ").catch(store.fail);
    },
  });
  const connection = () => api.connection().then((c) => store.set((s) => ({ ...s, connection: c }))).catch(() => {});
  void connection();
  // Events sent while the window was still loading are lost: a daemon the
  // app starts itself often connects just then. So the state is read again
  // until it settles.
  setInterval(() => {
    if (store.state.connection.state !== "connected") void connection();
  }, 1000);
  api.workspace().then((w) => store.setWorkspace(w)).catch(() => {});
  api.settings().then((st) => store.set((s) => ({ ...s, settings: st }))).catch(() => {});
  api.machines().then((m) => store.set((s) => ({ ...s, machines: m ?? [] }))).catch(() => {});
}

const route = /^#\/pane\/(.+)$/.exec(window.location.hash);
const observing = window.location.hash === "#/observe";
// An observer window loads nothing of the control side: no workspace, no
// screens, no commands.
if (!observing) boot();
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <MotionConfig reducedMotion="user">
      {observing ? <ObserverWindow /> : route ? <PaneWindow pane={decodeURIComponent(route[1])} /> : <App />}
    </MotionConfig>
  </StrictMode>,
);
