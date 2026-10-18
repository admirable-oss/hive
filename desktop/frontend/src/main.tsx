import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { api, listen } from "./api";
import { runCommand } from "./commands";
import { App, PaneWindow } from "./components/App";
import { show, store } from "./store";
import "./styles.css";

// boot loads the current state and follows the app's events.
export function boot() {
  store.onLost = (pane) => void api.refreshView(pane).catch(() => {});
  listen({
    connection: (c) => store.set((s) => ({ ...s, connection: c })),
    workspace: (w) => store.setWorkspace(w),
    screen: (u) => store.applyScreen(u),
    settings: (st) => store.set((s) => ({ ...s, settings: st })),
    focus: (pane) => show(store, pane),
    command: (name) => runCommand(name),
  });
  api.connection().then((c) => store.set((s) => ({ ...s, connection: c }))).catch(() => {});
  api.workspace().then((w) => store.setWorkspace(w)).catch(() => {});
  api.settings().then((st) => store.set((s) => ({ ...s, settings: st }))).catch(() => {});
}

const route = /^#\/pane\/(.+)$/.exec(window.location.hash);
boot();
createRoot(document.getElementById("root")!).render(
  <StrictMode>{route ? <PaneWindow pane={decodeURIComponent(route[1])} /> : <App />}</StrictMode>,
);
