import { useCallback, useEffect, useState } from "react";
import { CoreService, ProfileService, ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Group, Proxy, Status } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import ProxyDialog, { protocols } from "./ProxyDialog";
import ProxyDiagnose from "./ProxyDiagnose";
import GroupDialog from "./GroupDialog";
import { on, useClickAway } from "./hooks";

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
export const groupTypes: Record<string, string> = { Selector: "手动选择", URLTest: "自动测速", Fallback: "故障转移", LoadBalance: "负载均衡", Relay: "链式", Subnet: "Subnet 策略组" };

export function delayClass(ms: number) {
  if (ms <= 0) return "";
  return ms < 300 ? "delay-good" : ms < 800 ? "delay-warn" : "delay-bad";
}

// name is what the tests measure (a group's current node); target is what 删除 removes.
type Menu = { x: number; y: number; name: string; kind: "proxy" | "group"; target: string };

export default function Proxies({ status }: { status: Status }) {
  const [mode, setMode] = useState("");
  const [groups, setGroups] = useState<Group[]>([]);
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

  const load = useCallback(() => {
    CoreService.Groups().then((g) => setGroups(g ?? []));
  }, []);

  useEffect(() => {
    if (!status.running) return;
    CoreService.Groups().then((g) => {
      setGroups(g ?? []);
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
    CoreService.TestProxy(name).catch(() => 0).then(load).finally(() => setPending((p) => { const n = new Set(p); n.delete(name); return n; }));
  };
  const speed = (name: string) => {
    if (measuring) return;
    setMeasuring(name);
    setBandwidth((b) => ({ ...b, [name]: "测带宽中…" }));
    ToolService.SpeedTest(name)
      .then((r) => setBandwidth((b) => ({ ...b, [name]: `${r.mbps.toFixed(1)} Mbps` })))
      .catch(() => setBandwidth((b) => ({ ...b, [name]: "失败" })))
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
      </div>
      <div className="tiles">
        {nodes.map((p) => {
          const selected = mode === "global" && global?.now === p.name;
          return (
            <button key={p.name} className={`tile ${selected ? "selected" : ""}`} title={mode === "global" ? "点击测速并设为全局代理；右键更多操作" : "点击测速；右键更多操作"}
              onClick={() => { testOne(p.name); if (mode === "global") select("GLOBAL", p.name); }}
              onContextMenu={(e) => contextMenu(e, { name: p.name, kind: "proxy", target: p.name })}>
              <span className="kind">{p.type}</span>
              <span className="name">{p.name}</span>
              <span className={`foot ${pending.has(p.name) ? "" : delayClass(p.delay)}`}>{pending.has(p.name) ? "测试中" : bandwidth[p.name] ?? (p.delay > 0 ? `${p.delay} ms` : "")}</span>
            </button>
          );
        })}
        <button className="tile add" disabled={!status.running} onClick={() => setDialog("proxy")} title="增加代理">＋</button>
      </div>

      <div className="tile-head">
        <span className="tile-title groups">策略组</span>
        <button className="btn small-btn" disabled={testing || !status.running} onClick={testAll}>{testing ? "测试中…" : "测试全部"}</button>
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
                <button onClick={() => { setOpen(""); editGroup(g.name); }}>编辑此策略组</button>
                <button className="danger" onClick={() => ProfileService.RemoveGroup(g.name).then(load)}>删除此策略组</button>
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
          <button className="danger" onClick={() => (menu.kind === "proxy" ? ProfileService.RemoveProxy(menu.target) : ProfileService.RemoveGroup(menu.target)).then(load)}>删除</button>
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
