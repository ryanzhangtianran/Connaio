import { useSyncExternalStore } from "react";
import { CoreService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { LogLine, Traffic } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { on } from "./hooks";

// Logs and traffic history live here, outside React state, so each line or tick re-renders only
// the page that reads them (Logs, Activity) instead of the whole tree under App. Go keeps them too
// (CoreService.Recent), as closing the window frees this page: a new one starts from there.

const LOG_LINES = 500; // coreservice.go Recent keeps as many
const HISTORY = 60; // seconds of traffic kept for the sparklines, as Recent does
const FLUSH_MS = 250; // log lines are batched this often

type Log = LogLine & { id: number };

function store<T>(initial: T) {
  let value = initial;
  const subs = new Set<() => void>();
  return {
    get: () => value,
    set: (v: T) => { value = v; subs.forEach((s) => s()); },
    subscribe: (s: () => void) => { subs.add(s); return () => { subs.delete(s); }; },
  };
}

const logs = store<Log[]>([]);
// session is the traffic since the app started, from the per-second ticks: read along with useHistory
export const session = { up: 0, down: 0 };
const history = store<Traffic[]>([]);

let seq = 0;
let pending: Log[] = [];
let flush: ReturnType<typeof setTimeout> | undefined;

// start wires the Go events in; call once. Returns the unsubscribe.
// ponytail: what arrives between subscribing and Recent's reply is replaced by the reply, a line or
// a tick at most, and mostly in it anyway
export function start() {
  CoreService.Recent().then((r) => {
    logs.set((r.logs ?? []).map((l) => ({ ...l, id: ++seq })));
    history.set(r.traffic ?? []);
    session.up = r.session.up;
    session.down = r.session.down;
  });
  const offLog = on("log", (d) => {
    pending.push({ ...d, id: ++seq });
    flush ??= setTimeout(() => {
      flush = undefined;
      logs.set([...logs.get(), ...pending].slice(-LOG_LINES));
      pending = [];
    }, FLUSH_MS);
  });
  const offTraffic = on("traffic", (d) => {
    session.up += d.up;
    session.down += d.down;
    history.set([...history.get().slice(1 - HISTORY), d]);
  });
  return () => { offLog(); offTraffic(); clearTimeout(flush); flush = undefined; pending = []; };
}

export const clearLogs = () => { pending = []; logs.set([]); CoreService.ClearLogs(); };
export const useLogs = () => useSyncExternalStore(logs.subscribe, logs.get);
export const useHistory = () => useSyncExternalStore(history.subscribe, history.get);
