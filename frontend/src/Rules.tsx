import { useEffect, useMemo, useState } from "react";
import { CoreService, ProfileService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Rule, Status } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { useConfirm, usePoll } from "./hooks";
import { RuleDialog } from "./RuleDialog";

export default function Rules({ status }: { status: Status }) {
  const [rules, setRules] = useState<Rule[] | null>(null); // null: not loaded yet
  const [filter, setFilter] = useState("");
  const [own, setOwn] = useState<string[]>([]); // the user's own rules (prepend-rules), first in the list: the only ones that move or edit
  const mine = own.length;
  const [dialog, setDialog] = useState<{ index: number; rule: string } | true | null>(null); // true: a new rule
  const [armed, confirm] = useConfirm();
  const loadRules = () => CoreService.Rules().then((r) => setRules(r ?? []));
  // the user's own only change on an edit here; the rules' hit counts move on their own
  const load = () => Promise.all([loadRules(), ProfileService.UserItems().then((u) => setOwn(u.rules ?? []))]);
  useEffect(() => { if (status.running) load(); }, [status.running]);
  usePoll(loadRules, 10_000, status.running);

  const q = filter.toLowerCase();
  const shown = useMemo(() => (rules ?? []).filter((r) => !q || `${r.type} ${r.payload} ${r.proxy}`.toLowerCase().includes(q)), [rules, q]);
  const MAX_ROWS = 500; // ponytail: a cap instead of virtualisation; virtualise if people scroll past it
  const run = (p: Promise<unknown>) => { p.then(load); };
  const toggle = (r: Rule) => run(CoreService.SetRuleDisabled(r.index, !r.disabled));

  return (
    <div className="rules-page">
      <div className="card rules-card">
        <div className="head">
          <div className="label">{rules ? `共 ${rules.length} 条` : "加载中…"}</div>
          <div className="actions">
            <input data-search placeholder="搜索类型、内容、策略…" value={filter} onChange={(e) => setFilter(e.target.value)} style={{ width: 240 }} />
            <button className="btn primary" disabled={!status.running} onClick={() => setDialog(true)}>增加规则</button>
          </div>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>#</th><th>类型</th><th>内容</th><th>策略</th><th>命中</th><th /></tr></thead>
            <tbody>
              {shown.slice(0, MAX_ROWS).map((r) => (
                <tr key={r.index} className={r.disabled ? "muted" : ""}>
                  <td>{r.index}</td>
                  <td>{r.type}</td>
                  <td title={r.payload}>{r.payload}</td>
                  <td>{r.proxy}</td>
                  <td>{r.hits}</td>
                  <td className="rule-actions">
                    {r.index < mine && <>
                      <button className="btn" title="上移" disabled={r.index === 0} onClick={() => run(ProfileService.MoveRule(r.index, -1))}>↑</button>
                      <button className="btn" title="下移" disabled={r.index === mine - 1} onClick={() => run(ProfileService.MoveRule(r.index, 1))}>↓</button>
                    </>}
                    {r.index < mine && <button className="btn" onClick={() => setDialog({ index: r.index, rule: own[r.index] })}>编辑</button>}
                    <button className="btn" onClick={() => toggle(r)}>{r.disabled ? "启用" : "停用"}</button>
                    <button className={`btn danger${armed === String(r.index) ? " armed" : ""}`} disabled={r.type === "Match" && r.index >= mine} title={r.type === "Match" ? "兜底规则不能删除" : ""}
                      onClick={() => confirm(String(r.index), () => run(ProfileService.DeleteRule(r.index)))}>{armed === String(r.index) ? "确认删除" : "删除"}</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {shown.length > MAX_ROWS && <div className="muted small" style={{ padding: 8 }}>还有 {shown.length - MAX_ROWS} 条，搜索查看</div>}
          {rules && shown.length === 0 && <div className="muted small" style={{ padding: 8 }}>{q ? "没有匹配的规则" : status.running ? "没有规则" : "内核未运行"}</div>}
        </div>
      </div>
      {dialog && <RuleDialog editing={dialog === true ? undefined : dialog} onClose={() => { setDialog(null); load(); }} />}
    </div>
  );
}
