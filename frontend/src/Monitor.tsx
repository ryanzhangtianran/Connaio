import { useEffect, useRef, useState } from "react";
import { Window } from "@wailsio/runtime";
import { SettingsService, SystemProxyService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { ClientIcon, Picker, Routes, useWatch } from "./Activity";
import { errText, speed } from "./format";
import { on } from "./hooks";

// the window folded (the pet, as app.go builds it) and open, and the narrowest the open panel goes
const PET_W = 140, PET_H = 132, WIDTH = 340, OPEN = 248, MIN_W = 280, MIN_H = 52;

// the pet: 呆猫八条, by its creator on Weibo (credited on the about page), as a ChatGPT pet's sprite
// sheet: 8 columns of 192×208 frames, rows 0-8 its animations, 9 and 10 its 16 look directions
const SHEET = "/pets/batiao.png", ROWS = 11;
// the rows this plays, with each frame's time in ms, as the sheet's spec gives them
const frames = (n: number, ms: number, last: number) => [...Array(n - 1).fill(ms), last];
const ANIM = {
  idle: [0, [280, 110, 110, 140, 140, 320]],
  waving: [3, frames(4, 140, 280)],
  jumping: [4, frames(5, 140, 280)],
  failed: [5, frames(8, 140, 240)],
  waiting: [6, frames(6, 150, 260)],
  running: [7, frames(6, 120, 220)],
  review: [8, frames(6, 150, 280)],
} as const satisfies Record<string, readonly [number, readonly number[]]>;
type Anim = keyof typeof ANIM;
const ACTS: Anim[] = ["waving", "jumping"]; // played in turn as it's clicked
const still = matchMedia("(prefers-reduced-motion: reduce)").matches;

// offsets is, for each row of the sheet, how far (in frames, across and down) to move it so its
// drawing sits where the idle row's does: centred on it and on the same floor, however each row was
// drawn in its cells. Read from the sheet's pixels once a run, as the pet first shows.
let offsets: Promise<[number, number][]> | undefined;
function loadOffsets() {
  offsets ??= new Promise((resolve) => {
    const img = new Image();
    img.onload = () => {
      const c = document.createElement("canvas");
      c.width = img.width; c.height = img.height;
      const g = c.getContext("2d", { willReadFrequently: true });
      if (!g) return resolve([]);
      g.drawImage(img, 0, 0);
      const w = img.width / 8, h = img.height / ROWS;
      // each row's drawing across all its frames: its left and right within a cell, its floor
      const box = (r: number) => {
        const d = g.getImageData(0, r * h, img.width, h).data;
        let x0 = w, x1 = 0, y1 = 0;
        for (let y = 0; y < h; y++) for (let x = 0; x < img.width; x++) {
          if (d[(y * img.width + x) * 4 + 3] < 32) continue;
          const cx = x % w;
          if (cx < x0) x0 = cx;
          if (cx > x1) x1 = cx;
          if (y > y1) y1 = y;
        }
        return x1 < x0 ? null : [(x0 + x1) / 2, y1];
      };
      const base = box(0);
      resolve(!base ? [] : [...Array(ROWS)].map((_, r) => {
        const b = r === 0 ? base : box(r);
        return b ? [(base[0] - b[0]) / w, (base[1] - b[1]) / h] : [0, 0];
      }));
    };
    img.onerror = () => resolve([]);
    img.src = SHEET;
  });
  return offsets;
}

// Sprite plays anim, pace times as fast; look (x and y, -1 to 1, from its centre) turns it toward
// the pointer instead.
function Sprite({ anim, pace, look }: { anim: Anim; pace: number; look: [number, number] | null }) {
  const [frame, setFrame] = useState(0);
  const [shift, setShift] = useState<[number, number][]>([]);
  useEffect(() => { loadOffsets().then(setShift); }, []);
  const [row, ms] = ANIM[anim];
  useEffect(() => setFrame(0), [anim]);
  useEffect(() => {
    if (still) return;
    const t = setTimeout(() => setFrame((frame + 1) % ms.length), ms[frame % ms.length] / pace);
    return () => clearTimeout(t);
  }, [frame, ms, pace]);
  let cell = [frame % ms.length, row];
  if (look) {
    // 16 directions clockwise from straight up, 8 to a row
    const dir = Math.round((Math.atan2(look[0], -look[1]) * 180 / Math.PI + 360) % 360 / 22.5) % 16;
    cell = [dir % 8, 9 + (dir >> 3)];
  }
  const [dx, dy] = shift[cell[1]] ?? [0, 0];
  return <div className="sprite" style={{ backgroundImage: `url(${SHEET})`, backgroundSize: `800% ${ROWS * 100}%`,
    backgroundPosition: `${cell[0] / 7 * 100}% ${cell[1] / (ROWS - 1) * 100}%`, transform: `translate(${dx * 100}%, ${dy * 100}%)` }} />;
}

// useProxy is the system proxy: on, a switch under way, its last failure (for 5s), and the switch
function useProxy() {
  const [proxy, setProxy] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState("");
  useEffect(() => { SystemProxyService.Enabled().then(setProxy); return on("sysproxy", setProxy); }, []);
  useEffect(() => {
    if (!failed) return;
    const t = setTimeout(() => setFailed(""), 5000);
    return () => clearTimeout(t);
  }, [failed]);
  const toggle = () => {
    if (busy) return;
    setBusy(true);
    setFailed("");
    SystemProxyService.SetEnabled(!proxy).catch((e) => setFailed(errText(e))).finally(() => setBusy(false));
  };
  return { proxy, busy, failed, toggle };
}

const hide = () => SettingsService.ShowPet(false);

// Pet is the folded monitor: the pet, with the machine's speed under it on hover. It lies down while
// the system proxy is off, waits while it's switched, frowns when that fails; on, it sits idle or,
// as bytes move, works, the faster the quicker. It's dragged anywhere, waves or jumps when clicked,
// looks toward the pointer; right-click opens its menu; on hover, the proxy switch, unfold and hide.
function Pet({ unfold }: { unfold: () => void }) {
  const [rate, setRate] = useState<[number, number]>([0, 0]); // all of this machine's traffic, a second's
  useEffect(() => on("traffic", (t) => setRate([t.up, t.down])), []);
  const { proxy, busy, failed, toggle } = useProxy();
  const [act, setAct] = useState<{ anim: Anim } | null>(null); // the click playing, a new one each click
  const clicks = useRef(0);
  const [look, setLook] = useState<[number, number] | null>(null);
  const [menu, setMenu] = useState<[number, number] | null>(null); // where it was right-clicked
  useEffect(() => {
    if (!act) return;
    const t = setTimeout(() => setAct(null), ANIM[act.anim][1].reduce((a, b) => a + b));
    return () => clearTimeout(t);
  }, [act]);
  useEffect(() => {
    if (!menu) return;
    const close = (e: Event) => (e instanceof KeyboardEvent ? e.key === "Escape" : true) && setMenu(null);
    addEventListener("keydown", close);
    addEventListener("blur", close);
    return () => { removeEventListener("keydown", close); removeEventListener("blur", close); };
  }, [menu]);
  const total = rate[0] + rate[1];
  const anim: Anim = act?.anim ?? (busy ? "waiting" : failed ? "review" : !proxy ? "failed" : total < 2048 ? "idle" : "running");
  const pace = anim === "running" ? Math.min(4, 1 + Math.log10(total / 2048)) : 1;
  const state = busy ? (proxy ? "正在关闭代理…" : "正在开启代理…") : failed ? "代理开关失败" : !proxy ? "代理已关闭" : "";
  const tone = busy ? "busy" : failed ? "bad" : !proxy ? "off" : "";
  const item = (label: string, run: () => void) => <button onClick={(e) => { e.stopPropagation(); setMenu(null); run(); }}>{label}</button>;
  return (
    <div className={`pet ${state ? "shown" : ""}`}
      // on macOS Wails zooms a frameless window double-clicked where it drags: a quick second click pets, so not there
      onMouseDown={(e) => e.currentTarget.style.setProperty("--wails-draggable", e.detail > 1 ? "no-drag" : "drag")}
      onClick={() => menu ? setMenu(null) : setAct({ anim: ACTS[clicks.current++ % ACTS.length] })}
      onContextMenu={(e) => { e.preventDefault(); setMenu([e.clientX, e.clientY]); }}
      onMouseMove={(e) => {
        const r = e.currentTarget.getBoundingClientRect();
        setLook([(e.clientX - r.left) / r.width * 2 - 1, (e.clientY - r.top) / r.height * 2 - 1]);
      }}
      onMouseLeave={() => setLook(null)}>
      <Sprite anim={anim} pace={pace} look={act || menu ? null : look} />
      {/* under the pet: the proxy's state while it's not simply on, else the speed on hover */}
      <div className={`pet-tag ${tone}`} title={failed || undefined}>
        {state ? <><i />{state}</> : <><span><b className="down">↓</b> {short(rate[1])}</span><span><b className="up">↑</b> {short(rate[0])}</span></>}
      </div>
      <div className="pet-btns">
        <button className={`monitor-btn ${proxy ? "on" : ""}`} disabled={busy} onClick={(e) => { e.stopPropagation(); toggle(); }} title={proxy ? "关闭代理" : "开启代理"}>
          <svg viewBox="0 0 10 10" width="10" height="10"><path d="M5 1v4M2.6 2.8a3.4 3.4 0 1 0 4.8 0" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" /></svg>
        </button>
        <button className="monitor-btn" onClick={(e) => { e.stopPropagation(); unfold(); }} title="展开">
          <svg viewBox="0 0 10 6" width="10" height="6"><path d="M1 1l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" /></svg>
        </button>
        <button className="monitor-btn" onClick={(e) => { e.stopPropagation(); hide(); }} title="隐藏桌宠">
          <svg viewBox="0 0 10 10" width="9" height="9"><path d="M1 1l8 8M9 1l-8 8" fill="none" stroke="currentColor" strokeWidth="1.5" /></svg>
        </button>
      </div>
      {menu && (
        // kept inside the small window: it opens leftward or upward where it would run off
        <div className="pet-menu" style={{ left: Math.min(menu[0], PET_W - 112), top: Math.min(menu[1], PET_H - 90) }}
          onMouseDown={(e) => e.stopPropagation()} onClick={(e) => e.stopPropagation()}>
          {item(busy ? "切换中…" : proxy ? "关闭代理" : "开启代理", toggle)}
          {item("展开面板", unfold)}
          <hr />
          {item("隐藏桌宠", hide)}
        </div>
      )}
    </div>
  );
}

// short is a speed in at most four characters, for the pet's tag: 820B, 35K, 1.2M
const short = (bytes: number) => bytes < 1000 ? `${bytes}B` : bytes < 999500 ? `${Math.round(bytes / 1000)}K` : `${(bytes / 1e6).toFixed(1)}M`;

// Rate is one direction's speed: its arrow in the activity page's colour, the figure, a smaller unit.
function Rate({ dir, bytes }: { dir: "up" | "down"; bytes: number }) {
  const [n, unit] = speed(bytes);
  return <span className={`monitor-rate ${dir}`}><i>{dir === "down" ? "↓" : "↑"}</i><b>{n}</b><small>{unit}</small></span>;
}

// Panel is the unfolded monitor: a bar with the shown application, its icon and speed, over the
// activity page's 进程监视 card: its routes and their exit addresses. It watches the connections only
// while it's open; the bar drags the window.
function Panel({ fold }: { fold: () => void }) {
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
  const icon = conns.find((c) => c.app === shown)?.icon;
  // the node it goes through: the last hop of its busiest route, and how many routes it takes
  const [main] = [...routes].sort((a, b) => b[1].length - a[1].length);
  const hop = main ? main[0].split(" → ").pop()! : "";
  const node = !main ? "没有连接" : `${hop === "DIRECT" ? "直连" : hop}${routes.size > 1 ? ` 等 ${routes.size} 条` : ""}`;

  return (
    <div className="monitor">
      {/* each column centred as a whole, its two rows at fixed heights so they line up across: the
          app over the node it goes through, download over upload, fold over close */}
      <div className="monitor-bar">
        <ClientIcon path={icon} />
        <div className="monitor-col monitor-who">
          <div className="monitor-name"><Picker value={shown} options={procs} onChange={choose} /></div>
          <span className="monitor-node">{node}</span>
        </div>
        <div className="monitor-col monitor-speed">
          <Rate dir="down" bytes={rate[1]} />
          <Rate dir="up" bytes={rate[0]} />
        </div>
        <div className="monitor-btns">
          <button className="monitor-btn" onClick={fold} title="收起">
            <svg viewBox="0 0 10 6" width="10" height="6" style={{ transform: "rotate(180deg)" }}><path d="M1 1l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" /></svg>
          </button>
          <button className="monitor-btn" onClick={hide} title="隐藏桌宠">
            <svg viewBox="0 0 10 10" width="9" height="9"><path d="M1 1l8 8M9 1l-8 8" fill="none" stroke="currentColor" strokeWidth="1.5" /></svg>
          </button>
        </div>
      </div>
      <div className="monitor-body"><Routes shown={shown} routes={routes} /></div>
    </div>
  );
}

// folded is whether the monitor was last left folded (the pet), kept by the webview between runs
function loadFolded() {
  try { return localStorage.getItem("monitor.folded") !== "0"; } catch { return true; }
}

// Monitor is the floating process monitor: the desktop pet (Pet), unfolding into the panel (Panel).
export default function Monitor() {
  const [folded, setFolded] = useState(loadFolded);
  useEffect(() => {
    SettingsService.MonitorPet(folded);
    // the pet's window is fixed at its size; the panel's can be widened, never narrowed past its bar
    Window.SetMinSize(folded ? PET_W : MIN_W, folded ? PET_H : MIN_H);
    Window.SetSize(folded ? PET_W : WIDTH, folded ? PET_H : OPEN);
    try { localStorage.setItem("monitor.folded", folded ? "1" : "0"); } catch { /* a convenience only */ }
  }, [folded]);
  return folded ? <Pet unfold={() => setFolded(false)} /> : <Panel fold={() => setFolded(true)} />;
}
