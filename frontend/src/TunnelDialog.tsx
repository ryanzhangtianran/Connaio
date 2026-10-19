import { validPort } from "./format";
import { useEffect, useState } from "react";
import PasswordInput from "./PasswordInput";
import { CoreService, ProfileService, ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { localRule } from "./Tunnels";
import { on, useDirty } from "./hooks";

type Row = Record<string, string>;

function Rows({ cols, rows, setRows }: { cols: [string, string][]; rows: Row[]; setRows: (r: Row[]) => void }) {
  const update = (i: number, k: string, value: string) => setRows(rows.map((r, j) => (j === i ? { ...r, [k]: value } : r)));
  return (
    <table className="table">
      <thead><tr>{cols.map(([k, l]) => <th key={k}>{l}</th>)}<th /></tr></thead>
      <tbody>
        {rows.map((r, i) => (
          <tr key={i}>
            {cols.map(([k]) => (
              <td key={k}>
                {k === "type"
                  ? <select value={r[k] ?? "tcp"} onChange={(e) => update(i, k, e.target.value)}>{["tcp", "udp", "http", "https", "stcp"].map((t) => <option key={t}>{t}</option>)}</select>
                  : <input value={r[k] ?? ""} onChange={(e) => update(i, k, e.target.value)} style={{ width: "100%" }} />}
              </td>
            ))}
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

type Auth = "password" | "key";
const AuthSeg = ({ auth, setAuth }: { auth: Auth; setAuth: (a: Auth) => void }) => (
  <div className="seg">
    <button className={auth === "password" ? "on" : ""} onClick={() => setAuth("password")}>密码</button>
    <button className={auth === "key" ? "on" : ""} onClick={() => setAuth("key")}>私钥</button>
  </div>
);

// DeployFrps logs in to a server over SSH and installs frps there, then hands back its address,
// port and token for the tunnel form.
function DeployFrps({ onDone, onClose }: { onDone: (host: string, port: number, token: string) => void; onClose: () => void }) {
  const [d, setD] = useState<Record<string, string>>({ port: "22", user: "root", bindPort: "7000" });
  const [auth, setAuth] = useState<Auth>("password");
  const [log, setLog] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  useEffect(() => on("frps-deploy", (d) => setLog((l) => [...l, d])), []);
  const f = input(d, setD);
  const deploy = () => {
    setBusy(true); setLog([]);
    ToolService.DeployFRPS({ host: d.host, port: Number(d.port) || 22, user: d.user, password: auth === "password" ? d.password : "", key: auth === "key" ? d.key : "", bindPort: Number(d.bindPort) || 7000 })
      .then((r) => onDone(d.host, r.bindPort, r.token)).finally(() => setBusy(false));
  };
  return (
    <div className="modal-bg" onClick={busy ? undefined : onClose}>
      <div className="sheet diag-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="sheet-head"><div className="sheet-title">通过 SSH 部署 frps</div><div className="muted small">在 Linux 服务器上安装最新版 frps 并设为开机启动，需要 root 或免密 sudo。</div></div>
        <div className="sheet-body">
          <div className="form-grid" style={{ marginTop: 12 }}>
            <label>服务器</label><div className="inline">{f("host", "vps.example.com")} : <span style={{ width: 70 }}>{f("port", "22")}</span></div>
            <label>用户名</label>{f("user", "root")}
            <label>认证方式</label>
            <AuthSeg auth={auth} setAuth={setAuth} />
            {auth === "password"
              ? <><label>密码</label>{f("password", "", "password")}</>
              : <><label>私钥</label><textarea className="code" rows={3} placeholder="粘贴私钥内容" value={d.key ?? ""} onChange={(e) => setD({ ...d, key: e.target.value })} /></>}
            <label>frps 端口</label>{f("bindPort", "7000")}
          </div>
          {log.length > 0 && (
            <div className="diag deploy-log">
              {log.map((l, i) => <div key={i}>{l}</div>)}
            </div>
          )}
        </div>
        <div className="sheet-foot">
          {busy && <span className="spinner" />}
          <span className="grow" />
          <button className="btn" disabled={busy} onClick={onClose}>取消</button>
          <button className="btn primary" disabled={busy || !d.host || !d.user} onClick={deploy}>{busy ? "部署中" : "部署"}</button>
        </div>
      </div>
    </div>
  );
}

const str = (x: unknown) => (x == null ? "" : String(x));

// formOf fills the form from a tunnel as build writes it, for editing.
function formOf(t: Record<string, any>): { v: Record<string, string>; frpRows: Row[]; sshRows: Row[] } {
  const c = t.config ?? {};
  return {
    v: t.type === "frp"
      ? { name: t.name, dialer: t["dialer-proxy"], serverAddr: c.serverAddr, serverPort: str(c.serverPort), token: c.auth?.token, protocol: c.transport?.protocol ?? "tcp" }
      : { name: t.name, dialer: t["dialer-proxy"], server: t.server, port: str(t.port), username: t.username, password: t.password, key: t["private-key"], hostKey: t["host-key"]?.[0] },
    frpRows: (c.proxies ?? []).map((p: Record<string, any>) => ({ name: p.name, type: p.type, localIP: p.localIP, localPort: str(p.localPort), remotePort: str(p.remotePort), customDomains: (p.customDomains ?? []).join(", ") })),
    sshRows: t.forwards ?? [],
  };
}

// TunnelDialog adds a tunnel, or with editing set replaces that one (its type stays).
export default function TunnelDialog({ editing, onClose }: { editing?: Record<string, any>; onClose: () => void }) {
  const init = editing && formOf(editing);
  const [kind, setKind] = useState<"frp" | "ssh">(editing?.type ?? "frp");
  const [v, setV] = useState<Record<string, string>>(init?.v ?? { serverPort: "7000", port: "22", protocol: "tcp" });
  const [frpRows, setFrpRows] = useState<Row[]>(init?.frpRows ?? [{ name: "web", type: "tcp", localIP: "127.0.0.1", localPort: "8080", remotePort: "18080" }]);
  const [sshRows, setSshRows] = useState<Row[]>(init?.sshRows ?? [{ remote: "127.0.0.1:8080", local: "127.0.0.1:80" }]);
  const [names, setNames] = useState<string[]>([]);
  const [auth, setAuth] = useState<Auth>(editing?.["private-key"] ? "key" : "password");
  const [deploying, setDeploying] = useState(false);
  const [busy, setBusy] = useState(false);
  // installing: the one-off server password for putting the key's public half on the server
  const [installing, setInstalling] = useState<string | null>(null);
  const [keyNote, setKeyNote] = useState("");
  const dirty = useDirty([v, frpRows, sshRows, kind, auth]);
  const [mixedPort, setMixedPort] = useState(0);
  useEffect(() => { ProfileService.Value("mixed-port").then((p) => setMixedPort(Number(p) || 0)); }, []);
  // viaRules: a forward from the server to Connaio's proxy port, so devices using it as a proxy
  // through the tunnel get their traffic routed by the rules
  const proxyTarget = `127.0.0.1:${mixedPort}`;
  const viaRules = kind === "frp" ? frpRows.some((r) => r.name === "connaio") : sshRows.some((r) => r.local === proxyTarget);
  const setViaRules = (on: boolean) => {
    if (kind === "frp") setFrpRows(on ? [...frpRows, { name: "connaio", type: "tcp", localIP: "127.0.0.1", localPort: String(mixedPort), remotePort: "17890" }] : frpRows.filter((r) => r.name !== "connaio"));
    else setSshRows(on ? [...sshRows, { remote: "127.0.0.1:17890", local: proxyTarget }] : sshRows.filter((r) => r.local !== proxyTarget));
  };
  useEffect(() => { CoreService.ProxyNames().then((n) => setNames((n ?? []).filter((x) => x !== "DIRECT"))); }, []); // DIRECT is 直连
  const genKey = () => ToolService.KeyPair(v.name).then((k) => { setV((x) => ({ ...x, key: k.private })); setKeyNote(`已保存在 ${k.path}`); });
  const installKey = () => {
    setBusy(true);
    ToolService.InstallKey({ host: v.server, port: num(v.port) ?? 22, user: v.username, password: installing ?? "", hostKey: v.hostKey ?? "" }, v.key)
      .then(() => { setInstalling(null); setKeyNote(`公钥已加入 ${v.username}@${v.server} 的 ~/.ssh/authorized_keys`); }).finally(() => setBusy(false));
  };
  const field = input(v, setV);

  const build = (): Record<string, any> => {
    const base = clean({ name: v.name, type: kind, "dialer-proxy": v.dialer });
    if (kind === "frp") {
      return {
        ...base,
        config: clean({
          serverAddr: v.serverAddr,
          serverPort: num(v.serverPort),
          auth: v.token ? { token: v.token } : undefined,
          transport: v.protocol !== "tcp" ? { protocol: v.protocol } : undefined,
          proxies: frpRows.map((r) => clean({
            name: r.name, type: r.type || "tcp", localIP: r.localIP, localPort: num(r.localPort),
            remotePort: num(r.remotePort), customDomains: r.customDomains ? r.customDomains.split(",").map((d) => d.trim()) : undefined,
          })),
        }),
      };
    }
    return { ...base, ...clean({ server: v.server, port: num(v.port), username: v.username, password: auth === "password" ? v.password : undefined, "private-key": auth === "key" ? v.key : undefined, "host-key": v.hostKey ? [v.hostKey] : undefined }), forwards: sshRows };
  };

  const save = () => {
    setBusy(true);
    (editing
      ? ProfileService.EditTunnel(editing.name, build()).then(() => ProfileService.RemoveRule(localRule(editing.name)))
      : ProfileService.AddTunnel(build()))
      .then(() => (viaRules ? ProfileService.AddRule(localRule(v.name)) : undefined)).then(onClose).finally(() => setBusy(false));
  };
  // every port the form sends must be a real one; a blank optional port is fine
  const ports = kind === "frp"
    ? [v.serverPort, ...frpRows.flatMap((r) => [r.localPort, r.remotePort])]
    : [v.port];
  const ready = validName(v.name ?? "") && ports.every((p) => !p || validPort(p)) && (kind === "frp" ? !!v.serverAddr : !!v.server && !!v.username);

  return (
    <div className="modal-bg" onClick={dirty ? undefined : onClose}>
      <div className="modal card wide" onClick={(e) => e.stopPropagation()}>
        <h2 className="modal-title">{editing ? `编辑 ${editing.name}` : "增加隧道"}</h2>
        {!editing && <div className="seg" style={{ marginBottom: 8 }}>
          <button className={kind === "frp" ? "on" : ""} onClick={() => setKind("frp")}>FRP</button>
          <button className={kind === "ssh" ? "on" : ""} onClick={() => setKind("ssh")}>SSH</button>
        </div>}
        <div className="form-grid">
          <label>名称</label>{field("name", v.name && !validName(v.name) ? "只能用字母、数字、. _ -" : "必填")}
          {kind === "frp" ? (
            <>
              <label>frps 地址</label>
              <div className="inline">{field("serverAddr", "frps.example.com")} : <span style={{ width: 90 }}>{field("serverPort", "7000")}</span>
                <button className="btn small-btn" onClick={() => setDeploying(true)}>在服务器上部署</button></div>
              <label>Token</label>{field("token", "auth.token，可选", "password")}
              <label>传输协议</label>
              <select value={v.protocol} onChange={(e) => setV({ ...v, protocol: e.target.value })}>{["tcp", "websocket", "wss", "quic", "kcp"].map((p) => <option key={p}>{p}</option>)}</select>
            </>
          ) : (
            <>
              <label>SSH 服务器</label><div className="inline">{field("server", "vps.example.com")} : <span style={{ width: 90 }}>{field("port", "22")}</span></div>
              <label>用户名</label>{field("username", "必填")}
              <label>认证方式</label>
              <AuthSeg auth={auth} setAuth={setAuth} />
              {auth === "password"
                ? <><label>密码</label>{field("password", "", "password")}</>
                : <>
                  <label>私钥</label>
                  <div>
                    <textarea className="code" rows={3} placeholder="粘贴私钥内容，或生成一对" value={v.key ?? ""} onChange={(e) => setV({ ...v, key: e.target.value })} style={{ width: "100%" }} />
                    <div className="inline">
                      <button className="btn small-btn" disabled={!validName(v.name ?? "")} title="生成 ed25519 密钥对，保存在 ~/.connaio；已有则沿用" onClick={genKey}>生成密钥对</button>
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
                </>}
              <label>服务器公钥</label>
              <div className="inline">{field("hostKey", "建议填写")}
                <button className="btn small-btn" disabled={!v.server} onClick={() => ToolService.HostKey(v.server, num(v.port) ?? 22).then((k) => setV({ ...v, hostKey: k }))}>获取</button></div>
            </>
          )}
          <label>跳板代理</label>
          <select value={v.dialer ?? ""} onChange={(e) => setV({ ...v, dialer: e.target.value })}>
            <option value="">直连</option>
            {names.map((n) => <option key={n}>{n}</option>)}
          </select>
          <label>入站流量</label>
          <label className="radio" style={{ textAlign: "left", color: "var(--text)" }}>
            <input type="checkbox" checked={viaRules} disabled={!mixedPort} onChange={(e) => setViaRules(e.target.checked)} /> 按规则分流
            <span className="muted small" style={{ marginLeft: 8 }}>服务器 17890 端口作为 HTTP / SOCKS5 代理，接入 Connaio</span>
          </label>
        </div>
        <div className="section-title">{kind === "frp" ? "FRP 代理" : "转发（远端监听 → 本地目标）"}</div>
        {kind === "frp"
          ? <Rows cols={[["name", "名称"], ["type", "类型"], ["localIP", "本地 IP"], ["localPort", "本地端口"], ["remotePort", "远端端口"], ["customDomains", "域名（http/https）"]]} rows={frpRows} setRows={setFrpRows} />
          : <Rows cols={[["remote", "远端监听"], ["local", "本地目标"]]} rows={sshRows} setRows={setSshRows} />}
        <button className="btn" style={{ marginTop: 8 }} onClick={() => (kind === "frp" ? setFrpRows([...frpRows, { type: "tcp", localIP: "127.0.0.1" }]) : setSshRows([...sshRows, { remote: "127.0.0.1:" }]))}>{kind === "frp" ? "添加代理" : "添加转发"}</button>
        <div className="actions end">
          <button className="btn" onClick={onClose}>取消</button>
          <button className="btn primary" disabled={!ready || busy} onClick={save}>完成</button>
        </div>
      </div>
      {deploying && <div onClick={(e) => e.stopPropagation()}>{/* clicks in it must not close this dialog */}
        <DeployFrps onClose={() => setDeploying(false)}
          onDone={(host, port, token) => { setV((x) => ({ ...x, serverAddr: host, serverPort: String(port), token })); setDeploying(false); }} />
      </div>}
    </div>
  );
}
