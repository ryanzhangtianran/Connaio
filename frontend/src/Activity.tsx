import { useCallback, useEffect, useRef, useState } from "react";
import { Clipboard } from "@wailsio/runtime";
import { CoreService, DeviceService, EnhancedService, SettingsService, SystemProxyService, ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Activity as ActivityData, Conn, DiagSection, Group, IPInfo, LanInfo, Latency, Memory, RepairItem, Service, Status, Usage } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { delayClass, nodesOf } from "./Proxies";
import { errText, platform, size, sizeText, speed } from "./format";
import { DiagLog } from "./ProxyDiagnose";
import { on, usePoll, useCachedURL, useClickAway } from "./hooks";
import { Brand, Icon } from "./icons";
import { Switch } from "./Settings";
import { session, useHistory } from "./store";

function Num({ value, unit, cls = "big" }: { value: string; unit: string; cls?: string }) {
  return <div className={cls}>{value}<small>{unit}</small></div>;
}

// Head is a card's title, its icon before it, and the card's actions at the right.
function Head({ icon, title, children }: { icon: string; title: React.ReactNode; children?: React.ReactNode }) {
  return (
    <div className="head">
      <div className="card-title"><Icon name={icon} className="card-icon" />{title}</div>
      {children && <div className="head-actions">{children}</div>}
    </div>
  );
}

// IconButton is a small borderless action in a card's head; spin turns it while its work runs.
function IconButton({ icon, title, onClick, disabled, spin }: { icon: string; title: string; onClick: () => void; disabled?: boolean; spin?: boolean }) {
  return <button className={`icon-btn ${spin ? "spin" : ""}`} title={title} onClick={onClick} disabled={disabled}><Icon name={icon} className="btn-icon" /></button>;
}

// Sparkline plots the recent seconds across the card's full width, oldest at the left, scaled to the peak.
function Sparkline({ values, color }: { values: number[]; color: string }) {
  const [hover, setHover] = useState<number | null>(null);
  if (values.length < 2) return <div className="spark" />;
  const top = Math.max(1000, ...values); // 1 KB/s keeps idle lines flat
  const pts = values.map((v, i) => [(i / (values.length - 1)) * 100, 96 - (v / top) * 74]);
  // quadratic curves through the midpoints: smooth, and never overshoots below the baseline
  let line = `M${pts[0]}`;
  for (let i = 1; i < pts.length - 1; i++) line += ` Q${pts[i]} ${(pts[i][0] + pts[i + 1][0]) / 2},${(pts[i][1] + pts[i + 1][1]) / 2}`;
  line += ` L${pts[pts.length - 1]}`;
  const id = `fill-${color.replace(/\W/g, "")}`;
  const move = (e: React.PointerEvent) => {
    const r = e.currentTarget.getBoundingClientRect();
    const i = Math.round(((e.clientX - r.left) / r.width) * (values.length - 1));
    setHover(Math.min(values.length - 1, Math.max(0, i)));
  };
  const h = hover != null ? pts[hover] : null;
  const back = hover != null ? values.length - 1 - hover : 0;
  return (
    <div className="spark" onPointerMove={move} onPointerLeave={() => setHover(null)}>
      <svg viewBox="0 0 100 100" preserveAspectRatio="none">
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor={color} stopOpacity="0.22" />
            <stop offset="1" stopColor={color} stopOpacity="0" />
          </linearGradient>
        </defs>
        <path d={`${line} L${pts[pts.length - 1][0]},100 L${pts[0][0]},100 Z`} fill={`url(#${id})`} />
        <path d={line} fill="none" stroke={color} strokeWidth="2" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
      </svg>
      {h && hover != null && (
        <>
          <i className="spark-cross" style={{ left: `${h[0]}%` }} />
          <i className="spark-dot" style={{ left: `${h[0]}%`, top: `${h[1]}%`, background: color }} />
          <div className="spark-tip" style={{ left: `${h[0]}%`, transform: `translateX(${h[0] > 60 ? -100 : h[0] < 40 ? 0 : -50}%)` }}>
            {speed(values[hover]).join(" ")} · {back === 0 ? "现在" : `${back} 秒前`}
          </div>
        </>
      )}
    </div>
  );
}

// SpeedCard is one direction's speed now, its peak over the last minute and what has gone since the
// app started, over the minute's curve.
function SpeedCard({ dir, values }: { dir: "up" | "down"; values: number[] }) {
  const [v, u] = speed(values[values.length - 1] ?? 0);
  const color = dir === "up" ? "var(--up)" : "var(--down)";
  return (
    <div className="card speed-card" style={{ gridArea: dir }}>
      <Head icon={dir} title={dir === "up" ? "上传" : "下载"}><span className="muted small">峰值 {speed(Math.max(0, ...values)).join(" ")}</span></Head>
      <Num value={v} unit={u} />
      <div className="muted small">累计 {sizeText(session[dir])}</div>
      <Sparkline values={values} color={color} />
    </div>
  );
}

