import { MotionConfig } from "framer-motion";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { toast } from "sonner";
import { api, isWindows, listen, type UpdateStatus } from "./api";
import { quotePaths } from "./keys";
import { runCommand } from "./commands";
import { App, PaneWindow } from "./components/App";
import { ObserverWindow } from "./components/ObserverWindow";
import { currentPane, show, store } from "./store";
import "./index.css";

// onUpdate keeps the update status; the main window says when a release
// arrives, and how the last update went.
function onUpdate(u: UpdateStatus, announce: boolean) {
  const prev = store.state.update;
  store.set((s) => ({ ...s, update: u }));
  if (!announce) return;
  if (u.result && !prev?.result) {
    if (u.result.ok) toast.success(`Hive updated to ${u.result.to}.`);
    else toast.error(`The update to ${u.result.to} did not work: ${u.result.reason ?? "it did not start"}. Hive ${u.result.from} is back.`, { duration: Infinity });
  }
  if (u.available && u.available.tag !== prev?.available?.tag) {
    toast(`Hive ${u.available.version} is available.`, {
      description: "The app restarts on it; agents keep running.",
      duration: Infinity,
      action: { label: "Restart to update", onClick: () => runCommand("install-update") },
    });
  }
}

// boot loads the current state and follows the app's events. announce:
// this window tells about updates (the main window).
export function boot(announce = true) {
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
    update: (u) => onUpdate(u, announce),
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
  api.updateStatus().then((u) => onUpdate(u, announce)).catch(() => {});
}

const route = /^#\/pane\/(.+)$/.exec(window.location.hash);
const envRoute = /^#\/env\/(.+)$/.exec(window.location.hash);
const observing = window.location.hash === "#/observe";
// An observer window loads nothing of the control side: no workspace, no
// screens, no commands.
if (!observing) boot(!route && !envRoute);
// An environment's own window opens on it, without the sidebar.
if (envRoute) store.setUI({ env: decodeURIComponent(envRoute[1]), sidebar: false });
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <MotionConfig reducedMotion="user">
      {observing ? <ObserverWindow /> : route ? <PaneWindow pane={decodeURIComponent(route[1])} /> : <App />}
    </MotionConfig>
  </StrictMode>,
);
