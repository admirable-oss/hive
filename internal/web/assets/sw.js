// Hive web observer: the installed app's service worker.
//
// It keeps only the app's shell (styles, scripts, icons, the offline page)
// so the app opens without a network. It never stores pages, screens,
// output or events: those come from the server every time, with
// Cache-Control: no-store. It shows push notifications, which carry names
// at most, never screen contents, and opens the agent a notification is
// about when it is tapped.
"use strict";

const SHELL = /*SHELL*/[];
const VERSION = /*VERSION*/"";
const CACHE = "hive-shell-" + VERSION;

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k.startsWith("hive-shell-") && k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  // The shell: from the cache, which holds nothing about any agent.
  if (SHELL.includes(url.pathname + url.search)) {
    event.respondWith(caches.match(req).then((hit) => hit || fetch(req)));
    return;
  }
  // Pages: always from the network; the offline page when there is none.
  if (req.mode === "navigate") {
    event.respondWith(fetch(req).catch(() => caches.match("/offline")));
  }
  // Everything else (the stream, output, settings) goes to the network
  // untouched and is never stored.
});

self.addEventListener("push", (event) => {
  let n = {};
  try { n = event.data ? event.data.json() : {}; } catch (e) { n = {}; }
  const title = n.title || "Hive";
  event.waitUntil(self.registration.showNotification(title, {
    body: n.body || "",
    tag: n.tag || "hive",
    renotify: n.kind === "blocked",
    timestamp: n.ts || Date.now(),
    icon: SHELL.find((s) => s.includes("icon-192")) || undefined,
    data: { url: typeof n.url === "string" && n.url.startsWith("/") ? n.url : "/attention" },
  }));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const target = new URL(event.notification.data && event.notification.data.url || "/attention", self.location.origin).href;
  event.waitUntil((async () => {
    const wins = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    for (const w of wins) {
      if (new URL(w.url).origin === self.location.origin && "navigate" in w) {
        await w.focus();
        return w.navigate(target);
      }
    }
    return self.clients.openWindow(target);
  })());
});

// A subscription the browser replaced: tell the server about the new one.
self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil((async () => {
    const res = await fetch("/push/key", { credentials: "same-origin" });
    if (!res.ok) return;
    const { key } = await res.json();
    if (!key) return;
    const sub = await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64(key) });
    await fetch("/push/subscribe", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(sub) });
  })());
});

function b64(s) {
  const pad = "=".repeat((4 - (s.length % 4)) % 4);
  const raw = atob((s + pad).replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}