// Picker is a compact dropdown for a card's head. Not a <select>: on Linux its popup is GTK's own
// menu, out of keeping with the page; the menu is fixed to the window so the card doesn't clip it.
export function Picker({ value, options, onChange, label, className = "" }: { value: string; options: string[]; onChange: (v: string) => void; label?: string; className?: string }) {
  const [at, setAt] = useState<DOMRect | null>(null);
  useClickAway(!!at, () => setAt(null));
  return (
    <>
      {/* the click stops here: opening attaches the click-away listener, which it would reach next */}
      <button className={`picker ${className}`} onClick={(e) => { e.stopPropagation(); setAt(at ? null : e.currentTarget.getBoundingClientRect()); }}>
        <span>{label ?? (value || "—")}</span>
        <svg viewBox="0 0 10 6" width="10" height="6"><path d="M1 1l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" /></svg>
      </button>
      {at && (
        <div className="menu picker-menu" style={{ top: at.bottom + 4, right: window.innerWidth - at.right, minWidth: at.width }}>
          {options.map((o) => <button key={o} className={o === value ? "current" : ""} onClick={() => onChange(o)}>{o}</button>)}
        </div>
      )}
    </>
  );
}

const place = (i: IPInfo) => [i.city || i.region || i.country, i.code].filter(Boolean).join(", ");
// flag is a country's flag emoji from its ISO code.
const flag = (code: string) => (code.length === 2 ? String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0))) : "");
const fullPlace = (i: IPInfo) => [...new Set([i.city, i.region, i.country].filter(Boolean))].join(", ");

// exitIPs caches each policy's exit lookup for the session, a failed one dropped so it is asked again.
// ponytail: no expiry; a node whose address changes shows the old one until the app restarts.
const exitIPs: Record<string, Promise<IPInfo>> = {};
const exitIP = (policy: string) => (exitIPs[policy] ??= ToolService.ExitIP(policy).catch((e) => { delete exitIPs[policy]; throw e; }));

const modes: Record<string, string> = { rule: "规则判定", global: "全局代理", direct: "直接连接" };
const RULES = "按规则"; // the status card's exit as traffic takes it, not through one chosen policy

// cookie is a scalloped circle in a 100×100 box: the status card's pause button and country badge.
const cookie = (() => {
  const pts: string[] = [];
  for (let i = 0; i < 180; i++) {
    const t = (i / 180) * 2 * Math.PI, r = 46 + 4 * Math.cos(9 * t);
    pts.push(`${(50 + r * Math.sin(t)).toFixed(2)},${(50 - r * Math.cos(t)).toFixed(2)}`);
  }
  return `M${pts.join("L")}Z`;
})();
const Cookie = ({ className, children }: { className: string; children?: React.ReactNode }) => (
  <span className={className}><svg viewBox="0 0 100 100"><path d={cookie} /></svg>{children}</span>
);

let resumeMode = "rule"; // the mode the pause button goes back to

