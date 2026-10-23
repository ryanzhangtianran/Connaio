// applyTheme dresses the page before it paints, a plain script in index.html's head: the 外观 settings
// (Settings.tsx Appearance) from localStorage, .dark on the root for the dark palette and that mode's
// accent colour, again as the system's appearance or another window's choice changes.
const dark = matchMedia("(prefers-color-scheme: dark)");
function applyTheme() {
  const get = (k) => { try { return localStorage.getItem(k); } catch { return null; } };
  const theme = get("theme") || "system";
  const on = theme === "dark" || (theme === "system" && dark.matches);
  const root = document.documentElement;
  root.classList.toggle("dark", on);
  const accent = get(on ? "accent.dark" : "accent.light");
  if (accent) root.style.setProperty("--accent", accent);
  else root.style.removeProperty("--accent");
}
applyTheme();
dark.addEventListener("change", applyTheme); // macOS: also as SetTheme forces the window's appearance
addEventListener("storage", applyTheme); // set in the other window: the main one or the monitor
