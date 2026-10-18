import { useEffect, useRef, useState } from "react";
import { Window } from "@wailsio/runtime";
import { ClientIcon, Picker, Routes, useWatch } from "./Activity";
import { speed } from "./format";

const FOLDED = 52, OPEN = 248; // the window's height either way (app.go builds it folded)

// folded is whether the monitor was last left folded, kept by the webview between runs
function loadFolded() {
  try { return localStorage.getItem("monitor.folded") !== "0"; } catch { return true; }
}

// Monitor is the floating process monitor: a bar with the shown application, its icon and speed,
// unfolding into the activity page's 进程监视 card: its routes and their exit addresses. The bar
// drags the window; ✕ hides it.
export default function Monitor() {
  const { conns, procs, shown, choose, routes } = useWatch(1000);
  const [folded, setFolded] = useState(loadFolded);
  useEffect(() => {
    Window.Size().then((s) => Window.SetSize(s.width, folded ? FOLDED : OPEN));
    try { localStorage.setItem("monitor.folded", folded ? "1" : "0"); } catch { /* a convenience only */ }
  }, [folded]);

  // the speed is the bytes its connections moved since the last poll, a second ago
  const last = useRef(new Map<string, [number, number]>());
  const [rate, setRate] = useState<[number, number]>([0, 0]);
  useEffect(() => {
    let up = 0, down = 0;
    const next = new Map<string, [number, number]>();
    for (const c of conns) {
      const was = last.current.get(c.id);
      if (was && c.app === shown) { up += c.upload - was[0]; down += c.download - was[1]; }
      next.set(c.id, [c.upload, c.download]);
    }
    last.current = next;
    setRate([Math.max(0, up), Math.max(0, down)]);
  }, [conns, shown]);
  const icon = conns.find((c) => c.app === shown)?.icon;
  // the node it goes through: the last hop of its busiest route, and how many routes it takes
  const [main] = [...routes].sort((a, b) => b[1].length - a[1].length);
  const hop = main ? main[0].split(" → ").pop()! : "";
  const node = !main ? "没有连接" : `${hop === "DIRECT" ? "直连" : hop}${routes.size > 1 ? ` 等 ${routes.size} 条` : ""}`;

  return (
    <div className={`monitor ${folded ? "folded" : ""}`}>
      {/* two rows: the app over the node it goes through, download over upload */}
      <div className="monitor-bar">
        <ClientIcon path={icon} />
        {/* the picker's menu needs the open window's room; folded, the name alone */}
        <div className="monitor-name">{folded ? <span>{shown || "—"}</span> : <Picker value={shown} options={procs} onChange={choose} />}</div>
        <span className="monitor-node">{node}</span>
        <span className="monitor-down">↓ {speed(rate[1]).join(" ")}</span>
        <span className="monitor-up">↑ {speed(rate[0]).join(" ")}</span>
        <button className="monitor-btn fold" onClick={() => setFolded(!folded)} title={folded ? "展开" : "收起"}>
          <svg viewBox="0 0 10 6" width="9" height="6" style={{ transform: folded ? "" : "rotate(180deg)" }}><path d="M1 1l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" /></svg>
        </button>
        <button className="monitor-btn close" onClick={() => Window.Close()} title="关闭">
          <svg viewBox="0 0 10 10" width="8" height="8"><path d="M1 1l8 8M9 1l-8 8" fill="none" stroke="currentColor" strokeWidth="1.5" /></svg>
        </button>
      </div>
      {!folded && <div className="monitor-body"><Routes shown={shown} routes={routes} /></div>}
    </div>
  );
}
