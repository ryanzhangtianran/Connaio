import { errText, plain, validPort } from "./format";
import { useEffect, useState } from "react";
import PasswordInput from "./PasswordInput";
import { CoreService, ProfileService, ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { localRule, useMixedPort } from "./Tunnels";
import { useDirty } from "./hooks";
import { Switch } from "./Settings";

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
      <thead><tr>{cols.flatMap(([k, l]) => (addr.includes(k) ? [<th key={k}>{l} IP</th>, <th key={k + ":port"} style={{ width: 110 }}>{l}端口</th>] : [<th key={k}>{l}</th>]))}<th style={{ width: 40 }} /></tr></thead>
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

const num = (s: string) => (s && !isNaN(Number(s)) ? Number(s) : undefined);
const validName = (s: string) => /^[\w.-]+$/.test(s); // goes into an IN-NAME rule, so no commas or spaces
// input is a text field bound to key k of a form state; secret ("password") shows it through PasswordInput.
const input = (s: Record<string, string>, set: (v: Record<string, string>) => void) => (k: string, placeholder = "", secret = "") => {
  const props = { placeholder, value: s[k] ?? "", onChange: (e: React.ChangeEvent<HTMLInputElement>) => set({ ...s, [k]: e.target.value }) };
  return secret ? <PasswordInput {...props} /> : <input {...props} />;
};
const clean = (o: Record<string, any>) => Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== ""));

