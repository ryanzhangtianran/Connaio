import { useEffect, useRef, useState } from "react";
import { CoreService, ProfileService, ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Conn, Status, Tunnel } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import TunnelDialog from "./TunnelDialog";
import { ExitCell } from "./Activity";
import { Icon } from "./icons";
import { nonNull, sizeText, speed } from "./format";
import { useConfirm, usePoll } from "./hooks";
import { AddTile } from "./Settings";

const stateLabel: Record<string, string> = { running: "运行中", connecting: "连接中", retrying: "重试中", stopped: "已停止" };
const stateClass: Record<string, string> = { running: "delay-good", connecting: "delay-warn", retrying: "delay-bad", stopped: "muted" };

// localRule takes what comes in through a tunnel straight to its local target (here Connaio's own
// proxy port, whose requests then follow the rules) instead of matching the rules itself.
export const localRule = (tunnel: string) => `IN-NAME,${tunnel},DIRECT`;

// serverOf is the host a tunnel of the GUI's own connects to, "" for one from the profile.
const serverOf = (own?: Record<string, any>) => own?.server ?? "";
const hostOf = (addr: string) => addr.slice(0, addr.lastIndexOf(":")).replace(/^\[|\]$/g, "");
const portOf = (addr?: string) => addr?.slice(addr.lastIndexOf(":") + 1) ?? "";

// PathLine draws a way as a band across its box: a stop per hop (a dot, the label over the value),
// the links between them sharing what width is left; a value too long for its room shows whole on hover.
function PathLine({ hops }: { hops: [string, React.ReactNode, string?][] }) {
  return (
    <div className="pathline">
      {hops.flatMap(([label, value, title], i) => [
        ...(i > 0 ? [<i key={`${label}-link`} className="pl-link" />] : []),
        <div key={label} className="pl-stop">
          <i className="pl-dot" />
          <span className="pl-text">
            <span className="pl-label">{label}</span>
            <b className="pl-value selectable" title={title ?? (typeof value === "string" ? value : undefined)}>{value}</b>
          </span>
        </div>,
      ])}
    </div>
  );
}

// clientOf names who connected at the server's end: an address, or the server itself for loopback.
const clientOf = (from: string) => {
  const host = hostOf(from);
  return host === "127.0.0.1" || host === "::1" ? "服务器本机" : host;
};

// hopsTo are the fixed stretch of a tunnel's way: the server and port the user reached, the tunnel,
// and its target here (Connaio's proxy port, whose requests then follow the rules, or a local service).
function hopsTo(hop: NonNullable<Conn["tunnel"]>, tunnels: Tunnel[], mine: Record<string, any>[], mixedPort: number): [string, string][] {
  const forward = tunnels.find((t) => t.name === hop.name)?.forwards?.find((f) => f.local === hop.target);
  const server = serverOf(mine.find((m) => m.name === hop.name));
  return [
    ["服务器", `${server || "远端"}${forward?.remote ? ":" + portOf(forward.remote) : ""}`],
    [`${hop.type} 隧道`, hop.name],
    [isProxyPort(hop.target, mixedPort) ? "代理端口" : "本机服务", hop.target],
  ];
}
// isProxyPort: the target is Connaio's proxy port, as the dialog's 按规则分流 forward's is, whose
// requests then follow the rules, rather than a service of its own.
const isProxyPort = (target: string, mixedPort: number) => !!mixedPort && target === `127.0.0.1:${mixedPort}`;

// TunnelPath is one connection's whole way, for its detail on the connections page: the user, the
// fixed stretch, the rule, the policy, the host and where it comes out.
export function TunnelPath({ c, tunnels, mine, mixedPort }: { c: Conn; tunnels: Tunnel[]; mine: Record<string, any>[]; mixedPort: number }) {
  const hop = c.tunnel!;
  const policy = c.chain.split(" → ").pop() ?? "";
  const hops: [string, React.ReactNode, string?][] = [
    ["客户端", clientOf(hop.from), hop.from],
    ...hopsTo(hop, tunnels, mine, mixedPort),
    ["匹配规则", c.rule || "—"],
    ["策略", c.chain || "DIRECT"],
    ["目标", `${c.host}:${c.port}`],
  ];
  if (policy && policy !== "DIRECT") hops.push(["出口", <ExitCell policy={policy} />]);
  return <PathLine hops={hops} />;
}

// useTunnelSites is what TunnelPath reads besides the connection: the tunnels' forwards, the
// GUI's own tunnels' servers and Connaio's proxy port.
export function useTunnelSites(running: boolean) {
  const [tunnels, setTunnels] = useState<Tunnel[]>([]);
  const [mine, setMine] = useState<Record<string, any>[]>([]);
  const mixedPort = useMixedPort();
  useEffect(() => {
    if (!running) return;
    CoreService.ReverseTunnels().then((t) => setTunnels(t ?? []));
    ProfileService.UserItems().then((u) => setMine(nonNull(u.tunnels)));
  }, [running]);
  return { tunnels, mine, mixedPort };
}

