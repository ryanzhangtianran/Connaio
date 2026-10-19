import { useCallback, useEffect, useState } from "react";
import { CoreService, ProfileService, SettingsService, ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Activity as ActivityData, Conn, DiagSection, IPInfo, Latency, RepairItem, Status, Usage } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { nodesOf } from "./Proxies";
import { errText, platform, size, sizeText, speed } from "./format";
import { DiagLog } from "./ProxyDiagnose";
import { on, usePoll, useCachedURL, useClickAway } from "./hooks";
import { useHistory } from "./store";

const modes: Record<string, string> = { rule: "规则判定", global: "全局代理", direct: "直接连接" };

function Num({ value, unit, cls = "big" }: { value: string; unit: string; cls?: string }) {
  return <div className={cls}>{value}<small>{unit}</small></div>;
}

function Ms({ ms }: { ms: number }) {
  return ms > 0 ? <Num cls="mid" value={String(ms)} unit="ms" /> : <div className="mid muted">—</div>;
}

// Sparkline plots the recent seconds across the full width, oldest at the left, scaled to the peak.
function Sparkline({ values, color }: { values: number[]; color: string }) {
  const [hover, setHover] = useState<number | null>(null);
  if (values.length < 2) return null;
  const top = Math.max(1000, ...values); // the peak is the top gridline; 1 KB/s keeps idle lines flat
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
  const ago = hover != null ? values.length - 1 - hover : 0;
  return (
    <div className="spark" onPointerMove={move} onPointerLeave={() => setHover(null)}>
      <div className="spark-grid" style={{ top: "22%" }}><span>{speed(top).join(" ")}</span></div>
      <svg viewBox="0 0 100 100" preserveAspectRatio="none">
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor={color} stopOpacity="0.2" />
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
            {speed(values[hover]).join(" ")} · {ago === 0 ? "现在" : `${ago} 秒前`}
          </div>
        </>
      )}
    </div>
  );
}

function SpeedCard({ label, values, color }: { label: string; values: number[]; color: string }) {
  const [v, u] = speed(values[values.length - 1] ?? 0);
  return (
    <div className="card speed-card">
      <div className="label">{label}</div>
      <Num value={v} unit={u} />
      <Sparkline values={values} color={color} />
    </div>
  );
}

// TotalCard shows today's or this month's traffic, split into direct and proxied.
function TotalCard({ activity }: { activity: ActivityData | null }) {
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
  const part = (bytes: number) => { const [v, u] = size(bytes); return <Num cls="mid" value={v} unit={u} />; };
  return (
    <div className="card">
      <div className="head">
        <div className="label">总计</div>
        <div className="head-actions">
          <button className={`link-btn ${confirming ? "danger" : ""}`} onClick={clear} title="清除所有流量统计">{confirming ? "确认清除" : "清除"}</button>
          <div className="seg">
            <button className={range === "today" ? "on" : ""} onClick={() => setRange("today")}>今日</button>
            <button className={range === "month" ? "on" : ""} onClick={() => setRange("month")}>本月</button>
          </div>
        </div>
      </div>
      <Num value={total} unit={unit} />
      <div className="split">
        <div><div className="label">DIRECT</div>{part(direct)}</div>
        <div className="right"><div className="label">代理</div>{part(proxy)}</div>
      </div>
      <div className="split-bar">
        {direct > 0 && <i style={{ flexGrow: direct, background: "var(--up)" }} />}
        {proxy > 0 && <i style={{ flexGrow: proxy, background: "var(--down)" }} />}
      </div>
    </div>
  );
}

