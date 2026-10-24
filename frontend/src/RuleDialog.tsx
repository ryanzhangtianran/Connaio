import { useEffect, useState } from "react";
import { CoreService, ProfileService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Conn } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { Switch } from "./Settings";
import Dialog from "./Dialog";
import { errText, plain } from "./format";

export const ruleTypes = ["DOMAIN-SUFFIX", "DOMAIN", "DOMAIN-KEYWORD", "DOMAIN-REGEX", "DOMAIN-WILDCARD", "GEOSITE", "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR", "GEOIP", "IP-ASN",
  "DST-PORT", "SRC-PORT", "PROCESS-NAME", "PROCESS-PATH", "PROCESS-ANCESTOR", "PROCESS-ANCESTOR-PATH", "IN-NAME", "IN-TYPE", "NETWORK", "RULE-SET"];
const ipTypes = new Set(["IP-CIDR", "IP-CIDR6", "GEOIP", "IP-ASN"]); // these take no-resolve
const isIP = (h: string) => /^[\d.]+$/.test(h) || h.includes(":");

// suggest the registrable part of a host, e.g. translate.google.com -> google.com (naive: last two labels)
function suggest(c: Conn, type: string): string {
  if (type === "PROCESS-NAME" || type === "PROCESS-ANCESTOR") return c.process;
  if (type === "IP-CIDR") return isIP(c.host) ? `${c.host}/${c.host.includes(":") ? 128 : 32}` : "";
  if (type === "DOMAIN-SUFFIX") return c.host.split(".").slice(-2).join(".");
  return c.host;
}

export function age(start: string) {
  const s = Math.max(0, Math.round((Date.now() - Date.parse(start)) / 1000));
  return s < 60 ? `${s} 秒` : s < 3600 ? `${Math.floor(s / 60)} 分` : `${Math.floor(s / 3600)} 时`;
}

// RuleDialog adds a rule, for a connection (its host suggested, 立即生效 reconnecting it) or from the
// rules page, kept for now or written to the override; or edits one of the user's own (editing).
export function RuleDialog({ conn, editing, onClose }: { conn?: Conn; editing?: { index: number; rule: string }; onClose: () => void }) {
  const [t0, v0, p0, ...opts] = editing?.rule.split(",") ?? [];
  const [type, setType] = useState(t0 ?? (conn && isIP(conn.host) ? "IP-CIDR" : "DOMAIN-SUFFIX"));
  const [value, setValue] = useState(v0 ?? (conn ? suggest(conn, type) : ""));
  const [policies, setPolicies] = useState<string[]>([]);
  const [policy, setPolicy] = useState(p0 ?? "DIRECT");
  const [noResolve, setNoResolve] = useState(editing ? opts.includes("no-resolve") : true);
  const [permanent, setPermanent] = useState(!conn);
  const [reconnect, setReconnect] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => { CoreService.Policies().then((p) => setPolicies(p ?? [])); }, []);

  const payload = value.trim();
  const save = async () => {
    setBusy(true);
    setError("");
    try {
      const rule = [type, payload, policy, ...(ipTypes.has(type) && noResolve ? ["no-resolve"] : [])].join(",");
      if (editing) await ProfileService.EditRule(editing.index, rule);
      else if (permanent) await ProfileService.AddRule(rule);
      else await CoreService.AddTempRule(rule);
      if (conn && reconnect) await CoreService.CloseHost(type.startsWith("DOMAIN") ? payload : conn.host);
      onClose();
    } catch (e) {
      setError(errText(e, "保存失败"));
    } finally { setBusy(false); }
  };

  return (
    <Dialog title={editing ? "编辑规则" : "增加规则"} sub={conn && `${conn.host}${conn.process ? ` · ${conn.process}` : ""}`} className="narrow" locked={busy} onClose={onClose} foot={<>
      <button className="btn" onClick={onClose}>取消</button>
      <button className="btn primary" disabled={busy || !payload || payload.includes(",")} title={payload.includes(",") ? "值里不能有逗号" : ""} onClick={save}>完成</button>
    </>}>
      <div className="form-grid">
        <label>规则类型</label>
        <select value={type} onChange={(e) => { setType(e.target.value); if (conn) setValue(suggest(conn, e.target.value)); }}>
          {ruleTypes.map((t) => <option key={t}>{t}</option>)}
        </select>
        <label>值</label>
        <input {...plain} autoFocus={!conn} placeholder="如 google.com、10.0.0.0/8、ghostty" value={value} onChange={(e) => setValue(e.target.value)} />
        <label>策略</label>
        <select value={policy} onChange={(e) => setPolicy(e.target.value)}>
          {!policies.includes(policy) && <option>{policy}</option>}{/* the edited rule's, until the list arrives */}
          {policies.map((p) => <option key={p}>{p}</option>)}
        </select>
        {!editing && <>
          <label>有效期</label>
          <div className="inline">
            <div className="seg">
              <button className={permanent ? "" : "on"} onClick={() => setPermanent(false)}>临时</button>
              <button className={permanent ? "on" : ""} onClick={() => setPermanent(true)}>永久</button>
            </div>
            <span className="muted small">{permanent ? "写入覆写" : "重载配置后失效"}</span>
          </div>
        </>}
        {error && <><span /><div className="small error">{error}</div></>}
      </div>
      {ipTypes.has(type) && (
        <div className="setting-row option-row">
          <div><div>不解析域名</div><div className="muted small">只匹配直接访问的 IP，不为此规则解析域名</div></div>
          <Switch on={noResolve} onChange={setNoResolve} />
        </div>
      )}
      {conn && (
        <div className="setting-row option-row">
          <div><div>立即生效</div><div className="muted small">断开现有连接，按新规则重新连接</div></div>
          <Switch on={reconnect} onChange={setReconnect} />
        </div>
      )}
    </Dialog>
  );
}
