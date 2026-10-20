import { useCallback, useEffect, useState } from "react";
import { Browser } from "@wailsio/runtime";
import { CoreService, ProfileService, ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { EasyConnectTunnel, Status, VPN } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { errText, nonNull, validPort } from "./format";
import { usePoll } from "./hooks";
import PasswordInput from "./PasswordInput";

// A VPN is one node plus the rules that send traffic into it: the addresses listed here, picked
// by hand or from what the gateway (EasyConnect) or the tailnet (Tailscale) offers. Networks named
// under 直连 get a subnet group in between, which picks DIRECT there and the node elsewhere.
const defaultName = "VPN";
// lan: another Connaio on the LAN that is connected to the VPN, used through its proxy port (an http
// node marked connaio-lan), so one account is signed in once
const types: Record<string, string> = { easyconnect: "EasyConnect", wireguard: "WireGuard", openvpn: "OpenVPN", tailscale: "Tailscale", lan: "LAN" };
const defaultPort: Record<string, string> = { easyconnect: "443", lan: "7890" };
const noServer = new Set(["openvpn", "tailscale"]); // the address comes from the file, or the control plane

const stateLabel: Record<string, string> = { up: "已连接", connecting: "连接中", login: "待登录", down: "未连接" };
const stateClass: Record<string, string> = { up: "delay-good", connecting: "delay-warn", login: "delay-warn", down: "delay-bad" };

type Form = Record<string, string>;
const empty: Form = { type: "easyconnect", port: "443" };

// parseOvpn pulls what the core's openvpn node needs out of an .ovpn file.
function parseOvpn(text: string): Record<string, any> {
  const out: Record<string, any> = {};
  const block = (tag: string) => text.match(new RegExp(`<${tag}>\\s*([\\s\\S]*?)\\s*</${tag}>`))?.[1];
  const line = (key: string) => text.match(new RegExp(`^\\s*${key}\\s+(.+?)\\s*$`, "m"))?.[1];
  const remote = line("remote")?.split(/\s+/);
  if (remote) { out.server = remote[0]; out.port = Number(remote[1]) || 1194; if (remote[2]) out.proto = remote[2]; }
  for (const k of ["proto", "cipher", "auth", "dev", "key-direction"]) { const v = line(k); if (v) out[k] = v; }
  const dc = line("data-ciphers"); if (dc) out["data-ciphers"] = dc.split(":");
  if (/^\s*comp-lzo/m.test(text)) out["comp-lzo"] = line("comp-lzo") ?? "yes";
  for (const tag of ["ca", "cert", "key", "tls-auth", "tls-crypt", "tls-crypt-v2"]) {
    const v = block(tag); if (v) out[tag] = v;
  }
  return out;
}

// A KV is one typed entry: a route (rule type + value) or a bypass condition (kind + value).
export type KV = { t: string; v: string };
export type Kind = { key: string; label: string; placeholder?: string; fixed?: boolean }; // fixed: no value

const routeKinds: Kind[] = [
  { key: "IP-CIDR", label: "网段", placeholder: "10.0.0.0/8" },
  { key: "DOMAIN-SUFFIX", label: "域名后缀", placeholder: "example.edu.cn" },
  { key: "DOMAIN", label: "域名", placeholder: "git.example.edu.cn" },
  { key: "DOMAIN-KEYWORD", label: "域名关键字", placeholder: "example" },
  { key: "GEOSITE", label: "GEOSITE", placeholder: "cn" },
  { key: "GEOIP", label: "GEOIP", placeholder: "cn" },
];
export const bypassKinds: Kind[] = [
  { key: "SSID", label: "Wi-Fi 名称", placeholder: "名称" },
  { key: "BSSID", label: "Wi-Fi BSSID", placeholder: "aa:bb:cc:dd:ee:ff" },
  { key: "ROUTER", label: "网关地址", placeholder: "192.168.1.1" },
  { key: "CIDR", label: "本机网段", placeholder: "192.168.1.0/24" },
  { key: "TYPE:WIFI", label: "任何 Wi-Fi", fixed: true },
  { key: "TYPE:WIRED", label: "任何有线", fixed: true },
];
// ruleOf turns a route into a rule aimed at target; IP rules don't resolve names, as in the rules page.
const ruleOf = (r: KV, target: string) => `${r.t},${r.t === "IP-CIDR" && !r.v.includes("/") ? r.v + "/32" : r.v},${target}${r.t === "IP-CIDR" || r.t === "GEOIP" ? ",no-resolve" : ""}`;
// routeOf reads a rule back, or null for a rule the page didn't write.
const routeOf = (rule: string): KV | null => { const f = rule.split(",").map((x) => x.trim()); return routeKinds.some((k) => k.key === f[0]) ? { t: f[0], v: f[1] } : null; };
// condOf is the subnet group's key for a bypass entry, and bypassOf reads it back.
export const condOf = (b: KV) => (b.t === "CIDR" ? b.v : b.t.startsWith("TYPE:") ? b.t : `${b.t}:${b.v}`);
export const bypassOf = (key: string): KV => { const [t, ...rest] = key.split(":"); return key.startsWith("TYPE:") ? { t: key, v: "" } : ["SSID", "BSSID", "ROUTER"].includes(t) ? { t, v: rest.join(":") } : { t: "CIDR", v: key }; };

// KVRow is a kind selector and a value with a + button, as the rows above it, and a table of what
// was added, each row with a 删除.
function KVRow({ kinds, items, onChange, fill }: { kinds: Kind[]; items: KV[]; onChange: (v: KV[]) => void; fill?: { label: string; get: () => Promise<KV | null> } }) {
  const [t, setT] = useState(kinds[0].key);
  const [v, setV] = useState("");
  const kind = kinds.find((k) => k.key === t) ?? kinds[0];
  const has = (x: KV) => items.some((y) => y.t === x.t && y.v === x.v);
  const add = (x: KV) => { if ((x.v || kind.fixed) && !has(x)) onChange([...items, x]); setV(""); };
  const label = (key: string) => kinds.find((k) => k.key === key)?.label ?? key;
  return (
    <>
      <div className="form">
        <select style={{ width: 150 }} value={t} onChange={(e) => setT(e.target.value)}>{kinds.map((k) => <option key={k.key} value={k.key}>{k.label}</option>)}</select>
        <span className="pw grow">
          <input placeholder={kind.placeholder ?? ""} value={v} disabled={kind.fixed} onChange={(e) => setV(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add({ t, v: v.trim() })} style={fill ? { paddingRight: 72 } : undefined} />
          {fill && <button type="button" style={{ fontSize: 12, padding: "0 6px" }} onClick={() => fill.get().then((x) => { if (x) { setT(x.t); setV(x.v); } })}>{fill.label}</button>}
        </span>
        <button className="btn" disabled={!kind.fixed && !v.trim()} onClick={() => add({ t, v: v.trim() })} style={{ width: 80, fontSize: 18 }}>+</button>
      </div>
      {items.length > 0 && (
        <div className="table-wrap tight">
          <table className="table">
            <thead><tr><th>类型</th><th>值</th><th style={{ width: 1 }} /></tr></thead>
            <tbody>
              {items.map((x) => (
                <tr key={x.t + x.v}><td>{label(x.t)}</td><td>{x.v}</td><td><button className="btn small-btn" onClick={() => onChange(items.filter((y) => y !== x))}>删除</button></td></tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

// formOf fills the editor from a VPN as build writes it.
function formOf(v: VPN): { form: Form; addrs: KV[]; bypass: KV[] } {
  const p = v.proxy ?? {};
  const form: Form = { type: p["connaio-lan"] ? "lan" : String(p.type ?? "easyconnect") };
  for (const [k, x] of Object.entries(p)) if (k !== "name" && k !== "type" && k !== "connaio-lan" && (typeof x === "string" || typeof x === "number" || typeof x === "boolean")) form[k] = String(x);
  if (Array.isArray(p["allowed-ips"])) form["allowed-ips"] = p["allowed-ips"].join(", ");
  return { form, addrs: nonNull(v.rules).map(routeOf).filter((x): x is KV => !!x), bypass: Object.keys(v.group?.rules ?? {}).map(bypassOf) };
}

const protocolLabel: Record<string, string> = { all: "全部", tcp: "TCP", udp: "UDP", icmp: "ICMP" };

const ipNum = (a: string) => a.split(".").reduce((n, x) => n * 256 + Number(x), 0);
const ipStr = (n: number) => [24, 16, 8, 0].map((s) => Math.floor(n / 2 ** s) % 256).join(".");

// cidrsOf is an IPv4 range as the fewest CIDRs that cover it. A gateway lists networks without their
// network and broadcast addresses (10.0.0.1 ~ 10.255.255.254), so x.1 ~ y.254 counts as the whole
// networks, 10.0.0.0/8; an IPv6 or odd range is kept as its ends.
export function cidrsOf(from: string, to: string): string[] {
  if (from.includes(":")) return from === to ? [`${from}/128`] : [from, to].map((a) => `${a}/128`);
  let lo = ipNum(from), hi = ipNum(to);
  if (lo !== hi && lo % 256 === 1 && hi % 256 === 254) { lo -= 1; hi += 1; }
  const out: string[] = [];
  while (lo <= hi) {
    let bits = 32;
    while (bits > 0 && lo % 2 ** (33 - bits) === 0 && lo + 2 ** (33 - bits) - 1 <= hi) bits--; // the largest aligned block that fits
    out.push(`${ipStr(lo)}/${bits}`);
    lo += 2 ** (32 - bits);
  }
  return out;
}

// RouteRow is one route offered to pick: the VPN routes (rules) it stands for, and its columns.
type RouteRow = { key: string; routes: KV[]; cells: React.ReactNode[] };

// RoutePicker lists what a gateway or a tailnet offers, each row ticked when its routes are all among
// the VPN's (addrs); ticking adds them there, as rules of their own, unticking takes them out.
function RoutePicker({ head, rows, addrs, setAddrs, onPick }: { head: string[]; rows: RouteRow[]; addrs: KV[]; setAddrs: (a: KV[]) => void; onPick?: () => void }) {
  const has = (r: KV) => addrs.some((a) => a.t === r.t && a.v === r.v);
  const set = (row: RouteRow, on: boolean) => {
    if (on) { setAddrs([...addrs, ...row.routes.filter((r) => !has(r))]); onPick?.(); }
    else setAddrs(addrs.filter((a) => !row.routes.some((r) => r.t === a.t && r.v === a.v)));
  };
  return (
    <div className="table-wrap tight vpn-routes">
      <table className="table">
        <colgroup><col style={{ width: 52 }} /><col />{head.slice(1).map((h) => <col key={h} style={{ width: 120 }} />)}</colgroup>
        <thead><tr><th>选用</th>{head.map((h) => <th key={h}>{h}</th>)}</tr></thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.key}>
              <td><input type="checkbox" checked={row.routes.every(has)} onChange={(e) => set(row, e.target.checked)} /></td>
              {row.cells.map((c, i) => <td key={i}>{c}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// Fact is one labelled value of a VPN's state: 状态, 隧道地址, DNS.
const Fact = ({ label, children }: { label: string; children: React.ReactNode }) => <span className="vpn-fact"><span className="muted">{label}</span>{children}</span>;

// VpnEditor edits one VPN (initial null: a new one) and connects, disconnects or deletes it.
// live is the VPN as last listed (its state now), onConnected says a Tailscale one was just connected,
// so the list opens its login link when it comes.
function VpnEditor({ status, initial, live, tunnel, onDone, onConnected }: { status: Status; initial: VPN | null; live: VPN | null; tunnel: EasyConnectTunnel | null; onDone: () => void; onConnected: (name: string) => void }) {
  const prevName = String(initial?.proxy?.name ?? "");
  const init = initial && formOf(initial);
  const [form, setForm] = useState<Form>(init?.form ?? empty);
  const [addrs, setAddrs] = useState<KV[]>(init?.addrs ?? []);
  const [bypass, setBypass] = useState<KV[]>(init?.bypass ?? []);
  const [name, setName] = useState(prevName || defaultName);
  const [saved, setSaved] = useState(!!initial?.active);
  const fromProfile = !!initial?.profile; // the subscription's: 保存 puts a copy of it in the override
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [probe, setProbe] = useState<string[]>([]);
  const [probing, setProbing] = useState(false);

  const set = (k: string) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value });
  const text = (k: string, placeholder: string, extra: React.InputHTMLAttributes<HTMLInputElement> = {}) => <input className="grow" placeholder={placeholder} value={form[k] ?? ""} onChange={set(k)} {...extra} />;
  const secret = (k: string, placeholder: string) => <PasswordInput className="grow" placeholder={placeholder} value={form[k] ?? ""} onChange={set(k)} />;

  const ready = (() => {
    switch (form.type) {
      case "easyconnect": return form.server?.trim() && form.username?.trim() && form.password;
      case "wireguard": return form.server?.trim() && form["private-key"]?.trim() && form["public-key"]?.trim() && form.ip?.trim();
      case "openvpn": return form["ovpn-config"]?.trim();
      case "tailscale": return true; // without an auth key the core logs a login link
      case "lan": return form.server?.trim() && validPort(form.port ?? "");
    }
    return false;
  })();

  const conds = bypass.map(condOf);

  // build is the override's view of the form: the node, its rules and the bypass group.
  const build = () => {
    const proxy: Record<string, any> = { name, type: form.type, udp: true };
    const str = (k: string) => { const v = form[k]?.trim(); if (v) proxy[k] = v; };
    const num = (k: string, def?: number) => { const v = Number(form[k]) || def; if (v) proxy[k] = v; };
    let auto: string[] = [];
    switch (form.type) {
      case "easyconnect":
        str("server"); num("port", 443); str("username"); str("password"); str("totp-secret");
        break;
      case "wireguard":
        str("server"); num("port"); str("private-key"); str("public-key"); str("pre-shared-key"); str("ip"); str("ipv6"); num("mtu");
        auto = (form["allowed-ips"] ?? "").split(/[,\s]+/).filter(Boolean);
        proxy["allowed-ips"] = auto.length ? auto : ["0.0.0.0/0"];
        break;
      case "openvpn":
        Object.assign(proxy, parseOvpn(form["ovpn-config"] ?? ""));
        proxy["ovpn-config"] = form["ovpn-config"];
        str("username"); str("password");
        break;
      case "lan":
        Object.assign(proxy, { type: "http", "connaio-lan": true });
        delete proxy.udp;
        str("server"); num("port", 7890);
        break;
      case "tailscale":
        str("auth-key"); str("hostname"); str("control-url"); str("exit-node");
        if (form["accept-routes"] === "true") proxy["accept-routes"] = true;
        auto = ["100.64.0.0/10"]; // the tailnet's own addresses
        break;
    }
    const policy = conds.length ? `${name} 路由` : name;
    const rules: string[] = [];
    const routes = addrs.length ? addrs : auto.filter((a) => a !== "0.0.0.0/0" && a !== "::/0").map((a) => ({ t: "IP-CIDR", v: a }));
    const seen = new Set<string>();
    for (const r of routes) { const line = ruleOf(r, policy); if (!seen.has(line)) { seen.add(line); rules.push(line); } }
    const group = conds.length ? { name: policy, type: "subnet", default: name, rules: Object.fromEntries(conds.map((b) => [b, "DIRECT"])) } : null;
    return { proxy, rules, group };
  };

  const run = (p: Promise<unknown>) => {
    setBusy(true);
    setError("");
    p.catch((e) => setError(errText(e))).finally(() => setBusy(false));
  };
  // save keeps the settings (a connected VPN takes them at once); connect puts a disconnected one
  // in the config; reconnect drops a connected one's tunnel and logs in again
  const save = () => run(ProfileService.SaveVPN(build() as any, prevName).then(onDone));
  const connected = () => { if (form.type === "tailscale") onConnected(name); onDone(); };
  const connect = () => run(ProfileService.SetVPN(build() as any, prevName).then(() => { setSaved(true); connected(); }));
  const reconnect = () => run(ProfileService.ReconnectVPN(prevName || name).then(connected));
  // logging in takes the account from wherever else it is signed in: after a recent kick, ask first
  const [askKick, setAskKick] = useState(false);
  const login = () => { setAskKick(false); (saved ? reconnect : connect)(); };
  const disconnect = () => run(ProfileService.RemoveVPN(name).then(onDone));
  const remove = () => run(ProfileService.DeleteVPN(prevName || name).then(onDone));
  // probeGateway logs in with the form as it is, without saving: the core reports what the gateway handed out.
  const probeGateway = () => {
    setProbing(true);
    setProbe(["登录中…"]);
    ToolService.VPNProbe(build().proxy).then((l) => setProbe(nonNull(l))).catch((e) => setProbe([errText(e)])).finally(() => setProbing(false));
  };
  // currentNetwork is the subnet-group condition for the network this machine is on now.
  const currentNetwork = () => CoreService.CurrentNetwork().then((n): KV | null => (n.ssid ? { t: "SSID", v: n.ssid } : n.router ? { t: "ROUTER", v: n.router } : null));

  const routing = tunnel?.routing;
  // the gateway's routes as rows to pick: a range as its CIDRs, a name as a domain suffix
  const gatewayRows: RouteRow[] = routing ? [
    ...nonNull(routing.ip).map((r) => {
      const ports = r.portMin === 1 && r.portMax === 65535 ? "全部" : r.portMin === r.portMax ? String(r.portMin) : `${r.portMin}-${r.portMax}`;
      return { key: `${r.from}-${r.to}-${r.portMin}-${r.protocol}`, routes: cidrsOf(r.from, r.to).map((v) => ({ t: "IP-CIDR", v })),
        cells: [r.from === r.to ? r.from : `${r.from} ~ ${r.to}`, ports, protocolLabel[r.protocol] ?? r.protocol] };
    }),
    ...nonNull(routing.domains).map((d) => ({ key: d, routes: [{ t: "DOMAIN-SUFFIX", v: d }], cells: [d, "全部", "域名"] })),
  ] : [];
  // the subnets the tailnet's nodes advertise; reaching one needs accept-routes, ticked with it
  const tailnetRows: RouteRow[] = nonNull(live?.routes).map((r) => ({ key: r.prefix, routes: [{ t: "IP-CIDR", v: r.prefix }],
    cells: [r.prefix, <span className={r.online ? "" : "muted"}>{r.node}{r.online ? "" : "（离线）"}</span>] }));
  // the VPN's state, under the title: whether it is up, its address, and for Tailscale a way to log in
  const facts = saved && form.type === "tailscale" && live ? (
    <div className="vpn-facts">
      <Fact label="状态"><span className={stateClass[live.state]}>{stateLabel[live.state]}</span></Fact>
      {live.ips?.length ? <Fact label="Tailnet 地址"><span className="selectable">{live.ips.join("、")}</span></Fact> : null}
      {live.state === "login" && (live.authURL
        ? <button className="btn small-btn primary" onClick={() => Browser.OpenURL(live.authURL!)}>登录</button>
        : <span className="muted small">正在获取登录链接…</span>)}
    </div>
  ) : saved && form.type === "easyconnect" ? (<>
    <div className="vpn-facts">
      <Fact label="状态"><span className={stateClass[tunnel?.state ?? "down"]}>{stateLabel[tunnel?.state ?? "down"]}</span></Fact>
      {tunnel?.address && <Fact label="隧道地址"><span className="selectable">{tunnel.address}</span></Fact>}
      {routing?.dns?.length ? <Fact label="DNS"><span className="selectable">{routing.dns.join("、")}</span></Fact> : null}
    </div>
    {!tunnel?.kicked && tunnel?.kicks ? <div className="delay-warn small">网关于 {new Date(tunnel.lastKick).toLocaleTimeString("zh-CN", { hour12: false })} 结束会话（10 分钟内 {tunnel.kicks} 次），该账号可能已在其他设备登录；累计 3 次后将暂停自动重连。</div> : null}
    {tunnel?.kicked && <div className="error small">该账号可能已在其他设备登录，自动重连已暂停。请先在其他设备上退出，再点击「重连」。</div>}
  </>) : null;
  // what the gateway or the tailnet offers, picked into the VPN's routes, under the hand-typed ones
  const offered = form.type === "tailscale" ? (live?.state === "up" ? {
    title: "子网路由", rows: tailnetRows, head: ["网段", "节点"], empty: "Tailnet 中没有节点发布子网路由",
    note: "勾选后作为规则写入配置并经此 VPN 转发，同时开启「接受子网路由」",
    onPick: () => setForm({ ...form, "accept-routes": "true" }),
  } : null) : form.type === "easyconnect" && gatewayRows.length ? {
    title: "网关路由", rows: gatewayRows, head: ["网段或域名", "端口", "协议"], empty: "",
    note: "勾选后作为规则写入配置并经此 VPN 转发，端口与协议限制不随之写入", onPick: undefined,
  } : null;

  return (
    <div className="card vpn-editor" style={{ marginTop: 16 }}>
      <div className="label">{initial ? "编辑 VPN" : "添加 VPN"}</div>
      {facts}
      <div className="form">
        <input style={{ width: 150 }} placeholder="名称" value={name} onChange={(e) => setName(e.target.value)} />
        <select style={{ width: 150 }} value={form.type} disabled={saved} onChange={(e) => setForm({ ...empty, type: e.target.value, port: defaultPort[e.target.value] ?? "" })}>
          {Object.entries(types).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        {!noServer.has(form.type) && text("server", { easyconnect: "网关地址，如 vpn.example.edu.cn", lan: "另一台设备的局域网 IP，如 192.168.1.20" }[form.type] ?? "服务器地址")}
        {!noServer.has(form.type) && <input style={{ width: 80 }} placeholder={defaultPort[form.type] ?? "端口"} value={form.port ?? ""} onChange={set("port")} />}
      </div>
      {fromProfile && <div className="muted small" style={{ margin: "4px 0 8px" }}>此 VPN 来自订阅，断开或删除需在订阅中进行；保存后以本页设置为准。</div>}
      {form.type === "lan" && <div className="muted small" style={{ margin: "4px 0 8px" }}>
        用另一台已连接 VPN 的 Connaio 上网：那台设备在「设备」页打开「允许局域网设备连接」，端口填它的代理端口；下方「VPN 路由」填要经它访问的网段和域名。这样一个账号只在一处登录。
      </div>}
      {form.type === "easyconnect" && <>
        <div className="form">{text("username", "用户名")}{secret("password", "密码")}</div>
        <div className="form">{secret("totp-secret", "TOTP 密钥，可选")}</div>
      </>}
      {form.type === "wireguard" && <>
        <div className="form">{secret("private-key", "本机私钥")}{text("public-key", "对端公钥")}</div>
        <div className="form">{secret("pre-shared-key", "预共享密钥，可选")}{text("ip", "本机地址，如 10.0.0.2")}{text("mtu", "MTU，可选", { style: { width: 80, flex: "none" } })}</div>
        <div className="form">{text("allowed-ips", "对端网段（allowed-ips），逗号分隔，如 10.0.0.0/8")}</div>
      </>}
      {form.type === "openvpn" && <>
        <div className="form"><textarea className="grow" rows={5} placeholder="粘贴 .ovpn 文件内容（含 remote、<ca>、<cert>、<key>）" value={form["ovpn-config"] ?? ""} onChange={set("ovpn-config")} /></div>
        <div className="form">{text("username", "用户名，可选")}{secret("password", "密码，可选")}</div>
      </>}
      {form.type === "tailscale" && <>
        <div className="form">{secret("auth-key", "Auth key（可选，留空时连接后在浏览器中登录）")}{text("hostname", "本机在 tailnet 中的名称（可选）")}</div>
        <div className="form">{text("control-url", "控制服务器，Headscale 时填，可选")}{text("exit-node", "出口节点，可选")}
          <label className="radio"><input type="checkbox" checked={form["accept-routes"] === "true"} onChange={(e) => setForm({ ...form, "accept-routes": String(e.target.checked) })} /> 接受子网路由</label></div>
      </>}
      <div className="label section-label">直连网络</div>
      <KVRow kinds={bypassKinds} items={bypass} onChange={setBypass} fill={{ label: "当前网络", get: currentNetwork }} />
      <div className="label section-label">{form.type === "easyconnect" ? "附加路由" : "VPN 路由"}</div>
      <KVRow kinds={routeKinds} items={addrs} onChange={setAddrs} />
      {offered && <>
        <div className="vpn-offered-head"><span className="label">{offered.title}</span><span className="muted small">{offered.note}</span></div>
        {offered.rows.length ? <RoutePicker head={offered.head} rows={offered.rows} addrs={addrs} setAddrs={setAddrs} onPick={offered.onPick} /> : <div className="muted small">{offered.empty}</div>}
      </>}
      <div className="form vpn-actions">
        <button className="btn primary" disabled={busy || !ready || !name.trim() || !status.running} onClick={tunnel?.kicked || tunnel?.kicks ? () => setAskKick(true) : login}>{saved ? "重连" : "连接"}</button>
        <button className="btn" disabled={busy || !ready || !name.trim()} onClick={save}>保存</button>
        {saved && !fromProfile && <button className="btn" disabled={busy} onClick={disconnect}>断开</button>}
        {initial && !fromProfile && <button className="btn" style={{ color: "var(--bad)" }} disabled={busy} onClick={remove}>删除</button>}
        <button className="btn" disabled={busy} onClick={onDone}>取消</button>
        {form.type === "easyconnect" && tunnel?.state !== "up" && <button className="btn" disabled={probing || !ready || !status.running || tunnel?.state === "connecting"} onClick={probeGateway}>{probing ? "登录中…" : "测试登录"}</button>}
        {probe.length > 0 && <span className={`small ${/失败|错误|refused|rejected/i.test(probe[0]) ? "error" : "muted"}`} style={{ alignSelf: "center" }}>{probe.join("；")}</span>}
      </div>
      {askKick && <div className="form delay-warn small" style={{ alignItems: "center" }}>
        该账号近期在其他设备登录过。继续连接将使其他设备的会话下线。
        <button className="btn small-btn" disabled={busy} onClick={login}>仍然连接</button>
        <button className="btn small-btn" onClick={() => setAskKick(false)}>取消</button>
      </div>}
      {error && <div className="error small" style={{ marginTop: 8 }}>{error}</div>}

    </div>
  );
}

// Vpn lists the VPNs with their state, each with 连接 / 断开 and 编辑, an editor for the one being
// edited (or a new one), and a reachability test that goes through the rules.
export default function Vpn({ status }: { status: Status }) {
  const [vpns, setVpns] = useState<VPN[]>([]);
  const [tunnels, setTunnels] = useState<EasyConnectTunnel[]>([]);
  const [editing, setEditing] = useState<VPN | "new" | null>(null);
  const [target, setTarget] = useState("");
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<string[]>([]);
  // a Tailscale VPN just connected: its login link opens in the browser once the core has it
  const [awaiting, setAwaiting] = useState<{ name: string; until: number } | null>(null);

  const load = useCallback(() => Promise.all([
    ProfileService.VPNs().then((v) => setVpns(nonNull(v))),
    CoreService.EasyConnects().then((t) => setTunnels(nonNull(t))).catch(() => setTunnels([])),
  ]), []);
  usePoll(load, awaiting ? 1500 : 5000, status.running);
  useEffect(() => {
    if (!awaiting) return;
    const v = vpns.find((x) => x.proxy?.name === awaiting.name);
    if (v?.state === "login" && v.authURL) Browser.OpenURL(v.authURL);
    if (v?.state === "login" && v.authURL || v?.state === "up" || Date.now() > awaiting.until) setAwaiting(null);
  }, [vpns, awaiting]);

  const current = editing === "new" ? null : editing; // the VPN being edited, if an existing one
  const test = () => {
    setTesting(true);
    setResult([]);
    ToolService.VPNTest(target.trim()).then((l) => setResult(nonNull(l))).catch((e) => setResult([errText(e)])).finally(() => setTesting(false));
  };

  return (
    <>
      <div className="hint">仅目标为 VPN 网段或域名的流量经 VPN 隧道转发，其余流量按原有规则处理。</div>
      <div className="tile-head"><span className="tile-title groups">VPN</span></div>
      <div className="tiles">
        {vpns.map((v) => {
          const name = String(v.proxy?.name ?? "");
          const state = v.state;
          return (
            <button key={name} className={`tile ${current?.proxy?.name === name ? "selected" : ""}`} onClick={() => setEditing(current?.proxy?.name === name ? null : v)}>
              <span className="kind">{types[v.proxy?.["connaio-lan"] ? "lan" : String(v.proxy?.type)] ?? ""}</span>
              <span className="name">{name}</span>
              <span className={`foot ${v.kicked ? "delay-bad" : v.kicks ? "delay-warn" : stateClass[state]}`}>{v.kicked ? "别处登录，已暂停" : v.kicks ? `被打断 ${v.kicks} 次` : stateLabel[state]}</span>
            </button>
          );
        })}
        <button className="tile add" onClick={() => setEditing("new")} title="添加 VPN">＋</button>
      </div>
      {editing && <VpnEditor key={String(current?.proxy?.name ?? "new")} status={status} initial={current} live={vpns.find((v) => v.proxy?.name === current?.proxy?.name) ?? null}
        tunnel={tunnels.find((t) => t.name === current?.proxy?.name) ?? null} onDone={() => { setEditing(null); load(); }}
        onConnected={(name) => setAwaiting({ name, until: Date.now() + 60_000 })} />}

      {vpns.some((v) => v.active) && (
        <div className="card" style={{ marginTop: 16 }}>
          <div className="label">测试</div>
          <div className="form">
            <input className="grow" placeholder="目标地址，如 10.1.2.3:22 或 git.example.com:443" value={target} onChange={(e) => setTarget(e.target.value)} onKeyDown={(e) => e.key === "Enter" && target && !testing && test()} />
            <button className="btn primary" disabled={testing || !target.trim() || !status.running} onClick={test}>{testing ? "测试中…" : "测试"}</button>
          </div>
          {result.map((l, i) => <div key={i} className={`detail-row ${/失败|超时|拒绝|应答|连不上/.test(l) ? "error" : ""}`}>{l}</div>)}
        </div>
      )}
    </>
  );
}
