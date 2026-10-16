// Applies the chosen colour theme before the page paints. The choice (auto,
// light or dark) is the only thing the web observer keeps in the browser.
(function () {
  var t = null;
  try { t = localStorage.getItem("hive-theme"); } catch (e) {}
  if (t === "light" || t === "dark") document.documentElement.setAttribute("data-theme", t);
})();
