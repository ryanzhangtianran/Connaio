import { useEffect, useState } from "react";
import { CoreService, ProfileService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Conn, Status, Tunnel } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import TunnelDialog from "./TunnelDialog";
import { ExitCell } from "./Activity";
import { Icon } from "./icons";
import { nonNull, sizeText } from "./format";
import { usePoll } from "./hooks";

const stateLabel: Record<string, string> = { running: "运行中", connecting: "连接中", retrying: "重试中", stopped: "已停止" };
const stateClass: Record<string, string> = { running: "delay-good", connecting: "delay-warn", retrying: "delay-bad", stopped: "muted" };
const kindLabel: Record<string, string> = { frp: "FRP", ssh: "SSH" };

// localRule takes what comes in through a tunnel straight to its local target (here Connaio's own
// proxy port, whose requests then follow the rules) instead of matching the rules itself.
export const localRule = (tunnel: string) => `IN-NAME,${tunnel},DIRECT`;

// serverOf is the host a tunnel of the GUI's own connects to, "" for one from the profile.
const serverOf = (own?: Record<string, any>) => (own?.type === "frp" ? own.config?.serverAddr : own?.server) ?? "";
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
function hopsTo(hop: NonNullable<Conn["tunnel"]>, tunnels: Tunnel[], mine: Record<string, any>[]): [string, string][] {
  const forward = tunnels.find((t) => t.name === hop.name)?.forwards?.find((f) => f.local === hop.target);
  const server = serverOf(mine.find((m) => m.name === hop.name));
  return [
    ["服务器", `${server || "远端"}${forward?.remote ? ":" + portOf(forward.remote) : ""}`],
    [`${hop.type} 隧道`, hop.name],
    [isProxyPort(hop.target) ? "代理端口" : "本机服务", hop.target],
  ];
}
// isProxyPort: the target is a port on this machine, as the dialog's 经规则转发 forward to the proxy
// port is, rather than a service elsewhere on the LAN.
// ponytail: any loopback port counts; compare with mixed-port if local services show up here
const isProxyPort = (target: string) => /^127\.0\.0\.1:\d+$/.test(target);

// TunnelPath is one connection's whole way, for its detail on the connections page: the user, the
// fixed stretch, the rule, the policy, the host and where it comes out.
export function TunnelPath({ c, tunnels, mine }: { c: Conn; tunnels: Tunnel[]; mine: Record<string, any>[] }) {
  const hop = c.tunnel!;
  const policy = c.chain.split(" → ").pop() ?? "";
  const hops: [string, React.ReactNode, string?][] = [
    ["客户端", clientOf(hop.from), hop.from],
    ...hopsTo(hop, tunnels, mine),
    ["匹配规则", c.rule || "—"],
    ["策略", c.chain || "DIRECT"],
    ["目标", `${c.host}:${c.port}`],
  ];
  if (policy && policy !== "DIRECT") hops.push(["出口", <ExitCell policy={policy} />]);
  return <PathLine hops={hops} />;
}

// useTunnelSites is what TunnelPath reads besides the connection: the tunnels' forwards and the
// GUI's own tunnels' servers.
export function useTunnelSites(running: boolean) {
  const [tunnels, setTunnels] = useState<Tunnel[]>([]);
  const [mine, setMine] = useState<Record<string, any>[]>([]);
  useEffect(() => {
    if (!running) return;
    CoreService.ReverseTunnels().then((t) => setTunnels(t ?? []));
    ProfileService.UserItems().then((u) => setMine(nonNull(u.tunnels)));
  }, [running]);
  return { tunnels, mine };
}

