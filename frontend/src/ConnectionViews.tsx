import { useEffect, useState } from "react";
import { CoreService, SettingsService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Conn } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { ExitCell } from "./ExitIP";
import { platform } from "./format";
import { useCachedURL, usePoll } from "./hooks";
import { Icon } from "./icons";

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
          <div className="watch-head"><span className="watch-chain">{chain === "DIRECT" ? "直连" : chain}</span><span className="muted small">{cs.length} 个连接</span></div>
          <div className="watch-exit"><ExitCell policy={chain.split(" → ").pop()!} /></div>
        </div>
      ))}
    </>
  );
}

// ClientIcon is a client's Finder icon (an app's, or an executable's generic one), a reverse
// tunnel's tile (clientOf's "tunnel": the tunnels page's servers on blue, as the cards' switches), or
// a device glyph.
export function ClientIcon({ path }: { path?: string }) {
  const tunnel = path === "tunnel";
  const src = useCachedURL(tunnel ? undefined : path || (platform === "linux" ? "device" : undefined), SettingsService.AppIcon); // Linux has a theme icon for devices
  if (tunnel) return <span className="client-icon tunnel"><Icon name="tunnels" className="" /></span>;
  if (src) return <img className="client-icon" src={src} alt="" />;
  return (
    <span className="client-icon device">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
        <path d={path ? "M5 4h14a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1zM8 9l3 3-3 3M13 15h3" : "M4 6h12a1 1 0 0 1 1 1v8H3V7a1 1 0 0 1 1-1zM1.5 18h17M19 9h3v10h-3z"} />
      </svg>
    </span>
  );
}