// NetTools is the network repair (its checks run on open, each with its fix) and the diagnosis (on
// demand, each step shown as it finishes); every action sits in the head, beside the title.
function NetTools() {
  const [tab, setTab] = useState(0);
  const [items, setItems] = useState<RepairItem[]>([]);
  const [busy, setBusy] = useState("");
  const [notes, setNotes] = useState<Record<string, string>>({});
  const [sections, setSections] = useState<DiagSection[]>([]);
  const [running, setRunning] = useState(false);
  const load = () => ToolService.RepairChecks().then((r) => setItems(r ?? []));
  useEffect(() => {
    load();
    const off = on("diagnose", (d) => setSections((s) => [...s, d]));
    return () => { off(); ToolService.CancelDiagnose(); };
  }, []);
  const fix = (id: string) => {
    setBusy(id);
    ToolService.Repair(id).then((m) => setNotes((n) => ({ ...n, [id]: m }))).catch((e) => setNotes((n) => ({ ...n, [id]: errText(e, "失败，详情见事件") }))).finally(() => { setBusy(""); load(); });
  };
  const diagnose = () => {
    setSections([]);
    setRunning(true);
    ToolService.Diagnose().finally(() => setRunning(false));
  };
  const found = items.filter((i) => i.found);
  const isCheck = (i: RepairItem) => ["proxy", "orphan", "tun"].includes(i.id); // a check, as opposed to a reset
  return (
    <div className="card tools-card">
      <div className="head">
        <div className="label">
          网络诊断与修复
          {tab === 0 && <button className="icon-btn" onClick={load} disabled={!!busy} title="重新检查">↻</button>}
          {tab === 1 && (running ? <span className="spinner" /> : sections.length > 0 && <button className="icon-btn" onClick={diagnose} title="重新诊断">↻</button>)}
        </div>
        <div className="head-actions">
          {tab === 0 && found.length > 0 && <button className="link-btn" disabled={!!busy} onClick={() => found.forEach((i) => fix(i.id))}>全部修复</button>}
          {tab === 1 && running && <button className="link-btn" onClick={() => ToolService.CancelDiagnose()}>终止</button>}
          <div className="seg">
            {["修复", "诊断"].map((t, i) => <button key={t} className={i === tab ? "on" : ""} onClick={() => setTab(i)}>{t}</button>)}
          </div>
        </div>
      </div>
      <div className="card-scroll">
        {tab === 0 ? (
          items.length === 0 ? <div className="muted small">检查中…</div> : items.map((i) => {
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
          })
        ) : !running && sections.length === 0 ? (
          <div className="tool-empty">
            <span className="muted small">逐项检查配置、网络、DNS 和代理</span>
            <button className="btn" onClick={diagnose}>开始诊断</button>
          </div>
        ) : <div className="tool-log"><DiagLog sections={sections} /></div>}
      </div>
    </div>
  );
}

