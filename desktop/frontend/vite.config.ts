/// <reference types="vitest/config" />
import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

// The app loads only its own code: no remote scripts, styles, frames or
// connections. Development builds leave it out (Vite's hot reload needs a
// websocket and inline scripts).
const csp = [
  "default-src 'self'",
  "script-src 'self'",
  // Panes are positioned with inline styles.
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'self'",
  "frame-src 'none'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
].join("; ");

function contentSecurityPolicy(): Plugin {
  return {
    name: "hive-csp",
    apply: "build",
    transformIndexHtml(html, ctx) {
      if (ctx.server) return html;
      return html.replace(
        "<head>",
        `<head>\n    <meta http-equiv="Content-Security-Policy" content="${csp}" />`,
      );
    },
  };
}

// keepDist keeps frontend/dist/.gitkeep, so the Go module (which embeds
// the directory) builds before the frontend has been.
function keepDist(): Plugin {
  return {
    name: "hive-keep-dist",
    apply: "build",
    closeBundle() {
      writeFileSync(new URL("./dist/.gitkeep", import.meta.url), "");
    },
  };
}

export default defineConfig(({ mode }) => ({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  plugins: [react(), tailwindcss(), wails("./bindings"), keepDist(), ...(mode === "production" ? [contentSecurityPolicy()] : [])],
  // The bundle loads from the app's own disk, not a network: one chunk is fine.
  build: { target: "es2022", chunkSizeWarningLimit: 1500 },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
  },
}));
