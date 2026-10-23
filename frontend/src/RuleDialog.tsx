import { useEffect, useState } from "react";
import { CoreService, ProfileService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Conn } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { Switch } from "./Settings";

const ruleTypes = ["DOMAIN-SUFFIX", "DOMAIN", "DOMAIN-KEYWORD", "IP-CIDR", "PROCESS-NAME", "PROCESS-ANCESTOR"];
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
  return s < 60 ? `${s}s` : s < 3600 ? `${Math.floor(s / 60)}m` : `${Math.floor(s / 3600)}h`;
}

export function RuleDialog({ conn, onClose }: { conn: Conn; onClose: () => void }) {
  const [type, setType] = useState(isIP(conn.host) ? "IP-CIDR" : "DOMAIN-SUFFIX");
  const [value, setValue] = useState(suggest(conn, type));
  const [policies, setPolicies] = useState<string[]>([]);
  const [policy, setPolicy] = useState("DIRECT");
  const [permanent, setPermanent] = useState(false);
  const [reconnect, setReconnect] = useState(true);
  const [busy, setBusy] = useState(false);
  useEffect(() => { CoreService.Policies().then((p) => setPolicies(p ?? [])); }, []);

  const payload = value.trim();
  const add = async () => {
    setBusy(true);
    try {
      const rule = `${type},${payload},${policy}${type === "IP-CIDR" ? ",no-resolve" : ""}`; // IP rules don't resolve names, as the rules page writes them
      if (permanent) await ProfileService.AddRule(rule);
      else await CoreService.AddTempRule(rule);
      if (reconnect) await CoreService.CloseHost(type.startsWith("DOMAIN") ? payload : conn.host);
      onClose();
    } finally { setBusy(false); }
  };

  return (
    <div className="modal-bg" onClick={onClose}>
      <div className="modal card" onClick={(e) => e.stopPropagation()}>
        <h2 className="modal-title">增加规则</h2>
        <div className="muted small modal-sub">{conn.host}{conn.process && ` · ${conn.process}`}</div>
        <div className="form-grid">
          <label>规则类型</label>
          <select value={type} onChange={(e) => { setType(e.target.value); setValue(suggest(conn, e.target.value)); }}>
            {ruleTypes.map((t) => <option key={t}>{t}</option>)}
          </select>
          <label>值</label>
          <input value={value} onChange={(e) => setValue(e.target.value)} />
          <label>策略</label>
          <select value={policy} onChange={(e) => setPolicy(e.target.value)}>
            {policies.map((p) => <option key={p}>{p}</option>)}
          </select>
          <label>有效期</label>
          <div className="inline">
            <div className="seg">
              <button className={permanent ? "" : "on"} onClick={() => setPermanent(false)}>临时</button>
              <button className={permanent ? "on" : ""} onClick={() => setPermanent(true)}>永久</button>
            </div>
            <span className="muted small">{permanent ? "写入覆写" : "不写入配置，重载配置后失效"}</span>
          </div>
        </div>
        <div className="setting-row option-row">
          <div>
            <div>立即生效</div>
            <div className="muted small">断开现有连接，按新规则重新连接</div>
          </div>
          <Switch on={reconnect} onChange={setReconnect} />
        </div>
        <div className="actions end">
          <button className="btn" onClick={onClose}>取消</button>
          <button className="btn primary" disabled={busy || !payload || payload.includes(",")} title={payload.includes(",") ? "值里不能有逗号" : ""} onClick={add}>增加</button>
        </div>
      </div>
    </div>
  );
}

