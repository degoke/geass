// Sets the theme before the first paint, so a dark system does not flash light. It lives in a file because the
// dashboard's Content-Security-Policy (script-src 'self') refuses inline scripts. It mirrors Mantine's own
// ColorSchemeScript: the saved choice if there is one, otherwise the system's, and light when the system has none.
// The theme-color meta follows the scheme in use, whether it came from the system or from the theme menu.
(function () {
  var root = document.documentElement;
  var meta = document.querySelector('meta[name="theme-color"]');
  function paintMeta() {
    if (meta) meta.setAttribute("content", root.getAttribute("data-mantine-color-scheme") === "dark" ? "#0a0a0a" : "#ffffff");
  }
  try {
    var saved = localStorage.getItem("mantine-color-scheme-value");
    var scheme = saved === "light" || saved === "dark" ? saved : matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
    root.setAttribute("data-mantine-color-scheme", scheme);
  } catch (e) {}
  paintMeta();
  new MutationObserver(paintMeta).observe(root, { attributes: true, attributeFilter: ["data-mantine-color-scheme"] });
})();
