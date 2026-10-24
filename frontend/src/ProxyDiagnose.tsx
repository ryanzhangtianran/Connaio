import { useState } from "react";
import { ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import Dialog from "./Dialog";
import { errText } from "./format";

type Section = { title: string; lines: string[] | null; ok: boolean };

// DiagLog lists diagnosis sections, a failed one's title in red.
export const DiagLog = ({ sections }: { sections: Section[] }) => sections.map((s, i) => (
  <div key={i} className="diag-section">
    <div className={`diag-title ${s.ok ? "" : "bad"}`}>{s.title}</div>
    {(s.lines ?? []).map((l, j) => <div key={j}>{l}</div>)}
  </div>
));
const mbps = (s: { mbps: number; bytes: number; seconds: number }) => `${s.mbps.toFixed(1)} Mbps（${(s.bytes / 1e6).toFixed(1)} MB / ${s.seconds.toFixed(1)} 秒）`;

// ProxyDiagnose runs Surge's proxy diagnostics on one policy: TCP and UDP relay, UDP NAT type, and
// download and upload speed, one test at a time, each result added as it finishes.
export default function ProxyDiagnose({ nodes, groups, initial, onClose }: { nodes: string[]; groups: string[]; initial: string; onClose: () => void }) {
  const [policy, setPolicy] = useState(initial);
  const [log, setLog] = useState<Section[]>([]);
  const [busy, setBusy] = useState("");
  const tests: [string, () => Promise<string[]>][] = [
    ["TCP 转发", () => ToolService.TCPRelay(policy).then((l) => [l])],
    ["UDP 转发", () => ToolService.UDPRelay(policy).then((l) => [l])],
    ["NAT 类型", () => ToolService.NATType(policy).then((l) => l ?? [])],
    ["下载速度", () => ToolService.SpeedTest(policy).then((s) => [`下载：${mbps(s)}`])],
    ["上传速度", () => ToolService.UploadTest(policy).then((s) => [`上传：${mbps(s)}`])],
  ];
  const run = (title: string, test: () => Promise<string[]>) => {
    setBusy(title);
    test().then((lines) => ({ title: `${title} · ${policy}`, lines, ok: true }), (e) => ({ title: `${title} · ${policy}`, lines: [errText(e, "失败")], ok: false }))
      .then((s) => setLog((l) => [...l, s])).finally(() => setBusy(""));
  };
  return (
    <Dialog title="代理诊断" className="diag-sheet" bodyClass="diag diag-log" locked={!!busy} onClose={onClose} bar={
      <div className="diag-controls">
        <div className="inline">
          <span>策略</span>
          <select value={policy} onChange={(e) => setPolicy(e.target.value)}>
            <optgroup label="节点">{nodes.map((p) => <option key={p}>{p}</option>)}</optgroup>
            {groups.length > 0 && <optgroup label="策略组（测试当前选中的节点）">{groups.map((p) => <option key={p}>{p}</option>)}</optgroup>}
          </select>
        </div>
        <div className="diag-tests">
          {tests.map(([title, test]) => (
            <button key={title} className={`btn ${busy === title ? "running" : ""}`} disabled={!!busy} onClick={() => run(title, test)}>{title}</button>
          ))}
        </div>
      </div>
    } foot={<>
      {busy && <span className="spinner" />}
      <span className="grow" />
      <button className="btn" disabled={!log.length} onClick={() => setLog([])}>清除</button>
      <button className="btn primary" disabled={!!busy} title={busy ? "等测试结束" : ""} onClick={onClose}>完成</button>
    </>}>
      {log.length === 0 && !busy && <div className="muted">选择一项测试开始。</div>}
      <DiagLog sections={log} />
    </Dialog>
  );
}