// StatusCard is where traffic comes out (as the rules send it, or through a chosen node or DIRECT),
// where that is and whose network, and the latency; its button pauses
// proxying (the direct mode) and resumes the mode it left.
function StatusCard({ activity, mode, setMode, latency }: { activity: ActivityData | null; mode: string; setMode: (m: string) => void; latency: Latency | null }) {
  const [names, setNames] = useState<string[]>([]);
  const [policy, setPolicy] = useState(RULES);
  const [info, setInfo] = useState<IPInfo | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { CoreService.Groups().then((g) => setNames(["DIRECT", ...nodesOf(g ?? []).map((n) => n.name)])); }, []);
  const check = (fresh: boolean) => {
    if (fresh) delete exitIPs[policy];
    setBusy(true); setError(""); setInfo(null);
    (policy === RULES ? CoreService.ExternalInfo() : exitIP(policy)).then(setInfo).catch((e) => setError(errText(e, "查询失败"))).finally(() => setBusy(false));
  };
  useEffect(() => check(false), [policy, activity?.mode]); // the rules' exit moves with the mode
  const paused = mode === "direct";
  const toggle = () => { if (paused) setMode(resumeMode); else { resumeMode = mode || "rule"; setMode("direct"); } };
  const lat = latency && [latency.router && `路由 ${latency.router} ms`, latency.dns && `DNS ${latency.dns} ms`, latency.proxy && `${latency.group} ${latency.proxy} ms`].filter(Boolean).join(" · ");
  return (
    <div className="card hero" style={{ gridArea: "hero" }}>
      <svg className="hero-wave" viewBox="0 0 100 100" preserveAspectRatio="none"><path d="M34,0 C14,14 40,30 20,50 C2,68 30,82 12,100 L100,100 L100,0 Z" /></svg>
      <div className="card-title"><Icon name="activity" className="card-icon" />状态
        {paused ? <span className="chip warn"><i className="dot warn" />已暂停</span> : <span className="chip good"><i className="dot on" />运行中</span>}
      </div>
      <div className="hero-ip">
        <Cookie className="cc"><b>{info?.code || "—"}</b></Cookie>
        <div className="hero-main">
          <div className={`ip-huge selectable ${info ? "" : "muted"}`}>{info?.ip ?? (busy ? "查询中…" : "—")}</div>
          <div className="chips">
            {error ? <span className="chip bad" title={error}>{error}</span>
              : info && <span className="chip" title={fullPlace(info)}>{flag(info.code)} {place(info)}</span>}
            {/* the exit's route, which picks it: the rules (the mode's name), DIRECT or a node */}
            <Picker className="chip accent" value={policy} options={[RULES, ...names]} onChange={setPolicy} label={policy === RULES ? modes[mode] ?? RULES : `经 ${policy}`} />
            <IconButton icon="refresh" title="重新查询" onClick={() => check(true)} disabled={busy} spin={busy} />
          </div>
        </div>
        <button className={`hero-btn ${paused ? "paused" : ""}`} onClick={toggle} title={paused ? "恢复代理" : "暂停代理，全部直连"}>
          <Cookie className="hero-cookie" />
          <svg className="hero-glyph" viewBox="0 0 24 24" fill="currentColor">{paused ? <path d="M8 5.5v13l11-6.5z" /> : <><rect x="6.5" y="5" width="3.6" height="14" rx="1.8" /><rect x="13.9" y="5" width="3.6" height="14" rx="1.8" /></>}</svg>
        </button>
      </div>
      <div className="hero-stats">
        <div><div className="label">网络</div><div className="val" title={activity?.network}>{activity?.network || "—"}</div></div>
        <div><div className="label">延迟</div><div className="val" title={lat || ""}>{latency?.internet ? <span className={delayClass(latency.internet)}>{latency.internet}<small> ms</small></span> : "—"}</div></div>
        <div><div className="label">组织</div><div className="val" title={info?.isp}>{info?.isp || "—"}</div></div>
      </div>
    </div>
  );
}

const modeList: [string, string, string][] = [["rule", "规则", "rules"], ["global", "全局", "dns"], ["direct", "直连", "direct"]];

// ModeCard picks the outbound mode.
function ModeCard({ mode, setMode }: { mode: string; setMode: (m: string) => void }) {
  return (
    <div className="card row-card" style={{ gridArea: "mode" }}>
      <div className="card-title"><Icon name="proxies" className="card-icon" />出站模式</div>
      <div className="mode-seg">
        {modeList.map(([k, label, icon]) => <button key={k} className={mode === k ? "on" : ""} onClick={() => setMode(k)}><Icon name={icon} className="btn-icon" />{label}</button>)}
      </div>
    </div>
  );
}

// ToggleCard is one switch as a card: an icon that lights up while it is on (orange while only part
// of it is: partial enhancement), a title and its state.
function ToggleCard({ area, icon, title, sub, on, part, disabled, onChange, more }: {
  area: string; icon: string; title: string; sub: string; on: boolean; part?: boolean; disabled?: boolean; onChange: (v: boolean) => void; more?: () => void;
}) {
  return (
    <div className="card row-card" style={{ gridArea: area }}>
      <span className={`toggle-icon ${on ? "on" : part ? "part" : ""}`}><Icon name={icon} className="card-icon" /></span>
      <div className="toggle-text">
        <div className="toggle-title">{title}{more && <IconButton icon="more" title="打开设置" onClick={more} />}</div>
        <div className="muted small">{sub}</div>
      </div>
      <Switch on={on} disabled={disabled} onChange={onChange} />
    </div>
  );
}

// useHeld shows a switch's new position at once and holds it until the real state (actual) changes,
// or drops it when the change failed.
function useHeld(actual: boolean): [boolean, (v: boolean, p: Promise<unknown>) => void] {
  const [held, setHeld] = useState<boolean | null>(null);
  useEffect(() => setHeld(null), [actual]);
  return [held ?? actual, (v, p) => { setHeld(v); p.catch(() => setHeld(null)); }];
}

