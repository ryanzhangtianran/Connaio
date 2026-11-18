import { useEffect, useState } from "react";
import { CoreService, ProfileService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Conn, Tunnel } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { ExitCell } from "./ExitIP";
import { nonNull } from "./format";

// localRule takes what comes in through a tunnel straight to its local target (here Connaio's own
// proxy port, whose requests then follow the rules) instead of matching the rules itself.
export const localRule = (tunnel: string) => `IN-NAME,${tunnel},DIRECT`;

// serverOf is the host a tunnel of the GUI's own connects to, "" for one from the profile.
const serverOf = (own?: Record<string, any>) => own?.server ?? "";
export const hostOf = (addr: string) => addr.slice(0, addr.lastIndexOf(":")).replace(/^\[|\]$/g, "");
const portOf = (addr?: string) => addr?.slice(addr.lastIndexOf(":") + 1) ?? "";

// PathLine draws a way as a band across its box: a stop per hop (a dot, the label over the value),
// the links between them sharing what width is left; a value too long for its room shows whole on hover.
export function PathLine({ hops }: { hops: [string, React.ReactNode, string?][] }) {
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
export const clientOf = (from: string) => {
  const host = hostOf(from);
  return host === "127.0.0.1" || host === "::1" ? "服务器本机" : host;
};

// hopsTo are the fixed stretch of a tunnel's way: the server and port the user reached, the tunnel,
// and its target here (Connaio's proxy port, whose requests then follow the rules, or a local service).
export function hopsTo(hop: NonNullable<Conn["tunnel"]>, tunnels: Tunnel[], mine: Record<string, any>[], mixedPort: number): [string, string][] {
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
export const isProxyPort = (target: string, mixedPort: number) => !!mixedPort && target === `127.0.0.1:${mixedPort}`;

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
