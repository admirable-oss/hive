import { api } from "./api";
import { attention, currentEnv, currentPane, currentTab, show, store, writePref, type State } from "./store";

// cycle steps through items from the current one, by +1 or -1.
function cycle<T>(items: T[], at: number, step: number): T | undefined {
  if (items.length === 0) return undefined;
  return items[(((at < 0 ? 0 : at) + step) % items.length + items.length) % items.length];
}

// tabsOf are the tabs of the environment shown, in order.
const tabsOf = (s: State) => s.workspace.tabs.filter((t) => t.environment_id === currentEnv(s));

function showTab(id: string) {
  store.setUI({ tab: id, pane: undefined });
  api.focusTab(id).catch(store.fail);
}

// runCommand runs a named command: from a menu, the palette or a key.
export function runCommand(name: string) {
  const s = store.state;
  const pane = currentPane(s);
  const fail = store.fail;
  switch (name) {
    case "palette":
    case "goto":
      store.setUI({ palette: true });
      break;
    case "close-tab": {
      const tab = currentTab(s);
      if (!tab) break;
      const run = () => api.closeTab(tab.id).catch(fail);
      if (s.workspace.panes.some((p) => p.tab_id === tab.id && p.running)) {
        store.setUI({ dialog: { kind: "confirm", title: "Close this tab?", body: "Processes are still running in it; closing the tab stops them.", action: "Close tab", run } });
      } else run();
      break;
    }
    case "next-tab":
    case "prev-tab": {
      const tabs = tabsOf(s);
      const next = cycle(tabs, tabs.findIndex((t) => t.id === currentTab(s)?.id), name === "next-tab" ? 1 : -1);
      if (next) showTab(next.id);
      break;
    }
    case "next-env":
    case "prev-env": {
      const envs = s.workspace.environments;
      const next = cycle(envs, envs.findIndex((e) => e.id === currentEnv(s)), name === "next-env" ? 1 : -1);
      if (next) store.setUI({ env: next.id, tab: undefined, pane: undefined });
      break;
    }
    case "focus-left":
    case "focus-right":
    case "focus-up":
    case "focus-down":
      if (pane) {
        const dir = name.slice("focus-".length) as "left" | "right" | "up" | "down";
        // The daemon moves the focus; the tab's focused pane is then shown.
        api.focusNeighbour(pane.id, dir).then(() => store.setUI({ pane: undefined })).catch(fail);
      }
      break;
    case "focus-next":
    case "focus-prev":
    case "swap-next":
    case "swap-prev": {
      const tab = currentTab(s);
      const panes = s.workspace.panes.filter((p) => p.tab_id === tab?.id && !p.popup);
      const other = cycle(panes, panes.findIndex((p) => p.id === pane?.id), name.endsWith("next") ? 1 : -1);
      if (!pane || !other || other.id === pane.id) break;
      if (name.startsWith("focus")) {
        show(store, other.id);
        api.focusPane(other.id).catch(fail);
      } else api.swapPane(pane.id, other.id).catch(fail);
      break;
    }
    case "resize-left":
    case "resize-right":
    case "resize-up":
    case "resize-down":
      if (pane) api.resizePane(pane.id, name.slice("resize-".length) as "left" | "right" | "up" | "down", 2).catch(fail);
      break;
    case "paste":
      if (pane) api.pasteClipboard(pane.id).catch(fail);
      break;
    case "pop-out-env": {
      const env = currentEnv(s);
      if (env) api.openEnvironmentWindow(env).catch(fail);
      break;
    }
    case "check-updates":
      api
        .checkForUpdates()
        .then((u) => {
          store.set((st) => ({ ...st, update: u }));
          if (!u.available) store.toast(`Hive ${u.version} is up to date.`);
        })
        .catch(fail);
      break;
    case "install-update": {
      const u = s.update;
      if (!u?.available) {
        store.toast("No update is available. Check for one first.");
        break;
      }
      store.toast(`Installing Hive ${u.available.version}; the app restarts on it. Agents keep running.`);
      api.installUpdate().catch(fail);
      break;
    }
    case "toggle-update-checks": {
      const on = !(s.update?.automatic ?? true);
      api
        .setUpdateChecks(on)
        .then(() => store.toast(on ? "Hive checks for updates once a day." : "Hive no longer checks for updates by itself."))
        .catch(fail);
      break;
    }
    case "new-agent":
      store.setUI({ dialog: { kind: "new-agent", env: currentEnv(s) } });
      break;
    case "new-environment":
      store.setUI({ dialog: { kind: "new-environment" } });
      break;
    case "machines":
      store.setUI({ dialog: { kind: "machines" } });
      break;
    case "observe":
      store.setUI({ dialog: { kind: "observe" } });
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
    default: {
      const tab = /^tab-([1-9])$/.exec(name);
      const agent = /^focus-agent-([1-9])$/.exec(name);
      if (tab) {
        const t = tabsOf(s)[Number(tab[1]) - 1];
        if (t) showTab(t.id);
      } else if (agent) {
        const a = s.workspace.agents.filter((x) => x.pane_id)[Number(agent[1]) - 1];
        if (a) {
          show(store, a.pane_id!);
          api.focusPane(a.pane_id!).catch(fail);
        }
      }
      break;
    }
  }
}
