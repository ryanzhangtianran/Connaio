import { useState } from "react";
import { clearLogs, useLogs } from "./store";

const levels = ["debug", "info", "warning", "error"];

export default function Logs() {
  const lines = useLogs();
  const [level, setLevel] = useState("info");
  const [filter, setFilter] = useState("");
  const min = levels.indexOf(level);
  const q = filter.toLowerCase();
  const shown = lines.filter((l) => levels.indexOf(l.type) >= min && (!q || l.payload.toLowerCase().includes(q)));
  return (
    <div className="card logs-card">
      <div className="head">
        <div className="seg">
          {levels.slice(1).map((l) => <button key={l} className={l === level ? "on" : ""} onClick={() => setLevel(l)}>{l}</button>)}
        </div>
        <div className="actions">
          <input placeholder="搜索…" value={filter} onChange={(e) => setFilter(e.target.value)} style={{ width: 200 }} />
          <button className="btn" onClick={clearLogs}>清空</button>
        </div>
      </div>
      <div className="logs">
        <div className="log-rows">
          {shown.slice().reverse().map((l) => (
            <div key={l.id} className={`log ${l.type}`}><span className="muted time">{l.time}</span><span className="lvl">{l.type}</span><span className="msg">{l.payload}</span></div>
          ))}
        </div>
      </div>
    </div>
  );
}
