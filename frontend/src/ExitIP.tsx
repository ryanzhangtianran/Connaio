import { useEffect, useState } from "react";
import { ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { IPInfo } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";

export const place = (i: IPInfo) => [i.city || i.region || i.country, i.code].filter(Boolean).join(", ");
// flag is a country's flag emoji from its ISO code.
export const flag = (code: string) => (code.length === 2 ? String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0))) : "");
export const fullPlace = (i: IPInfo) => [...new Set([i.city, i.region, i.country].filter(Boolean))].join(", ");

// exitIPs caches each policy's exit lookup for the session, a failed one dropped so it is asked again.
// ponytail: no expiry; a node whose address changes shows the old one until the app restarts.
const exitIPs: Record<string, Promise<IPInfo>> = {};
export const exitIP = (policy: string) => (exitIPs[policy] ??= ToolService.ExitIP(policy).catch((e) => { delete exitIPs[policy]; throw e; }));

export const invalidateExitIP = (policy: string) => { delete exitIPs[policy]; };

// ExitCell is the exit address of one route, looked up once per session.
export function ExitCell({ policy }: { policy: string }) {
  const [text, setText] = useState("查询出口…");
  const [full, setFull] = useState("");
  useEffect(() => {
    if (policy === "REJECT") { setText("拒绝"); return; }
    exitIP(policy).then((i) => { setText(`${flag(i.code)} ${i.ip} · ${place(i)}`); setFull(`${i.ip} · ${fullPlace(i)} · ${i.isp}`); }).catch(() => setText("出口查询失败"));
  }, [policy]);
  return <span className="selectable" title={full}>{text}</span>;
}