// Picker is a compact dropdown for a card's head. Not a <select>: on Linux its popup is GTK's own
// menu, out of keeping with the page; the menu is fixed to the window so the card doesn't clip it.
export function Picker({ value, options, onChange }: { value: string; options: string[]; onChange: (v: string) => void }) {
  const [at, setAt] = useState<DOMRect | null>(null);
  useClickAway(!!at, () => setAt(null));
  return (
    <>
      {/* the click stops here: opening attaches the click-away listener, which it would reach next */}
      <button className="picker" onClick={(e) => { e.stopPropagation(); setAt(at ? null : e.currentTarget.getBoundingClientRect()); }}>
        <span>{value || "—"}</span>
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

// ExitIP shows where traffic through a chosen node comes out: the address, then where it is and
// whose network, laid out like the cards beside it. Nodes and DIRECT only: a group's exit is its node's.
function ExitIP() {
  const [names, setNames] = useState<string[]>([]);
  const [policy, setPolicy] = useState("");
  const [info, setInfo] = useState<IPInfo | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    CoreService.Groups().then((g) => {
      const nodes = nodesOf(g ?? []).map((n) => n.name);
      setNames(["DIRECT", ...nodes]); // DIRECT: this network's own address, to compare against
      setPolicy((p) => p || nodes[0] || "DIRECT"); // a node first: DIRECT when the profile has none
    });
  }, []);
  const check = (fresh: boolean) => {
    if (!policy) return;
    if (fresh) delete exitIPs[policy];
    setBusy(true); setError(""); setInfo(null);
    exitIP(policy).then(setInfo).catch((e) => setError(errText(e, "失败，详情见事件"))).finally(() => setBusy(false));
  };
  useEffect(() => check(false), [policy]);
  return (
    <div className="card">
      <div className="head">
        <div className="label">
          出口 IP
          <button className="icon-btn" onClick={() => check(true)} disabled={busy} title="重新查询">↻</button>
        </div>
        <Picker value={policy} options={names} onChange={setPolicy} />
      </div>
      {error ? <div className="exit-error small" title={error}>{error}</div>
        : <div className={`ip-big selectable ${info ? "" : "muted"}`}>{info?.ip ?? (busy ? "查询中…" : "—")}</div>}
      <div className="stats exit-stats">
        <div><div className="label">位置</div><div className="exit-value" title={info ? fullPlace(info) : ""}>{info ? `${flag(info.code)} ${place(info)}` : "—"}</div></div>
        <div><div className="label">运营商</div><div className="exit-value" title={info?.isp}>{info?.isp || "—"}</div></div>
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

// ProcessWatch is the activity page's card of useWatch, which also opens as a floating window.
function ProcessWatch() {
  const { procs, shown, choose, routes } = useWatch(2000);
  return (
    <div className="card">
      <div className="head">
        <div className="label">
          进程监视
          <button className="icon-btn" onClick={() => SettingsService.OpenMonitor()} title="在悬浮窗中显示">⧉</button>
        </div>
        <Picker value={shown} options={procs} onChange={choose} />
      </div>
      <div className="card-scroll"><Routes shown={shown} routes={routes} /></div>
    </div>
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

// Ranking is today's traffic as two cards sharing the 全部 / 仅代理 choice: per hour, and the top
// eight applications (a command counted towards the one it was started from), hosts or policies,
// as many as the card shows whole.
function Ranking({ activity }: { activity: ActivityData }) {
  const [proxyOnly, setProxyOnly] = useState(false);
  const [tab, setTab] = useState(0);
  const pick = (s: { direct: number; proxy: number }) => (proxyOnly ? s.proxy : s.direct + s.proxy);
  const tabs: [string, Usage[] | null][] = [["应用", activity.byProcess], ["域名", activity.byHost], ["策略", activity.byPolicy]];
  const rows = (tabs[tab][1] ?? []).filter((r) => pick(r) > 0).sort((a, b) => pick(b) - pick(a)).slice(0, 8);
  const peak = Math.max(1, ...rows.map(pick));
  return (
    <>
      <div className="card" style={{ gridArea: "hours" }}>
        <div className="head">
          <div className="label">今日流量</div>
          <div className="seg">
            <button className={!proxyOnly ? "on" : ""} onClick={() => setProxyOnly(false)}>全部</button>
            <button className={proxyOnly ? "on" : ""} onClick={() => setProxyOnly(true)}>仅代理</button>
          </div>
        </div>
        <HourChart values={(activity.hours ?? []).map(pick)} />
      </div>
      <div className="card ranking" style={{ gridArea: "rank" }}>
        <div className="head">
          <div className="label">流量排行{proxyOnly && " · 仅代理"}</div>
          <div className="seg">
            {tabs.map(([name], i) => <button key={name} className={i === tab ? "on" : ""} onClick={() => setTab(i)}>{name}</button>)}
          </div>
        </div>
        {rows.length === 0 && <div className="muted small">今天还没有流量</div>}
        <div className="rank-scroll">
          <div className={`rank ${tab === 0 ? "with-icons" : ""}`}>
            {rows.map((r) => (
              <div key={r.name} className="row">
                {tab === 0 && <ClientIcon path={r.icon} />}
                <span className="name" title={r.name}>{r.name}</span>
                <span className="bytes">{sizeText(pick(r))}</span>
                <span className="bar"><i style={{ width: `${(pick(r) / peak) * 100}%` }} /></span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </>
  );
}

export default function Activity({ status, activity }: { status: Status; activity: ActivityData | null }) {
  const history = useHistory();
  const [latency, setLatency] = useState<Latency | null>(null);
  const [measuring, setMeasuring] = useState(false);
  const [profile, setProfile] = useState(""); // the profile in use, by name
  useEffect(() => {
    const show = (p: { current: string; profiles: { id: string; name: string }[] | null }) =>
      setProfile((p.profiles ?? []).find((x) => x.id === p.current)?.name ?? "");
    ProfileService.Profiles().then(show);
    return on("profiles", (d) => show(d));
  }, []);
  const [ip, setIP] = useState("");

  const measure = useCallback(() => {
    setMeasuring(true);
    CoreService.Latency().then(setLatency).finally(() => setMeasuring(false));
  }, []);

  usePoll(measure, 60_000, status.running);

  const mode = activity?.mode ?? "";
  useEffect(() => {
    if (!status.running) return;
    setIP("");
    CoreService.ExternalIP().then(setIP).catch(() => setIP("—"));
  }, [status.running, mode]);

  if (!status.running) {
    return (
      <div className="card"><div className="muted">{status.error ? "内核未运行" : "内核启动中…"}</div></div>
    );
  }

  return (
    <div className="activity">
      <div className="facts">
        <div className="fact"><div className="label">网络</div><div className="value">{activity?.network ?? "—"}</div></div>
        <div className="fact"><div className="label">配置</div><div className="value">{profile || "—"}</div></div>
        <div className="fact"><div className="label">出站模式</div><div className="value">{modes[mode] ?? mode}</div></div>
        <div className="fact"><div className="label">外部 IP</div><div className="value">{ip || "…"}</div></div>
      </div>

      {/* one grid of four columns: figures across the top, then charts, then lists */}
      <div className="grid">
        <div className="card" style={{ gridArea: "lat" }}>
          <div className="head">
            <div className="label">
              INTERNET 延迟
              <button className="icon-btn" onClick={measure} disabled={measuring} title="重新测试">↻</button>
            </div>
          </div>
          {latency && latency.internet > 0 ? <Num value={String(latency.internet)} unit="ms" /> : <div className="big muted">—</div>}
          <div className="stats">
            <div><div className="label">路由</div><Ms ms={latency?.router ?? 0} /></div>
            <div><div className="label">DNS</div><Ms ms={latency?.dns ?? 0} /></div>
            <div><div className="label">{(latency?.group ?? "代理").toUpperCase()}</div><Ms ms={latency?.proxy ?? 0} /></div>
          </div>
        </div>

        <div className="card" style={{ gridArea: "conn" }}>
          <div className="head">
            <div className="label">活动连接</div>
            <span className="dot on" />
          </div>
          <div className="big">{activity?.connections ?? 0}</div>
          <div className="stats">
            <div><div className="mid">{activity?.processes ?? 0}</div><div className="label">进程</div></div>
            <div><div className="mid">{activity?.devices ?? 0}</div><div className="label">设备</div></div>
            <div><div className="mid">{activity?.hosts ?? 0}</div><div className="label">域名</div></div>
          </div>
        </div>

        <div className="cell" style={{ gridArea: "total" }}><TotalCard activity={activity} /></div>
        <div className="cell" style={{ gridArea: "exit" }}><ExitIP /></div>
        <div className="cell" style={{ gridArea: "up" }}><SpeedCard label="上传" values={history.map((t) => t.up)} color="var(--up)" /></div>
        <div className="cell" style={{ gridArea: "down" }}><SpeedCard label="下载" values={history.map((t) => t.down)} color="var(--down)" /></div>
        {activity && <Ranking activity={activity} />}
        <div className="cell" style={{ gridArea: "watch" }}><ProcessWatch /></div>
        <div className="cell" style={{ gridArea: "tools" }}><NetTools /></div>
      </div>
    </div>
  );
}
