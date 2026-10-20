import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";

// on subscribes to an event the Go side declares (application.RegisterEvent), so a misspelt name
// fails to compile and the callback gets the typed data; it returns the unsubscribe.
export const on = <K extends keyof Events.CustomEvents>(name: K, cb: (data: Events.CustomEvents[K]) => void) =>
  Events.On(name, (e) => cb(e.data));

// usePoll calls fn now and again ms after each call settles while enabled (the latest fn, so it may
// close over state). Runs never overlap, and none start while the document is hidden; the first
// one after it becomes visible again runs at once.
export function usePoll(fn: () => void | Promise<unknown>, ms: number, enabled = true) {
  const latest = useRef(fn);
  latest.current = fn;
  useEffect(() => {
    if (!enabled) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let stopped = false;
    const run = () => {
      if (stopped || document.hidden) return;
      Promise.resolve(latest.current()).finally(() => { if (!stopped) timer = setTimeout(run, ms); });
    };
    const visible = () => { if (!document.hidden) { clearTimeout(timer); run(); } };
    document.addEventListener("visibilitychange", visible);
    run();
    return () => { stopped = true; clearTimeout(timer); document.removeEventListener("visibilitychange", visible); };
  }, [ms, enabled]);
}

// useClickAway calls close on any document click while open: menus and popovers that dismiss on
// a click elsewhere; the opener stops its own click's propagation.
export function useClickAway(open: boolean, close: () => void) {
  useEffect(() => {
    if (!open) return;
    document.addEventListener("click", close);
    return () => document.removeEventListener("click", close);
  }, [open]);
}

// useCachedURL is fetch(key)'s data URL (an icon from Go), fetched once per key for the app's lifetime.
const urls = new Map<string, Promise<string>>();
export function useCachedURL(key: string | undefined, fetch: (key: string) => Promise<string>) {
  const [src, setSrc] = useState<string>();
  useEffect(() => {
    if (!key) return;
    let alive = true;
    if (!urls.has(key)) urls.set(key, fetch(key).catch(() => ""));
    urls.get(key)!.then((u) => alive && setSrc(u));
    return () => { alive = false; };
  }, [key]);
  return src;
}

// useDirty is true once any of deps has changed since mount: a form with edits to protect.
export function useDirty(deps: unknown[]) {
  const initial = useRef(deps); // compared, not counted: StrictMode runs the mount effect twice
  return deps.some((d, i) => !Object.is(d, initial.current[i]));
}

// useFlip slides a list's rows to where a change put them instead of letting them jump: capture()
// reads where the container's [data-flip] children are just before the change, and once React has
// laid the list out again each row that moved starts from there and slides home. Positions are
// read at the change, not at the last render, so a <details> opened in between doesn't skew them.
export function useFlip<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const before = useRef<Map<string, number> | null>(null);
  const tops = () => {
    const el = ref.current, out = new Map<string, number>();
    if (!el) return out;
    const top = el.getBoundingClientRect().top;
    el.querySelectorAll<HTMLElement>(":scope > [data-flip]").forEach((c) => out.set(c.dataset.flip!, c.getBoundingClientRect().top - top));
    return out;
  };
  useLayoutEffect(() => {
    const was = before.current;
    if (!was) return;
    before.current = null;
    const el = ref.current;
    if (!el) return;
    for (const [key, top] of tops()) {
      const from = was.get(key);
      if (from === undefined || from === top) continue;
      el.querySelector<HTMLElement>(`:scope > [data-flip="${CSS.escape(key)}"]`)
        ?.animate([{ transform: `translateY(${from - top}px)` }, { transform: "none" }], { duration: 320, easing: "cubic-bezier(.2, .8, .2, 1)" });
    }
  });
  return { ref, capture: () => { before.current = tops(); } };
}

// useWholeRows caps a scrolling list at as many whole rows as its box holds, so none shows cut off
// at the bottom whatever the window's height; the room left under the last one stays empty. Its
// .whole-rows scroll-snap keeps the rows whole as it scrolls, the cap being whole rows tall. The
// list wants no bottom padding: the next row would show through it.
export function useWholeRows<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const fit = () => {
    const el = ref.current;
    if (!el) return;
    el.style.maxHeight = "";
    const rows = el.children as HTMLCollectionOf<HTMLElement>;
    if (rows.length < 2) return;
    const s = getComputedStyle(el), pad = parseFloat(s.paddingTop) + parseFloat(s.paddingBottom);
    const first = rows[0].offsetHeight, pitch = rows[1].offsetTop - rows[0].offsetTop;
    const n = Math.max(1, Math.floor((el.clientHeight - pad - first) / pitch) + 1);
    if (n < rows.length) el.style.maxHeight = `${pad + first + (n - 1) * pitch}px`;
  };
  useLayoutEffect(fit); // the rows change with the data
  useEffect(() => { // and the room with the window
    const box = ref.current?.parentElement;
    if (!box) return;
    const ro = new ResizeObserver(fit);
    ro.observe(box);
    return () => ro.disconnect();
  }, []);
  return ref;
}
