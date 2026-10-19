import { api } from "./api";
import { attention, currentEnv, currentPane, currentTab, show, store, writePref } from "./store";

// runCommand runs a named command: from a menu, the palette or a key.
export function runCommand(name: string) {
  const s = store.state;
  const pane = currentPane(s);
  const fail = store.fail;
  switch (name) {
    case "palette":
      store.setUI({ palette: true });
      break;
    case "new-agent":
      store.setUI({ dialog: { kind: "new-agent", env: currentEnv(s) } });
      break;
    case "new-environment":
      store.setUI({ dialog: { kind: "new-environment" } });
      break;
    case "machines":
      store.setUI({ dialog: { kind: "machines" } });
      break;
    case "prompt": {
      const agent = s.workspace.agents.find((a) => a.pane_id === pane?.id);
      store.setUI({ dialog: { kind: "prompt", agent: agent?.id } });
      break;
    }
    case "new-tab": {
      const env = currentEnv(s);
      if (env) api.newTab(env).then((p) => store.reveal(p)).catch(fail);
      break;
    }
    case "split-right":
    case "split-down":
      if (pane) api.splitPane(pane.id, name === "split-right" ? "right" : "down").then((p) => store.reveal(p)).catch(fail);
      break;
    case "zoom":
      if (pane) api.zoomPane(pane.id).catch(fail);
      break;
    case "pop-out":
      if (pane) api.popOut(pane.id).catch(fail);
      break;
    case "close-pane":
      if (pane) {
        const tab = currentTab(s);
        const onlyOne = s.workspace.panes.filter((p) => p.tab_id === tab?.id).length === 1;
        const run = () => (onlyOne && tab ? api.closeTab(tab.id) : api.closePane(pane.id)).catch(fail);
        if (pane.running) {
          store.setUI({
            dialog: {
              kind: "confirm",
              title: "Close this pane?",
              body: "Its process is still running; closing the pane stops it.",
              action: "Close pane",
              run,
            },
          });
        } else run();
      }
      break;
    case "next-attention": {
      const list = attention(s.workspace).filter((a) => a.pane_id);
      if (list.length === 0) {
        store.toast("No agent needs you.");
        break;
      }
      const at = list.findIndex((a) => a.pane_id === pane?.id);
      const next = list[(at + 1) % list.length];
      show(store, next.pane_id!);
      api.focusPane(next.pane_id!).catch(fail);
      break;
    }
    case "toggle-sidebar":
      store.setUI({ sidebar: !s.ui.sidebar });
      break;
    case "history":
      if (pane) store.setUI({ history: pane.id });
      break;
    case "toggle-option-meta": {
      const on = !s.ui.optionIsMeta;
      writePref("hive.optionIsMeta", on ? "1" : "0");
      store.setUI({ optionIsMeta: on });
      store.toast(on ? "Option now types Alt (Meta) in terminals." : "Option now types characters, as macOS does.");
      break;
    }
    case "toggle-login":
      api
        .startAtLogin()
        .then((on) => api.setStartAtLogin(!on).then(() => store.toast(on ? "Hive no longer starts when you log in." : "Hive starts when you log in.")))
        .catch(fail);
      break;
  }
}
