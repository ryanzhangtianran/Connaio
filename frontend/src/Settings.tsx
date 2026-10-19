import { useCallback, useEffect, useState } from "react";
import { CoreService, EnhancedService, ProfileService, SettingsService, ToolService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { About, DNSLeak, LeakServer, Update, UpdateProgress } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { on } from "./hooks";
import { errText, platform, sizeText, words, validPort } from "./format";


// Row is one setting: title and description, the control at the right; a list row lists its items below.
function Row({ title, desc, list, children }: { title: string; desc?: string; list?: boolean; children: React.ReactNode }) {
  return (
    <div className={list ? "setting-row list-row" : "setting-row"}>
      <div><div className="setting-title">{title}</div>{desc && <div className="muted small">{desc}</div>}</div>
      <div className={list ? "list-items" : "setting-control"}>{children}</div>
    </div>
  );
}

// Switch is WebKit's native toggle (<input type=checkbox switch>, Safari 17.4+).
export function Switch({ on, onChange, disabled }: { on: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return <input type="checkbox" {...{ switch: "" }} checked={on} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />;
}

// sameShape keeps a value only if it matches the fallback's type: an unset Go value can reach JS
// as a non-null placeholder (not null), which must not replace e.g. an empty list.
function sameShape<T>(v: unknown, fallback: T): T {
  if (Array.isArray(fallback)) return (Array.isArray(v) ? v : fallback) as T;
  return (v != null && typeof v === typeof fallback ? v : fallback) as T;
}

// useSetting reads a dotted config value and writes it through the override.
export function useSetting<T>(path: string, fallback: T): [T, (v: T) => Promise<void>] {
  const [value, setValue] = useState<T>(fallback);
  useEffect(() => {
    const load = () => ProfileService.Value(path).then((v) => setValue(sameShape(v, fallback)));
    load();
    return on("setting", (d) => d === path && load()); // also changed from the menu bar
  }, [path]);
  const save = (v: T) => ProfileService.Set(path, v).then(() => setValue(v));
  return [value, save];
}

export function General() {
  const [daemon, setDaemon] = useState(false);
  const [busy, setBusy] = useState("");
  const [login, setLogin] = useState(false);
  const [traySpeed, setTraySpeed] = useState(true);
  const [port, setPort] = useSetting<number>("mixed-port", 7890);
  const [portText, setPortText] = useState("");
  const [logLevel, setLogLevel] = useSetting<string>("log-level", "info");
  const [version, setVersion] = useState("");
  const [update, setUpdate] = useState<Update | null>(null);
  const [progress, setProgress] = useState<UpdateProgress | null>(null); // the update's download, then install
  useEffect(() => { SettingsService.About().then((a) => setVersion(a.version)); }, []);
  useEffect(() => on("update-progress", setProgress), []);

  const load = useCallback(() => {
    EnhancedService.DaemonInstalled().then(setDaemon);
    SettingsService.LaunchAtLogin().then(setLogin);
    SettingsService.TraySpeed().then(setTraySpeed);
  }, []);
  useEffect(() => { load(); return on("enhanced", load); }, [load]);
  useEffect(() => setPortText(String(port)), [port]);

  const run = (p: Promise<unknown>, key = "") => {
    setBusy(key);
    p.finally(() => { setBusy(""); load(); });
  };

  return (
    <>
      <div className="card settings-card">
        <div className="section-title">内核</div>
        <Row title="代理端口" desc="HTTP 与 SOCKS5 共用">
          <input className="ctl" value={portText} onChange={(e) => setPortText(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && (validPort(portText) && portText !== String(port) ? setPort(Number(portText)) : setPortText(String(port)))}
            onBlur={() => setPortText(String(port))} />
        </Row>
        <Row title="日志级别">
          <select className="ctl" value={logLevel} onChange={(e) => setLogLevel(e.target.value)}>
            {["debug", "info", "warning", "error", "silent"].map((l) => <option key={l}>{l}</option>)}
          </select>
        </Row>
        <Row title="重启内核" desc="重新启动内核进程并载入当前配置">
          <button className="btn ctl" disabled={!!busy} onClick={() => run(CoreService.Restart(), "restart")}>{busy === "restart" ? "重启中…" : "重启"}</button>
        </Row>
        <Row title="root 权限" desc={daemon ? "已授予，内核以 root 运行" : "授予后才能开启增强模式"}>
          {daemon
            ? <button className="btn ctl" disabled={!!busy} onClick={() => run(EnhancedService.Uninstall(), "root")}>{busy === "root" ? "移除中…" : "移除"}</button>
            : <button className="btn ctl" disabled={!!busy} onClick={() => run(EnhancedService.Grant(), "root")}>{busy === "root" ? "授予中…" : "授予"}</button>}
        </Row>
      </div>

      <div className="card settings-card">
        <div className="section-title">应用</div>
        <Row title="开机时启动">
          <Switch on={login} onChange={(v) => run(SettingsService.SetLaunchAtLogin(v))} />
        </Row>
        {platform === "macos" && ( // only the menu bar has room for it: the Linux and Windows trays show the icon alone
          <Row title={`${words.tray}显示网速`}>
            <Switch on={traySpeed} onChange={(v) => run(SettingsService.SetTraySpeed(v))} />
          </Row>
        )}
      </div>

      <div className="card settings-card">
        <div className="section-title">更新</div>
        <Row title={`当前版本 ${version}`} desc={update ? (update.newer ? `发现新版本 ${update.latest}，从 GitHub Releases 下载安装后自动重启` : "已是最新版本") : undefined}>
          {update?.newer
            ? <button className="btn ctl" disabled={!!busy} onClick={() => { setProgress(null); run(SettingsService.InstallUpdate(), "update"); }}>{busy === "update" ? updateLabel(progress) : "更新"}</button>
            : <button className="btn ctl" disabled={!!busy} onClick={() => run(SettingsService.CheckUpdate().then(setUpdate), "check")}>{busy === "check" ? "检查中…" : "检查更新"}</button>}
        </Row>
        {busy === "update" && (
          <div className="update-progress">
            <div className="plist-meter"><i className={progress?.total ? "" : "busy"} style={{ width: progress?.total ? `${(progress.done / progress.total) * 100}%` : progress?.stage === "install" ? "100%" : "30%" }} /></div>
            <div className="muted small">
              {progress?.stage === "install" ? "下载完成，正在安装，完成后自动重启"
                : progress ? `${sizeText(progress.done)}${progress.total ? ` / ${sizeText(progress.total)}` : ""}` : "正在连接…"}
            </div>
          </div>
        )}
      </div>
    </>
  );
}

// updateLabel is the update button's text while it runs: the download's percentage, then the install.
function updateLabel(p: UpdateProgress | null) {
  if (p?.stage === "install") return "安装中…";
  return p?.total ? `下载中 ${Math.floor((p.done / p.total) * 100)}%` : "下载中…";
}

// ListSetting edits a string list setting item by item: each one a row that can be removed, and an
// input to add one; every change is saved right away.
function ListSetting({ title, desc, path, placeholder }: { title: string; desc?: string; path: string; placeholder: string }) {
  const [list, setList] = useSetting<string[]>(path, []);
  const [item, setItem] = useState("");
  const items = list ?? [];
  const add = () => { const v = item.trim(); if (v && !items.includes(v)) setList([...items, v]).then(() => setItem("")); };
  return (
    <Row list title={title} desc={desc}>
      {items.map((v, i) => <ListItem key={v} onRemove={() => setList(items.filter((_, j) => j !== i))}><span className="list-value">{v}</span></ListItem>)}
      <div className="list-add">
        <input className="code-input" placeholder={placeholder} value={item} onChange={(e) => setItem(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add()} />
        <button className="btn" disabled={!item.trim()} onClick={add}>添加</button>
      </div>
    </Row>
  );
}

// PolicySetting edits dns.nameserver-policy: a domain (or geosite:, rule-set:) per row and its servers.
function PolicySetting() {
  const [policy, setPolicy] = useSetting<Record<string, unknown>>("dns.nameserver-policy", {});
  const [domain, setDomain] = useState("");
  const [servers, setServers] = useState("");
  const entries = Object.entries(policy ?? {});
  const list = (v: unknown) => (Array.isArray(v) ? v : [v]).map(String);
  const add = () => {
    const d = domain.trim(), sv = servers.split(",").map((x) => x.trim()).filter(Boolean);
    if (!d || !sv.length) return;
    setPolicy({ ...(policy ?? {}), [d]: sv.length === 1 ? sv[0] : sv }).then(() => { setDomain(""); setServers(""); });
  };
  return (
    <Row list title="分域名服务器" desc="指定域名用指定的服务器解析。">
      {entries.map(([d, v]) => (
        <ListItem key={d} onRemove={() => setPolicy(Object.fromEntries(entries.filter(([k]) => k !== d)))}>
          <span className="list-value">{d}</span><span className="list-value muted">{list(v).join(", ")}</span>
        </ListItem>
      ))}
      <div className="list-add">
        <input className="code-input" placeholder="geosite:cn" value={domain} onChange={(e) => setDomain(e.target.value)} style={{ flex: "0 1 40%" }} />
        <input className="code-input" placeholder="服务器，多个用逗号分隔" value={servers} onChange={(e) => setServers(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add()} />
        <button className="btn" disabled={!domain.trim() || !servers.trim()} onClick={add}>添加</button>
      </div>
    </Row>
  );
}

function ListItem({ onRemove, children }: { onRemove: () => void; children: React.ReactNode }) {
  return <div className="list-item">{children}<button className="list-remove" title="删除" onClick={onRemove}>−</button></div>;
}

// servers sums a list of DNS servers up by network: "CloudFlare Inc（AS13335）· United States of America · 2 台",
// the ones that leaked first and marked.
function Servers({ list, leaked }: { list: LeakServer[]; leaked: Set<string> }) {
  const nets = new Map<string, { s: LeakServer; n: number; bad: boolean }>();
  for (const s of list) {
    const e = nets.get(s.asn) ?? { s, n: 0, bad: false };
    e.n++;
    e.bad ||= leaked.has(s.ip);
    nets.set(s.asn, e);
  }
  return (
    <div className="leak-servers selectable">
      {[...nets.values()].sort((a, b) => Number(b.bad) - Number(a.bad)).map(({ s, n, bad }) => {
        const [num, ...name] = s.asn.split(" ");
        return <div key={s.asn} className={bad ? "delay-bad" : "muted"} title={list.filter((x) => x.asn === s.asn).map((x) => x.ip).join("\n")}>{name.join(" ") || num}（{num}）· {s.country} · {n} 台</div>;
      })}
    </div>
  );
}

// LeakTest asks bash.ws which DNS servers looked up fresh test domains: once through the rules as
// any app's traffic goes, once directly as the baseline (ToolService.DNSLeakTest).
function LeakTest() {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<DNSLeak | null>(null);
  const [error, setError] = useState("");
  const run = () => {
    setBusy(true); setError(""); setResult(null);
    ToolService.DNSLeakTest().then(setResult).catch((e) => setError(errText(e, "检测失败，详情见事件"))).finally(() => setBusy(false));
  };
  const leaked = new Set((result?.leaked ?? []).map((s) => s.ip));
  const desc = busy ? "约需 20 秒" : error ? error : !result ? "按规则访问测试域名，看哪些 DNS 服务器解析了它；需要「规则判定」模式"
    : leaked.size ? `可能泄漏：本地网络的 ${leaked.size} 台 DNS 服务器解析了走代理的域名` : "没有泄漏：走代理的域名只由代理一侧的 DNS 服务器解析";
  return (
    <>
      <Row title="DNS 泄漏检测" desc={desc}>
        <button className="btn" disabled={busy} onClick={run}>{busy ? "检测中…" : "开始检测"}</button>
      </Row>
      {result && <>
        <Row title="代理一侧" desc={`测试域名经 ${result.policy || "代理"}，出口 ${result.exit.ip}（${result.exit.country}）`}>
          <Servers list={result.proxied ?? []} leaked={leaked} />
        </Row>
        <Row title="本地网络" desc={`直连出口 ${result.directIP.ip}（${result.directIP.country}）`}>
          <Servers list={result.direct ?? []} leaked={new Set()} />
        </Row>
      </>}
    </>
  );
}

export function Dns() {
  const [enable, setEnable] = useSetting<boolean>("dns.enable", false);
  const [mode, setMode] = useSetting<string>("dns.enhanced-mode", "redir-host");
  const [ipv6, setIPv6] = useSetting<boolean>("dns.ipv6", false);
  const [dual, setDual] = useSetting<boolean>("ipv6", true); // the core's ipv6: off, TUN carries no IPv6 and outbound resolves IPv4 only
  const [h3, setH3] = useSetting<boolean>("dns.prefer-h3", false);
  const [split, setSplit] = useSetting<boolean>("dns.use-system-resolvers", false);
  const [hosts, setHosts] = useSetting<boolean>("dns.use-system-hosts", true);
  const [respect, setRespect] = useSetting<boolean>("dns.respect-rules", false);
  const [range, setRange] = useSetting<string>("dns.fake-ip-range", "198.18.0.1/16");
  const [rangeText, setRangeText] = useState("");
  useEffect(() => setRangeText(range ?? ""), [range]);
  return (
    <>
      <div className="card settings-card">
        <div className="section-title">基本</div>
        <Row title="启用 DNS"><Switch on={enable} onChange={setEnable} /></Row>
        <Row title="模式">
          <div className="seg">
            {["fake-ip", "redir-host"].map((m) => <button key={m} className={mode === m ? "on" : ""} onClick={() => setMode(m)}>{m.toUpperCase()}</button>)}
          </div>
        </Row>
        <Row title="IP 版本" desc="仅 IPv4 时增强模式不再接管 IPv6 流量；若本机网络有 IPv6，应用会绕过代理直接走 IPv6，需同时在系统网络设置里关闭 IPv6。">
          <div className="seg">
            <button className={dual ? "on" : ""} onClick={() => setDual(true)}>双栈</button>
            <button className={!dual ? "on" : ""} onClick={() => setDual(false)}>仅 IPv4</button>
          </div>
        </Row>
        <Row title="解析 IPv6"><Switch on={ipv6} onChange={setIPv6} /></Row>
        <Row title="DoH 优先使用 HTTP/3"><Switch on={h3} onChange={setH3} /></Row>
        <Row title="DNS 查询遵循规则" desc="解析也按规则走代理，减少泄漏。"><Switch on={respect} onChange={setRespect} /></Row>
      </div>

      <div className="card settings-card">
        <div className="section-title">上游服务器</div>
        <ListSetting title="主服务器" path="dns.nameserver" placeholder="如 https://doh.pub/dns-query 或 223.5.5.5" />
        <ListSetting title="引导服务器" desc="解析 DoH 地址本身的域名，只能填 IP。" path="dns.default-nameserver" placeholder="如 223.5.5.5" />
        <ListSetting title="代理节点域名" desc="只用来解析代理服务器的域名。" path="dns.proxy-server-nameserver" placeholder="如 https://doh.pub/dns-query" />
        <ListSetting title="直连域名" desc="直连的连接用它解析。" path="dns.direct-nameserver" placeholder="如 system" />
        <ListSetting title="备用服务器" desc="主服务器结果可能被污染时使用。" path="dns.fallback" placeholder="如 https://1.1.1.1/dns-query" />
        <PolicySetting />
      </div>

      <div className="card settings-card">
        <div className="section-title">Fake-IP</div>
        <Row title="地址段">
          <div className="inline">
            <input value={rangeText} onChange={(e) => setRangeText(e.target.value)} style={{ width: 160 }} />
            <button className="btn" disabled={rangeText === range || !/^\d{1,3}(\.\d{1,3}){3}\/\d{1,2}$/.test(rangeText.trim())} onClick={() => setRange(rangeText.trim())}>保存</button>
          </div>
        </Row>
        <ListSetting title="不分配虚拟 IP 的域名" desc="局域网、时间服务器、游戏主机等需要真实 IP 的域名。" path="dns.fake-ip-filter" placeholder="如 +.lan、time.*.com" />
      </div>

      <div className="card settings-card">
        <div className="section-title">系统</div>
        <Row title="跟随系统分域名 DNS" desc="VPN 等为特定域名设置的 DNS 继续生效。"><Switch on={split} onChange={setSplit} /></Row>
        <Row title="使用系统 HOSTS"><Switch on={hosts} onChange={setHosts} /></Row>
      </div>

      <div className="card settings-card">
        <div className="section-title">检测</div>
        <LeakTest />
      </div>
    </>
  );
}

export function AboutPage() {
  const [about, setAbout] = useState<About | null>(null);
  useEffect(() => { SettingsService.About().then(setAbout); }, []);
  if (!about) return null;
  return (
    <div className="card settings-card">
      <Row title="Connaio">{about.version}</Row>
      <Row title="内核">{about.coreVersion || "—"}</Row>
      <Row title="内核运行方式">{about.attached ? "root" : "当前用户"}</Row>
      <Row title="数据目录" desc={about.dataDir}><button className="btn" onClick={() => SettingsService.OpenDataDir()}>{words.showInFiles}</button></Row>
    </div>
  );
}