const str = (x: unknown) => (x == null ? "" : String(x));
// randomPassword is 18 random bytes as base64url: 24 characters, 144 bits, no bias
const randomPassword = () => btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(18)))).replace(/\+/g, "-").replace(/\//g, "_");
const validIP = (s = "") => /^(\d{1,3}\.){3}\d{1,3}$/.test(s) && s.split(".").every((n) => Number(n) <= 255);

// a forward's remote is where connaio-relay listens on the server: on every address, for the
// public; the proxy port's on the server's own loopback only, not to be an open proxy
const publicHost = "0.0.0.0";
const serverOnly = "127.0.0.1";

// formOf fills the form from a tunnel as build writes it, for editing.
function formOf(t: Record<string, any>): { v: Record<string, string>; rows: Row[] } {
  return {
    v: { name: t.name, dialer: t["dialer-proxy"], server: t.server, port: str(t.port), ip: t.ip, password: t.password },
    rows: t.forwards ?? [],
  };
}

// TunnelDialog adds a WireGuard tunnel, or with editing set replaces that one.
export default function TunnelDialog({ editing, onClose }: { editing?: Record<string, any>; onClose: () => void }) {
  const init = editing && formOf(editing);
  const [v, setV] = useState<Record<string, string>>(init?.v ?? { port: "51820" });
  const pwOK = (v.password ?? "").length >= 8;
  const [rows, setRows] = useState<Row[]>(init?.rows ?? []);
  const [names, setNames] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  // login: the one-off SSH login that sets connaio-relay up on the server (ToolService.InstallRelay), as
  // an ordinary user; its password isn't kept. admin: also, once, BBR and the firewall's ports as root.
  // fingerprint: the server's key, shown and confirmed before the password goes out; an edit of the
  // login or the server asks for it again.
  const [login, setLogin] = useState<Record<string, string> | null>(null);
  const [admin, setAdmin] = useState(false);
  const [fingerprint, setFingerprint] = useState("");
  useEffect(() => setFingerprint(""), [v.server, login?.port]);
  const [installed, setInstalled] = useState<{ ok: boolean; text: string } | null>(null);
  const fail = (e: unknown) => setInstalled({ ok: false, text: errText(e) });
  const stopInstall = () => { setLogin(null); setFingerprint(""); };
  const askFingerprint = () => {
    setBusy(true);
    setInstalled(null);
    ToolService.HostFingerprint(v.server, num(login!.port) ?? 22).then(setFingerprint, fail).finally(() => setBusy(false));
  };
  // install saves the tunnel first: the server gets what Connaio has, never a draft the dialog may yet drop
  const install = () => {
    setBusy(true);
    setInstalled(null);
    persist().then(() => ToolService.InstallRelay({
      login: { host: v.server, port: num(login!.port) ?? 22, user: login!.user, password: login!.password }, hostKey: fingerprint,
      tunnel: v.name, password: v.password, peer: v.ip, port: num(v.port) ?? 51820, forwards: rows.map((r) => r.remote), admin,
    })).then((m) => { setInstalled({ ok: true, text: m }); stopInstall(); }, fail).finally(() => setBusy(false));
  };
  const dirty = useDirty([v, rows]);
  const mixedPort = useMixedPort();
  // viaRules: a forward from the server to Connaio's proxy port, so devices using it as a proxy
  // through the tunnel get their traffic routed by the rules
  const proxyTarget = `127.0.0.1:${mixedPort}`;
  const viaRules = rows.some((r) => r.local === proxyTarget);
  const setViaRules = (on: boolean) => setRows(on ? [...rows, { remote: `${serverOnly}:`, local: proxyTarget }] : rows.filter((r) => r.local !== proxyTarget));
  const viaPort = splitAddr(rows.find((r) => r.local === proxyTarget)?.remote)[1];
  const lockedCell = (r: Row, k: string) => r.local === proxyTarget && k === "local";
  // the jump proxy: DIRECT (none) or a policy group, not a single node
  useEffect(() => { CoreService.Groups().then((g) => setNames((g ?? []).map((x) => x.name).filter((n) => n !== "GLOBAL"))); }, []);
  const field = input(v, setV);

  const build = (): Record<string, any> => ({
    ...clean({
      name: v.name, type: "wireguard", "dialer-proxy": v.dialer, disabled: editing?.disabled || undefined, // editing keeps a stopped tunnel stopped
      server: v.server, port: num(v.port), ip: v.ip, password: v.password,
    }),
    forwards: rows,
  });

  // saved: the tunnel's name in the config once there (editing's, or a new one's after install saved
  // it), so saving again edits it
  const [saved, setSaved] = useState<string | undefined>(editing?.name);
  const persist = () =>
    (saved ? ProfileService.EditTunnel(saved, build()) : ProfileService.AddTunnel(build()))
      // the name's rule goes first: an edit's, or one an old SSH tunnel left that this one took the name of
      .then(() => ProfileService.RemoveRule(localRule(saved ?? v.name)))
      .then(() => (viaRules ? ProfileService.AddRule(localRule(v.name)) : undefined)).then(() => setSaved(v.name));
  const save = () => {
    setBusy(true);
    persist().then(onClose).finally(() => setBusy(false));
  };
  // every port the form sends must be a real one; a blank optional port is fine
  const ports = [v.port, ...rows.flatMap((r) => [splitAddr(r.remote)[1], splitAddr(r.local)[1]])];
  const filled = validName(v.name ?? "") && ports.every((p) => !p || validPort(p)) && !!v.server && validIP(v.ip) && pwOK
    && rows.length > 0 // the core takes no tunnel without a forward
    && (!viaRules || validPort(viaPort)); // the via-rules forward's port has no default

  return (
    <div className="modal-bg" onClick={dirty ? undefined : onClose}>
      <div className="modal card wide" onClick={(e) => e.stopPropagation()}>
        <h2 className="modal-title">{editing ? `编辑 ${editing.name}` : "增加隧道"}</h2>
        <div className="form-grid">
          <label>名称</label>{field("name", v.name && !validName(v.name) ? "只能用字母、数字、. _ -" : "必填")}
          <label>服务器</label>
          <div className="inline">{field("server", "vps.example.com")} : <span style={{ width: 90, flex: "none" }}>{field("port", "51820")}</span><span className="muted small" title="服务器程序接收 WireGuard 的端口，与 SSH 端口无关">UDP</span></div>
          <label>本机隧道地址</label>{field("ip", v.ip && !validIP(v.ip) ? "须为 IPv4 地址" : "10.8.0.2")}
          <label>密码</label>
          <div className="inline">
            <span className="grow">{field("password", v.password && !pwOK ? "至少 8 位" : "至少 8 位，两端密钥由此生成", "password")}</span>
            {/* a new password means new keys: the server needs installing again */}
            <button className="btn small-btn" onClick={() => setV({ ...v, password: randomPassword() })}>随机生成</button>
          </div>
          <label>跳板代理</label>
          <select value={v.dialer ?? ""} onChange={(e) => setV({ ...v, dialer: e.target.value })}>
            <option value="">DIRECT</option>
            {v.dialer && !names.includes(v.dialer) && <option>{v.dialer}</option>}{/* a node set before, kept until another is picked */}
            {names.map((n) => <option key={n}>{n}</option>)}
          </select>
        </div>

        <div className="section-title">转发 <span className="section-note">服务器监听 → 本地目标，仅 TCP</span></div>
        <div className="setting-row option-row">
          <div>
            <div>按规则分流</div>
            <div className="muted small">{viaRules ? `服务器本机 ${viaPort || "（在下方填写）"} 端口作为 HTTP / SOCKS5 代理，接入 Connaio 的 ${mixedPort} 端口` : "服务器本机的一个端口作为 HTTP / SOCKS5 代理，接入 Connaio"}</div>
          </div>
          <Switch on={viaRules} disabled={!mixedPort} onChange={setViaRules} />
        </div>
        <Rows cols={[["remote", "服务器"], ["local", "本地"]]} rows={rows} setRows={setRows} addr={["remote", "local"]} locked={lockedCell} />
        <button className="btn small-btn" style={{ marginTop: 8 }} onClick={() => setRows([...rows, { remote: `${publicHost}:` }])}>添加转发</button>

        <div className="section-title">服务器程序</div>
        <div className="install-box">
          {!login && <>
            <div className="install-head">
              <span className="muted small">经 SSH 登录一次，以登录用户安装并启动，无需 root，密码不保存。修改密码、端口或转发后需重新安装。</span>
              <button className="btn small-btn" disabled={busy || !filled} title="先保存此隧道，再安装到服务器" onClick={() => { setInstalled(null); setLogin({ user: "", port: "22", password: "" }); }}>安装到服务器</button>
            </div>
            {installed && <div className={`small install-result ${installed.ok ? "delay-good" : "error"}`}>{installed.text}</div>}
          </>}
          {login && !fingerprint && <>
            <div className="form-grid">
              <label>SSH 用户</label><input {...plain} value={login.user} placeholder="建议使用普通用户" onChange={(e) => setLogin({ ...login, user: e.target.value })} />
              <label>SSH 端口</label><input value={login.port} onChange={(e) => setLogin({ ...login, port: e.target.value })} style={{ width: 90 }} />
              <label>登录密码</label><PasswordInput value={login.password} placeholder="仅用于这次安装，不保存" onChange={(e) => setLogin({ ...login, password: e.target.value })} />
              {installed && !installed.ok && <><span /><div className="small error">{installed.text}</div></>}
            </div>
            <div className="setting-row option-row">
              <div>
                <div>同时开启 BBR 并放行防火墙端口</div>
                <div className="muted small">
                  {admin ? "需 sudo 或 root：仅写入 BBR 设置，并在运行中的 ufw 或 firewalld 中放行本隧道的端口，不保留任何权限。" : "以管理员权限执行一次，提升弱网下的速度。"}
                  {login.user === "root" && " 以 root 登录时，服务器程序也将以 root 运行。"}
                </div>
              </div>
              <Switch on={admin} onChange={setAdmin} />
            </div>
            <div className="actions end">
              <button className="btn small-btn" onClick={stopInstall}>返回</button>
              <button className="btn small-btn primary" disabled={busy || !login.user || !login.password || !validPort(login.port)} onClick={askFingerprint}>下一步</button>
            </div>
          </>}
          {login && fingerprint && <>
            <div className="form-grid">
              <label>登录</label><span className="selectable">{login.user}@{v.server}:{login.port}{admin && <span className="muted">，同时开启 BBR 并放行端口</span>}</span>
              <label>服务器指纹</label><span className="fingerprint selectable">{fingerprint}</span>
              <span /><span className="muted small">请与服务器控制台中的指纹核对，一致后再安装；密码仅发送给此服务器。</span>
              {installed && !installed.ok && <><span /><div className="small error">{installed.text}</div></>}
            </div>
            <div className="actions end">
              <button className="btn small-btn" disabled={busy} onClick={() => { setFingerprint(""); setInstalled(null); }}>返回</button>
              <button className="btn small-btn primary" disabled={busy} onClick={install}>{busy ? "安装中…" : "确认并安装"}</button>
            </div>
          </>}
        </div>
        <div className="actions end">
          <button className="btn" onClick={onClose}>取消</button>
          <button className="btn primary" disabled={!filled || busy} onClick={save}>完成</button>
        </div>
      </div>
    </div>
  );
}
