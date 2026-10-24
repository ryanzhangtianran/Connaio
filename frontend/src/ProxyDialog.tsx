import { errText, plain, validPort } from "./format";
import { useEffect, useState } from "react";
import { CoreService, ProfileService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { Switch } from "./Settings";
import PasswordInput from "./PasswordInput";
import { useDirty } from "./hooks";
import Dialog from "./Dialog";

type Field = {
  key: string; // mihomo key; dots nest: "ws-opts.path"
  label: string;
  kind?: "text" | "number" | "password" | "bool" | "select";
  options?: string[];
  placeholder?: string;
  when?: [string, string[]]; // show only when another field has one of these values
  def?: string | boolean;
};

const tlsFields: Field[] = [
  { key: "sni", label: "SNI", placeholder: "默认为服务器地址" },
  { key: "skip-cert-verify", label: "跳过证书验证", kind: "bool" },
];
const transport: Field[] = [
  { key: "network", label: "传输方式", kind: "select", options: ["tcp", "ws", "grpc", "h2", "http"], def: "tcp" },
  { key: "ws-opts.path", label: "WS 路径", placeholder: "/", when: ["network", ["ws"]] },
  { key: "ws-opts.headers.Host", label: "WS Host", when: ["network", ["ws"]] },
  { key: "grpc-opts.grpc-service-name", label: "gRPC 服务名", when: ["network", ["grpc"]] },
];
const ssCiphers = ["aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305", "none"];

export const protocols: Record<string, { label: string; fields: Field[] }> = {
  http: { label: "HTTP", fields: [{ key: "username", label: "用户名" }, { key: "password", label: "密码", kind: "password" }, { key: "tls", label: "TLS", kind: "bool" }, ...tlsFields] },
  socks5: { label: "SOCKS5", fields: [{ key: "username", label: "用户名" }, { key: "password", label: "密码", kind: "password" }, { key: "tls", label: "TLS", kind: "bool" }, { key: "skip-cert-verify", label: "跳过证书验证", kind: "bool" }] },
  ss: { label: "Shadowsocks", fields: [{ key: "cipher", label: "加密方式", kind: "select", options: ssCiphers, def: "aes-128-gcm" }, { key: "password", label: "密码", kind: "password" }] },
  vmess: { label: "VMess", fields: [{ key: "uuid", label: "UUID" }, { key: "alterId", label: "alterId", kind: "number", def: "0" }, { key: "cipher", label: "加密", kind: "select", options: ["auto", "aes-128-gcm", "chacha20-poly1305", "none"], def: "auto" }, { key: "tls", label: "TLS", kind: "bool" }, { key: "servername", label: "SNI" }, ...transport] },
  vless: { label: "VLESS", fields: [{ key: "uuid", label: "UUID" }, { key: "flow", label: "Flow", kind: "select", options: ["", "xtls-rprx-vision"], def: "" }, { key: "tls", label: "TLS", kind: "bool", def: true }, { key: "servername", label: "SNI" }, { key: "client-fingerprint", label: "指纹", kind: "select", options: ["", "chrome", "firefox", "safari", "ios", "random"], def: "" }, { key: "reality-opts.public-key", label: "Reality 公钥" }, { key: "reality-opts.short-id", label: "Reality short-id" }, ...transport] },
  trojan: { label: "Trojan", fields: [{ key: "password", label: "密码", kind: "password" }, ...tlsFields, ...transport] },
  hysteria2: { label: "Hysteria2", fields: [{ key: "password", label: "密码", kind: "password" }, ...tlsFields, { key: "up", label: "上行", placeholder: "如 50 Mbps" }, { key: "down", label: "下行", placeholder: "如 200 Mbps" }, { key: "obfs", label: "混淆", kind: "select", options: ["", "salamander"], def: "" }, { key: "obfs-password", label: "混淆密码", kind: "password", when: ["obfs", ["salamander"]] }] },
  tuic: { label: "TUIC", fields: [{ key: "uuid", label: "UUID" }, { key: "password", label: "密码", kind: "password" }, ...tlsFields, { key: "congestion-controller", label: "拥塞控制", kind: "select", options: ["bbr", "cubic", "new_reno"], def: "bbr" }] },
  anytls: { label: "AnyTLS", fields: [{ key: "password", label: "密码", kind: "password" }, ...tlsFields] },
  ssh: { label: "SSH", fields: [{ key: "username", label: "用户名" }, { key: "password", label: "密码", kind: "password" }, { key: "private-key", label: "私钥（内容）", placeholder: "-----BEGIN OPENSSH PRIVATE KEY-----…" }] },
  wireguard: { label: "WireGuard", fields: [{ key: "private-key", label: "私钥" }, { key: "public-key", label: "对端公钥" }, { key: "ip", label: "IPv4 地址", placeholder: "10.0.0.2" }, { key: "ipv6", label: "IPv6 地址" }, { key: "mtu", label: "MTU", kind: "number" }] },
};

// set writes value at a dotted key, creating nested maps.
function set(obj: Record<string, any>, key: string, value: unknown) {
  const parts = key.split(".");
  let cur = obj;
  for (const p of parts.slice(0, -1)) cur = cur[p] ??= {};
  cur[parts[parts.length - 1]] = value;
}

// get reads a dotted key, as set writes it.
const get = (obj: Record<string, any>, key: string) => key.split(".").reduce<any>((o, k) => o?.[k], obj);

// formOf fills the form from a proxy's fields, for editing (its type must be one of protocols).
function formOf(p: Record<string, any>): Record<string, any> {
  const v: Record<string, any> = { name: p.name, server: p.server, port: String(p.port ?? "") };
  for (const k of ["udp", "tfo", "ip-version", "interface-name", "dialer-proxy"]) if (p[k] !== undefined) v[k] = p[k];
  for (const f of protocols[p.type]?.fields ?? []) {
    const x = get(p, f.key);
    if (x !== undefined) v[f.key] = f.kind === "number" ? String(x) : x;
  }
  return v;
}

// ProxyDialog adds a proxy, or with editing set edits that one: its name and type stay, and only the
// fields changed in the form are sent (a cleared one as null), so the rest are kept and, for a
// subscription's proxy, keep following the subscription.
export default function ProxyDialog({ groups, editing, onClose }: { groups: string[]; editing?: Record<string, any>; onClose: () => void }) {
  const [type, setType] = useState(editing?.type ?? "ss");
  const [v, setV] = useState<Record<string, any>>(editing ? formOf(editing) : {});
  const [names, setNames] = useState<string[]>([]);
  const [join, setJoin] = useState<string[]>(groups.slice(0, 1));
  const [extra, setExtra] = useState("");
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const dirty = useDirty([v, type, extra, join]);
  useEffect(() => { CoreService.ProxyNames().then((n) => setNames((n ?? []).filter((x) => x !== "DIRECT"))); }, []); // DIRECT is 不使用

  const fields = protocols[type].fields;
  const val = (f: Field) => v[f.key] ?? f.def ?? (f.kind === "bool" ? false : "");
  const shown = (f: Field) => !f.when || f.when[1].includes(String(v[f.when[0]] ?? fields.find((x) => x.key === f.when![0])?.def ?? ""));

  const build = () => {
    const proxy: Record<string, any> = { name: v.name, type, server: v.server, port: Number(v.port) };
    for (const f of fields) {
      if (!shown(f)) continue;
      const x = val(f);
      if (x === "" || (f.key === "network" && x === "tcp")) continue;
      set(proxy, f.key, f.kind === "number" ? Number(x) : x);
    }
    for (const k of ["udp", "tfo"]) if (v[k]) proxy[k] = true;
    for (const k of ["ip-version", "interface-name", "dialer-proxy"]) if (v[k]) proxy[k] = v[k];
    return proxy;
  };

  // patch is what the form changed in editing: each field it manages, new value or null if cleared
  const patch = () => {
    const built = build(), p: Record<string, any> = {};
    for (const k of ["server", "port", "udp", "tfo", "ip-version", "interface-name", "dialer-proxy", ...fields.map((f) => f.key)]) {
      const a = get(editing!, k), b = get(built, k);
      if ((a || b) && String(a) !== String(b)) set(p, k, b ?? null);
    }
    return p;
  };
  const save = () => {
    setBusy(true);
    setError("");
    (editing ? ProfileService.EditProxy(editing.name, patch(), extra) : ProfileService.AddProxy(build(), extra, join))
      .then(onClose, (e) => setError(errText(e, "保存失败"))).finally(() => setBusy(false));
  };

  const input = (f: Field) => {
    const x = val(f);
    const on = (value: unknown) => setV({ ...v, [f.key]: value });
    if (f.kind === "bool") return <Switch on={!!x} onChange={on} />;
    if (f.kind === "select") return <select value={x} onChange={(e) => on(e.target.value)}>{f.options!.map((o) => <option key={o} value={o}>{o || "无"}</option>)}</select>;
    if (f.kind === "password") return <PasswordInput placeholder={f.placeholder ?? "可选"} value={x} onChange={(e) => on(e.target.value)} />;
    return <input {...plain} placeholder={f.placeholder ?? "可选"} value={x} onChange={(e) => on(e.target.value)} />;
  };
  const row = (label: string, control: React.ReactNode, key?: string) => (
    <div key={key ?? label} className="form-row"><span>{label}</span>{control}</div>
  );
  const duplicate = !editing && names.includes(v.name);
  const ready = !!(v.name && v.server) && validPort(v.port ?? "") && !duplicate;

  return (
    <Dialog title={editing ? `编辑 ${editing.name}` : "增加代理"} className="wide" bodyClass="two-col" locked={dirty || busy} onClose={onClose} foot={<>
      <button className="btn" onClick={() => setMore(!more)}>{more ? "收起更多设置" : "更多设置…"}</button>
      <span className="grow" />
      {error && <span className="small error foot-error">{error}</span>}
      <button className="btn" onClick={onClose}>取消</button>
      <button className="btn primary" disabled={!ready || busy} onClick={save}>完成</button>
    </>}>
          <div>
            <div className="group-title">基本信息</div>
            <div className="plist">
              {row("名称", <input autoFocus placeholder={duplicate ? "已有同名代理" : "必填"} disabled={!!editing} value={v.name ?? ""} onChange={(e) => setV({ ...v, name: e.target.value })} style={duplicate ? { borderColor: "var(--bad)" } : undefined} />)}
              {row("协议", <select value={type} disabled={!!editing} onChange={(e) => setType(e.target.value)}>{Object.entries(protocols).map(([k, p]) => <option key={k} value={k}>{p.label}</option>)}</select>)}
            </div>
            <div className="group-title">服务器信息</div>
            <div className="plist">
              {row("服务器地址", (
                <div className="inline">
                  <input placeholder="地址" value={v.server ?? ""} onChange={(e) => setV({ ...v, server: e.target.value })} style={{ flex: 1, minWidth: 0 }} />
                  <span className="muted">:</span>
                  <input placeholder="端口" value={v.port ?? ""} onChange={(e) => setV({ ...v, port: e.target.value })} style={{ width: 72 }} />
                </div>
              ))}
            </div>
            <div className="group-title">{protocols[type].label}</div>
            <div className="plist">{fields.filter(shown).map((f) => row(f.label, input(f), f.key))}</div>
            <div className="group-title">代理链</div>
            <div className="plist">
              {row("跳板代理", (
                <select value={v["dialer-proxy"] ?? ""} onChange={(e) => setV({ ...v, "dialer-proxy": e.target.value })}>
                  <option value="">不使用</option>
                  {names.map((n) => <option key={n}>{n}</option>)}
                </select>
              ))}
            </div>
          </div>
          <div>
            {groups.length > 0 && !editing && (
              <>
                <div className="group-title">加入策略组</div>
                <div className="plist">
                  {groups.map((g) => row(g, <Switch on={join.includes(g)} onChange={(on) => setJoin(on ? [...join, g] : join.filter((x) => x !== g))} />, g))}
                </div>
              </>
            )}
            <div className="group-title">出站设置</div>
            <div className="plist">
              {row("网络接口", <input placeholder="默认" value={v["interface-name"] ?? ""} onChange={(e) => setV({ ...v, "interface-name": e.target.value })} />)}
              {row("IP 版本", (
                <select value={v["ip-version"] ?? ""} onChange={(e) => setV({ ...v, "ip-version": e.target.value })}>
                  {["", "ipv4", "ipv6", "ipv4-prefer", "ipv6-prefer"].map((o) => <option key={o} value={o}>{o || "双栈"}</option>)}
                </select>
              ))}
            </div>
            <div className="group-title">选项</div>
            <div className="plist">
              {row("允许 UDP", <Switch on={!!v.udp} onChange={(on) => setV({ ...v, udp: on })} />)}
              {row("TCP Fast Open", <Switch on={!!v.tfo} onChange={(on) => setV({ ...v, tfo: on })} />)}
            </div>
            {more && (
              <>
                <div className="group-title">更多设置（YAML，合并到上面的字段）</div>
                <div className="plist">
                  <textarea className="code override" rows={6} spellCheck={false} placeholder={"plugin: obfs\nplugin-opts:\n  mode: tls"} value={extra} onChange={(e) => setExtra(e.target.value)} />
                </div>
              </>
            )}
          </div>
    </Dialog>
  );
}
