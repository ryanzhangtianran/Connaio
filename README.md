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
- 反向隧道：WireGuard，由一个密码生成两端密钥；服务器端程序经 SSH 一键安装，无需 root；局域网设备可把本机当作代理或网关。
- 活动页：网络延迟、出口 IP（按节点或 DIRECT）、实时速度、今日流量、按应用的流量排行、进程监视、测速、DNS 泄漏检测、网络修复与诊断。
- 流量按应用归类：子进程和从终端启动的命令算到它所属的应用。
- 订阅与本地配置管理，覆写叠加在配置之上，订阅更新不会覆盖。
- 菜单栏 / 托盘：出站模式、策略组、VPN、网络质量、接管开关和配置切换。

## 无界面版（Linux）

不带窗口的 Connaio，界面在浏览器中打开，适合服务器、NAS 等没有桌面的 Linux（amd64 / arm64，无需图形库）。一行安装：

```sh
curl -fsSL https://connaio-dl.ryanzhangtianran.top/install.sh | sudo sh
```

- 安装到 `/opt/connaio`，以 systemd 服务 `connaio` 运行，数据在 `/var/lib/connaio-web`；结束时输出带访问令牌的地址，首次用它打开即可登录。再次运行即更新。
- 首次安装且在终端中运行时，会提示输入订阅地址以导入配置，可留空跳过。
- 未使用 systemd 的系统（如容器）由 `/etc/init.d/connaio start|stop|restart|status` 管理，日志位于 `/var/log/connaio.log`。容器重启后服务不会自动启动，请将 `/etc/init.d/connaio start` 加入容器的启动命令。
- 默认只监听本机，可经 `ssh -L 9090:127.0.0.1:9090 服务器` 访问。如需通过公网 IP 直接访问，安装时指定监听地址：`curl -fsSL https://connaio-dl.ryanzhangtianran.top/install.sh | sudo CONNAIO_LISTEN=0.0.0.0:9090 sh`；该地址保存在 `/etc/default/connaio`，更新时保留，修改后重启服务生效。此方式连接未加密。
- 推荐使用 HTTPS：将域名解析到本机并放行 443 端口，在安装时的提示中输入该域名（或安装时指定 `CONNAIO_DOMAIN=connaio.example.com`），即可通过 `https://connaio.example.com` 访问。没有域名时可使用 `<公网 IP>.sslip.io`。证书自动从 Let's Encrypt 申请并续期，保存在数据目录中。域名不能经过 CDN 代理（如 Cloudflare 的代理模式），否则无法完成证书验证。
- 部分 TUN：只接管选中的程序及其子进程，不读代理变量的程序（如 ssh）也能走代理，不影响登录本机的 SSH 连接。可在网页的 TUN 页面、管理菜单或命令行（`connaio partial on`、`connaio partial add python`）中设置。
- 让单条命令经代理联网，无需 export：`connaio run curl https://example.com`（为该命令设置 `http_proxy`、`https_proxy`、`all_proxy`，对读取这些变量的程序有效）。
- 以 root 运行，可开启 TUN 模式（接管本机全部流量）；系统代理、开机启动、托盘等桌面功能不提供。
- 也可从 Releases 下载 `Connaio-<版本>-linux-<架构>.tar.gz` 手动运行：`./connaio --listen 127.0.0.1:9090`。
- 在终端中直接运行 `connaio` 打开管理菜单：启停服务、查看日志、更新、切换节点与配置等，按编号选择。
- 命令行控制运行中的 Connaio。安装时执行 sudo 的用户会获得访问令牌（`~/.config/Connaio/web-token`），无需 sudo；其他用户需 sudo 或 `--token`：

  ```sh
  connaio status                 # 运行状态
  connaio mode global            # 出站模式：rule / global / direct
  connaio groups -a              # 策略组与节点；select <策略组> <节点> 切换，test <策略组> 测延迟
  connaio profiles               # 配置；profile use|update <名称>，profile add <URL>
  connaio tunnels                # 反向隧道；tunnel start|stop <名称>
  connaio help                   # 全部命令
  ```

## Contributions

Issue and PR welcome!

## Acknowledgement

Connaio was based on or inspired by these projects and so on:

- [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): A rule-based tunnel in Go.
- [clash-verge-rev/clash-verge-rev](https://github.com/clash-verge-rev/clash-verge-rev): A Clash Meta GUI based on Tauri.
- [wailsapp/wails](https://github.com/wailsapp/wails): Build desktop applications using Go and web technologies.
- [vitejs/vite](https://github.com/vitejs/vite): Next generation frontend tooling.
- [Lucide](https://lucide.dev): The sidebar icons.
- [Maple Mono](https://github.com/subframe7536/maple-font): The monospace font in the logs and editors.

## Privacy

Connaio 不收集任何用户数据，配置、日志和流量统计仅保存在本地。App 只在以下情况访问外部服务：测量延迟（gstatic.com、captive.apple.com）、查询出口 IP（ip-api.com、ipify.org）、测速（speed.cloudflare.com）、DNS 泄漏检测（bash.ws），以及手动检查更新（api.github.com）。

## License

GPL-3.0 License. See [License here](./LICENSE) for details.
