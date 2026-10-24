import { useCallback, useEffect, useRef, useState } from "react";
import { Clipboard } from "@wailsio/runtime";
import { ProfileService, SettingsService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Offer, OverrideParts, Profiles as ProfilesData } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Profile } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/profile";
import { ago, errText, plain, sizeText, web, words } from "./format";
import { on, useConfirm } from "./hooks";
import Dialog from "./Dialog";

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
  connaio: "仅 Connaio 支持的部分，保存时其中的规则排在前面",
};

// FilePick is the web UI's 导入…: the browser reads the chosen YAML file and hands its text, and
// its name without the extension, to onText. The app's own dialog would pick a file on the machine
// it runs on, which for the web UI is the server.
function FilePick({ disabled, title, onText }: { disabled?: boolean; title?: string; onText: (name: string, text: string) => void }) {
  return (
    <label className={`btn ${disabled ? "disabled" : ""}`} title={title}>
      导入…
      <input type="file" accept=".yaml,.yml" hidden disabled={disabled}
        onChange={(e) => { const f = e.target.files?.[0]; e.target.value = ""; if (f) f.text().then((t) => onText(f.name.replace(/\.ya?ml$/i, ""), t)); }} />
    </label>
  );
}

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
  const [field, setField] = useState("");
  const [value, setValue] = useState("");
  const [editing, setEditing] = useState<{ id: string; name: string; text: string; saved?: string } | null>(null);
  const [setting, setSetting] = useState<Profile | null>(null); // the profile whose 设置 sheet is open
  const [error, setError] = useState({ key: "", text: "" }); // a failed action's, shown by the part it belongs to
  const [armed, confirm] = useConfirm();

  const load = useCallback(() => ProfileService.Profiles().then(setData), []);
  const loadParts = () => ProfileService.OverrideParts().then((p) => { setParts(p); setSavedParts(p); });
  const dirty = parts.clash !== savedParts.clash || parts.connaio !== savedParts.connaio;
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  useEffect(() => {
    load();
    loadParts();
    // a provider's 一键导入 fills the add box: the user adds it
    const offer = (o: Offer | null) => { if (o) { setAdding(true); setLink(o.url); setName(o.name); } };
    ProfileService.Offered().then(offer);
    // a setting changed from the tray or another page: shown, unless an edit here would be lost
    const offs = [on("profiles", (d) => setData(d)), on("setting", () => { if (!dirtyRef.current) loadParts(); }),
      on("offer", () => ProfileService.Offered().then(offer))];
    return () => offs.forEach((off) => off());
  }, [load]);

  const run = (key: string, action: () => Promise<unknown>) => {
    setBusy(key);
    setError({ key: "", text: "" });
    action().catch((e) => setError({ key, text: errText(e, "操作失败") })).finally(() => setBusy(""));
  };
  const failed = (...keys: string[]) => keys.includes(error.key) && <div className="small error plist-error">{error.text}</div>;
  // ＋ opens the add box, its link from the clipboard when that holds one
  const startAdding = () => {
    setAdding(true);
    (web ? navigator.clipboard?.readText() ?? Promise.resolve("") : Clipboard.Text())
      .then((t) => { const s = t.trim(); if (/^https?:\/\/\S+$/.test(s)) setLink((l) => l || s); }).catch(() => {});
  };
  // a field goes straight into the saved override, so it waits until the editor holds no unsaved edits
  const canAdd = !!field.trim() && !dirty && !busy;
  const addField = () => run("field", () => ProfileService.SetText(field, value).then(loadParts).then(() => { setField(""); setValue(""); }));
  const list = data.profiles ?? [];
  const selected = list.find((p) => p.id === sel);
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
                <div className="plist-name">{p.name}</div>
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
            <input {...plain} placeholder="订阅链接 https://…" value={link} autoFocus onChange={(e) => setLink(e.target.value)} onKeyDown={(e) => e.key === "Enter" && link && add()} className="grow" />
            <button className="btn primary" disabled={!link || !!busy} onClick={add}>{busy === "add" ? "下载中…" : "添加"}</button>
            <button className="btn" onClick={() => setAdding(false)}>取消</button>
          </div>
        )}
        <div className="plist-tools">
          <button className="btn" title="从 URL 添加订阅" onClick={startAdding}>＋</button>
          <button className={`btn ${armed === "remove" ? "armed" : ""}`} title="删除所选配置" disabled={!selected || !!busy}
            onClick={() => selected && confirm("remove", () => run("remove", () => ProfileService.Remove(selected.id).then(() => setSel(""))))}>{armed === "remove" ? "确认删除" : "－"}</button>
          <button className="btn" disabled={!selected?.url || !!busy} onClick={() => selected && run("update", () => ProfileService.Update(selected.id))}>{busy === "update" ? "更新中…" : "更新"}</button>
          <button className="btn" disabled={!list.some((p) => p.url) || !!busy} onClick={() => run("updateAll", () => ProfileService.UpdateAll())}>{busy === "updateAll" ? "更新中…" : "全部更新"}</button>
          <button className="btn" disabled={!selected || !!busy} onClick={() => selected && ProfileService.Content(selected.id).then((text) => setEditing({ id: selected.id, name: selected.name, text, saved: text }))}>编辑</button>
          <button className="btn" disabled={!selected || !!busy} onClick={() => selected && setSetting(selected)}>设置</button>
          {web
            ? <FilePick disabled={!!busy} onText={(name, text) => run("import", () => ProfileService.AddLocal(name, text))} />
            : <button className="btn" disabled={!!busy} onClick={() => run("import", () => ProfileService.ImportFile())}>导入…</button>}
          <span className="grow" />
          <button className="btn" disabled={!selected || selected.id === data.current || !!busy} onClick={() => selected && use(selected.id)}>{busy === "use" ? "切换中…" : "使用"}</button>
        </div>
        {failed("add", "remove", "update", "updateAll", "use", "import")}
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
          {web
            ? <FilePick disabled={dirty || !!busy} title={dirty ? "先保存或还原编辑框里的改动" : "合并 YAML 文件到覆写：同名项以导入的为准，列表追加"} onText={(_, text) => run("import", () => ProfileService.MergeOverride(text).then(loadParts))} />
            : <button className="btn" disabled={dirty || !!busy} title={dirty ? "先保存或还原编辑框里的改动" : "合并 YAML 文件到覆写：同名项以导入的为准，列表追加"} onClick={() => run("import", () => ProfileService.ImportOverride().then(loadParts))}>导入…</button>}
          <button className={`btn ${armed === "clear" ? "armed" : ""}`} disabled={!parts[part] || !!busy} onClick={() => confirm("clear", () => setParts({ ...parts, [part]: "" }))}>{armed === "clear" ? "确认清除" : "清除"}</button>
          <span className="grow" />
          <button className="btn" disabled={!dirty || !!busy} onClick={() => setParts(savedParts)}>还原</button>
          <button className="btn primary" disabled={!dirty || !!busy} onClick={() => run("override", () => ProfileService.SetOverrideParts(parts).then(loadParts))}>
            {busy === "override" ? "应用中…" : "保存并应用"}
          </button>
        </div>
        {failed("field", "override")}
      </div>

      {editing && <ProfileEditor editing={editing} subscription={!!list.find((p) => p.id === editing.id)?.url} onClose={() => setEditing(null)} />}
      {setting && <ProfileSettings profile={setting} onClose={() => setSetting(null)} />}

      <div className="group-title">配置存储</div>
      <div className="plist">
        <div className="plist-row static">
          <div className="plist-main"><div className="plist-name">数据目录</div><div className="muted small">{web ? "保存在运行 Connaio 的服务器上" : "配置、覆写与流量统计保存在本机"}</div></div>
          {!web && <button className="btn" onClick={() => SettingsService.OpenDataDir()}>{words.showInFiles}</button>}
        </div>
      </div>
    </>
  );
}

