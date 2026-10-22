import { errText, validPort } from "./format";
import { useEffect, useState } from "react";
import PasswordInput from "./PasswordInput";
import { CoreService, ProfileService, ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { KeyPair } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { localRule } from "./Tunnels";
import { useDirty } from "./hooks";

type Row = Record<string, string>;

// splitAddr is "host:port" at its last colon; joinAddr puts it back.
const splitAddr = (s = "") => { const i = s.lastIndexOf(":"); return i < 0 ? [s, ""] : [s.slice(0, i), s.slice(i + 1)]; };
const joinAddr = (host: string, port: string) => `${host}:${port}`;

// Rows edits a list of forwards: a column per key, or two for a "host:port" key in addr (its IP, its
// port); a cell locked for a row keeps what Connaio set there.
function Rows({ cols, rows, setRows, addr = [], locked }: { cols: [string, string][]; rows: Row[]; setRows: (r: Row[]) => void; addr?: string[]; locked?: (r: Row, k: string) => boolean }) {
  const update = (i: number, k: string, value: string) => setRows(rows.map((r, j) => (j === i ? { ...r, [k]: value } : r)));
  return (
    <table className="table">
      <thead><tr>{cols.flatMap(([k, l]) => (addr.includes(k) ? [<th key={k}>{l} IP</th>, <th key={k + ":port"}>{l}端口</th>] : [<th key={k}>{l}</th>]))}<th /></tr></thead>
      <tbody>
        {rows.map((r, i) => (
          <tr key={i}>
            {cols.flatMap(([k]) => {
              const fixed = !!locked?.(r, k);
              if (addr.includes(k)) {
                const [host, port] = splitAddr(r[k]);
                return [
                  <td key={k}><input value={host} readOnly={fixed} onChange={(e) => update(i, k, joinAddr(e.target.value, port))} style={{ width: "100%" }} /></td>,
                  <td key={k + ":port"}><input value={port} readOnly={fixed} placeholder="端口" onChange={(e) => update(i, k, joinAddr(host, e.target.value))} style={{ width: "100%" }} /></td>,
                ];
              }
              return [
                <td key={k}>
                  <input value={r[k] ?? ""} readOnly={fixed} onChange={(e) => update(i, k, e.target.value)} style={{ width: "100%" }} />
                </td>,
              ];
            })}
            <td><button className="btn" onClick={() => setRows(rows.filter((_, j) => j !== i))}>×</button></td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// KeySelect picks an SSH key from the pairs kept in ~/.ssh, or generates one named genName there:
// keys come only from there, so each one's public half is at hand to install on a server.
function KeySelect({ value, genName, onChange }: { value: string; genName: string; onChange: (key: string, note: string) => void }) {
  const [keys, setKeys] = useState<KeyPair[]>([]);
  const load = () => ToolService.KeyPairs().then((k) => setKeys(k ?? []));
  useEffect(() => { load(); }, []);
  const current = keys.find((k) => k.private === value);
  const other = value && !current; // a key the config has from before, kept until another is chosen
  return (
    <>
      <select value={current?.name ?? (other ? "\u0000" : "")} onChange={(e) => { const k = keys.find((x) => x.name === e.target.value); if (k) onChange(k.private, k.path); }}>
        <option value="" disabled>{keys.length ? "选择密钥" : "暂无密钥，请先生成"}</option>
        {other && <option value={"\u0000"}>配置中原有的密钥</option>}
        {keys.map((k) => <option key={k.name} value={k.name}>{k.name}</option>)}
      </select>
      <button className="btn small-btn" disabled={!validName(genName)} title={`生成 ed25519 密钥对 ~/.ssh/${genName.toLowerCase().replace(/-/g, "_") || "<名称>"}_id_ed25519；已有则沿用`}
        onClick={() => ToolService.KeyPair(genName).then((k) => { load(); onChange(k.private, `已保存在 ${k.path}`); })}>生成密钥对</button>
    </>
  );
}

const num = (s: string) => (s && !isNaN(Number(s)) ? Number(s) : undefined);
const validName = (s: string) => /^[\w.-]+$/.test(s); // goes into an IN-NAME rule, so no commas or spaces
// input is a text field bound to key k of a form state; secret ("password") shows it through PasswordInput.
const input = (s: Record<string, string>, set: (v: Record<string, string>) => void) => (k: string, placeholder = "", secret = "") => {
  const props = { placeholder, value: s[k] ?? "", onChange: (e: React.ChangeEvent<HTMLInputElement>) => set({ ...s, [k]: e.target.value }) };
  return secret ? <PasswordInput {...props} /> : <input {...props} />;
};
const clean = (o: Record<string, any>) => Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== ""));

const str = (x: unknown) => (x == null ? "" : String(x));

// formOf fills the form from a tunnel as build writes it, for editing.
function formOf(t: Record<string, any>): { v: Record<string, string>; sshRows: Row[] } {
  return {
    v: { name: t.name, dialer: t["dialer-proxy"], server: t.server, port: str(t.port), username: t.username, password: t.password, key: t["private-key"], hostKey: t["host-key"]?.[0] },
    sshRows: t.forwards ?? [],
  };
}

// TunnelDialog adds an SSH tunnel, or with editing set replaces that one.
export default function TunnelDialog({ editing, onClose }: { editing?: Record<string, any>; onClose: () => void }) {
  const init = editing && formOf(editing);
  const [v, setV] = useState<Record<string, string>>(init?.v ?? { port: "22" });
  const [sshRows, setSshRows] = useState<Row[]>(init?.sshRows ?? [{ remote: "127.0.0.1:8080", local: "127.0.0.1:80" }]);
  const [names, setNames] = useState<string[]>([]);
  // an SSH tunnel logs in with a key only: a password would sit in the config as plain text.
  // passwordOnly: a tunnel saved before, with a password and no key, until a key is chosen
  const passwordOnly = !!editing?.password && !editing?.["private-key"];
  const [busy, setBusy] = useState(false);
  // installing: the one-off server password for putting the key's public half on the server
  const [installing, setInstalling] = useState<string | null>(null);
  const [keyNote, setKeyNote] = useState("");
  const dirty = useDirty([v, sshRows]);
  const [mixedPort, setMixedPort] = useState(0);
  useEffect(() => { ProfileService.Value("mixed-port").then((p) => setMixedPort(Number(p) || 0)); }, []);
  // viaRules: a forward from the server to Connaio's proxy port, so devices using it as a proxy
  // through the tunnel get their traffic routed by the rules
  const proxyTarget = `127.0.0.1:${mixedPort}`;
  const viaRules = sshRows.some((r) => r.local === proxyTarget);
  const setViaRules = (on: boolean) => setSshRows(on ? [...sshRows, { remote: "127.0.0.1:", local: proxyTarget }] : sshRows.filter((r) => r.local !== proxyTarget));
  const viaPort = splitAddr(sshRows.find((r) => r.local === proxyTarget)?.remote)[1];
  const lockedCell = (r: Row, k: string) => r.local === proxyTarget && k === "local";
  // test: the server tried from here before saving (ToolService.TestTunnel)
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);
  const runTest = () => {
    setTest({ ok: true, text: "测试中…" });
    ToolService.TestTunnel({ host: v.server ?? "", port: num(v.port) ?? 22, user: v.username, password: "", key: v.key, hostKey: v.hostKey })
      .then((t) => setTest({ ok: true, text: t + (v.dialer ? "（直接连接测试，未经跳板代理）" : "") }))
      .catch((e) => setTest({ ok: false, text: errText(e) }));
  };
  useEffect(() => { CoreService.ProxyNames().then((n) => setNames((n ?? []).filter((x) => x !== "DIRECT"))); }, []); // DIRECT is 直连
  const installKey = () => {
    setBusy(true);
    ToolService.InstallKey({ host: v.server, port: num(v.port) ?? 22, user: v.username, password: installing ?? "", hostKey: v.hostKey ?? "" }, v.key)
      .then(() => { setInstalling(null); setKeyNote(`公钥已加入 ${v.username}@${v.server} 的 ~/.ssh/authorized_keys`); }).finally(() => setBusy(false));
  };
  const field = input(v, setV);

  const build = (): Record<string, any> => {
    const base = clean({ name: v.name, type: "ssh", "dialer-proxy": v.dialer, disabled: editing?.disabled || undefined }); // editing keeps a stopped tunnel stopped
    return { ...base, ...clean({ server: v.server, port: num(v.port), username: v.username, "private-key": v.key, "host-key": v.hostKey ? [v.hostKey] : undefined }), forwards: sshRows };
  };

  const save = () => {
    setBusy(true);
    (editing
      ? ProfileService.EditTunnel(editing.name, build()).then(() => ProfileService.RemoveRule(localRule(editing.name)))
      : ProfileService.AddTunnel(build()))
      .then(() => (viaRules ? ProfileService.AddRule(localRule(v.name)) : undefined)).then(onClose).finally(() => setBusy(false));
  };
  // every port the form sends must be a real one; a blank optional port is fine
  const ports = [v.port, ...sshRows.flatMap((r) => [splitAddr(r.remote)[1], splitAddr(r.local)[1]])];
  const ready = validName(v.name ?? "") && ports.every((p) => !p || validPort(p)) && !!v.server && !!v.username && !!v.key
    && (!viaRules || validPort(viaPort)); // the via-rules forward's server port has no default

  return (
    <div className="modal-bg" onClick={dirty ? undefined : onClose}>
      <div className="modal card wide" onClick={(e) => e.stopPropagation()}>
        <h2 className="modal-title">{editing ? `编辑 ${editing.name}` : "增加隧道"}</h2>
        <div className="form-grid">
          <label>名称</label>{field("name", v.name && !validName(v.name) ? "只能用字母、数字、. _ -" : "必填")}
            <label>SSH 服务器</label><div className="inline">{field("server", "vps.example.com")} : <span style={{ width: 90 }}>{field("port", "22")}</span></div>
            <label>用户名</label>{field("username", "必填")}
            {passwordOnly && <><span /><div className="delay-warn small">此隧道原以密码登录，密码以明文保存在配置中。请选择或生成私钥并安装到服务器，保存后原密码随之删除。</div></>}
            <label>私钥</label>
            <div>
              <div className="inline">
                <KeySelect value={v.key ?? ""} genName={v.name ?? ""} onChange={(key, note) => { setV((x) => ({ ...x, key })); setKeyNote(note); }} />
                <button className="btn small-btn" disabled={!v.key || !v.server || !v.username} title="用一次服务器密码把公钥加入 ~/.ssh/authorized_keys，密码不保存" onClick={() => setInstalling("")}>安装公钥到服务器</button>
                {keyNote && <span className="muted small">{keyNote}</span>}
              </div>
            </div>
            {installing !== null && <>
              <label>服务器密码</label>
              <div className="inline">
                <PasswordInput placeholder="仅用于这次安装" value={installing} onChange={(e) => setInstalling(e.target.value)} />
                <button className="btn small-btn" disabled={busy || !installing} onClick={installKey}>安装</button>
                <button className="btn small-btn" onClick={() => setInstalling(null)}>取消</button>
              </div>
            </>}
            <label>服务器公钥</label>
            <div className="inline">{field("hostKey", "建议填写")}
              <button className="btn small-btn" disabled={!v.server} onClick={() => ToolService.HostKey(v.server, num(v.port) ?? 22).then((k) => setV({ ...v, hostKey: k }))}>获取</button></div>
          <label>跳板代理</label>
          <select value={v.dialer ?? ""} onChange={(e) => setV({ ...v, dialer: e.target.value })}>
            <option value="">直连</option>
            {names.map((n) => <option key={n}>{n}</option>)}
          </select>
          <label>入站流量</label>
          <label className="radio" style={{ textAlign: "left", color: "var(--text)" }}>
            <input type="checkbox" checked={viaRules} disabled={!mixedPort} onChange={(e) => setViaRules(e.target.checked)} /> 按规则分流
            <span className="muted small" style={{ marginLeft: 8 }}>{viaRules ? `服务器 ${viaPort || "（在下方填写）"} 端口作为 HTTP / SOCKS5 代理，接入 Connaio 的 ${mixedPort} 端口` : "服务器上的一个端口作为 HTTP / SOCKS5 代理，接入 Connaio"}</span>
          </label>
        </div>
        <div className="section-title">转发（远端监听 → 本地目标）</div>
        <Rows cols={[["remote", "远端"], ["local", "本地"]]} rows={sshRows} setRows={setSshRows} addr={["remote", "local"]} locked={lockedCell} />
        <button className="btn" style={{ marginTop: 8 }} onClick={() => setSshRows([...sshRows, { remote: "127.0.0.1:" }])}>添加转发</button>
        <div className="actions end">
          <button className="btn" disabled={!(v.server && v.username)} onClick={runTest}>测试</button>
          <span className={`small tunnel-test ${test ? (test.ok ? "muted" : "error") : ""}`}>{test?.text}</span>
          <button className="btn" onClick={onClose}>取消</button>
          <button className="btn primary" disabled={!ready || busy} onClick={save}>完成</button>
        </div>
      </div>
    </div>
  );
}