// LiveRoutes is what comes in through the tunnels now, a card per tunnel and target: its fixed
// stretch drawn once, then the flows on it, by user, host and policy, with how many and how much.
function LiveRoutes({ conns, tunnels, mine }: { conns: Conn[]; tunnels: Tunnel[]; mine: Record<string, any>[] }) {
  const byTunnel = new Map<string, Conn[]>();
  for (const c of conns) {
    if (!c.tunnel) continue;
    const key = `${c.tunnel.name}\n${c.tunnel.target}`;
    byTunnel.set(key, [...(byTunnel.get(key) ?? []), c]);
  }
  if (byTunnel.size === 0) return <div className="live-empty muted small">当前没有经隧道进入的连接</div>;
  const total = (cs: Conn[]) => cs.reduce((n, x) => n + x.upload + x.download, 0);
  return (
    <div className="live-routes">
      {[...byTunnel.values()].map((cs) => {
        const hop = cs[0].tunnel!;
        const flows = new Map<string, Conn[]>();
        for (const c of cs) {
          const key = [hostOf(c.tunnel!.from), c.host, c.port, c.rule, c.chain].join("\n");
          flows.set(key, [...(flows.get(key) ?? []), c]);
        }
        const proxied = isProxyPort(hop.target);
        return (
          <div key={`${hop.name}-${hop.target}`} className="card live-card">
            <div className="live-head">
              <span className="client-icon tunnel"><Icon name="tunnels" className="" /></span>
              <b>{hop.name}</b>
              <span className="chip">{hop.type}</span>
              <span className="grow" />
              <span className="live-stat"><span className="muted">连接数</span>{cs.length}</span>
              <span className="live-stat"><span className="muted">流量</span>{sizeText(total(cs))}</span>
            </div>
            <PathLine hops={[...hopsTo(hop, tunnels, mine), ["分流", proxied ? "规则匹配" : "本机服务"]]} />
            <div className="table-wrap live-table">
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
            </div>
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
  const [conns, setConns] = useState<Conn[]>([]);

  const loadMine = () => ProfileService.UserItems().then((u) => setMine(nonNull(u.tunnels)));
  const poll = () => CoreService.ReverseTunnels().then((t) => setTunnels(t ?? []));
  const load = () => Promise.all([poll(), loadMine()]);
  useEffect(() => { if (status.running) loadMine(); }, [status.running]);
  usePoll(poll, 2000, status.running);
  usePoll(() => CoreService.Connections().then((c) => setConns(c ?? [])), 2000, status.running);

  const remove = (name: string) => ProfileService.RemoveTunnel(name).then(() => ProfileService.RemoveRule(localRule(name))).then(load);

  return (
    <>
      <div className="hint">通过 FRP 或 SSH 把本机服务暴露到公网。</div>

      <div className="tile-head"><span className="tile-title groups">隧道</span></div>
      <div className="tiles">
        {tunnels.map((t) => {
          const forwards = t.forwards ?? [];
          const own = mine.find((m) => m.name === t.name);
          return (
            <div key={t.name} className="tile-wrap">
              <button className={`tile ${open === t.name ? "selected" : ""}`} onClick={() => setOpen(open === t.name ? "" : t.name)}>
                <span className="kind">{kindLabel[t.type] ?? t.type} · {forwards.length} 条转发</span>
                <span className="name">{t.name}</span>
                <span className={`foot ${stateClass[t.state] ?? ""}`}>{stateLabel[t.state] ?? t.state}{t.error && " · 有错误"}</span>
              </button>
              {open === t.name && (
                <div className="menu members detail" onClick={(e) => e.stopPropagation()}>
                  <div className="detail-head">
                    {stateLabel[t.state] ?? t.state} · 自 {new Date(t.since).toLocaleTimeString()}
                  </div>
                  {t.error && <div className="error small detail-head">{t.error}</div>}
                  {forwards.map((f, i) => (
                    <div key={i} className="detail-row">
                      <span>{f.name ? `${f.name} · ` : ""}{f.type || "tcp"}</span>
                      <span className="muted">{f.remote || "—"} → {f.local}</span>
                      {f.state && <span className={f.state === "running" ? "delay-good" : "delay-warn"}>{f.state}</span>}
                      {f.error && <span className="error small">{f.error}</span>}
                    </div>
                  ))}
                  {own && <>
                    {/* a stopped tunnel stays in the config, disabled, until started again */}
                    <button onClick={() => { ProfileService.SetTunnelDisabled(t.name, t.state !== "stopped"); setOpen(""); }}>{t.state === "stopped" ? "开始连接" : "停止连接"}</button>
                    <button onClick={() => { setDialog(own); setOpen(""); }}>编辑此隧道</button>
                    <button className="danger" onClick={() => { remove(t.name); setOpen(""); }}>删除此隧道</button>
                  </>}
                </div>
              )}
            </div>
          );
        })}
        <button className="tile add" disabled={!status.running} onClick={() => setDialog(true)} title="增加隧道">＋</button>
      </div>

      <div className="tile-head"><span className="tile-title groups">实时链路</span></div>
      <LiveRoutes conns={conns} tunnels={tunnels} mine={mine} />

      {dialog && <TunnelDialog editing={dialog === true ? undefined : dialog} onClose={() => { setDialog(null); load(); }} />}
    </>
  );
}