// ProfileEditor edits a profile's YAML; edits not yet saved hold it open, and 取消 asks before it drops them.
function ProfileEditor({ editing, subscription, onClose }: { editing: { id: string; name: string; text: string }; subscription: boolean; onClose: () => void }) {
  const [text, setText] = useState(editing.text);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [armed, confirm] = useConfirm();
  const dirty = text !== editing.text;
  const run = (key: string, action: () => Promise<unknown>) => {
    setBusy(key);
    setError("");
    action().catch((e) => setError(errText(e, "操作失败"))).finally(() => setBusy(""));
  };
  return (
    <Dialog title={`编辑 ${editing.name}`} sub={subscription && "这是订阅，下次更新时会被覆盖。"} locked={dirty || !!busy} onClose={onClose} foot={<>
      <button className="btn" disabled={!!busy} onClick={() => run("format", () => ProfileService.Format(text).then(setText))}>格式化</button>
      <span className="grow" />
      {error && <span className="small error foot-error">{error}</span>}
      <button className={`btn ${armed ? "armed" : ""}`} onClick={() => (dirty ? confirm("drop", onClose) : onClose())}>{armed ? "确认放弃" : "取消"}</button>
      <button className="btn primary" disabled={!!busy} onClick={() => run("content", () => ProfileService.SetContent(editing.id, text).then(onClose))}>
        {busy === "content" ? "应用中…" : "完成"}
      </button>
    </>}>
      <textarea className="code" rows={24} spellCheck={false} value={text} onChange={(e) => setText(e.target.value)} style={{ marginTop: 16 }} />
    </Dialog>
  );
}

// ProfileSettings sets a profile's name and, for a subscription, its link and how often it updates.
function ProfileSettings({ profile, onClose }: { profile: Profile; onClose: () => void }) {
  const [name, setName] = useState(profile.name);
  const [link, setLink] = useState(profile.url ?? "");
  const [hours, setHours] = useState(String(profile.interval));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const remote = !!profile.url;
  const ready = !!name.trim() && (!remote || (/^https?:\/\/\S+$/.test(link.trim()) && /^\d+$/.test(hours)));
  const save = () => {
    setBusy(true);
    setError("");
    ProfileService.Edit(profile.id, name, link, Number(hours)).then(onClose, (e) => setError(errText(e, "保存失败"))).finally(() => setBusy(false));
  };
  return (
    <Dialog title="配置设置" className="narrow" locked={busy} onClose={onClose} foot={<>
      <button className="btn" onClick={onClose}>取消</button>
      <button className="btn primary" disabled={!ready || busy} onClick={save}>完成</button>
    </>}>
      <div className="form-grid">
        <label>名称</label><input autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        {remote && <>
          <label>订阅链接</label><input {...plain} value={link} onChange={(e) => setLink(e.target.value)} />
          <label>自动更新</label>
          <div className="inline"><input value={hours} onChange={(e) => setHours(e.target.value)} style={{ width: 72 }} /><span className="muted small">小时一次，0 为不自动更新</span></div>
        </>}
        {error && <><span /><div className="small error">{error}</div></>}
      </div>
    </Dialog>
  );
}
