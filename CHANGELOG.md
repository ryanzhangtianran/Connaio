# Connaio 版本历史

历史补丁按系列合并在下面。每组末尾的链接指向该系列最后一个正式发布，可下载对应的安装包。

## 2.2 系列 · 实时连接与配置可靠性（2.2.0–2.2.1）

- 命令行菜单的「连接」页约每秒自动更新，尽量保留滚动位置。离开后停止刷新。
- 命令行菜单的选中项改为整行高亮，不再显示箭头，各项文字左对齐。
- TUN 模式切换统一写入设置、校验和重载配置。操作失败时恢复先前的设置、覆写和磁盘启动配置。
- 桌面端共用连接视图、出口 IP 查询和隧道路由展示。服务端统一管理连接快照与流量采样。

[下载 2.2.1](https://github.com/ryanzhangtianran/Connaio/releases/tag/v2.2.1)

## 2.1 系列 · Linux 命令行管理（2.1.0–2.1.9）

- Linux 无界面版改用命令行和全屏管理菜单。菜单支持节点选择与测速、配置管理、VPN 登录与断开、TUN 设置及服务管理。
- 支持编辑订阅地址、删除配置和按 Linux 用户限制 TUN 接管范围。修复无界面版开启增强模式后未启用 TUN 的问题。
- 完善订阅内 VPN 的手动登录、Tailscale 节点命名和 VPN 网段的系统代理处理。

[下载 2.1.9](https://github.com/ryanzhangtianran/Connaio/releases/tag/v2.1.9)

## 2.0 系列 · 桌面版与 VPN（2.0.0–2.0.1）

- 改进 macOS 菜单栏的进程流量展示和刷新，避免菜单打开时的行数抖动和裁切。
- 完善订阅内 VPN 管理，配置页与规则保持同步。修复 Linux 用户安装下的服务管理。

[下载 2.0.1](https://github.com/ryanzhangtianran/Connaio/releases/tag/v2.0.1)

## 1.1–1.2 系列 · 安装与配置管理（1.1.0–1.2.0）

- 改进 Linux 安装界面和进度提示，支持从当前 shell 开启或关闭代理环境变量。
- 收紧订阅可下发的配置范围，修正规则编辑、流量账本保存和服务更新的边界情况。
- 将 `connaio-core` 纳入仓库的子模块，便于跟踪和构建配套内核。

[下载 1.2.0](https://github.com/ryanzhangtianran/Connaio/releases/tag/v1.2.0)

## 早期版本至 1.0.2 · 桌面与 Linux 无界面版

- 逐步提供规则分流、系统代理、TUN、VPN 和反向隧道，以及配置管理、连接与流量监视。
- Linux 无界面版支持指定监听地址、在无 systemd 环境运行，并提供 `connaio run` 和部分 TUN 管理命令。

[下载 1.0.2](https://github.com/ryanzhangtianran/Connaio/releases/tag/v1.0.2)
