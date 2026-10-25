import { useMemo, useRef, useState } from "react";
import { CoreService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Conn, Status, TempRule } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { RuleDialog, age } from "./RuleDialog";
import { ClientIcon } from "./Activity";
import { TunnelPath, useTunnelSites } from "./Tunnels";
import { sizeText, speed } from "./format";
import { usePoll, useClickAway, useConfirm } from "./hooks";
import { Seg } from "./Settings";

// method approximates Surge's column from what the core knows: transport and port.
function method(c: Conn) {
  if (c.network === "udp") return c.port === "443" ? "QUIC" : "UDP";
  return c.port === "443" ? "HTTPS" : c.port === "80" ? "HTTP" : "TCP";
}
// client names a connection's client as the activity page does: the app it counts towards, a tunnel's name
// for one a reverse tunnel brought in
const client = (c: Conn) => c.app || (c.inbound === "Inner" ? "Connaio" : c.process || c.source || "未知");

function Detail({ c, sites, onRule, onClose }: { c: Conn; sites: ReturnType<typeof useTunnelSites>; onRule: () => void; onClose: () => void }) {
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
          <button className="btn" title="关闭" aria-label="关闭" onClick={onClose}>×</button>
        </span>
      </div>
      {c.tunnel && <TunnelPath c={c} {...sites} />}
      <div className="detail-grid">
        {rows.map(([k, v]) => [<span key={k} className="muted">{k}</span>, <span key={k + "v"} className="selectable">{v}</span>])}
      </div>
    </div>
  );
}

