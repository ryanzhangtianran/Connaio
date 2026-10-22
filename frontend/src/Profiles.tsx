import { useCallback, useEffect, useRef, useState } from "react";
import { ProfileService, SettingsService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { OverrideParts, Profiles as ProfilesData } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { ago, sizeText, words } from "./format";
import { on } from "./hooks";

const host = (url: string) => { try { return new URL(url).host; } catch { return url; } };

// overrideFields are suggestions for the field box; any dotted path works.
const overrideFields = [
  // general
  "mixed-port", "port", "socks-port", "redir-port", "tproxy-port", "allow-lan", "bind-address", "lan-allowed-ips", "lan-disallowed-ips",
  "authentication", "skip-auth-prefixes", "mode", "log-level", "ipv6", "unified-delay", "tcp-concurrent", "find-process-mode",
  "global-client-fingerprint", "keep-alive-interval", "keep-alive-idle", "disable-keep-alive", "interface-name", "routing-mark",
  "geodata-mode", "geodata-loader", "geo-auto-update", "geo-update-interval", "geox-url.geoip", "geox-url.geosite", "geox-url.mmdb",
  "profile.store-selected", "profile.store-fake-ip", "global-ua", "etag-support",
  // dns
  "dns.enable", "dns.listen", "dns.ipv6", "dns.enhanced-mode", "dns.fake-ip-range", "dns.fake-ip-filter", "dns.fake-ip-filter-mode",
  "dns.default-nameserver", "dns.nameserver", "dns.fallback", "dns.fallback-filter.geoip", "dns.fallback-filter.ipcidr",
  "dns.proxy-server-nameserver", "dns.direct-nameserver", "dns.nameserver-policy", "dns.use-hosts", "dns.use-system-hosts",
  "dns.use-system-resolvers", "dns.respect-rules", "dns.prefer-h3", "dns.cache-algorithm", "hosts",
  // tun
  "tun.stack", "tun.mtu", "tun.dns-hijack", "tun.auto-route", "tun.auto-detect-interface", "tun.strict-route",
  "tun.route-address", "tun.route-exclude-address", "tun.inet4-address", "tun.inet6-address", "tun.endpoint-independent-nat",
  // sniffer
  "sniffer.enable", "sniffer.force-dns-mapping", "sniffer.parse-pure-ip", "sniffer.override-destination",
  "sniffer.sniff.HTTP.ports", "sniffer.sniff.TLS.ports", "sniffer.sniff.QUIC.ports", "sniffer.force-domain", "sniffer.skip-domain",
  // ntp, experimental
  "ntp.enable", "ntp.server", "ntp.port", "ntp.interval", "ntp.write-to-system", "experimental.quic-go-disable-gso",
  // lists the override extends or trims
  "prepend-rules", "append-rules", "prepend-proxies", "prepend-proxy-groups", "remove-proxies", "remove-proxy-groups", "rules",
];

// Profiles lists subscriptions and local configs (click to select, double-click to use), the
// override merged on top, and where it is all stored.
// partNote says what each part of the override holds.
const partNote: Record<keyof OverrideParts, string> = {
  clash: "Clash（mihomo）通用的部分，可在其他客户端中使用",
  connaio: "仅 Connaio 支持的部分：反向隧道、接管方式、EasyConnect 节点、Subnet 策略组及相关规则；保存时此部分的规则排在前面",
};

export default function Profiles() {
  const [data, setData] = useState<ProfilesData>({ current: "", profiles: [] });
  const [sel, setSel] = useState("");
  const [busy, setBusy] = useState("");
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [link, setLink] = useState("");
  // the override as two parts (ProfileService.OverrideParts), one shown at a time
  const [parts, setParts] = useState<OverrideParts>({ clash: "", connaio: "" });
  const [savedParts, setSavedParts] = useState<OverrideParts>({ clash: "", connaio: "" });
  const [part, setPart] = useState<keyof OverrideParts>("clash");
  const [dataDir, setDataDir] = useState("");
  const [field, setField] = useState("");
  const [value, setValue] = useState("");
  const [editing, setEditing] = useState<{ id: string; name: string; text: string; saved?: string } | null>(null);
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null); // the row whose name is an input

  const load = useCallback(() => ProfileService.Profiles().then(setData), []);
  const loadParts = () => ProfileService.OverrideParts().then((p) => { setParts(p); setSavedParts(p); });
  const dirty = parts.clash !== savedParts.clash || parts.connaio !== savedParts.connaio;
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  useEffect(() => {
    load();
    loadParts();
    SettingsService.About().then((a) => setDataDir(a.dataDir));
    // a setting changed from the tray or another page: shown, unless an edit here would be lost
    const offs = [on("profiles", (d) => setData(d)), on("setting", () => { if (!dirtyRef.current) loadParts(); })];
    return () => offs.forEach((off) => off());
  }, [load]);

  const run = (key: string, action: () => Promise<unknown>) => {
    setBusy(key);
    action().finally(() => setBusy(""));
  };
  // a field goes straight into the saved override, so it waits until the editor holds no unsaved edits
  const canAdd = !!field.trim() && !dirty && !busy;
  const addField = () => run("field", () => ProfileService.SetText(field, value).then(loadParts).then(() => { setField(""); setValue(""); }));
  const list = data.profiles ?? [];
  const selected = list.find((p) => p.id === sel);
  const rename = () => {
    const r = renaming;
    setRenaming(null);
    if (r && r.name.trim() && r.name.trim() !== list.find((p) => p.id === r.id)?.name) run("rename", () => ProfileService.Rename(r.id, r.name));
  };
  const use = (id: string) => id !== data.current && run("use", () => ProfileService.Select(id));
  const add = () => run("add", () => ProfileService.Add(name, link).then(() => { setName(""); setLink(""); setAdding(false); }));

  return (
    <>

      <div className="group-title">配置列表</div>
      <div className="plist">
        <div className="plist-rows">
        {list.length === 0 && !adding && <div className="plist-empty muted">还没有配置，从 URL 添加订阅或导入文件。</div>}
        {list.map((p) => {
          const used = p.usage ? p.usage.upload + p.usage.download : 0;
          const total = p.usage?.total ?? 0;
          return (
            <div key={p.id} className={`plist-row ${p.id === sel ? "selected" : ""}`} onClick={() => setSel(p.id)} onDoubleClick={() => use(p.id)}>
              <span className="plist-check">{p.id === data.current ? "✓" : ""}</span>
              <div className="plist-main">
                {renaming?.id === p.id
                  ? <input className="plist-rename" value={renaming.name} autoFocus onFocus={(e) => e.target.select()}
                      onChange={(e) => setRenaming({ id: p.id, name: e.target.value })} onBlur={rename}
                      onKeyDown={(e) => { if (e.key === "Enter") rename(); if (e.key === "Escape") setRenaming(null); }}
                      onClick={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()} />
                  : <div className="plist-name">{p.name}</div>}
                <div className="muted small">
                  {p.url ? host(p.url) : "本地"} · 更新于 {ago(p.updatedAt)}{p.interval > 0 && ` · 每 ${p.interval} 小时自动更新`}
                </div>
              </div>
              {total > 0 && (
                <div className="plist-usage">
                  <div className="small">{sizeText(used)} / {sizeText(total)}</div>
                  <div className="plist-meter"><i style={{ width: `${Math.min(100, (used / total) * 100)}%` }} /></div>
                  {(p.usage?.expire ?? 0) > 0 && <div className="muted small">{new Date(p.usage!.expire * 1000).toLocaleDateString()} 到期</div>}
                </div>
              )}
            </div>
          );
        })}
        </div>
        {adding && (
          <div className="plist-add">
            <input placeholder="名称（可选）" value={name} onChange={(e) => setName(e.target.value)} style={{ width: 130 }} />
            <input placeholder="订阅链接 https://…" value={link} autoFocus onChange={(e) => setLink(e.target.value)} onKeyDown={(e) => e.key === "Enter" && link && add()} className="grow" />
            <button className="btn primary" disabled={!link || !!busy} onClick={add}>{busy === "add" ? "下载中…" : "添加"}</button>
            <button className="btn" onClick={() => setAdding(false)}>取消</button>
          </div>
        )}
        <div className="plist-tools">
          <button className="btn" title="从 URL 添加订阅" onClick={() => setAdding(true)}>＋</button>
          <button className="btn" title="删除所选配置" disabled={!selected || !!busy} onClick={() => selected && run("remove", () => ProfileService.Remove(selected.id).then(() => setSel("")))}>－</button>
          <button className="btn" disabled={!selected?.url || !!busy} onClick={() => selected && run("update", () => ProfileService.Update(selected.id))}>{busy === "update" ? "更新中…" : "更新"}</button>
          <button className="btn" disabled={!selected || !!busy} onClick={() => selected && ProfileService.Content(selected.id).then((text) => setEditing({ id: selected.id, name: selected.name, text, saved: text }))}>编辑</button>
          <button className="btn" disabled={!selected || !!busy} onClick={() => selected && setRenaming({ id: selected.id, name: selected.name })}>重命名</button>
          <button className="btn" disabled={!!busy} onClick={() => run("import", () => ProfileService.ImportFile())}>导入…</button>
          <span className="grow" />
          <button className="btn" disabled={!selected || selected.id === data.current || !!busy} onClick={() => selected && use(selected.id)}>{busy === "use" ? "切换中…" : "使用"}</button>
        </div>
      </div>

      <div className="group-title">覆写</div>
      <div className="plist override-card">
        <div className="plist-add">
          <input className="field-input" list="override-fields" placeholder="字段，如 dns.enable" value={field} onChange={(e) => setField(e.target.value)} />
          <datalist id="override-fields">{overrideFields.map((f) => <option key={f} value={f} />)}</datalist>
          <input placeholder="值，如 true、7890、[a, b]" value={value} onChange={(e) => setValue(e.target.value)} onKeyDown={(e) => e.key === "Enter" && canAdd && addField()} className="grow" />
          <button className="btn" disabled={!canAdd} title={dirty ? "先保存或还原编辑框里的改动" : ""} onClick={addField}>{busy === "field" ? "添加中…" : "添加"}</button>
        </div>
        <div className="plist-tools override-tabs">
          <div className="seg">
            <button className={part === "clash" ? "on" : ""} onClick={() => setPart("clash")}>Clash</button>
            <button className={part === "connaio" ? "on" : ""} onClick={() => setPart("connaio")}>Connaio</button>
          </div>
          <span className="muted small">{partNote[part]}</span>
        </div>
        <textarea className="code override" rows={10} spellCheck={false} value={parts[part]} onChange={(e) => setParts({ ...parts, [part]: e.target.value })} />
        <div className="plist-tools">
          <button className="btn" disabled={dirty || !!busy} title={dirty ? "先保存或还原编辑框里的改动" : "合并 YAML 文件到覆写：同名项以导入的为准，列表追加"} onClick={() => run("import", () => ProfileService.ImportOverride().then(loadParts))}>导入…</button>
          <button className="btn" disabled={!parts[part] || !!busy} onClick={() => setParts({ ...parts, [part]: "" })}>清除</button>
          <span className="grow" />
          <button className="btn" disabled={!dirty || !!busy} onClick={() => setParts(savedParts)}>还原</button>
          <button className="btn primary" disabled={!dirty || !!busy} onClick={() => run("override", () => ProfileService.SetOverrideParts(parts).then(loadParts))}>
            {busy === "override" ? "应用中…" : "保存并应用"}
          </button>
        </div>
      </div>

      {editing && (
        <div className="modal-bg" onClick={() => editing.text === editing.saved && setEditing(null)}>
          <div className="sheet" onClick={(e) => e.stopPropagation()}>
            <div className="sheet-head">
              <div className="sheet-title">编辑 {editing.name}</div>
              {list.find((p) => p.id === editing.id)?.url && <div className="muted small">这是订阅，下次更新时会被覆盖。</div>}
            </div>
            <div className="sheet-body">
              <textarea className="code" rows={24} spellCheck={false} value={editing.text} onChange={(e) => setEditing({ ...editing, text: e.target.value })} style={{ marginTop: 16 }} />
            </div>
            <div className="sheet-foot">
              <button className="btn" disabled={!!busy} onClick={() => run("format", () => ProfileService.Format(editing.text).then((text) => setEditing({ ...editing, text })))}>格式化</button>
              <span className="grow" />
              <button className="btn" onClick={() => setEditing(null)}>取消</button>
              <button className="btn primary" disabled={!!busy} onClick={() => run("content", () => ProfileService.SetContent(editing.id, editing.text).then(() => setEditing(null)))}>
                {busy === "content" ? "应用中…" : "保存"}
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="group-title">配置存储</div>
      <div className="plist">
        <div className="plist-row static">
          <div className="plist-main"><div className="plist-name">数据目录</div><div className="muted small selectable">{dataDir}</div></div>
          <button className="btn" onClick={() => SettingsService.OpenDataDir()}>{words.showInFiles}</button>
        </div>
      </div>
    </>
  );
}
