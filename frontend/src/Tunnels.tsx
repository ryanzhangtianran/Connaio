import { useEffect, useState } from "react";
import { CoreService, ProfileService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Status, Tunnel } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import TunnelDialog from "./TunnelDialog";
import { nonNull } from "./format";
import { usePoll } from "./hooks";

const stateLabel: Record<string, string> = { running: "运行中", connecting: "连接中", retrying: "重试中", stopped: "已停止" };
const stateClass: Record<string, string> = { running: "delay-good", connecting: "delay-warn", retrying: "delay-bad", stopped: "muted" };
const kindLabel: Record<string, string> = { frp: "FRP", ssh: "SSH" };

// localRule takes what comes in through a tunnel straight to its local target (here Connaio's own
// proxy port, whose requests then follow the rules) instead of matching the rules itself.
export const localRule = (tunnel: string) => `IN-NAME,${tunnel},DIRECT`;

export default function Tunnels({ status }: { status: Status }) {
  const [tunnels, setTunnels] = useState<Tunnel[]>([]);
  const [mine, setMine] = useState<Record<string, any>[]>([]);
  const [dialog, setDialog] = useState(false);
  const [editing, setEditing] = useState<Record<string, any> | null>(null);
  const [open, setOpen] = useState("");

  const loadMine = () => ProfileService.UserItems().then((u) => setMine(nonNull(u.tunnels)));
  const poll = () => CoreService.ReverseTunnels().then((t) => setTunnels(t ?? []));
  const load = () => Promise.all([poll(), loadMine()]);
  useEffect(() => { if (status.running) loadMine(); }, [status.running]);
  usePoll(poll, 2000, status.running);

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
                    <button onClick={() => { setEditing(own); setOpen(""); }}>编辑此隧道</button>
                    <button className="danger" onClick={() => { remove(t.name); setOpen(""); }}>删除此隧道</button>
                  </>}
                </div>
              )}
            </div>
          );
        })}
        <button className="tile add" disabled={!status.running} onClick={() => setDialog(true)} title="增加隧道">＋</button>
      </div>

      {dialog && <TunnelDialog onClose={() => { setDialog(false); load(); }} />}
      {editing && <TunnelDialog editing={editing} onClose={() => { setEditing(null); load(); }} />}
    </>
  );
}
