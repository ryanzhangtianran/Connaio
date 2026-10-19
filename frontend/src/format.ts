// platform is the OS the window runs on, read from the user agent: the runtime's System.IsWindows
// is false while this module loads on Windows, where WebView2 hands the environment over only once
// the page has loaded, and Windows got macOS's layout. words are what that OS calls its parts, one
// row per phrase and a column per platform, so a new platform fills a column rather than every page.
const ua = navigator.userAgent;
export const platform = ua.includes("Windows") ? "windows" : ua.includes("Linux") ? "linux" : "macos";
export const words = {
  showInFiles: { macos: "在 Finder 中显示", linux: "在文件管理器中显示", windows: "在资源管理器中显示" }[platform],
  pickApp: { macos: "从 Finder 添加…", linux: "选择应用…", windows: "选择应用…" }[platform],
  tray: { macos: "菜单栏", linux: "托盘", windows: "通知区域" }[platform],
};

// validPort accepts 1..65535 as typed.
export const validPort = (s: string) => /^\d+$/.test(s) && Number(s) >= 1 && Number(s) <= 65535;

// [value, unit] for big-number displays, e.g. 813 KB/s
export function speed(bytes: number): [string, string] {
  if (bytes < 1000) return [String(bytes), "B/s"];
  if (bytes < 1000 * 1000) return [(bytes / 1000).toFixed(0), "KB/s"];
  return [(bytes / 1000 / 1000).toFixed(1), "MB/s"];
}

export function size(bytes: number): [string, string] {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = bytes, i = 0;
  while (v >= 1000 && i < units.length - 1) { v /= 1000; i++; }
  return [i === 0 ? String(v) : v.toFixed(v < 10 ? 2 : 1), units[i]];
}

export const sizeText = (bytes: number) => size(bytes).join(" ");

// errText is a failed call's text without its "RuntimeError: " prefix (MarshalError in app.go), or
// fallback when there is none.
export const errText = (e: unknown, fallback = "") => String(e).replace(/^\w*Error:\s*/, "") || fallback;

// nonNull drops the nulls Go's bindings allow in slices of maps.
export const nonNull = <T,>(a: (T | null)[] | null | undefined): T[] => (a ?? []).filter((x): x is T => x != null);
