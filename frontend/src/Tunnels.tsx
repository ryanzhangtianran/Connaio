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

// TunnelPath is a connection's way from the user at the server's end to where it goes out: the
// server and port the user reached, the tunnel, its target here (Connaio's proxy port, whose request
// then follows the rules, or a local service), the rule, the policy down to the node, and the host.
export function TunnelPath({ c, tunnels, mine }: { c: Conn; tunnels: Tunnel[]; mine: Record<string, any>[] }) {
  const hop = c.tunnel!;
  const forward = tunnels.find((t) => t.name === hop.name)?.forwards?.find((f) => f.local === hop.target);
  const server = serverOf(mine.find((m) => m.name === hop.name));
  const carried = c.inbound !== "SSH" && c.inbound !== "FRP"; // a request the tunnel brought to the proxy port
  const policy = c.chain.split(" → ").pop() ?? "";
  const hops: [string, React.ReactNode][] = [
    ["来源", hop.from],
    ["服务器", `${server || "远端"}${forward?.remote ? ":" + portOf(forward.remote) : ""}`],
    [`${hop.type} 隧道`, hop.name],
    [carried ? "代理端口" : "本机服务", hop.target],
    ["规则", c.rule || "—"],
    ["策略", c.chain || "DIRECT"],
    ["目标", `${c.host}:${c.port}`],
  ];
  if (carried && policy !== "DIRECT") hops.push(["出口", <ExitCell policy={policy} />]);
  return (
    <div className="path">
      {/* each arrow with the hop after it, so a wrapped line starts with one rather than ending in one */}
      {hops.map(([label, value], i) => (
        <span key={label} className="hop-step">
          {i > 0 && <Icon name="more" className="path-arrow" />}
          <div className="hop"><span>{label}</span><b className="selectable">{value}</b></div>
        </span>
      ))}
    </div>
  );
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

// LiveRoutes is what comes in through the tunnels now: each tunnel's connections by the user and the
// route they take, with how many and how much.
function LiveRoutes({ conns, tunnels, mine }: { conns: Conn[]; tunnels: Tunnel[]; mine: Record<string, any>[] }) {
  const routes = new Map<string, Conn[]>();
  for (const c of conns) {
    if (!c.tunnel) continue;
    const key = [c.tunnel.name, hostOf(c.tunnel.from), c.tunnel.target, c.rule, c.chain].join("\n");
    routes.set(key, [...(routes.get(key) ?? []), c]);
  }
  if (routes.size === 0) return <div className="muted small">当前没有经隧道进入的连接</div>;
  return (
    <div className="live-routes">
      {[...routes.values()].map((cs) => {
        const c = cs[0];
        const hosts = [...new Set(cs.map((x) => `${x.host}:${x.port}`))];
        const bytes = cs.reduce((n, x) => n + x.upload + x.download, 0);
        return (
          <div key={c.id} className="live-route">
            <div className="watch-head">
              <span className="watch-chain">{c.tunnel!.name} · {hostOf(c.tunnel!.from)}</span>
              <span className="muted small">{cs.length} 个连接 · {sizeText(bytes)}</span>
            </div>
            {/* the path with the first host; the rest listed under it */}
            <TunnelPath c={c} tunnels={tunnels} mine={mine} />
            {hosts.length > 1 && <div className="muted small live-hosts" title={hosts.join("\n")}>另有 {hosts.slice(1).join("、")}</div>}
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
