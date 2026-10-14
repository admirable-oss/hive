import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { init } from "@hive-oss/plugin-sdk";

const plugin = await init({ id: "auto-tab-title", version: "0.1.0" });
try {
  if (plugin.context.trigger !== "event" || !plugin.context.event) process.exitCode = 0;
  else {
    const eventData = plugin.context.event.data as { id?: string } | undefined;
    const processId = eventData?.id;
    if (processId) {
      const workspace = await plugin.hive.workspace.snapshot();
      const pane = workspace.panes.find(item => item.process_id === processId);
      const tab = pane && workspace.tabs.find(item => item.id === pane.tab_id);
      if (pane && tab) {
        const agent = workspace.agents.find(item => item.id === processId || item.pane_id === pane.id);
        const terminal = await plugin.hive.terminal.snapshot(processId);
        const processRecord = workspace.processes.find(item => item.id === processId);
        const command = processRecord?.command?.split(/[\\/]/).at(-1);
        const proposed = [terminal.title, agent?.name, command].find(value => value?.trim());
        const dataDir = process.env.HIVE_PLUGIN_DATA;
        if (proposed && dataDir && proposed !== tab.name) {
          await mkdir(dataDir, { recursive: true });
          const stateFile = join(dataDir, "titles.json");
          let titles: Record<string, string> = {};
          try { titles = JSON.parse(await readFile(stateFile, "utf8")) as Record<string, string>; } catch { /* first run */ }
          // Keep manual names. Only update generated names or names this plugin owns.
          if ((titles[tab.id] && titles[tab.id] === tab.name) || (!titles[tab.id] && /^tab \d+$/.test(tab.name))) {
            const title = proposed.trim().replace(/[\r\n\t]/g, " ").slice(0, 64);
            if (title) {
              await plugin.hive.tabs.rename(tab.id, title);
              titles[tab.id] = title;
              await writeFile(stateFile, JSON.stringify(titles), "utf8");
            }
          } else if (titles[tab.id]) {
            delete titles[tab.id];
            await writeFile(stateFile, JSON.stringify(titles), "utf8");
          }
        }
      }
    }
  }
} finally {
  plugin.close();
}
