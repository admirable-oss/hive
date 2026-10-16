// Hive web observer: keeps the page live. The server renders every part of
// the page; this only swaps in the fragments it sends over one event stream,
// shows whether the data is live, and closes the stream while the page is
// hidden. It stores nothing but the colour theme.
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
    if (npre) npre.scrollTop = atBottom ? npre.scrollHeight : scroll;
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

  document.addEventListener("DOMContentLoaded", function () {
    signInFromLink();
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
    // An agent's prompt is at the bottom of its screen: start there.
    document.querySelectorAll("pre.screen").forEach(function (p) { p.scrollTop = p.scrollHeight; });
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
