import { Browser, Clipboard } from "@wailsio/runtime";
// platform is the OS the window runs on, read from the user agent: the runtime's System.IsWindows
// is false while this module loads on Windows, where WebView2 hands the environment over only once
// the page has loaded, and Windows got macOS's layout. words are what that OS calls its parts, one
// row per phrase and a column per platform, so a new platform fills a column rather than every page.
// web: the page runs in a browser, served by Connaio (internal/app/web.go sets the cookie),
// not in the app's window: no window, tray, file dialogs or desktop to drive, its platform "web".
export const web = document.cookie.split("; ").includes("connaio_web=1");
const ua = navigator.userAgent;
export const platform = web ? "web" : ua.includes("Windows") ? "windows" : ua.includes("Linux") ? "linux" : "macos";
export const words = {
  showInFiles: { macos: "在 Finder 中显示", linux: "在文件管理器中显示", windows: "在资源管理器中显示", web: "" }[platform],
  pickApp: { macos: "从 Finder 添加…", linux: "选择应用…", windows: "选择应用…", web: "" }[platform],
  tray: { macos: "菜单栏", linux: "托盘", windows: "通知区域", web: "托盘" }[platform],
};
// openURL and copyText act where the page is seen: on the web, in this browser, not on the server
export const openURL = (url: string) => (web ? void window.open(url, "_blank", "noopener") : void Browser.OpenURL(url));
export const copyText = (text: string) => void (web ? navigator.clipboard?.writeText(text) : Clipboard.SetText(text))?.catch(() => {}); // Wails': navigator.clipboard is missing in some webviews

// validPort accepts 1..65535 as typed.
export const validPort = (s: string) => /^\d+$/.test(s) && Number(s) >= 1 && Number(s) <= 65535;

// cidr reads an address range as typed, a bare IP becoming a range of one; null when it is neither.
// IPv6 is checked loosely: the core rejects what slips through.
export function cidr(s: string): string | null {
  const [ip, len, ...rest] = s.split("/");
  const v6 = ip.includes(":");
  const v4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(ip);
  if (rest.length || (v6 ? !/^[0-9a-f:.]+$/i.test(ip) : !v4 || v4.slice(1).some((o) => Number(o) > 255))) return null;
  const max = v6 ? 128 : 32;
  if (len === undefined) return `${ip}/${max}`;
  return /^\d{1,3}$/.test(len) && Number(len) <= max ? s : null;
}

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

// ago is a short relative time, e.g. 刚刚, 5 分钟前, 3 天前.
export function ago(iso: string) {
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (!(s >= 0)) return "—";
  if (s < 60) return "刚刚";
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return `${Math.floor(s / 86400)} 天前`;
}