// useMixedPort is Connaio's proxy port, 0 until read.
export function useMixedPort() {
  const [port, setPort] = useState(0);
  useEffect(() => { ProfileService.Value("mixed-port").then((p) => setPort(Number(p) || 0)); }, []);
  return port;
}

// LiveRoutes is what comes in through the tunnels now, a card per tunnel and target: its fixed
// stretch drawn once, then the flows on it, by user, host and policy, with how many and how much.
function LiveRoutes({ conns, tunnels, mine, mixedPort, rates }: { conns: Conn[]; tunnels: Tunnel[]; mine: Record<string, any>[]; mixedPort: number; rates: Map<string, [number, number]> }) {
  // a card per tunnel and forward, there with no traffic too; the connections fill in its flows
  const byTunnel = new Map<string, { hop: NonNullable<Conn["tunnel"]>; cs: Conn[] }>();
  for (const t of tunnels) {
    for (const f of t.forwards ?? []) byTunnel.set(`${t.name}\n${f.local}`, { hop: { name: t.name, type: "WireGuard", from: "", target: f.local }, cs: [] });
  }
  for (const c of conns) {
    if (!c.tunnel) continue;
    const key = `${c.tunnel.name}\n${c.tunnel.target}`;
    const entry = byTunnel.get(key) ?? { hop: c.tunnel, cs: [] };
    entry.cs.push(c);
    byTunnel.set(key, entry);
  }
  if (byTunnel.size === 0) return <div className="live-empty muted small">暂无隧道</div>;
  const total = (cs: Conn[]) => cs.reduce((n, x) => n + x.upload + x.download, 0);
  return (
    <div className="live-routes">
      {[...byTunnel.entries()].map(([key, { hop, cs }]) => {
        const [up, down] = rates.get(key) ?? [0, 0];
        const flows = new Map<string, Conn[]>();
        for (const c of cs) {
          const key = [hostOf(c.tunnel!.from), c.host, c.port, c.rule, c.chain].join("\n");
          flows.set(key, [...(flows.get(key) ?? []), c]);
        }
        const proxied = isProxyPort(hop.target, mixedPort);
        return (
          <div key={`${hop.name}-${hop.target}`} className="card live-card">
            <div className="live-head">
              <span className="client-icon tunnel"><Icon name="tunnels" className="" /></span>
              <b>{hop.name}</b>
              <span className="chip">{hop.type}</span>
              <span className="grow" />
              <span className="live-stat"><span className="muted">下载</span>{speed(down).join(" ")}</span>
              <span className="live-stat"><span className="muted">上传</span>{speed(up).join(" ")}</span>
              <span className="live-stat"><span className="muted">连接数</span>{cs.length}</span>
              <span className="live-stat"><span className="muted">流量</span>{sizeText(total(cs))}</span>
            </div>
            <PathLine hops={[...hopsTo(hop, tunnels, mine, mixedPort), ["分流", proxied ? "规则匹配" : "本机服务"]]} />
            {cs.length === 0 ? <div className="live-idle muted small">当前没有连接</div> : <div className="table-wrap live-table">
              <table className="table">
                <colgroup><col style={{ width: "14%" }} /><col /><col style={{ width: "19%" }} /><col style={{ width: "16%" }} /><col style={{ width: "20%" }} /><col style={{ width: 64 }} /><col style={{ width: 88 }} /></colgroup>
                <thead><tr><th>客户端</th><th>目标</th><th>匹配规则</th><th>策略</th><th>出口</th><th className="num">连接数</th><th className="num">流量</th></tr></thead>
                <tbody>
                  {[...flows.values()].map((fs) => {
                    const c = fs[0], policy = c.chain.split(" → ").pop() ?? "";
                    return (
                      <tr key={c.id}>
                        <td className="selectable" title={c.tunnel!.from}>{clientOf(c.tunnel!.from)}</td>
                        <td className="selectable" title={`${c.host}:${c.port}`}>{c.host}:{c.port}</td>
                        <td title={c.rule}>{c.rule || "—"}</td>
                        <td title={c.chain}>{c.chain || "DIRECT"}</td>
                        <td>{policy && policy !== "DIRECT" ? <ExitCell policy={policy} /> : <span className="muted">直连</span>}</td>
                        <td className="num">{fs.length}</td>
                        <td className="num">{sizeText(total(fs))}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>}
          </div>
        );
      })}
    </div>
  );
}

export default function Tunnels({ status }: { status: Status }) {
  const [tunnels, setTunnels] = useState<Tunnel[]>([]);
  const [mine, setMine] = useState<Record<string, any>[]>([]);
  const [dialog, setDialog] = useState<Record<string, any> | true | null>(null); // true: a new tunnel; else the one edited
  const [open, setOpen] = useState("");
  const [switching, setSwitching] = useState(""); // the tunnel being started or stopped
  const [armed, confirm] = useConfirm();
  const [conns, setConns] = useState<Conn[]>([]);
  const mixedPort = useMixedPort();
  // relays: the connaio-relay versions this app carries; a tunnel's relay reporting another is outdated
  const [relays, setRelays] = useState<string[]>([]);
  useEffect(() => { ToolService.RelayVersions().then((v) => setRelays(v ?? [])); }, []);
  const outdated = (t: Tunnel) => !!t.relay && relays.length > 0 && !relays.includes(t.relay);

  const loadMine = () => ProfileService.UserItems().then((u) => setMine(nonNull(u.tunnels)));
  const poll = () => CoreService.ReverseTunnels().then((t) => setTunnels(t ?? []));
  const load = () => Promise.all([poll(), loadMine()]);
  useEffect(() => { if (status.running) loadMine(); }, [status.running]);
  usePoll(poll, 2000, status.running);
  usePoll(() => CoreService.Connections().then((c) => setConns(c ?? [])), 2000, status.running);
  // rates: each card's speed (by tunnel and target), [up, down] bytes a second, from what its
  // connections moved since the last poll, as the floating monitor's; download is the visitor's
  const last = useRef({ at: 0, bytes: new Map<string, [number, number]>() });
  const [rates, setRates] = useState(new Map<string, [number, number]>());
  useEffect(() => {
    const now = Date.now(), secs = (now - last.current.at) / 1000;
    const bytes = new Map<string, [number, number]>(), next = new Map<string, [number, number]>();
    for (const c of conns) {
      if (!c.tunnel) continue;
      const was = last.current.bytes.get(c.id);
      bytes.set(c.id, [c.upload, c.download]);
      if (!was) continue;
      const key = `${c.tunnel.name}\n${c.tunnel.target}`, [u, d] = next.get(key) ?? [0, 0];
      next.set(key, [u + Math.max(0, c.upload - was[0]) / secs, d + Math.max(0, c.download - was[1]) / secs]);
    }
    last.current = { at: now, bytes };
    setRates(next);
  }, [conns]);

  const remove = (name: string) => ProfileService.RemoveTunnel(name).then(() => ProfileService.RemoveRule(localRule(name))).then(load);

  return (
    <>
      <div className="hint">经 WireGuard 将本机服务暴露至公网。</div>

      <div className="tile-head"><span className="tile-title groups">隧道</span></div>
      <div className="tiles">
        {tunnels.map((t) => {
          const forwards = t.forwards ?? [];
          const own = mine.find((m) => m.name === t.name);
          return (
            <div key={t.name} className="tile-wrap">
              <button className={`tile ${open === t.name ? "selected" : ""}`} onClick={() => setOpen(open === t.name ? "" : t.name)}>
                <span className="kind">WireGuard · {forwards.length} 条转发</span>
                <span className="name">{t.name}</span>
                <span className={`foot ${stateClass[t.state] ?? ""}`}>{switching === t.name ? "切换中…" : stateLabel[t.state] ?? t.state}{t.error && " · 有错误"}{outdated(t) && <span className="delay-warn"> · 服务器程序需更新</span>}</span>
              </button>
              {open === t.name && (
                <div className="menu members detail" onClick={(e) => e.stopPropagation()}>
                  <div className="detail-head">
                    {stateLabel[t.state] ?? t.state} · 自 {new Date(t.since).toLocaleTimeString()}
                  </div>
                  {t.error && <div className="error small detail-head">{t.error}</div>}
                  {outdated(t) && <div className="delay-warn small detail-head">服务器程序较旧，请在编辑中重新安装。</div>}
                  {forwards.map((f, i) => (
                    <div key={i} className="detail-row">
                      <span className="muted">{f.remote} → {f.local}</span>
                    </div>
                  ))}
                  {own && <>
                    {/* a stopped tunnel stays in the config, disabled, until started again */}
                    <button onClick={() => { setOpen(""); setSwitching(t.name); ProfileService.SetTunnelDisabled(t.name, t.state !== "stopped").then(poll).finally(() => setSwitching("")); }}>{t.state === "stopped" ? "开始连接" : "停止连接"}</button>
                    <button onClick={() => { setDialog(own); setOpen(""); }}>编辑此隧道</button>
                    <button className={`danger ${armed === t.name ? "armed" : ""}`} onClick={() => confirm(t.name, () => { remove(t.name); setOpen(""); })}>{armed === t.name ? "确认删除" : "删除此隧道"}</button>
                  </>}
                </div>
              )}
            </div>
          );
        })}
        <AddTile label="添加隧道" empty={tunnels.length === 0} disabled={!status.running} onClick={() => setDialog(true)} />
      </div>

      <div className="tile-head"><span className="tile-title groups">实时链路</span></div>
      <LiveRoutes conns={conns} tunnels={tunnels} mine={mine} mixedPort={mixedPort} rates={rates} />

      {dialog && <TunnelDialog editing={dialog === true ? undefined : dialog} onClose={() => { setDialog(null); load(); }} />}
    </>
  );
}
