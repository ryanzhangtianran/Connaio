import { useCallback, useEffect, useState } from "react";
import { CoreService, DeviceService } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import type { Device, LanInfo, Status } from "../bindings/github.com/ryanzhangtianran/Connaio/internal/app";
import { sizeText } from "./format";
import { Switch } from "./Settings";
import { usePoll } from "./hooks";

function PolicyMenu({ device, policies, onDone }: { device: Device; policies: string[]; onDone: () => void }) {
  const [policy, setPolicy] = useState(device.policy || "DIRECT");
  const [permanent, setPermanent] = useState(!device.temporary && !!device.policy);
  return (
    <div className="menu members detail" onClick={(e) => e.stopPropagation()}>
      <div className="detail-head">{device.ip}{device.mac && ` · ${device.mac}`} · {device.connections} 个连接</div>
      <div className="detail-row"><span>上传 {sizeText(device.upload)}</span><span>下载 {sizeText(device.download)}</span></div>
      <div className="detail-row" style={{ alignItems: "center" }}>
        <span>策略</span>
        <select value={policy} onChange={(e) => setPolicy(e.target.value)}>{policies.map((p) => <option key={p}>{p}</option>)}</select>
      </div>
      <div className="detail-row">
        <label className="radio"><input type="radio" checked={!permanent} onChange={() => setPermanent(false)} /> 临时</label>
        <label className="radio"><input type="radio" checked={permanent} onChange={() => setPermanent(true)} /> 永久（写入覆写）</label>
      </div>
      <button onClick={() => DeviceService.SetPolicy(device.ip, policy, permanent).finally(onDone)}>应用：此设备全部走 {policy}</button>
      {device.policy && <button className="danger" onClick={() => DeviceService.ClearPolicy(device.ip).finally(onDone)}>恢复按规则分流</button>}
    </div>
  );
}

export default function Devices({ status }: { status: Status }) {
  const [lan, setLan] = useState<LanInfo | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [policies, setPolicies] = useState<string[]>([]);
  const [open, setOpen] = useState("");

  const load = useCallback(() => {
    DeviceService.Lan().then(setLan);
    DeviceService.Devices().then((d) => setDevices(d ?? []));
  }, []);

  usePoll(load, 2000, status.running);
  useEffect(() => { if (status.running) CoreService.Policies().then((p) => setPolicies(p ?? [])); }, [status.running]);

  return (
    <>
      <div className="group-title">局域网访问</div>
      <div className="plist">
        <div className="plist-row static">
          <div className="plist-main">
            <div className="plist-name">允许局域网设备连接</div>
            <div className="muted small">
              {lan?.allowLan
                ? <>在其他设备的 Wi-Fi 设置中把 HTTP 或 SOCKS5 代理设为 {(lan.addresses ?? []).join(" 或 ")} 端口 {lan.port}</>
                : "局域网内的手机、电脑等可以通过本机代理上网"}
            </div>
          </div>
          <Switch on={!!lan?.allowLan} disabled={!status.running} onChange={(on) => { if (lan) setLan({ ...lan, allowLan: on }); DeviceService.SetAllowLan(on).finally(load); }} />
        </div>
      </div>

      <div className="group-title">局域网设备</div>
      {devices.length === 0 && (
        <div className="plist">
          <div className="plist-row static">
            <div className="plist-main muted">{lan?.allowLan ? "还没有设备连接" : "未开启局域网访问"}</div>
          </div>
        </div>
      )}
      <div className="tiles">
        {devices.map((d) => (
          <div key={d.ip} className="tile-wrap">
            <button className={`tile ${open === d.ip ? "selected" : ""}`} onClick={() => setOpen(open === d.ip ? "" : d.ip)}>
              <span className="kind">{d.ip}{d.connections > 0 ? ` · ${d.connections} 个连接` : " · 离线"}</span>
              <span className="name">{d.name || d.mac || "未知设备"}</span>
              <span className="foot">{d.policy ? `全部走 ${d.policy}${d.temporary ? "（临时）" : ""}` : sizeText(d.upload + d.download)}</span>
            </button>
            {open === d.ip && (
              <PolicyMenu device={d} policies={policies} onDone={() => { setOpen(""); load(); }} />
            )}
          </div>
        ))}
      </div>
    </>
  );
}
