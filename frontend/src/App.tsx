import { useEffect, useState } from "react";
import { Window } from "@wailsio/runtime";
import { CoreService, EnhancedService, SystemProxyService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Activity as ActivityData, Event as AppEvent, Status } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import Activity from "./Activity";
import Processes from "./Processes";
import Proxies from "./Proxies";
import Dashboard from "./Dashboard";
import Logs from "./Logs";
import Rules from "./Rules";
import Tunnels from "./Tunnels";
import Vpn from "./Vpn";
import Devices from "./Devices";
import { AboutPage, Dns, General } from "./Settings";
import Profiles from "./Profiles";
import ErrorBoundary from "./ErrorBoundary";
import { Icon } from "./icons";
import { platform, web, words } from "./format";
import { on, usePoll, useClickAway } from "./hooks";
import { start as startStore } from "./store";

// NavIcon is the page's line icon, grey or, for the page shown, the accent colour.
const NavIcon = ({ page }: { page: string }) => <Icon name={page} />;

// WindowButtons stand in for the title bar Linux and Windows windows go without (frameless, see app.go); close
// hides to the tray as the system one did.
const WindowButtons = () => (
  <div className="winbtns">
    <button title="最小化" onClick={() => Window.Minimise()}><NavIcon page="minimise" /></button>
    <button title="最大化" onClick={() => Window.ToggleMaximise()}><NavIcon page="maximise" /></button>
    <button title="关闭" onClick={() => Window.Close()}><NavIcon page="close" /></button>
  </div>
);

const levelNames: Record<string, string> = { info: "状态", warning: "警告", error: "错误" };

