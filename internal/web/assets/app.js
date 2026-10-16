// Hive web observer: keeps the page live. The server renders every part of
// the page; this only swaps in the fragments it sends over one event stream,
// shows whether the data is live, and closes the stream while the page is
// hidden. It stores nothing but the colour theme and the screen text size.
(function () {
  "use strict";

  var es = null;          // the EventSource, while open
  var lastMessage = 0;    // when the stream last said anything
  var hideTimer = null;
  var checked = 0;        // when the session was last checked after an error
  var STALE_AFTER = 45000; // the server pings every 15 s
  var HIDDEN_GRACE = 30000;

  function $(id) { return document.getElementById(id); }

  // --- theme ---
  var themes = ["auto", "light", "dark"];
  function currentTheme() {
    return document.documentElement.getAttribute("data-theme") || "auto";
  }
  function showTheme() {
    var b = $("theme");
    if (b) b.textContent = "Theme: " + currentTheme();
  }
  function cycleTheme() {
    var next = themes[(themes.indexOf(currentTheme()) + 1) % themes.length];
    if (next === "auto") document.documentElement.removeAttribute("data-theme");
    else document.documentElement.setAttribute("data-theme", next);
    try { localStorage.setItem("hive-theme", next); } catch (e) {}
    showTheme();
  }

  // --- connection indicator ---
  function setConn(state) {
    var c = $("conn");
    if (!c) return;
    c.classList.remove("conn-live", "conn-connecting", "conn-reconnecting", "conn-stale", "conn-offline", "conn-refused", "conn-idle");
    c.classList.add("conn-" + state);
    var t = c.querySelector(".conn-text");
    if (t && state !== "live" && state !== "stale") {
      t.textContent = { connecting: "Connecting…", reconnecting: "Reconnecting…", offline: "Offline" }[state] || t.textContent;
    }
  }
  function touch() {
    lastMessage = Date.now();
    var c = $("conn");
    if (c) c.classList.remove("conn-stale");
  }

  // --- fragments ---
  function swap(p) {
    var old = $(p.id);
    if (!old) return;
    var have = Number(old.getAttribute("data-v") || 0);
    if (p.v < have) return; // older than what is shown
    var t = document.createElement("template");
    t.innerHTML = p.html;
    var next = t.content.firstElementChild;
    if (!next) return;
    // Keep keyboard focus and screen scroll where they were.
    var focused = document.activeElement && old.contains(document.activeElement) ? document.activeElement : null;
    var href = focused && focused.getAttribute("href");
    var pre = old.querySelector("pre.screen");
    var atBottom = pre && pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 4;
    var scroll = pre ? pre.scrollTop : 0;
    old.replaceWith(next);
    var npre = next.querySelector("pre.screen");
    if (npre) {
      applyScreen(next);
      npre.scrollTop = screenPrefs.follow || atBottom ? npre.scrollHeight : scroll;
    }
    if (href) {
      var again = next.querySelector('[href="' + CSS.escape(href) + '"]');
      if (again) again.focus();
    }
    relativeTimes(next);
  }

  // --- the stream ---
  function streamURL() {
    return "/stream?path=" + encodeURIComponent(location.pathname + location.search);
  }
  function open() {
    if (es || !$("main") || $("login")) return;
    setConn(lastMessage ? "reconnecting" : "connecting");
    es = new EventSource(streamURL());
    es.addEventListener("patch", function (e) { touch(); swap(JSON.parse(e.data)); });
    es.addEventListener("ping", touch);
    es.addEventListener("end", function (e) {
      var d = JSON.parse(e.data);
      close();
      if (d.reason === "revoked" || d.reason === "expired" || d.reason === "out") {
        location.assign("/login?reason=" + encodeURIComponent(d.reason));
        return;
      }
      // The server restarted or its runtimes changed: catch up shortly.
      setConn("reconnecting");
      setTimeout(open, 1000 + Math.random() * 2000);
    });
    es.onopen = touch;
    es.onerror = function () {
      setConn(navigator.onLine === false ? "offline" : "reconnecting");
      checkSession();
    };
  }
  function close() {
    if (es) { es.close(); es = null; }
  }
  function reopen() { close(); open(); }

  // After a stream error: was access ended? (EventSource cannot say.)
  function checkSession() {
    if (Date.now() - checked < 10000) return;
    checked = Date.now();
    fetch("/session", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (r.status === 401) return r.json().then(function (d) {
        close();
        location.assign("/login?reason=" + encodeURIComponent(d.reason || "out"));
      });
    }).catch(function () {});
  }

  // --- times ---
  function ago(ms) {
    var s = Math.round(Math.abs(ms) / 1000), out;
    if (s < 60) out = s + "s";
    else if (s < 3600) out = Math.floor(s / 60) + "m";
    else if (s < 172800) out = Math.floor(s / 3600) + "h" + String(Math.floor(s / 60) % 60).padStart(2, "0") + "m";
    else out = Math.floor(s / 86400) + "d";
    if (ms < 0) return "in " + out;
    return s < 10 ? "just now" : out + " ago";
  }
  function relativeTimes(root) {
    var now = Date.now();
    (root || document).querySelectorAll("time[datetime]").forEach(function (t) {
      var at = Date.parse(t.getAttribute("datetime"));
      if (!isNaN(at)) t.textContent = ago(now - at);
    });
  }

  // --- filters (dashboard) ---
  var filterTimer = null;
  function applyFilters() {
    var q = $("q"), st = $("state");
    var params = new URLSearchParams();
    if (q && q.value.trim()) params.set("q", q.value.trim());
    if (st && st.value) params.set("state", st.value);
    var qs = params.toString();
    history.replaceState(null, "", location.pathname + (qs ? "?" + qs : ""));
    reopen();
  }

  // --- output history: read only when asked ---
  function loadOutput(e) {
    var a = e.target.closest("[data-output]");
    if (!a) return;
    e.preventDefault();
    a.setAttribute("aria-busy", "true");
    fetch(a.getAttribute("data-output"), { credentials: "same-origin", cache: "no-store" })
      .then(function (r) { return r.text(); })
      .then(function (html) {
        var t = document.createElement("template");
        t.innerHTML = html;
        var out = $("output");
        if (out && t.content.firstElementChild) out.replaceWith(t.content.firstElementChild);
        var pre = document.querySelector("#output pre");
        if (pre) { pre.scrollTop = pre.scrollHeight; pre.focus(); }
      })
      .finally(function () { a.removeAttribute("aria-busy"); });
  }

  // --- sign-in from a link: the code is in the fragment, never sent in a URL ---
  function signInFromLink() {
    var form = $("login");
    if (!form || location.hash.length < 6) return;
    var code = decodeURIComponent(location.hash.slice(1));
    history.replaceState(null, "", location.pathname);
    $("code").value = code;
    form.submit();
  }

  // --- the installed app: service worker ---
  function registerWorker() {
    if (!("serviceWorker" in navigator) || $("login")) return;
    navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(function () {});
  }

  // --- notifications on this device (Settings) ---
  function b64(s) {
    var pad = "=".repeat((4 - (s.length % 4)) % 4);
    var raw = atob((s + pad).replace(/-/g, "+").replace(/_/g, "/"));
    return Uint8Array.from(raw, function (c) { return c.charCodeAt(0); });
  }
  function postJSON(url, body) {
    return fetch(url, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body || {}) });
  }
  function pushUI() {
    var state = $("push-state"), on = $("push-on"), off = $("push-off"), test = $("push-test");
    if (!state) return;
    function show(text, canOn, canOff) {
      state.textContent = text;
      on.hidden = !canOn; off.hidden = !canOff; test.hidden = !canOff;
    }
    if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) {
      var ios = /iPhone|iPad/.test(navigator.userAgent) && !navigator.standalone;
      show(ios ? "Add Hive to the Home Screen to get notifications on this device." : "This browser cannot show notifications from Hive.", false, false);
      return;
    }
    var key = null;
    function refresh() {
      fetch("/push/key", { credentials: "same-origin", cache: "no-store" }).then(function (r) { return r.json(); }).then(function (k) {
        if (!k.enabled) { show("Notifications are off on this server.", false, false); return; }
        key = k.key;
        return navigator.serviceWorker.ready.then(function (reg) { return reg.pushManager.getSubscription(); }).then(function (sub) {
          var mine = sub && (k.endpoints || []).indexOf(sub.endpoint) >= 0;
          if (Notification.permission === "denied") show("Notifications are blocked for this site in the browser's settings.", false, false);
          else if (mine) show("This device gets notifications.", false, true);
          else show("This device does not get notifications.", true, false);
        });
      }).catch(function () { show("Could not check notifications.", false, false); });
    }
    on.addEventListener("click", function () {
      Notification.requestPermission().then(function (perm) {
        if (perm !== "granted") { refresh(); return; }
        return navigator.serviceWorker.ready.then(function (reg) {
          return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64(key) });
        }).then(function (sub) { return postJSON("/push/subscribe", sub.toJSON()); }).then(refresh);
      }).catch(function (e) { state.textContent = "Could not turn notifications on: " + e.message; });
    });
    off.addEventListener("click", function () {
      navigator.serviceWorker.ready.then(function (reg) { return reg.pushManager.getSubscription(); }).then(function (sub) {
        var endpoint = sub ? sub.endpoint : "";
        return (sub ? sub.unsubscribe() : Promise.resolve()).then(function () { return postJSON("/push/unsubscribe", { endpoint: endpoint }); });
      }).then(refresh);
    });
    test.addEventListener("click", function () {
      postJSON("/push/test").then(function () { state.textContent = "A test notification is on its way."; });
    });
    refresh();
  }

  // --- screen view: size, fit, wrap, follow (no input ever reaches Hive) ---
  var screenPrefs = { size: 13, wrap: false, follow: true };
  try { screenPrefs.size = Number(localStorage.getItem("hive-screen-size")) || 13; } catch (e) {}
  function applyScreen(root) {
    (root || document).querySelectorAll("pre.screen").forEach(function (p) {
      p.style.setProperty("--screen-size", screenPrefs.size + "px");
      p.classList.toggle("wrap", screenPrefs.wrap);
      if (screenPrefs.follow) p.scrollTop = p.scrollHeight;
    });
    document.querySelectorAll(".screen-tools").forEach(function (t) {
      t.hidden = false;
      var w = t.querySelector('[data-screen="wrap"]'), f = t.querySelector('[data-screen="follow"]');
      if (w) w.setAttribute("aria-pressed", String(screenPrefs.wrap));
      if (f) f.setAttribute("aria-pressed", String(screenPrefs.follow));
    });
  }
  function screenTool(e) {
    var b = e.target.closest("[data-screen]");
    if (!b) return;
    var pre = document.querySelector("#screen pre.screen");
    switch (b.getAttribute("data-screen")) {
      case "smaller": screenPrefs.size = Math.max(8, screenPrefs.size - 1); break;
      case "larger": screenPrefs.size = Math.min(24, screenPrefs.size + 1); break;
      case "fit":
        if (pre) {
          var cols = Number(pre.getAttribute("data-cols")) || 80;
          screenPrefs.size = Math.max(6, Math.min(24, Math.floor((pre.clientWidth - 24) / (cols * 0.6))));
          screenPrefs.wrap = false;
        }
        break;
      case "wrap": screenPrefs.wrap = !screenPrefs.wrap; break;
      case "follow": screenPrefs.follow = !screenPrefs.follow; break;
    }
    try { localStorage.setItem("hive-screen-size", String(screenPrefs.size)); } catch (err) {}
    applyScreen();
  }

  // Back links return through history, so the list keeps its place.
  function backLink(e) {
    var a = e.target.closest("a[data-back]");
    var nav = window.navigation;
    if (!a || !nav || !nav.canGoBack || !nav.currentEntry) return;
    var prev = nav.entries()[nav.currentEntry.index - 1];
    if (!prev || new URL(prev.url).pathname !== new URL(a.href).pathname) return;
    e.preventDefault();
    history.back();
  }

  document.addEventListener("DOMContentLoaded", function () {
    signInFromLink();
    registerWorker();
    pushUI();
    applyScreen();
    document.addEventListener("click", screenTool);
    document.addEventListener("click", backLink);
    showTheme();
    var tb = $("theme");
    if (tb) tb.addEventListener("click", cycleTheme);
    var q = $("q"), st = $("state");
    if (q) q.addEventListener("input", function () { clearTimeout(filterTimer); filterTimer = setTimeout(applyFilters, 300); });
    if (st) st.addEventListener("change", applyFilters);
    var filters = document.querySelector("form.filters");
    if (filters) filters.addEventListener("submit", function (e) { e.preventDefault(); applyFilters(); });
    document.addEventListener("click", loadOutput);
    document.addEventListener("keydown", function (e) {
      var tag = (e.target.tagName || "").toLowerCase();
      if (e.key === "/" && q && tag !== "input" && tag !== "textarea" && tag !== "select") { e.preventDefault(); q.focus(); }
      if (e.key === "Escape" && e.target === q && q.value) { q.value = ""; applyFilters(); }
    });
    relativeTimes();
    setInterval(function () {
      relativeTimes();
      if (es && lastMessage && Date.now() - lastMessage > STALE_AFTER) {
        var c = $("conn");
        if (c) c.classList.add("conn-stale");
      }
    }, 5000);
    open();
  });

  // Stop streaming while nobody looks; catch up on return.
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) {
      hideTimer = setTimeout(close, HIDDEN_GRACE);
    } else {
      clearTimeout(hideTimer);
      if (!es) open();
    }
  });
  window.addEventListener("online", reopen);
  window.addEventListener("offline", function () { setConn("offline"); });
  window.addEventListener("pageshow", function (e) { if (e.persisted) reopen(); });
  window.addEventListener("pagehide", close);
})();
