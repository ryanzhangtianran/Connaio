import { useCallback, useEffect, useState } from "react";
import { CoreService, ProfileService, ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Group, Proxy, Status } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import ProxyDialog, { protocols } from "./ProxyDialog";
import ProxyDiagnose from "./ProxyDiagnose";
import GroupDialog from "./GroupDialog";
import { on, useClickAway, useConfirm } from "./hooks";
import { errText } from "./format";

const modes = [
  { key: "direct", label: "直接连接", hint: "所有请求都将被直接发往目标服务器。" },
  { key: "global", label: "全局代理", hint: "所有请求都将被转发给 GLOBAL 中选中的代理。" },
  { key: "rule", label: "规则判定", hint: "根据规则系统判定处理方式。" },
];
const builtin = new Set(["Direct", "Reject", "RejectDrop", "Compatible", "Pass", "Dns"]);
let lastTested = 0; // when the page last probed every node on open; not again within 5 minutes

// nodesOf is every non-group member across groups, built-ins (DIRECT, REJECT…) left out.
export function nodesOf(groups: Group[]): Proxy[] {
  const groupNames = new Set(groups.map((g) => g.name));
  const nodes: Proxy[] = [];
  const seen = new Set<string>();
  for (const g of groups) for (const p of g.all ?? []) {
    if (!groupNames.has(p.name) && !builtin.has(p.type) && !seen.has(p.name)) { seen.add(p.name); nodes.push(p); }
  }
  return nodes;
}
const groupTypes: Record<string, string> = { Selector: "手动选择", URLTest: "自动测速", Fallback: "故障转移", LoadBalance: "负载均衡", Relay: "链式", Subnet: "Subnet 策略组" };

export function delayClass(ms: number) {
  if (ms <= 0) return "";
  return ms < 300 ? "delay-good" : ms < 800 ? "delay-warn" : "delay-bad";
}

// name is what the tests measure (a group's current node); target is what 删除 removes.
type Menu = { x: number; y: number; name: string; kind: "proxy" | "group"; target: string };

