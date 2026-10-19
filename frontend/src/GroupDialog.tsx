import { useEffect, useState } from "react";
import { CoreService, ProfileService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { useDirty } from "./hooks";
import { bypassKinds, bypassOf, condOf } from "./Vpn";

const types = [
  ["select", "手动选择"],
  ["url-test", "自动测速（最低延迟）"],
  ["fallback", "故障转移"],
  ["load-balance", "负载均衡"],
  ["subnet", "Subnet（按网络自动选择）"],
];

// A Rule is one subnet condition (a bypassKinds kind and its value) and the policy it picks.
type Rule = { t: string; v: string; target: string };

// GroupDialog adds a group, or with editing set replaces that one (its name stays: rules and other
// groups refer to it).
export default function GroupDialog({ editing, onClose }: { editing?: Record<string, any>; onClose: () => void }) {
  const [name, setName] = useState<string>(editing?.name ?? "");
  const [type, setType] = useState<string>(editing?.type ?? "select");
  const [names, setNames] = useState<string[]>([]);
  const [members, setMembers] = useState<string[]>(editing?.proxies ?? []);
  const [url, setURL] = useState<string>(editing?.url ?? "https://www.gstatic.com/generate_204");
  const [period, setPeriod] = useState(String(editing?.interval ?? "300"));
  const [strategy, setStrategy] = useState<string>(editing?.strategy ?? "consistent-hashing");
  const [def, setDef] = useState<string>(editing?.default ?? "DIRECT");
  const [rules, setRules] = useState<Rule[]>(editing?.rules
    ? Object.entries(editing.rules as Record<string, string>).map(([cond, target]) => ({ ...bypassOf(cond), target }))
    : [{ t: "SSID", v: "", target: "DIRECT" }]);
  const [busy, setBusy] = useState(false);
  const dirty = useDirty([name, type, members, url, period, strategy, def, rules]);
  useEffect(() => { CoreService.ProxyNames().then((n) => setNames([...new Set([...(n ?? []), "DIRECT", "REJECT"])].filter((x) => x !== editing?.name))); }, []);

  const probes = type !== "select" && type !== "subnet"; // url-test, fallback, load-balance
  const fixed = (t: string) => !!bypassKinds.find((k) => k.key === t)?.fixed;
  const save = () => {
    const kept = editing?.type === type ? editing : {}; // fields the form doesn't show, while the type stays
    const group: Record<string, any> = type === "subnet"
      ? { ...kept, name, type, default: def, rules: Object.fromEntries(rules.filter((r) => fixed(r.t) || r.v.trim()).map((r) => [condOf({ t: r.t, v: r.v.trim() }), r.target])) }
      : { ...kept, name, type, proxies: members };
    if (probes) Object.assign(group, { url, interval: Number(period) });
    if (type === "load-balance") group.strategy = strategy;
    setBusy(true);
    (editing ? ProfileService.EditGroup(editing.name, group) : ProfileService.AddGroup(group)).then(onClose).finally(() => setBusy(false));
  };
  const setRule = (i: number, r: Partial<Rule>) => setRules(rules.map((x, j) => (j === i ? { ...x, ...r } : x)));
  const duplicate = !editing && names.includes(name.trim());
  const ready = !!name.trim() && !duplicate && (type === "subnet" || members.length > 0) && (!probes || (/^\d+$/.test(period) && Number(period) > 0));

  return (
    <div className="modal-bg" onClick={dirty ? undefined : onClose}>
      <div className="modal card" onClick={(e) => e.stopPropagation()}>
        <h2 className="modal-title">{editing ? `编辑 ${editing.name}` : "增加策略组"}</h2>
        <div className="form-grid">
          <label>名称</label><input autoFocus={!editing} readOnly={!!editing} placeholder={duplicate ? "已有同名策略组或代理" : "必填"} value={name} onChange={(e) => setName(e.target.value)} style={duplicate ? { borderColor: "var(--bad)" } : undefined} />
          <label>类型</label>
          <select value={type} onChange={(e) => setType(e.target.value)}>{types.map(([k, l]) => <option key={k} value={k}>{l}</option>)}</select>
          {probes && <><label>测试 URL</label><input value={url} onChange={(e) => setURL(e.target.value)} /><label>间隔（秒）</label><input value={period} onChange={(e) => setPeriod(e.target.value)} /></>}
          {type === "subnet" && <><label>其他网络</label><select value={def} onChange={(e) => setDef(e.target.value)}>{names.map((n) => <option key={n}>{n}</option>)}</select></>}
          {type === "load-balance" && (
            <><label>策略</label><select value={strategy} onChange={(e) => setStrategy(e.target.value)}>{["consistent-hashing", "round-robin", "sticky-sessions"].map((s) => <option key={s}>{s}</option>)}</select></>
          )}
        </div>
        {type === "subnet" ? (
          <>
            <div className="section-title">按网络选择</div>
            {rules.map((r, i) => (
              <div key={i} className="subnet-row">
                <select value={r.t} onChange={(e) => setRule(i, { t: e.target.value, v: "" })}>{bypassKinds.map((k) => <option key={k.key} value={k.key}>{k.label}</option>)}</select>
                {fixed(r.t) ? <span /> : <input value={r.v} placeholder={bypassKinds.find((k) => k.key === r.t)?.placeholder} onChange={(e) => setRule(i, { v: e.target.value })} />}
                <span className="muted">→</span>
                <select value={r.target} onChange={(e) => setRule(i, { target: e.target.value })}>{names.map((n) => <option key={n}>{n}</option>)}</select>
                <button className="btn" onClick={() => setRules(rules.filter((_, j) => j !== i))}>×</button>
              </div>
            ))}
            <button className="btn" onClick={() => setRules([...rules, { t: "SSID", v: "", target: "DIRECT" }])}>增加一条</button>
          </>
        ) : (
          <>
            <div className="section-title">成员（按顺序）</div>
            <div className="checks scroll">
              {names.map((n) => (
                <label key={n} className="radio"><input type="checkbox" checked={members.includes(n)} onChange={(e) => setMembers(e.target.checked ? [...members, n] : members.filter((x) => x !== n))} /> {n}</label>
              ))}
            </div>
          </>
        )}
        <div className="actions end">
          <button className="btn" onClick={onClose}>取消</button>
          <button className="btn primary" disabled={!ready || busy} onClick={save}>完成</button>
        </div>
      </div>
    </div>
  );
}