// EventsButton lists what the app has to tell (core started, proxy restored, failed actions…).
function EventsButton() {
  const [list, setList] = useState<AppEvent[]>([]);
  const [unread, setUnread] = useState(0);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    CoreService.Events().then((l) => setList(l ?? []));
    return on("event", (d) => { setList((l) => [...l.slice(-49), d]); setUnread((n) => n + 1); });
  }, []);
  useClickAway(open, () => setOpen(false));
  const last = list[list.length - 1];
  const label = list.length === 0 ? "无事件" : unread > 0 ? `${unread} 个新事件` : `${list.length} 个事件`;
  const toggle = (e: React.MouseEvent) => { e.stopPropagation(); setOpen(!open); setUnread(0); };
  const clear = () => { CoreService.ClearEvents(); setList([]); setUnread(0); setOpen(false); };
  return (
    <div className="events">
      <button className="pill" onClick={toggle}>
        <span className={`dot ${unread > 0 && last ? { error: "err", warning: "warn", info: "on" }[last.level] ?? "" : ""}`} />{label}
      </button>
      {open && (
        <div className="menu events-menu" onClick={(e) => e.stopPropagation()}>
          <div className="events-head"><span>事件</span>{list.length > 0 && <button className="link" onClick={clear}>清除</button>}</div>
          {list.length === 0 && <div className="muted event-text">暂无事件</div>}
          {[...list].reverse().map((ev, i) => (
            <div key={i} className={`event-row ${ev.level}`}>
              <div className="event-meta"><span className="dot" />{levelNames[ev.level] ?? ev.level} · {ev.time}</div>
              <div className="event-text">{ev.text}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function lastPage() {
  try { return localStorage.getItem("page"); } catch { return null; }
}

export default function App() {
  // a new window opens on the page asked for (app.go show), else where the last one was left
  const [current, setCurrent] = useState(() => new URLSearchParams(location.search).get("page") || lastPage() || "activity");
  useEffect(() => { try { localStorage.setItem("page", current); } catch { /* unavailable: opens on 活动 */ } }, [current]);
  const [status, setStatus] = useState<Status>({ running: false, version: "", error: "" });
  const [activity, setActivity] = useState<ActivityData | null>(null);
  const [sysProxy, setSysProxy] = useState(false);
  const [enhanced, setEnhanced] = useState(""); // EnhancedService.Mode: "", "tun" or "process-tun"
  const [rooted, setRooted] = useState(false); // root granted: enhancing needs it
  const [enhancedBusy, setEnhancedBusy] = useState(false);
  const [restarting, setRestarting] = useState(false);

  useEffect(() => {
    CoreService.Status().then(setStatus);
    if (!web) SystemProxyService.Enabled().then(setSysProxy);
    const mode = () => { EnhancedService.Mode().then(setEnhanced); EnhancedService.DaemonInstalled().then(setRooted); };
    mode();
    const offs = [
      startStore(),
      on("enhanced", mode),
      on("navigate", (d) => setCurrent(d)),
      on("setting", (d) => d === "capture.mode" && mode()),
      on("sysproxy", (d) => setSysProxy(d)),
      on("status", (d) => setStatus(d)),
    ];
    return () => offs.forEach((off) => off());
  }, []);

  // the "status" event can be missed while the page loads, and the first Status() reply can land
  // after it with the stale "starting"; ask again until the core has settled either way
  usePoll(() => CoreService.Status().then(setStatus), 1000, !status.running && !status.error);
  usePoll(() => CoreService.Activity().then(setActivity), 1000, status.running && current === "activity");

  // ⌘F or Ctrl+F (the web UI runs on any OS) goes to the page's search box, when it has one
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== "f" || !(e.metaKey || e.ctrlKey)) return;
      const box = document.querySelector<HTMLInputElement>(".page [data-search]");
      if (box) { e.preventDefault(); box.focus(); box.select(); }
    };
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, []);

  // one list, from what you check most to what you touch least; only the page shown is mounted
  const pages: { key: string; title: string; page: React.ReactNode }[] = [
    { key: "activity", title: "活动", page: <Activity status={status} activity={activity} sysProxy={sysProxy} enhanced={enhanced} rooted={rooted} go={setCurrent} /> },
    { key: "proxies", title: "策略", page: <Proxies status={status} /> },
    { key: "tunnels", title: "反向", page: <Tunnels status={status} /> },
    { key: "vpn", title: "VPN", page: <Vpn status={status} /> },
    { key: "devices", title: "设备", page: <Devices status={status} /> },
    { key: "rules", title: "规则", page: <Rules status={status} /> },
    { key: "processes", title: web ? "TUN" : "增强", page: <Processes status={status} mode={enhanced} rooted={rooted} /> },
    { key: "profiles", title: "配置", page: <div className="grouped profiles-page"><Profiles /></div> },
    { key: "dns", title: "DNS", page: <div className="grouped"><Dns /></div> },
    { key: "dashboard", title: "连接", page: <Dashboard status={status} /> },
    { key: "logs", title: "日志", page: <Logs /> },
    { key: "general", title: "通用", page: <div className="grouped"><General /></div> },
    { key: "about", title: "关于", page: <div className="grouped"><AboutPage /></div> },
  ];

  return (
    <div className="app">
      <ErrorBoundary>
      <nav className="sidebar">
        {pages.map((p) => (
          <button key={p.key} className={`item ${p.key === current ? "active" : ""}`} aria-current={p.key === current ? "page" : undefined} onClick={() => setCurrent(p.key)}><NavIcon page={p.key} />{p.title}</button>
        ))}
      </nav>
      <main className="main">
        <div className="topbar" onDoubleClick={(e) => platform === "linux" && (e.target === e.currentTarget || (e.target as Element).classList.contains("page-title")) && Window.ToggleMaximise()}>
          <h1 className="page-title">{pages.find((p) => p.key === current)?.title}</h1>
          <EventsButton />
          {!web && <button
            className={`pill ${sysProxy ? "active" : ""}`}
            aria-pressed={sysProxy}
            disabled={!status.running}
            title="将系统代理指向本机代理端口"
            onClick={() => SystemProxyService.SetEnabled(!sysProxy)}
          >
            <span className={`dot ${sysProxy ? "on" : ""}`} />系统代理
          </button>}
          <button
            className={`pill ${enhanced ? "active" : ""}`}
            aria-pressed={!!enhanced}
            disabled={!status.running || enhancedBusy || !rooted}
            title={!rooted ? (web ? "需以 root 运行 Connaio" : "需先在「通用」中授予 root 权限") : enhanced === "process-tun" ? `TUN 只接管选中的应用；点击关闭${words.partial}` : "TUN 接管全部流量"}
            onClick={() => {
              // it turns off what it shows: partial enhancement when that runs, else enhanced mode
              setEnhancedBusy(true);
              (enhanced === "process-tun" ? EnhancedService.SetPartial(false) : EnhancedService.SetFull(enhanced !== "tun")).finally(() => setEnhancedBusy(false));
            }}
          >
            <span className={`dot ${enhanced === "tun" ? "on" : enhanced === "process-tun" ? "part" : ""}`} />
            {enhancedBusy ? "切换中…" : enhanced === "process-tun" ? words.partial : words.enhanced}
          </button>
          {(platform === "linux" || platform === "windows") && <WindowButtons />}
        </div>
        <div className="page">
          {/* the core stopped: why, and the way back, above whichever page is shown */}
          {!status.running && status.error && (
            <div className="card core-down">
              <div className="grow"><div className="setting-title">内核未运行</div><div className="small error">{status.error}</div></div>
              <button className="btn" onClick={() => setCurrent("logs")}>查看日志</button>
              <button className="btn primary" disabled={restarting} onClick={() => { setRestarting(true); CoreService.Restart().finally(() => setRestarting(false)); }}>{restarting ? "重启中…" : "重启内核"}</button>
            </div>
          )}
          <ErrorBoundary key={current} onReset={() => setCurrent("activity")}>
          {pages.find((p) => p.key === current)?.page}
          </ErrorBoundary>
        </div>
      </main>
      </ErrorBoundary>
    </div>
  );
}
