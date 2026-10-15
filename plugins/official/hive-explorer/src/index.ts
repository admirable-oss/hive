import { join } from "node:path";
import { init } from "@hive-oss/plugin-sdk";

const plugin = await init({ id: "hive-explorer", version: "0.2.0" });
try {
  if (plugin.context.trigger === "action") {
    const dir = process.env.HIVE_PLUGIN_DIR;
    if (!dir) throw new Error("Hive did not provide HIVE_PLUGIN_DIR");
    if (!plugin.context.tab) throw new Error("Hive Explorer needs an active tab");
    await plugin.hive.panes.popup({
      tab_id: plugin.context.tab,
      spec: {
        name: "Hive Explorer",
        command: [process.execPath, join(dir, "dist/ui.mjs")],
        cwd: process.cwd(),
        env: { HIVE_PLUGIN_DIR: dir },
      },
      width_pct: 92,
      height_pct: 88,
    });
  }
} finally {
  plugin.close();
}
