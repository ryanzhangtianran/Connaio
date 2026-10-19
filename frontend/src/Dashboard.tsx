import { useMemo, useState } from "react";
import { CoreService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Conn, Status, TempRule } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { RuleDialog, age } from "./RuleDialog";
import { sizeText } from "./format";
import { usePoll, useClickAway } from "./hooks";

// method approximates Surge's column from what the core knows: transport and port.
function method(c: Conn) {
  if (c.network === "udp") return c.port === "443" ? "QUIC" : "UDP";
  return c.port === "443" ? "HTTPS" : c.port === "80" ? "HTTP" : "TCP";
}
// client names a connection's client as the activity page does: the app it counts towards, 隧道 <name>
// for one a reverse tunnel brought in
const client = (c: Conn) => c.app || (c.inbound === "Inner" ? "Connaio" : c.process || c.source || "未知");

function Detail({ c, onRule, onClose }: { c: Conn; onRule: () => void; onClose: () => void }) {
  const rows: [string, string][] = [
    ["客户端", client(c)], ["进程路径", c.path || "—"], ["入站", c.inbound], ["来源", c.source],
    ["目标", `${c.host}:${c.port}`], ["规则", c.rule], ["策略链", c.chain],
    ["上传 / 下载", `${sizeText(c.upload)} / ${sizeText(c.download)}`], ["开始", new Date(c.start).toLocaleString()],
  ];
  return (
    <div className="dash-detail">
      <div className="head">
        <b>{client(c)} → {c.host}:{c.port}</b>
        <span className="actions">
          <button className="btn" onClick={onRule}>为此主机添加规则</button>
          <button className="btn" onClick={() => CoreService.CloseConnection(c.id).then(onClose)}>断开</button>
          <button className="btn" onClick={onClose}>×</button>
        </span>
      </div>
      <div className="detail-grid">
        {rows.map(([k, v]) => [<span key={k} className="muted">{k}</span>, <span key={k + "v"} className="selectable">{v}</span>])}
      </div>
    </div>
  );
}

// Dashboard is the connections page: live connections grouped by client or host, with details.
export default function Dashboard({ status }: { status: Status }) {
  const [conns, setConns] = useState<Conn[]>([]);
  const [temp, setTemp] = useState<TempRule[]>([]);
  const [groupBy, setGroupBy] = useState<"client" | "host">("client");
  const [selected, setSelected] = useState(""); // client or host filter
  const [query, setQuery] = useState("");
  const [detail, setDetail] = useState<string>(""); // connection id
  const [dialog, setDialog] = useState<Conn | null>(null);
  const [tempOpen, setTempOpen] = useState(false);

  usePoll(() => Promise.all([
    CoreService.Connections().then((c) => setConns(c ?? [])),
    CoreService.TempRules().then((r) => setTemp(r ?? [])),
  ]), 2000, status.running);
  useClickAway(tempOpen, () => setTempOpen(false));

  const keyOf = (c: Conn) => (groupBy === "client" ? client(c) : c.host);
  const groups = useMemo(() => {
    const m = new Map<string, number>();
    for (const c of conns) m.set(keyOf(c), (m.get(keyOf(c)) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0], undefined, { sensitivity: "base" }));
  }, [conns, groupBy]);
  const q = query.toLowerCase();
  const shown = useMemo(() => conns.filter((c) => (!selected || keyOf(c) === selected) && (!q || `${client(c)} ${c.host} ${c.rule} ${c.chain}`.toLowerCase().includes(q))), [conns, groupBy, selected, q]);
  const MAX_ROWS = 300; // ponytail: a cap instead of virtualisation; virtualise if people scroll past it
  const current = conns.find((c) => c.id === detail);

  return (
    <div className="dash">

      {/* one split-view card: grouping and list on the left, search, actions and table on the right */}
      <div className="dash-body">
        <aside className="dash-side">
          <div className="seg">
            <button className={groupBy === "client" ? "on" : ""} onClick={() => { setGroupBy("client"); setSelected(""); }}>按客户端</button>
            <button className={groupBy === "host" ? "on" : ""} onClick={() => { setGroupBy("host"); setSelected(""); }}>按主机名</button>
          </div>
          <button className={`dash-item ${selected === "" ? "active" : ""}`} onClick={() => setSelected("")}>
            <span>{groupBy === "client" ? "所有客户端" : "所有主机"}</span><span className="muted">{conns.length}</span>
          </button>
          {groups.map(([name, n]) => (
            <button key={name} className={`dash-item ${selected === name ? "active" : ""}`} onClick={() => setSelected(name)} title={name}>
              <span className="name">{name}</span><span className="muted">{n}</span>
            </button>
          ))}
        </aside>
        <div className="dash-main">
          <div className="dash-top">
            <input className="dash-search" placeholder="搜索" title="按客户端、主机、规则或策略搜索" value={query} onChange={(e) => setQuery(e.target.value)} />
            <div className="dash-actions">
              <button className="btn" disabled={!status.running} onClick={() => CoreService.CloseAll()}>断开全部</button>
              <button className="btn" disabled={!status.running} onClick={() => CoreService.Reload()}>重新载入</button>
              <span className="tile-wrap">
                <button className="btn" onClick={(e) => { e.stopPropagation(); setTempOpen(!tempOpen); }}>临时规则（{temp.length}）</button>
                {tempOpen && (
                  <div className="menu members detail drop" onClick={(e) => e.stopPropagation()}>
                    {temp.length === 0 && <div className="detail-head">没有临时规则，重载配置后会清空</div>}
                    {temp.map((r) => (
                      <div key={r.id} className="detail-row">
                        <span>{r.type},{r.payload} → <b>{r.proxy}</b></span>
                        <span className="muted">命中 {r.hits}</span>
                        <button className="btn small-btn" onClick={() => CoreService.RemoveTempRule(r.id)}>删除</button>
                      </div>
                    ))}
                  </div>
                )}
              </span>
            </div>
          </div>
          <div className="dash-table">
            <table className="table">
              <colgroup><col /><col /><col style={{ width: 88 }} /><col style={{ width: 74 }} /><col style={{ width: 74 }} /><col style={{ width: 52 }} /></colgroup>
              <thead><tr><th>客户端</th><th className="addr">地址</th><th>策略</th><th className="num">上传</th><th className="num">下载</th><th className="num">时长</th></tr></thead>
              <tbody>
                {shown.slice(0, MAX_ROWS).map((c) => (
                  <tr key={c.id} className={c.id === detail ? "row-selected" : ""} onClick={() => setDetail(c.id === detail ? "" : c.id)}>
                    <td title={c.path}>{client(c)}</td>
                    <td className="addr" title={`${c.host}:${c.port}`}><span className={`method m-${method(c).toLowerCase()}`}>{method(c)}</span>{c.host}:{c.port}</td>
                    <td title={`${c.rule} → ${c.chain}`}>{c.chain}</td>
                    <td className="num">{sizeText(c.upload)}</td>
                    <td className="num">{sizeText(c.download)}</td>
                    <td className="num">{age(c.start)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {shown.length === 0 && <div className="dash-empty muted">没有连接</div>}
            {shown.length > MAX_ROWS && <div className="dash-empty muted">还有 {shown.length - MAX_ROWS} 条，搜索或按客户端筛选查看</div>}
          </div>
        </div>
      </div>
      {current && <Detail c={current} onRule={() => setDialog(current)} onClose={() => setDetail("")} />}
      {dialog && <RuleDialog conn={dialog} onClose={() => setDialog(null)} />}
    </div>
  );
}