// LanCard is this machine's LAN address and whether other devices may use the proxy port.
function LanCard({ lan, reload, go }: { lan: LanInfo | null; reload: () => void; go: (page: string) => void }) {
  const [allow, setAllow] = useHeld(!!lan?.allowLan);
  const addrs = lan?.addresses ?? [];
  const copy = () => { Clipboard.SetText(`${addrs[0]}:${lan?.port}`).catch(() => {}); }; // Wails': navigator.clipboard is missing in some webviews
  return (
    <div className="card" style={{ gridArea: "lan" }}>
      <Head icon="devices" title="局域网">
        {addrs.length > 0 && <IconButton icon="copy" title="复制代理地址" onClick={copy} />}
        <IconButton icon="more" title="设备" onClick={() => go("devices")} />
      </Head>
      <div className="ip-big selectable" title={addrs.join("\n")}>{addrs[0] ?? "—"}</div>
      <div className="inset-row">
        <span className="toggle-icon small"><Icon name="devices" className="card-icon" /></span>
        <div className="toggle-text"><div className="toggle-title">局域网代理</div><div className="muted small">{allow ? `端口 ${lan?.port}` : "已关闭"}</div></div>
        <Switch on={allow} disabled={!lan} onChange={(v) => setAllow(v, DeviceService.SetAllowLan(v).finally(reload))} />
      </div>
    </div>
  );
}

