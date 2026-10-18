import { useEffect, useRef, useState } from "react";
import { Window } from "@wailsio/runtime";
import { Picker, Routes, useWatch } from "./Activity";
import { speed } from "./format";

// Monitor is the floating process monitor: the activity page's 进程监视 card in a small window kept
// above the others, with the shown application's speed. Its head drags the window; ✕ hides it.
export default function Monitor() {
  const { conns, procs, shown, choose, routes } = useWatch(1000);
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
  return (
    <div className="monitor">
      <div className="monitor-head">
        <Picker value={shown} options={procs} onChange={choose} />
        <span className="monitor-rate">↑ {speed(rate[0]).join(" ")}　↓ {speed(rate[1]).join(" ")}</span>
        <button className="monitor-close" onClick={() => Window.Close()} title="关闭">✕</button>
      </div>
      <div className="monitor-body"><Routes shown={shown} routes={routes} /></div>
    </div>
  );
}
