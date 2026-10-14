// src/ui.ts
import { execFileSync } from "node:child_process";
import { realpath, readdir, readFile, stat } from "node:fs/promises";
import { basename, dirname, isAbsolute, join, relative, resolve } from "node:path";
import process from "node:process";
var root = await realpath(resolve(process.cwd()));
var cwd = root;
var mode = "files";
var selected = 0;
var query = "";
var entries = [];
var preview = "";
var gitContent = "";
function insideRoot(path) {
  const rel = relative(root, path);
  return rel === "" || rel !== ".." && !rel.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`) && !isAbsolute(rel);
}
async function list(dir) {
  const values = await readdir(dir, { withFileTypes: true });
  return values.filter((item) => item.name !== ".git" && item.name !== "node_modules").map((item) => ({ name: item.name, dir: item.isDirectory(), link: item.isSymbolicLink(), path: join(dir, item.name) })).sort((a, b) => Number(b.dir) - Number(a.dir) || a.name.localeCompare(b.name));
}
async function readPreview(path) {
  const actual = await realpath(path);
  if (!insideRoot(actual)) return "Preview is limited to files inside the workspace.";
  const info = await stat(actual);
  if (!info.isFile()) return "Not a regular file.";
  if (info.size > 128 * 1024) return `File is ${info.size} bytes; preview is limited to 128 KiB.`;
  const data = await readFile(actual);
  if (data.includes(0)) return "Binary file; no text preview.";
  return data.toString("utf8").split("\n").slice(0, 200).join("\n");
}
async function search(dir, needle, out, count) {
  if (count.n >= 2e3) return;
  let children;
  try {
    children = await list(dir);
  } catch {
    return;
  }
  for (const item of children) {
    if (count.n >= 2e3) return;
    count.n++;
    const filenameMatch = item.name.toLowerCase().includes(needle.toLowerCase());
    if (filenameMatch) out.push(item);
    else if (!item.dir && !item.link) {
      try {
        const actual = await realpath(item.path);
        if (!insideRoot(actual)) continue;
        const info = await stat(actual);
        if (info.size <= 64 * 1024) {
          const data = await readFile(actual);
          if (!data.includes(0)) {
            const text = data.toString("utf8");
            const at = text.toLowerCase().indexOf(needle.toLowerCase());
            if (at >= 0) {
              const line = text.slice(0, at).split("\n").length;
              const excerpt = text.split("\n")[line - 1]?.trim() || "";
              out.push({ ...item, match: `L${line}: ${excerpt}` });
            }
          }
        }
      } catch {
      }
    }
    if (item.dir) await search(item.path, needle, out, count);
  }
}
function gitStatus() {
  try {
    return execFileSync("git", ["status", "--short", "--branch"], { cwd: root, encoding: "utf8", timeout: 3e3 }).trim() || "Working tree clean.";
  } catch (error) {
    return `Git status failed: ${String(error)}`;
  }
}
function gitDiff(path) {
  const file = relative(root, path);
  if (!file || file === ".." || file.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`)) return "Select a file inside the workspace.";
  try {
    const unstaged = execFileSync("git", ["diff", "--no-ext-diff", "--", file], { cwd: root, encoding: "utf8", timeout: 3e3 });
    if (unstaged.trim()) return unstaged;
    const staged = execFileSync("git", ["diff", "--cached", "--no-ext-diff", "--", file], { cwd: root, encoding: "utf8", timeout: 3e3 });
    return staged.trim() || "No staged or unstaged changes for this file.";
  } catch (error) {
    return `Git diff failed: ${String(error)}`;
  }
}
function fit(text, width) {
  const line = text.replace(/[\r\n\t]/g, " ");
  return line.length > width ? `${line.slice(0, Math.max(0, width - 1))}\u2026` : line;
}
function draw() {
  const width = process.stdout.columns || 80;
  const height = process.stdout.rows || 24;
  const leftWidth = Math.max(24, Math.floor(width * 0.37));
  const bodyHeight = Math.max(4, height - 5);
  let right;
  if (mode === "preview") right = preview.split("\n");
  else if (mode === "git") right = gitContent.split("\n");
  else if (mode === "search") right = entries.map((item) => `${item.dir ? "\u25B8" : " "} ${relative(root, item.path)}${item.match ? `  ${item.match}` : ""}`);
  else right = ["Enter open \xB7 p preview \xB7 / search \xB7 g Git status", "Backspace parent \xB7 q close"];
  const rows = [];
  rows.push(` HIVE / ${fit(relative(root, cwd) || basename(root), width - 10)}`);
  rows.push(`${"\u2500".repeat(leftWidth)}\u252C${"\u2500".repeat(Math.max(1, width - leftWidth - 1))}`);
  for (let i = 0; i < bodyHeight; i++) {
    const item = entries[i + Math.max(0, selected - bodyHeight + 1)];
    const name = item ? `${item.dir ? "\u25B8" : item.link ? "\u21AA" : " "} ${item.name}${item.dir ? "/" : ""}` : "";
    const marker = item && i + Math.max(0, selected - bodyHeight + 1) === selected ? "\u203A" : " ";
    rows.push(`${marker}${fit(name, leftWidth - 2).padEnd(leftWidth - 1)}\u2502${fit(right[i] || "", width - leftWidth - 1)}`);
  }
  rows.push(`${"\u2500".repeat(leftWidth)}\u2534${"\u2500".repeat(Math.max(1, width - leftWidth - 1))}`);
  rows.push(mode === "search" ? ` Search files: ${query}_` : " \u2191/\u2193 move   Enter open/preview   / search   g status   d diff   p preview   q close");
  process.stdout.write("\x1B[?25l\x1B[2J\x1B[H" + rows.slice(0, height).join("\r\n"));
}
async function refresh() {
  if (mode === "search") {
    entries = [];
    if (query) await search(root, query, entries, { n: 0 });
  } else {
    try {
      entries = await list(cwd);
    } catch {
      entries = [];
    }
  }
  selected = Math.max(0, Math.min(selected, entries.length - 1));
  draw();
}
async function activate() {
  const item = entries[selected];
  if (!item) return;
  if (item.link) {
    preview = "Symlink traversal is disabled to keep browsing inside the workspace.";
    mode = "preview";
    draw();
    return;
  }
  if (mode === "search") cwd = dirname(item.path);
  if (item.dir) {
    cwd = item.path;
    mode = "files";
    query = "";
    selected = 0;
    await refresh();
  } else {
    preview = await readPreview(item.path);
    mode = "preview";
    draw();
  }
}
process.stdin.setRawMode(true);
process.stdin.resume();
process.stdout.write("\x1B[?1049h");
await refresh();
process.stdin.on("data", async (buffer) => {
  const key = buffer.toString("utf8");
  if (key === "q" || key === "") {
    process.stdout.write("\x1B[?25h\x1B[?1049l");
    process.exit(0);
  }
  if (key === "\x1B[A") selected = Math.max(0, selected - 1);
  else if (key === "\x1B[B") selected = Math.min(entries.length - 1, selected + 1);
  else if (key === "\r") {
    if (mode === "search") mode = "files";
    else if (mode === "preview" || mode === "git") mode = "files";
    else {
      await activate();
      return;
    }
  } else if (key === "\x7F") {
    if (mode === "search") query = query.slice(0, -1);
    else if (mode === "preview" || mode === "git") mode = "files";
    else if (cwd !== root) cwd = dirname(cwd);
  } else if (key === "/") {
    mode = "search";
    query = "";
    selected = 0;
  } else if (key === "g") {
    mode = "git";
    gitContent = gitStatus();
  } else if (key === "d") {
    const item = entries[selected];
    if (item && !item.dir && !item.link) {
      mode = "git";
      gitContent = gitDiff(item.path);
    }
  } else if (key === "p") {
    const item = entries[selected];
    if (item && !item.dir && !item.link) {
      preview = await readPreview(item.path);
      mode = "preview";
    }
  } else if (mode === "search" && key.length === 1 && key >= " ") {
    query += key;
    selected = 0;
  }
  await refresh();
});