// ServicesCard is whether well-known sites answer through the rules, and how fast.
function ServicesCard({ measure }: { measure: () => void }) {
  const [list, setList] = useState<Service[]>([]);
  const [busy, setBusy] = useState(false);
  // rounds with no answer at all before the first that had one: just after launch the core is
  // still settling, so those read as 测试中, not 不可达, until the third
  const [blank, setBlank] = useState(0);
  const load = () => {
    setBusy(true);
    return CoreService.Services().then((s) => {
      setList(s ?? []);
      setBlank((n) => (n < 0 || s?.some((x) => x.ms) ? -1 : n + 1));
    }).finally(() => setBusy(false));
  };
  const down = list.filter((s) => !s.ms).length;
  const settling = blank >= 0 && blank < 3;
  usePoll(load, settling ? 5_000 : down ? 15_000 : 60_000);
  const testing = busy || settling;
  return (
    <div className="card" style={{ gridArea: "svc" }}>
      <Head icon="vpn" title={<>服务状态{list.length > 0 && !testing && <span className={`chip ${down ? "bad" : "good"}`}>{down ? `${down} 项不可达` : "全部可达"}</span>}</>}>
        <IconButton icon="processes" title="重新测试" onClick={() => { load(); measure(); }} disabled={busy} />
      </Head>
      <div className="svc-grid">
        {(list.length ? list : ["Google", "GitHub", "Claude", "ChatGPT"].map((name) => ({ name, ms: 0 }))).map((t) => (
          <div key={t.name} className="svc">
            <span className="svc-logo"><Brand name={t.name} /><i className={`dot ${t.ms > 0 ? "on" : testing ? "" : "err"}`} /></span>
            <span className="svc-text">
              <span className="svc-name">{t.name}</span>
              <span className={`svc-ms ${delayClass(t.ms)}`}>{t.ms > 0 ? `${t.ms} ms` : testing ? "测试中…" : "不可达"}</span>
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

// MemoryCard is what Connaio holds in memory: the app (its window's web view included) and the core.
function MemoryCard() {
  const [mem, setMem] = useState<Memory | null>(null);
  usePoll(() => CoreService.Memory().then(setMem), 3000);
  const app = mem?.app ?? 0, core = mem?.core ?? 0;
  const [total, unit] = size(app + core);
  return (
    <div className="card" style={{ gridArea: "mem" }}>
      <Head icon="memory" title="内存" />
      <Num value={mem ? total : "—"} unit={mem ? unit : ""} />
      <div className="traffic-foot">
        <div className="split-bar">
          {app > 0 && <i style={{ flexGrow: app, background: "var(--accent)" }} />}
          {core > 0 && <i style={{ flexGrow: core, background: "color-mix(in srgb, var(--accent) 45%, transparent)" }} />}
        </div>
        <div className="legend">
          <span><i style={{ background: "var(--accent)" }} />应用 <b>{sizeText(app)}</b></span>
          <span><i style={{ background: "color-mix(in srgb, var(--accent) 45%, transparent)" }} />内核 <b>{core ? sizeText(core) : "—"}</b></span>
        </div>
      </div>
    </div>
  );
}

// TrafficSum is today's or this month's traffic, split into direct and proxied, above the ranking's chart.
function TrafficSum({ activity }: { activity: ActivityData | null }) {
  const [range, setRange] = useState<"today" | "month">("today");
  const [confirming, setConfirming] = useState(false); // 清除 asks once more before it forgets
  useEffect(() => {
    if (!confirming) return;
    const t = setTimeout(() => setConfirming(false), 3000);
    return () => clearTimeout(t);
  }, [confirming]);
  const clear = () => {
    if (!confirming) { setConfirming(true); return; }
    setConfirming(false);
    CoreService.ClearTraffic();
  };
  const { direct, proxy } = activity?.[range] ?? { direct: 0, proxy: 0 };
  const [total, unit] = size(direct + proxy);
  return (
    <div className="traffic-sum">
      <div className="traffic-top">
        <Num value={total} unit={unit} />
        <span className="traffic-range">
          {(["today", "month"] as const).map((r) => <button key={r} className={range === r ? "on" : ""} onClick={() => setRange(r)}>{r === "today" ? "今日" : "本月"}</button>)}
          <button className={`link-btn ${confirming ? "danger" : ""}`} onClick={clear} title="清除所有流量统计">{confirming ? "确认清除" : "清除"}</button>
        </span>
      </div>
      <div className="split-bar">
        {direct > 0 && <i style={{ flexGrow: direct, background: "var(--up)" }} />}
        {proxy > 0 && <i style={{ flexGrow: proxy, background: "var(--down)" }} />}
      </div>
      <div className="legend">
        <span><i style={{ background: "var(--up)" }} />DIRECT <b>{sizeText(direct)}</b></span>
        <span><i style={{ background: "var(--down)" }} />代理 <b>{sizeText(proxy)}</b></span>
      </div>
    </div>
  );
}

// ExitCell is the exit address of one route, looked up once per session.
function ExitCell({ policy }: { policy: string }) {
  const [text, setText] = useState("查询出口…");
  const [full, setFull] = useState("");
  useEffect(() => {
    if (policy === "REJECT") { setText("拒绝"); return; }
    exitIP(policy).then((i) => { setText(`${flag(i.code)} ${i.ip} · ${place(i)}`); setFull(`${i.ip} · ${fullPlace(i)} · ${i.isp}`); }).catch(() => setText("出口查询失败"));
  }, [policy]);
  return <span className="selectable" title={full}>{text}</span>;
}

let watched = ""; // the application the monitor shows, kept across page switches

// useWatch follows one application's live connections, polled every ms: the applications with any
// (as the applications card names them), the one shown (until one is chosen, the one with the most
// connections, then kept) and its connections by route.
export function useWatch(ms: number) {
  const [conns, setConns] = useState<Conn[]>([]);
  const [proc, setProc] = useState(watched);
  usePoll(() => CoreService.Connections().then((c) => setConns(c ?? [])), ms);
  const choose = (p: string) => { watched = p; setProc(p); };
  const counts = new Map<string, number>();
  for (const c of conns) counts.set(c.app, (counts.get(c.app) ?? 0) + 1);
  const busiest = [...counts].sort((a, b) => b[1] - a[1])[0]?.[0] ?? "";
  const shown = proc || busiest;
  useEffect(() => { if (!proc && busiest) choose(busiest); }, [proc, busiest]);
  const procs = [...new Set([...counts.keys(), ...(shown ? [shown] : [])])].sort((a, b) => a.localeCompare(b));
  const routes = new Map<string, Conn[]>();
  for (const c of conns) if (c.app === shown) routes.set(c.chain || "DIRECT", [...(routes.get(c.chain || "DIRECT") ?? []), c]);
  return { conns, procs, shown, choose, routes };
}

// Routes lists each route the shown application takes (the rule's policy down to the node), the
// address that route comes out at, and the hosts on it.
export function Routes({ shown, routes }: { shown: string; routes: Map<string, Conn[]> }) {
  if (routes.size === 0) return <div className="muted small">{shown ? `${shown} 当前没有连接` : "当前没有连接"}</div>;
  return (
    <>
      {[...routes].map(([chain, cs]) => (
        <div key={chain} className="watch-route" title={[...new Set(cs.map((c) => c.host))].join("\n")}>
          <div className="watch-head"><span className="watch-chain">{chain}</span><span className="muted small">{cs.length} 个连接</span></div>
          <div className="watch-exit"><ExitCell policy={chain.split(" → ").pop()!} /></div>
        </div>
      ))}
    </>
  );
}

// niceScale rounds a peak up to 1, 2 or 5 × 10ⁿ bytes, so the gridlines land on round values.
function niceScale(v: number) {
  const p = 10 ** Math.floor(Math.log10(Math.max(v, 1)));
  return [1, 2, 5, 10].map((m) => m * p).find((m) => m >= v)!;
}

// HourChart is today's traffic per hour: a bar where there was any, a dot where there was none.
function HourChart({ values }: { values: number[] }) {
  const peak = Math.max(0, ...values);
  const top = niceScale(Math.max(peak, 1));
  const now = new Date().getHours();
  return (
    <div className="hours">
      {[1, 0.5].map((f) => <div key={f} className="hours-grid" style={{ bottom: `calc(18px + ${f} * (100% - 18px))` }}>{peak > 0 && <span>{sizeText(top * f)}</span>}</div>)}
      <div className="hours-bars">
        {values.map((v, h) => (
          <div key={h} className="hours-col" title={`${h}:00 · ${sizeText(v)}`}>
            {v > 0 ? <i style={{ height: `${Math.max(4, (v / top) * 100)}%` }} /> : <b className={h > now ? "later" : ""} />}
          </div>
        ))}
      </div>
      <div className="hours-axis">{["12AM", "6AM", "12PM", "6PM"].map((l) => <span key={l}>{l}</span>)}</div>
    </div>
  );
}

// ClientIcon is a client's Finder icon (an app's, or an executable's generic one), or a device glyph.
export function ClientIcon({ path }: { path?: string }) {
  const src = useCachedURL(path || (platform === "linux" ? "device" : undefined), SettingsService.AppIcon); // Linux has a theme icon for devices
  if (src) return <img className="client-icon" src={src} alt="" />;
  return (
    <span className="client-icon device">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
        <path d={path ? "M5 4h14a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1zM8 9l3 3-3 3M13 15h3" : "M4 6h12a1 1 0 0 1 1 1v8H3V7a1 1 0 0 1 1-1zM1.5 18h17M19 9h3v10h-3z"} />
      </svg>
    </span>
  );
}

// RankPane is the traffic totals and today's by hour beside the top applications (a command counted towards the
// one it was started from), hosts or policies.
function RankPane({ activity, by, proxyOnly }: { activity: ActivityData | null; by: number; proxyOnly: boolean }) {
  const pick = (s: { direct: number; proxy: number }) => (proxyOnly ? s.proxy : s.direct + s.proxy);
  const lists: (Usage[] | null | undefined)[] = [activity?.byProcess, activity?.byHost, activity?.byPolicy];
  // as many whole rows as the pane is tall: no scrollbar, no row cut in half
  const box = useRef<HTMLDivElement>(null);
  const [fit, setFit] = useState(6);
  useEffect(() => {
    const el = box.current!;
    const measure = () => {
      const row = el.firstElementChild as HTMLElement | null;
      if (!row?.classList.contains("row")) return;
      const gap = parseFloat(getComputedStyle(el).rowGap) || 0;
      setFit(Math.max(1, Math.floor((el.clientHeight + gap) / (row.offsetHeight + gap))));
    };
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const rows = (lists[by] ?? []).filter((r) => pick(r) > 0).sort((a, b) => pick(b) - pick(a)).slice(0, fit);
  const peak = Math.max(1, ...rows.map(pick));
  return (
    <div className="pane rank-pane">
      <div className="rank-left">
        <TrafficSum activity={activity} />
        <HourChart values={(activity?.hours ?? []).map(pick)} />
      </div>
      <div ref={box} className={`rank ${by === 0 ? "with-icons" : ""}`}>
        {rows.length === 0 ? <div className="muted small">今天还没有流量</div> : rows.map((r) => (
            <div key={r.name} className="row">
              {by === 0 && <ClientIcon path={r.icon} />}
              <span className="name" title={r.name}>{r.name}</span>
              <span className="bytes">{sizeText(pick(r))}</span>
              <span className="bar"><i style={{ width: `${(pick(r) / peak) * 100}%` }} /></span>
            </div>
          ))}
      </div>
    </div>
  );
}

// ListCard is today's ranking and one application's routes, as tabs, with the live connections'
// count opening the connections page.
function ListCard({ activity, go }: { activity: ActivityData | null; go: (page: string) => void }) {
  const [tab, setTab] = useState(0);
  const [by, setBy] = useState(0);
  const [proxyOnly, setProxyOnly] = useState(false);
  const { procs, shown, choose, routes } = useWatch(2000);
  return (
    <div className="card list-card" style={{ gridArea: "list" }}>
      <div className="head">
        <div className="tabs">
          <button className={tab === 0 ? "on" : ""} onClick={() => setTab(0)}>流量排行</button>
          <button className={tab === 1 ? "on" : ""} onClick={() => setTab(1)}>进程监视</button>
        </div>
        <div className="head-actions">
          {tab === 0 ? <>
            <div className="seg">{["应用", "域名", "策略"].map((n, i) => <button key={n} className={i === by ? "on" : ""} onClick={() => setBy(i)}>{n}</button>)}</div>
            <div className="seg">
              <button className={!proxyOnly ? "on" : ""} onClick={() => setProxyOnly(false)}>全部</button>
              <button className={proxyOnly ? "on" : ""} onClick={() => setProxyOnly(true)}>仅代理</button>
            </div>
          </> : <>
            <IconButton icon="window" title="在悬浮窗中显示" onClick={() => SettingsService.OpenMonitor()} />
            <Picker value={shown} options={procs} onChange={choose} />
          </>}
          <button className="chip link-chip" onClick={() => go("dashboard")} title="连接">{activity?.connections ?? 0} 个连接<Icon name="more" className="chip-icon" /></button>
        </div>
      </div>
      {tab === 0 ? <RankPane activity={activity} by={by} proxyOnly={proxyOnly} /> : <div className="pane route-tiles"><Routes shown={shown} routes={routes} /></div>}
    </div>
  );
}

// NodeFlag is the flag of the country a node comes out in (its exit looked up once per session), or
// fallback until that is known.
function NodeFlag({ node, fallback }: { node: string; fallback: string }) {
  const [code, setCode] = useState("");
  useEffect(() => {
    setCode("");
    if (node && node !== "REJECT") exitIP(node).then((i) => setCode(i.code)).catch(() => {});
  }, [node]);
  return code ? <span className="mono flag" title={code}>{flag(code)}</span> : <span className="mono">{fallback}</span>;
}

// GroupsCard is the policy groups, each with the node it uses now; a tile opens the policies page.
function GroupsCard({ go, running }: { go: (page: string) => void; running: boolean }) {
  const [groups, setGroups] = useState<Group[]>([]);
  usePoll(() => CoreService.Groups().then((g) => setGroups((g ?? []).filter((x) => x.name !== "GLOBAL"))), 5000, running);
  return (
    <div className="card" style={{ gridArea: "side" }}>
      <Head icon="layers" title={<>策略组<span className="chip">{groups.length}</span></>}>
        <IconButton icon="more" title="策略" onClick={() => go("proxies")} />
      </Head>
      {groups.length === 0 ? <div className="pane-empty muted small">配置中没有策略组</div> : (
        <div className="pane group-tiles">
          {groups.map((g) => {
            const delay = g.all?.find((p) => p.name === g.now)?.delay ?? 0;
            return (
              <button key={g.name} className="gtile" onClick={() => go("proxies")} title={`${g.name} → ${g.now}`}>
                <NodeFlag node={g.now} fallback={[...g.name][0]} />
                <span className="gtext"><span className="g-name">{g.name}</span><span className="g-now">{g.now || "—"}</span></span>
                {delay > 0 && <span className={`g-ms ${delayClass(delay)}`}>{delay} ms</span>}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

// HealthCard is the network checks, run on open, each with its fix; 诊断 opens the step-by-step diagnosis.
function HealthCard() {
  const [items, setItems] = useState<RepairItem[]>([]);
  const [busy, setBusy] = useState("");
  const [notes, setNotes] = useState<Record<string, string>>({});
  const [diagnosing, setDiagnosing] = useState(false);
  const load = () => ToolService.RepairChecks().then((r) => setItems(r ?? []));
  useEffect(() => { load(); }, []);
  const fix = (id: string) => {
    setBusy(id);
    ToolService.Repair(id).then((m) => setNotes((n) => ({ ...n, [id]: m }))).catch((e) => setNotes((n) => ({ ...n, [id]: errText(e, "失败，详情见事件") }))).finally(() => { setBusy(""); load(); });
  };
  const found = items.filter((i) => i.found);
  const isCheck = (i: RepairItem) => ["proxy", "orphan", "tun"].includes(i.id); // a check, as opposed to a reset
  return (
    <div className="card health-card" style={{ gridArea: "health" }}>
      <Head icon="wrench" title={<>网络健康{items.length > 0 && <span className={`chip ${found.length ? "bad" : "good"}`}>{found.length ? `${found.length} 个问题` : "未发现问题"}</span>}</>}>
        {found.length > 0 && <button className="link-btn" disabled={!!busy} onClick={() => found.forEach((i) => fix(i.id))}>全部修复</button>}
        <IconButton icon="refresh" title="重新检查" onClick={load} disabled={!!busy} />
        <button className="btn small-btn" onClick={() => setDiagnosing(true)}>诊断</button>
      </Head>
      <div className="health-rows">
        {items.length === 0 && <div className="muted small">检查中…</div>}
        {items.map((i) => {
          const detail = notes[i.id] ?? `${i.found ? "发现问题：" : ""}${i.detail}${i.reboot ? "，重启后生效" : ""}`;
          return (
            <div key={i.id} className="tool-row">
              <span className="tool-title">{i.title}</span>
              <span className={`tool-detail ${i.found && !notes[i.id] ? "delay-bad" : "muted"}`} title={detail}>{detail}</span>
              <span className="tool-action">
                {i.fix && (i.found || !isCheck(i)) ? <button className="btn" disabled={!!busy} onClick={() => fix(i.id)}>{busy === i.id ? "处理中…" : i.fix}</button>
                  : isCheck(i) ? <span className="delay-good">正常</span> : null}
              </span>
            </div>
          );
        })}
      </div>
      {diagnosing && <DiagnoseSheet onClose={() => setDiagnosing(false)} />}
    </div>
  );
}

// DiagnoseSheet checks the configuration, network, DNS and proxy step by step as soon as it opens,
// each step shown as it finishes.
function DiagnoseSheet({ onClose }: { onClose: () => void }) {
  const [sections, setSections] = useState<DiagSection[]>([]);
  const [running, setRunning] = useState(false);
  const diagnose = () => {
    setSections([]);
    setRunning(true);
    ToolService.Diagnose().finally(() => setRunning(false));
  };
  useEffect(() => {
    const off = on("diagnose", (d) => setSections((s) => [...s, d]));
    diagnose();
    return () => { off(); ToolService.CancelDiagnose(); };
  }, []);
  return (
    <div className="modal-bg" onClick={running ? undefined : onClose}>
      <div className="sheet diag-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="sheet-head"><div className="sheet-title">网络诊断</div></div>
        <div className="sheet-body diag-log tool-log">
          {sections.length === 0 && <div className="muted">诊断中…</div>}
          <DiagLog sections={sections} />
        </div>
        <div className="sheet-foot">
          {running && <span className="spinner" />}
          <span className="grow" />
          {running ? <button className="btn" onClick={() => ToolService.CancelDiagnose()}>终止</button> : <button className="btn" onClick={diagnose}>重新诊断</button>}
          <button className="btn primary" disabled={running} onClick={onClose}>完成</button>
        </div>
      </div>
    </div>
  );
}

export default function Activity({ status, activity, sysProxy, enhanced, rooted, go }: {
  status: Status; activity: ActivityData | null; sysProxy: boolean; enhanced: string; rooted: boolean; go: (page: string) => void;
}) {
  const history = useHistory();
  const [latency, setLatency] = useState<Latency | null>(null);
  const [lan, setLan] = useState<LanInfo | null>(null);
  const [proxyOn, setProxyOn] = useHeld(sysProxy);
  // the mode shows at once when chosen, here or by the pause button, and holds until the core reports it
  const [pendingMode, setPendingMode] = useState<string | null>(null);
  useEffect(() => setPendingMode(null), [activity?.mode]);
  const mode = pendingMode ?? activity?.mode ?? "";
  const setMode = (m: string) => { if (m === mode) return; setPendingMode(m); CoreService.SetMode(m).catch(() => setPendingMode(null)); };
  const [tunOn, setTunOn] = useHeld(enhanced === "tun");

  const measure = useCallback(() => CoreService.Latency().then(setLatency), []);
  usePoll(measure, 60_000, status.running);
  const loadLan = () => DeviceService.Lan().then(setLan).catch(() => setLan(null));
  usePoll(loadLan, 10_000, status.running); // allow-lan and the port can change from the devices page or the tray

  if (!status.running) {
    return <div className="card"><div className="muted">{status.error ? "内核未运行" : "内核启动中…"}</div></div>;
  }

  const tunSub = !rooted ? "需先在「通用」中授予 root 权限" : enhanced === "tun" ? "TUN 接管全部流量" : enhanced === "process-tun" ? "部分增强运行中" : "已关闭";
  return (
    <div className="activity">
      <StatusCard activity={activity} mode={mode} setMode={setMode} latency={latency} />
      <SpeedCard dir="up" values={history.map((t) => t.up)} />
      <SpeedCard dir="down" values={history.map((t) => t.down)} />
      <ModeCard mode={mode} setMode={setMode} />
      <ToggleCard area="sys" icon="shuffle" title="系统代理" sub={proxyOn ? `端口 ${lan?.port ?? "—"}` : "已关闭"} on={proxyOn}
        onChange={(v) => setProxyOn(v, SystemProxyService.SetEnabled(v))} />
      <ToggleCard area="tun" icon="processes" title="增强模式" sub={tunSub} on={tunOn} part={enhanced === "process-tun"} disabled={!rooted}
        onChange={(v) => setTunOn(v, EnhancedService.SetFull(v))} more={() => go("processes")} />
      <LanCard lan={lan} reload={loadLan} go={go} />
      <HealthCard />
      <ServicesCard measure={measure} />
      <MemoryCard />
      <ListCard activity={activity} go={go} />
      <GroupsCard go={go} running={status.running} />
    </div>
  );
}