type Col = "client" | "addr" | "chain" | "rate" | "upload" | "download" | "start";
// sortKeys is what each column sorts by; start sorts by age, so ascending is newest first
const sortKeys: Record<Col, (c: Conn, rates: Map<string, number>) => string | number> = {
  client: (c) => client(c), addr: (c) => c.host, chain: (c) => c.chain, rate: (c, r) => r.get(c.id) ?? 0,
  upload: (c) => c.upload, download: (c) => c.download, start: (c) => -Date.parse(c.start),
};

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
  const [paused, setPaused] = useState(false);
  const [sort, setSort] = useState<{ col: Col; desc: boolean }>({ col: "start", desc: false }); // newest first, as the core lists them
  const [armed, confirm] = useConfirm();
  const sites = useTunnelSites(status.running);
  // rates: each connection's bytes a second, up and down together, from what it moved since the last poll
  const last = useRef({ at: 0, bytes: new Map<string, number>() });
  const [rates, setRates] = useState(new Map<string, number>());

  usePoll(() => Promise.all([
    CoreService.Connections().then((c) => {
      const list = c ?? [], now = Date.now(), secs = (now - last.current.at) / 1000;
      const bytes = new Map(list.map((x) => [x.id, x.upload + x.download]));
      setRates(new Map(list.map((x) => [x.id, last.current.bytes.has(x.id) ? Math.max(0, bytes.get(x.id)! - last.current.bytes.get(x.id)!) / secs : 0])));
      last.current = { at: now, bytes };
      setConns(list);
    }),
    CoreService.TempRules().then((r) => setTemp(r ?? [])),
  ]), 2000, status.running && !paused);
  useClickAway(tempOpen, () => setTempOpen(false));

  const keyOf = (c: Conn) => (groupBy === "client" ? client(c) : c.host);
  const groups = useMemo(() => {
    const m = new Map<string, number>();
    for (const c of conns) m.set(keyOf(c), (m.get(keyOf(c)) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0], undefined, { sensitivity: "base" }));
  }, [conns, groupBy]);
  const q = query.toLowerCase();
  const shown = useMemo(() => {
    const list = conns.filter((c) => (!selected || keyOf(c) === selected) && (!q || `${client(c)} ${c.host} ${c.rule} ${c.chain}`.toLowerCase().includes(q)));
    const key = sortKeys[sort.col];
    const by = (a: Conn, b: Conn) => { const x = key(a, rates), y = key(b, rates); return typeof x === "string" ? x.localeCompare(y as string, "zh") : x - (y as number); };
    return sort.col === "start" && !sort.desc ? list : list.sort((a, b) => (sort.desc ? -by(a, b) : by(a, b)));
  }, [conns, groupBy, selected, q, sort, rates]);
  const th = (col: Col, label: string, className = "") => (
    <th className={`sortable ${className}`} onClick={() => setSort({ col, desc: sort.col === col ? !sort.desc : col !== "client" && col !== "addr" && col !== "chain" })}>
      {label}{sort.col === col && (sort.col !== "start" || sort.desc) ? (sort.desc ? " ↓" : " ↑") : ""}
    </th>
  );
  const MAX_ROWS = 300; // ponytail: a cap instead of virtualisation; virtualise if people scroll past it
  const current = conns.find((c) => c.id === detail);
  const iconOf = useMemo(() => new Map(conns.map((c) => [client(c), c.icon])), [conns]);

  return (
    <div className="dash">

      {/* one split-view card: grouping and list on the left, search, actions and table on the right */}
      <div className="dash-body">
        <aside className="dash-side">
          <Seg options={[["client", "按客户端"], ["host", "按主机名"]]} value={groupBy} onChange={(v) => { setGroupBy(v); setSelected(""); }} />
          <button className={`dash-item ${selected === "" ? "active" : ""}`} onClick={() => setSelected("")}>
            <span>{groupBy === "client" ? "所有客户端" : "所有主机"}</span><span className="muted">{conns.length}</span>
          </button>
          {groups.map(([name, n]) => (
            <button key={name} className={`dash-item ${selected === name ? "active" : ""}`} onClick={() => setSelected(name)} title={name}>
              <span className="name">{groupBy === "client" && <ClientIcon path={iconOf.get(name)} />}{name}</span><span className="muted">{n}</span>
            </button>
          ))}
        </aside>
        <div className="dash-main">
          <div className="dash-top">
            <input data-search className="dash-search" placeholder="搜索" title="按客户端、主机、规则或策略搜索" value={query} onChange={(e) => setQuery(e.target.value)} />
            <div className="dash-actions">
              <button className="btn" disabled={!status.running} onClick={() => setPaused(!paused)}>{paused ? "继续" : "暂停"}</button>
              <button className={`btn ${armed ? "armed" : ""}`} disabled={!status.running || !conns.length} onClick={() => confirm("all", () => CoreService.CloseAll())}>{armed ? "确认断开" : "断开全部"}</button>
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
              {/* at least these shares, the address taking the rest; wider content widens its column (nothing is cut) */}
              <colgroup><col style={{ width: "17%" }} /><col /><col style={{ width: "15%" }} /><col style={{ width: "10%" }} /><col style={{ width: "9%" }} /><col style={{ width: "9%" }} /><col style={{ width: "8%" }} /><col style={{ width: 36 }} /></colgroup>
              <thead><tr>{th("client", "客户端")}{th("addr", "地址", "addr")}{th("chain", "策略")}{th("rate", "速度", "num")}{th("upload", "上传", "num")}{th("download", "下载", "num")}{th("start", "时长", "num")}<th /></tr></thead>
              <tbody>
                {shown.slice(0, MAX_ROWS).map((c) => (
                  <tr key={c.id} className={c.id === detail ? "row-selected" : ""} onClick={() => setDetail(c.id === detail ? "" : c.id)}>
                    <td title={c.path}><span className="client-cell"><ClientIcon path={c.icon} />{client(c)}</span></td>
                    <td className="addr" title={`${c.host}:${c.port}`}><span className={`method m-${method(c).toLowerCase()}`}>{method(c)}</span>{c.host}:{c.port}</td>
                    <td title={`${c.rule} → ${c.chain}`}>{c.chain}</td>
                    <td className="num">{rates.get(c.id) ? speed(Math.round(rates.get(c.id)!)).join(" ") : "—"}</td>
                    <td className="num">{sizeText(c.upload)}</td>
                    <td className="num">{sizeText(c.download)}</td>
                    <td className="num">{age(c.start)}</td>
                    <td className="row-close"><button className="btn" title="断开" aria-label="断开" onClick={(e) => { e.stopPropagation(); CoreService.CloseConnection(c.id).then(() => setConns((l) => l.filter((x) => x.id !== c.id))); }}>×</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
            {shown.length === 0 && <div className="dash-empty muted">{status.running ? "没有连接" : "内核未运行"}</div>}
            {shown.length > MAX_ROWS && <div className="dash-empty muted">还有 {shown.length - MAX_ROWS} 条，搜索或按客户端筛选查看</div>}
          </div>
        </div>
      </div>
      {current && <Detail c={current} sites={sites} onRule={() => setDialog(current)} onClose={() => setDetail("")} />}
      {dialog && <RuleDialog conn={dialog} onClose={() => setDialog(null)} />}
    </div>
  );
}
