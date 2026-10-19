<h1 align="center">
  <img src="./build/appicon.png" alt="Connaio" width="128" />
  <br>
  Connaio
  <br>
</h1>

<h3 align="center">
基于 <a href="https://github.com/MetaCubeX/mihomo">mihomo</a> 的代理客户端，规则分流、VPN 与反向隧道，界面基于 <a href="https://github.com/wailsapp/wails">Wails 3</a>。
</h3>

## Preview

| Dark                             | Light                             |
| -------------------------------- | --------------------------------- |
| ![预览](./docs/preview_dark.png) | ![预览](./docs/preview_light.png) |

## Download

请到发布页面下载对应的安装包：[Release page](https://github.com/ryanzhangtianran/Connaio/releases)

支持 macOS 15+（Apple 芯片）、Windows 10 1809+ / 11（x64）和 Ubuntu 22.04+（x64）。使用方法见 [使用说明](./usage.md)。

---

## Features

- 内置 [connaio-core](https://github.com/ryanzhangtianran/connaio-core) 内核，基于 mihomo，增加了按进程接管、进程祖先规则和 VPN 出站。
- 三种接管方式：系统代理、增强模式（TUN 接管全部流量）、部分增强（只接管勾选的应用及其子进程）。
- VPN：EasyConnect（深信服 SSL VPN）、WireGuard、OpenVPN、Tailscale；只有网关下发或手动列出的网段和域名走 VPN，可指定在某些网络时直连。
- 反向隧道：FRP 和 SSH，可通过 SSH 一键在服务器上部署 frps；局域网设备可把本机当作代理或网关。
- 活动页：网络延迟、出口 IP（按节点或 DIRECT）、实时速度、今日流量、按应用的流量排行、进程监视、测速、DNS 泄漏检测、网络修复与诊断。
- 流量按应用归类：子进程和从终端启动的命令算到它所属的应用。
- 订阅与本地配置管理，覆写叠加在配置之上，订阅更新不会覆盖。
- 菜单栏 / 托盘：出站模式、策略组、VPN、网络质量、接管开关和配置切换。

## Contributions

Issue and PR welcome!

## Acknowledgement

Connaio was based on or inspired by these projects and so on:

- [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): A rule-based tunnel in Go.
- [clash-verge-rev/clash-verge-rev](https://github.com/clash-verge-rev/clash-verge-rev): A Clash Meta GUI based on Tauri.
- [wailsapp/wails](https://github.com/wailsapp/wails): Build desktop applications using Go and web technologies.
- [fatedier/frp](https://github.com/fatedier/frp): A fast reverse proxy.
- [vitejs/vite](https://github.com/vitejs/vite): Next generation frontend tooling.
- [Lucide](https://lucide.dev): The sidebar icons.
- [Maple Mono](https://github.com/subframe7536/maple-font): The monospace font in the logs and editors.

## Privacy

Connaio 不收集任何用户数据，配置、日志和流量统计仅保存在本地。App 只在以下情况访问外部服务：测量延迟（gstatic.com、captive.apple.com）、查询出口 IP（ip-api.com、ipify.org）、测速（speed.cloudflare.com）、DNS 泄漏检测（bash.ws），以及手动检查更新（api.github.com）。

## License

GPL-3.0 License. See [License here](./LICENSE) for details.