export default function Proxies({ status }: { status: Status }) {
  const [mode, setMode] = useState("");
  const [groups, setGroups] = useState<Group[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [testing, setTesting] = useState(false);
  const [bandwidth, setBandwidth] = useState<Record<string, string>>({});
  const [measuring, setMeasuring] = useState("");
  const [dialog, setDialog] = useState<"proxy" | "group" | "">("");
  const [editing, setEditing] = useState<Record<string, any> | null>(null);
  const [editingGroup, setEditingGroup] = useState<Record<string, any> | null>(null);
  const [diagnose, setDiagnose] = useState("");
  const [pending, setPending] = useState<Set<string>>(new Set()); // nodes being tested
  const [open, setOpen] = useState(""); // group whose members are shown
  const [menu, setMenu] = useState<Menu | null>(null);
  const [query, setQuery] = useState("");
  const [order, setOrder] = useState(""); // "": the config's order, "delay" or "name"
  const [hideDead, setHideDead] = useState(false); // leave out nodes whose last test failed
  const [dead, setDead] = useState<Set<string>>(new Set()); // nodes whose own test just failed: 超时, not blank
  const [armed, confirm] = useConfirm();

  const load = useCallback(() => {
    CoreService.Groups().then((g) => { setGroups(g ?? []); setLoaded(true); });
  }, []);

  useEffect(() => {
    if (!status.running) return;
    CoreService.Groups().then((g) => {
      setGroups(g ?? []);
      setLoaded(true);
      if (Date.now() - lastTested > 5 * 60_000) { // latencies on open, as Surge shows them
        lastTested = Date.now();
        CoreService.TestGroup("GLOBAL").finally(load);
        return;
      }
      // within those five minutes only the blank ones: a node whose last test failed, often the
      // first connection after the node sat idle, would otherwise stay blank until clicked
      for (const p of nodesOf(g ?? [])) if (p.delay <= 0) testOne(p.name);
    });
    CoreService.Mode().then(setMode);
    return on("mode", (d) => setMode(d));
  }, [status.running, load]);

  useClickAway(!!menu || !!open, () => { setMenu(null); setOpen(""); });

  const nodes = nodesOf(groups);
  const q = query.trim().toLowerCase();
  const shownNodes = nodes
    .filter((p) => (!q || p.name.toLowerCase().includes(q) || p.type.toLowerCase().includes(q)) && (!hideDead || p.delay > 0 || pending.has(p.name)))
    .sort(order === "name" ? (a, b) => a.name.localeCompare(b.name, "zh") : order === "delay" ? (a, b) => (a.delay || 1e9) - (b.delay || 1e9) : () => 0);
  const shownGroups = groups.filter((g) => g.name !== "GLOBAL");
  const global = groups.find((g) => g.name === "GLOBAL");

  const choose = (m: string) => CoreService.SetMode(m);
  const select = (group: string, name: string) => CoreService.Select(group, name).then(load);
  const testAll = () => {
    setTesting(true);
    setPending(new Set(nodes.map((n) => n.name)));
    CoreService.TestGroup("GLOBAL").finally(() => { setTesting(false); setPending(new Set()); load(); });
  };
  const testOne = (name: string) => {
    setPending((p) => new Set(p).add(name));
    CoreService.TestProxy(name).catch(() => 0)
      .then((ms) => setDead((d) => { const n = new Set(d); if (ms > 0) n.delete(name); else n.add(name); return n; }))
      .then(load).finally(() => setPending((p) => { const n = new Set(p); n.delete(name); return n; }));
  };
  const speed = (name: string) => {
    if (measuring) return;
    setMeasuring(name);
    setBandwidth((b) => ({ ...b, [name]: "测带宽中…" }));
    ToolService.SpeedTest(name)
      .then((r) => setBandwidth((b) => ({ ...b, [name]: `${r.mbps.toFixed(1)} Mbps` })))
      .catch((e) => setBandwidth((b) => ({ ...b, [name]: `失败：${errText(e)}` })))
      .finally(() => setMeasuring(""));
  };
  const contextMenu = (e: React.MouseEvent, menu: Omit<Menu, "x" | "y">) => {
    e.preventDefault();
    setMenu({ x: e.clientX, y: e.clientY, ...menu });
  };

  const hint = modes.find((m) => m.key === mode)?.hint;
  const editGroup = (name: string) => ProfileService.Group(name).then(setEditingGroup);

  return (
    <>
      <div className="seg big-seg">
        {modes.map((m) => (
          <button key={m.key} className={m.key === mode ? "on accent" : ""} disabled={!status.running} onClick={() => choose(m.key)}>{m.label}</button>
        ))}
      </div>
      {hint && <div className="hint">{hint}</div>}

      <div className="tile-head">
        <span className="tile-title proxies">代理</span>
        <button className="btn small-btn" disabled={testing || !status.running} onClick={testAll}>{testing ? "测试中…" : "测试全部"}</button>
        <span className="grow" />
        <input data-search className="small-input" placeholder="搜索节点…" value={query} onChange={(e) => setQuery(e.target.value)} />
        <div className="seg small-seg">
          {[["", "默认"], ["delay", "延迟"], ["name", "名称"]].map(([k, l]) => <button key={k} className={order === k ? "on" : ""} onClick={() => setOrder(k)}>{l}</button>)}
        </div>
        <button className={`btn small-btn ${hideDead ? "on" : ""}`} onClick={() => setHideDead(!hideDead)}>{hideDead ? "显示超时" : "隐藏超时"}</button>
      </div>
      <div className="tiles">
        {shownNodes.map((p) => {
          const selected = mode === "global" && global?.now === p.name;
          return (
            <button key={p.name} className={`tile ${selected ? "selected" : ""}`} title={mode === "global" ? "点击测速并设为全局代理；右键更多操作" : "点击测速；右键更多操作"}
              onClick={() => { testOne(p.name); if (mode === "global") select("GLOBAL", p.name); }}
              onContextMenu={(e) => contextMenu(e, { name: p.name, kind: "proxy", target: p.name })}>
              <span className="kind">{p.type}</span>
              <span className="name">{p.name}</span>
              <span className={`foot ${pending.has(p.name) ? "" : p.delay > 0 ? delayClass(p.delay) : dead.has(p.name) ? "delay-bad" : ""}`} title={bandwidth[p.name]}>
                {pending.has(p.name) ? "测试中" : bandwidth[p.name] ?? (p.delay > 0 ? `${p.delay} ms` : dead.has(p.name) ? "超时" : "")}
              </span>
            </button>
          );
        })}
        {!query && <button className="tile add" disabled={!status.running} onClick={() => setDialog("proxy")} title="增加代理">＋</button>}
        {query && shownNodes.length === 0 && <div className="muted tiles-empty">没有匹配的节点</div>}
        {!loaded && status.running && <div className="muted tiles-empty">加载中…</div>}
      </div>

      <div className="tile-head">
        <span className="tile-title groups">策略组</span>
      </div>
      <div className="tiles">
        {shownGroups.map((g) => (
          <div key={g.name} className="tile-wrap">
            <button className={`tile ${open === g.name ? "selected" : ""}`} onClick={(e) => { e.stopPropagation(); setMenu(null); setOpen(open === g.name ? "" : g.name); }}
              onContextMenu={(e) => contextMenu(e, { name: g.now, kind: "group", target: g.name })}>
              <span className="kind">{groupTypes[g.type] ?? g.type}</span>
              <span className="name">{g.name}</span>
              <span className="foot">{g.now}</span>
            </button>
            {open === g.name && (
              <div className="menu members" onClick={(e) => e.stopPropagation()}>
                {(g.all ?? []).map((p) => (
                  <button key={p.name} disabled={g.type !== "Selector"} className={p.name === g.now ? "current" : ""}
                    onClick={() => { select(g.name, p.name); setOpen(""); }}>
                    <span>{p.name === g.now ? "✓ " : ""}{p.name}</span>
                    <span className={delayClass(p.delay)}>{p.delay > 0 ? `${p.delay} ms` : ""}</span>
                  </button>
                ))}
                <button onClick={() => { setOpen(""); setTesting(true); CoreService.TestGroup(g.name).finally(() => { setTesting(false); load(); }); }}>测试此组</button>
                <button onClick={() => { setOpen(""); editGroup(g.name); }}>编辑此策略组</button>
                <button className={`danger ${armed === g.name ? "armed" : ""}`} onClick={() => confirm(g.name, () => ProfileService.RemoveGroup(g.name).then(load))}>{armed === g.name ? "确认删除" : "删除此策略组"}</button>
              </div>
            )}
          </div>
        ))}
        <button className="tile add" disabled={!status.running} onClick={() => setDialog("group")} title="增加策略组">＋</button>
      </div>

      {menu && (
        <div className="menu" style={{ position: "fixed", left: menu.x, top: menu.y }}>
          {menu.kind === "proxy" && <button onClick={() => ProfileService.Proxy(menu.target)
            .then((p) => (protocols[p?.type] ? setEditing(p) : CoreService.Notify("warning", `${p?.type} 类型的代理暂不支持表单编辑，可在「配置」中直接编辑 YAML`)))}>编辑</button>}
          {menu.kind === "group" && <button onClick={() => editGroup(menu.target)}>编辑</button>}
          <button onClick={() => testOne(menu.name)}>测试延迟</button>
          <button disabled={!!measuring} onClick={() => speed(menu.name)}>测试带宽</button>
          <button onClick={() => setDiagnose(menu.target)}>诊断</button>
          <button className={`danger ${armed === `menu:${menu.target}` ? "armed" : ""}`} onClick={(e) => {
            e.stopPropagation(); // the first click arms it, and mustn't close the menu
            confirm(`menu:${menu.target}`, () => { setMenu(null); (menu.kind === "proxy" ? ProfileService.RemoveProxy(menu.target) : ProfileService.RemoveGroup(menu.target)).then(load); });
          }}>{armed === `menu:${menu.target}` ? "确认删除" : "删除"}</button>
        </div>
      )}
      {dialog === "proxy" && <ProxyDialog groups={shownGroups.filter((g) => g.type === "Selector").map((g) => g.name)} onClose={() => { setDialog(""); load(); }} />}
      {diagnose && <ProxyDiagnose nodes={nodes.map((n) => n.name)} groups={shownGroups.map((g) => g.name)} initial={diagnose} onClose={() => setDiagnose("")} />}
      {editing && <ProxyDialog groups={[]} editing={editing} onClose={() => { setEditing(null); load(); }} />}
      {dialog === "group" && <GroupDialog onClose={() => { setDialog(""); load(); }} />}
      {editingGroup && <GroupDialog editing={editingGroup} onClose={() => { setEditingGroup(null); load(); }} />}
    </>
  );
}
