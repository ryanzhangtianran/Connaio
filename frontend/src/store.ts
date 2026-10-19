import { useSyncExternalStore } from "react";
import type { LogLine, Traffic } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { on } from "./hooks";

// Logs and traffic history live here, outside React state, so each line or tick re-renders only
// the page that reads them (Logs, Activity) instead of the whole tree under App.

const LOG_LINES = 500;
const HISTORY = 60; // seconds of traffic kept for the sparklines
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
const history = store<Traffic[]>([]);

let seq = 0;
let pending: Log[] = [];
let flush: ReturnType<typeof setTimeout> | undefined;

// start wires the Go events in; call once. Returns the unsubscribe.
export function start() {
  const offLog = on("log", (d) => {
    pending.push({ ...d, id: ++seq });
    flush ??= setTimeout(() => {
      flush = undefined;
      logs.set([...logs.get(), ...pending].slice(-LOG_LINES));
      pending = [];
    }, FLUSH_MS);
  });
  const offTraffic = on("traffic", (d) => history.set([...history.get().slice(1 - HISTORY), d]));
  return () => { offLog(); offTraffic(); clearTimeout(flush); flush = undefined; pending = []; };
}

export const clearLogs = () => { pending = []; logs.set([]); };
export const useLogs = () => useSyncExternalStore(logs.subscribe, logs.get);
export const useHistory = () => useSyncExternalStore(history.subscribe, history.get);
