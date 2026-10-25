// applyTheme dresses the page before it paints, a plain script in index.html's head: the 外观 settings
// (Settings.tsx Appearance) from localStorage, .dark on the root for the dark palette and the accent
// colour (one for both; accent.light was the light mode's, before), again as the system's appearance or another window's choice changes.
const dark = matchMedia("(prefers-color-scheme: dark)");
// contrastWithWhite is the WCAG contrast of white text on a #rrggbb colour.
function contrastWithWhite(hex) {
  const lin = (i) => { const c = parseInt(hex.slice(i, i + 2), 16) / 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; };
  const l = 0.2126 * lin(1) + 0.7152 * lin(3) + 0.0722 * lin(5);
  return 1.05 / (l + 0.05);
}
function applyTheme() {
  const get = (k) => { try { return localStorage.getItem(k); } catch { return null; } };
  const theme = get("theme") || "system";
  const on = theme === "dark" || (theme === "system" && dark.matches);
  const root = document.documentElement;
  root.classList.toggle("dark", on);
  const accent = get("accent") || get("accent.light");
  if (accent) root.style.setProperty("--accent", accent);
  else root.style.removeProperty("--accent");
  // text on the accent: white unless the accent is too light to read it on (under 3:1), then near-black
  if (accent && contrastWithWhite(accent) < 3) root.style.setProperty("--on-accent", "#1d1d1f");
  else root.style.removeProperty("--on-accent");
}
applyTheme();
dark.addEventListener("change", applyTheme); // macOS: also as SetTheme forces the window's appearance
addEventListener("storage", applyTheme); // set in the other window: the main one or the monitor
