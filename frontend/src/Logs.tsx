import { useState } from "react";
import { clearLogs, useLogs } from "./store";
import { copyText } from "./format";

const levels = ["debug", "info", "warning", "error"];
const levelNames: Record<string, string> = { debug: "调试", info: "信息", warning: "警告", error: "错误" };

export default function Logs() {
  const live = useLogs();
  const [held, setHeld] = useState<typeof live | null>(null); // 暂停: the lines as they were, while new ones keep arriving
  const lines = held ?? live;
  const [level, setLevel] = useState("info");
  const [filter, setFilter] = useState("");
  const min = levels.indexOf(level);
  const q = filter.toLowerCase();
  const shown = lines.filter((l) => levels.indexOf(l.type) >= min && (!q || l.payload.toLowerCase().includes(q)));
  return (
    <div className="card logs-card">
      <div className="head">
        <div className="seg">
          {levels.map((l) => <button key={l} className={l === level ? "on" : ""} onClick={() => setLevel(l)}>{levelNames[l]}</button>)}
        </div>
        <div className="actions">
          <input data-search placeholder="搜索…" value={filter} onChange={(e) => setFilter(e.target.value)} style={{ width: 200 }} />
          <button className="btn" onClick={() => setHeld(held ? null : live)}>{held ? "继续" : "暂停"}</button>
          <button className="btn" disabled={!shown.length} onClick={() => copyText(shown.map((l) => `${l.time} ${levelNames[l.type] ?? l.type} ${l.payload}`).join("\n"))}>复制</button>
          <button className="btn" onClick={() => { setHeld(null); clearLogs(); }}>清空</button>
        </div>
      </div>
      <div className="logs">
        {shown.length === 0 && <div className="muted logs-empty">{lines.length ? "没有符合条件的日志" : "暂无日志"}</div>}
        <div className="log-rows">
          {shown.slice().reverse().map((l) => (
            <div key={l.id} className={`log ${l.type}`}><span className="muted time">{l.time}</span><span className="lvl">{levelNames[l.type] ?? l.type}</span><span className="msg">{l.payload}</span></div>
          ))}
        </div>
      </div>
    </div>
  );
}
