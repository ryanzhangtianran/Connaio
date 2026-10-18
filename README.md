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

## Install

第一次使用请看 [使用说明](./usage.md)。

请到发布页面下载对应的安装包：[Release page](https://github.com/ryanzhangtianran/Connaio/releases)

支持 macOS 15+（Apple 芯片）、Windows 10 1809+ / 11（x64）和 Ubuntu 22.04+（x64）。

| 平台 | 文件 | 安装 |
| :-- | :-- | :-- |
| macOS | `Connaio-<版本>-macos-arm64.dmg` | 打开 DMG，把 Connaio 拖到 Applications |
| Windows | `Connaio-<版本>-windows-amd64.exe` | 运行安装器 |
| Linux | `Connaio-<版本>-linux-amd64.deb` | `sudo apt install ./Connaio-<版本>-linux-amd64.deb` |

#### 安装说明

- **macOS**：App 没有经过 Apple 公证，首次打开前在终端执行一次 `xattr -cr /Applications/Connaio.app`。
- **增强模式（TUN）**：需要先在「通用」中授予 root 权限：macOS 在「系统设置 → 通用 → 登录项与扩展」中允许 Connaio 在后台运行一次，内核随 App 一起更新，之后不再需要密码；Windows 一次 UAC 确认，内核装成 Windows 服务，Wintun 驱动随安装包附带；Linux 通过 polkit 授权，内核装成 systemd 服务。
- **Linux 托盘**：需要桌面支持 AppIndicator（Ubuntu 自带的 GNOME 默认开启）。

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

### FAQ

**macOS 提示「已损坏，无法打开」？** App 没有经过 Apple 公证，在终端执行 `xattr -cr /Applications/Connaio.app` 后再打开。

**开启增强模式提示没有权限？** 先在「通用 → root 权限」中授予，内核会以系统服务运行。macOS 上内核随 App 一起更新，不会再提示；Windows 和 Linux 上更新 App 后如果内核有变化，会再提示授予一次。

**流量排行里某个命令没有归到它的应用？** 内核在连接建立时读取进程的祖先链来归类；macOS 上实在找不到所属应用的命令会合并到 macOS 一项。Windows 的内核暂不支持读取祖先链，改为按进程名查找，退出很快的进程可能无法归类。

## Development

需要 Go 1.26、Node 24，以及与 `go.mod` 相同版本的 Wails CLI，装在仓库内：

```shell
GOBIN=$PWD/bin/tools go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28
export PATH=$PWD/bin/tools:$PATH
```

内核源码默认在 `../connaio-core`，可用 `CORE_DIR=...` 覆盖。

```shell
wails3 dev                         # 开发模式（前端热更新）
wails3 task darwin:package:dmg     # macOS：bin/Connaio.app 和 DMG；钥匙串里有 Apple Development 证书时用它签名，否则 ad-hoc
wails3 task linux:package          # Linux（Ubuntu 22.04）：bin/connaio.deb
wails3 task windows:package        # Windows：bin/Connaio-<版本>-windows-amd64.exe，需要 makensis；不依赖 cgo，在 macOS 上也能交叉编译
```

`build/preview/render.sh` 用演示数据在无界面 Chrome 中渲染活动页，更新上面的预览图（先构建前端）。

推送 `v*` 标签时，GitHub Actions 在三个平台上构建并把安装包附到对应的 Release。

### 结构

- `internal/app`：暴露给前端的服务（内核控制、配置、增强模式、设置、设备）、菜单栏、流量账本、诊断与测速
- `internal/core`：以子进程启动内核，或连接已安装的 root 内核，经 Unix socket 访问 REST API
- `internal/daemon`：安装 / 卸载 root 内核（macOS LaunchDaemon、Linux systemd、Windows 服务），增强模式需要它
- `internal/profile`：订阅与本地配置的存储（`profile.go`）、合并成内核配置（`merge.go`）、编辑覆写（`override.go`）
- `internal/sysproxy`：系统代理
- `frontend/src`：各页面，`frontend/bindings` 由 `wails3 generate bindings` 生成

macOS 上的运行数据（`config.yaml`、`core.log`、`cache.db`、`traffic.json`、`profiles/`）在 `~/Library/Application Support/Connaio/`；root 内核的数据在 `/Library/Application Support/Connaio Core/`。

### 覆写

`profiles/override.yaml` 叠加在当前配置之上，订阅更新不会覆盖它。按顺序合并：

1. 普通键逐层合并：字典按键合并，其他值直接替换
2. `prepend-` / `append-` 加 `rules`、`proxies`、`proxy-groups`：放到配置里对应列表的前 / 后
3. `remove-rules`：删除配置里文本完全相同的规则
4. `group-members`（策略组 → 节点列表）：把节点放到已有策略组的最前
5. `patch-proxies`（节点 → 字段）：把字段合并到该节点上，值为 `null` 表示删除该字段
6. `remove-proxies` / `remove-proxy-groups`：按名字删除，并清理所有引用（空了的策略组回落到 DIRECT，删掉指向它们的规则）

`reverse-tunnels`（反向隧道）和 `capture`（流量接入方式）是内核直接读取的普通键。GUI 编辑覆写时保留用户的注释和格式。

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
