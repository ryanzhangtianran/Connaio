import { useCallback, useEffect, useState } from "react";
import { EnhancedService, SettingsService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { ProcessChoice, Status } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { ListSetting, Switch, useSetting } from "./Settings";
import { usePoll } from "./hooks";
import { cidr, words } from "./format";

const sources: Record<string, string> = { app: "正在运行", network: "有网络连接" };

// tools are command-line programs that ignore the system proxy; picking one captures it and what it
// spawns (git's ssh and git-remote-https, pip's and npm's downloads) by PROCESS-ANCESTOR, which
// matches the executable's name anywhere up the chain: claude and codex installed natively are
// these names, installed through npm they run as node.
const defaultTools = ["ssh", "scp", "sftp", "rsync", "git", "curl", "wget", "pip3", "python3", "npm", "node", "go", "brew", "kubectl", "conda", "claude", "codex"];

// Processes is the enhanced page: enhanced mode routes all traffic through TUN, partial enhancement
// only the selected apps (and their child processes). They are chosen apart; enhanced mode covers
// partial enhancement, which waits underneath until it is off (EnhancedService.set).
export default function Processes({ status, mode, rooted }: { status: Status; mode: string; rooted: boolean }) {
  const [busy, setBusy] = useState("");
  const [processes, setProcesses] = useSetting<string[]>("capture.processes", []);
  const [choices, setChoices] = useState<ProcessChoice[]>([]);
  const [query, setQuery] = useState("");
  const [newTool, setNewTool] = useState("");

  const [partial, setPartial] = useState(false); // partial enhancement chosen, running or waiting under enhanced mode
  const refresh = () => EnhancedService.Partial().then(setPartial);
  // the switches move when clicked, not when the core answers (a jump, not a slide): enhanced mode
  // holds the new position until the mode changes, or drops it if the switch failed
  const [full, setFull] = useState<boolean | null>(null);
  useEffect(() => { refresh(); setFull(null); }, [mode]);
  const load = useCallback(() => SettingsService.ProcessChoices().then((c) => setChoices(c ?? [])), []);
  usePoll(load, 5000, status.running && partial);

  const run = (key: string, p: Promise<unknown>) => {
    setBusy(key);
    p.finally(() => { setBusy(""); refresh(); });
  };
  const picked = processes ?? [];
  const has = (exec: string) => picked.some((p) => p.toLowerCase() === exec.toLowerCase());
  const toggle = (exec: string, on: boolean) => setProcesses(on ? [...picked, exec] : picked.filter((p) => p.toLowerCase() !== exec.toLowerCase()));
  const pick = () => SettingsService.PickApp().then((c) => { if (c.exec && !has(c.exec)) toggle(c.exec, true); });

  const known = new Map(choices.map((c) => [c.exec.toLowerCase(), c]));
  // the tools: the defaults plus whatever was picked by name, minus those running now, which show
  // in the tree under the app they were started from
  const toolNames = [...new Set([...defaultTools, ...picked].map((t) => t.toLowerCase()))].filter((t) => !known.has(t)).sort();
  const isTool = (exec: string) => toolNames.includes(exec.toLowerCase());
  const pickedTools = toolNames.filter(has);
  const setTools = (on: boolean) => setProcesses(on ? [...picked, ...toolNames.filter((t) => !has(t))] : picked.filter((p) => !isTool(p)));
  // the tree: apps and processes under no app on top, networked processes nested under the app
  // they were started from (claude under Ghostty), which is what capturing that app covers
  const kids = (exec: string) => choices.filter((c) => c.parent?.toLowerCase() === exec.toLowerCase());
  const rows = [
    ...picked.filter((p) => !isTool(p) && !known.get(p.toLowerCase())?.parent).map((exec) => known.get(exec.toLowerCase()) ?? { name: exec, exec, source: "" }),
    ...choices.filter((c) => !has(c.exec) && !c.parent),
  ];
  const q = query.toLowerCase();
  const matches = (r: ProcessChoice) => `${r.name} ${r.exec}`.toLowerCase().includes(q);
  const shown = rows.filter((r) => !q || matches(r) || kids(r.exec).some(matches));
  const showTools = !q || "shell tools".includes(q) || toolNames.some((t) => t.includes(q));
  const off = !status.running || !!busy || !rooted;
  const partialState = busy === "partial" ? "切换中…" : !rooted ? "需先在「通用」中授予 root 权限" : !partial ? "选中的应用及其子进程经 TUN 按规则分流，其他应用照常遵循系统代理" : mode === "tun" ? "增强模式已接管全部流量，关闭后恢复部分增强" : picked.length === 0 ? "尚未选择应用，暂不接管" : `已增强 ${picked.length} 个应用`;

  return (
    <>
      <div className="group-title">增强模式</div>
      <div className="plist">
        <div className="plist-row static">
          <div className="plist-main"><div className="plist-name">增强模式</div><div className="muted small">{busy === "enh" ? "切换中…" : !rooted ? "需先在「通用」中授予 root 权限" : mode === "tun" ? "TUN 已接管全部流量" : "以 TUN 接管全部流量，包括不遵循系统代理的应用"}</div></div>
          <Switch on={full ?? mode === "tun"} disabled={off} onChange={(v) => { setFull(v); run("enh", EnhancedService.SetFull(v).catch((e) => { setFull(null); throw e; })); }} />
        </div>
      </div>

      <div className="grouped"><div className="card settings-card">
        <div className="section-title">路由排除</div>
        <ListSetting title="排除网段" desc="目标为以下网段的流量不经 TUN 转发，由系统路由处理；适用于增强模式与部分增强。" path="tun.route-exclude-address" placeholder="如 192.168.0.0/16" fix={cidr} invalid="网段格式无效，如 192.168.0.0/16 或 10.1.2.3" />
      </div></div>

      <div className="group-title">部分增强</div>
      <div className="plist">
        <div className="plist-row static">
          <div className="plist-main"><div className="plist-name">部分增强</div><div className="muted small">{partialState}</div></div>
          <Switch on={partial} disabled={off} onChange={(v) => { setPartial(v); run("partial", EnhancedService.SetPartial(v)); }} />
        </div>
      </div>

      {partial && <>
        <div className="plist tree" style={{ marginTop: 12 }}>
          <div className="plist-tools">
            <input className="dash-search" placeholder="搜索" value={query} onChange={(e) => setQuery(e.target.value)} />
            <span className="grow" />
            <button className="btn" onClick={pick}>{words.pickApp}</button>
            <button className="btn" onClick={load}>刷新</button>
          </div>
          {showTools && <details className="plist-group">
            <summary className="plist-row static">
              <div className="plist-main"><div className="plist-name">Shell Tools</div></div>
              <Switch on={pickedTools.length > 0} onChange={setTools} />
            </summary>
            {toolNames.map((t) => (
              <div key={t} className="plist-row static sub">
                <div className="plist-main"><div className="plist-name">{t}</div></div>
                <Switch on={has(t)} onChange={(v) => toggle(t, v)} />
              </div>
            ))}
            <form className="plist-row static sub" onSubmit={(e) => { e.preventDefault(); const t = newTool.trim(); if (!has(t)) toggle(t, true); setNewTool(""); }}>
              <input className="grow" placeholder="添加命令名，如 codex、uv；也可直接勾选终端应用接管其中启动的一切" value={newTool} onChange={(e) => setNewTool(e.target.value)} />
              <button className="btn" type="submit" disabled={!newTool.trim()}>添加</button>
            </form>
          </details>}
          {shown.length === 0 && !showTools && <div className="plist-empty muted">没有找到应用或进程</div>}
          {shown.map((r) => {
            const ks = kids(r.exec);
            const row = <>
              <div className="plist-main">
                <div className="plist-name">{r.name}</div>
                <div className="muted small">{r.exec}</div>
              </div>
              <span className="muted small">{sources[r.source] ?? "未运行"}</span>
              <Switch on={has(r.exec)} onChange={(v) => toggle(r.exec, v)} />
            </>;
            if (ks.length === 0) return <div key={r.exec} className="plist-row static">{row}</div>;
            return (
              <details key={r.exec} className="plist-group">
                <summary className="plist-row static">{row}</summary>
                {ks.map((k) => (
                  <div key={k.exec} className="plist-row static sub">
                    <div className="plist-main"><div className="plist-name">{k.name}</div></div>
                    <span className="muted small">{has(r.exec) ? `随 ${r.name} 接管` : "有网络连接"}</span>
                    <Switch on={has(k.exec) || has(r.exec)} disabled={has(r.exec)} onChange={(v) => toggle(k.exec, v)} />
                  </div>
                ))}
              </details>
            );
          })}
        </div>
      </>}
    </>
  );
}
