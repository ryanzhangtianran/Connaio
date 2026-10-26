#!/bin/sh
# Installs Connaio's headless build on Linux (amd64, arm64), its UI in a browser: the latest release
# into /opt/connaio, run as the systemd service connaio on 127.0.0.1:9090 (without systemd, as in a
# container, by /etc/init.d/connaio), and prints the address to sign in with. Again, it updates it, keeping the data in /var/lib/connaio-web.
#   curl -fsSL https://connaio-dl.ryanzhangtianran.top/install.sh | sudo sh
# From the site: its latest names the version (v1.2.3), <tag>/ holds that version's tarballs.
# CONNAIO_TARBALL=<path> installs that tarball instead of downloading one.
set -eu
site=https://connaio-dl.ryanzhangtianran.top
dir=/opt/connaio
data=/var/lib/connaio-web

fail() { echo "connaio: $*" >&2; exit 1; }
[ "$(uname -s)" = Linux ] || fail "仅支持 Linux"
[ "$(id -u)" = 0 ] || fail "需要 root：curl -fsSL $site/install.sh | sudo sh"
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "不支持的架构：$(uname -m)" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tarball=${CONNAIO_TARBALL:-}
if [ -z "$tarball" ]; then
  tag=$(curl -fsSL "$site/latest" | tr -d '[:space:]')
  case "$tag" in v*) ;; *) fail "无法获取最新版本" ;; esac
  echo "下载 Connaio ${tag#v}（linux-$arch）…"
  curl -fL --progress-bar -o "$tmp/connaio.tar.gz" "$site/$tag/Connaio-${tag#v}-linux-$arch.tar.gz"
  tarball=$tmp/connaio.tar.gz
fi

systemd=
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then systemd=1; fi
if [ -n "$systemd" ]; then systemctl stop connaio 2>/dev/null || true
elif [ -x /etc/init.d/connaio ]; then /etc/init.d/connaio stop >/dev/null; fi
mkdir -p "$dir" "$data"
chmod 700 "$data"
tar -xzf "$tarball" -C "$dir"
# a desktop install's /usr/bin/connaio is the app with a window: left as the command it is
[ -e /usr/bin/connaio ] || { mkdir -p /usr/local/bin && ln -sf "$dir/connaio" /usr/local/bin/connaio; }

if [ -n "$systemd" ]; then
  install -m 644 "$dir/connaio.service" /etc/systemd/system/connaio.service
  systemctl daemon-reload
  systemctl enable --now connaio >/dev/null 2>&1 || systemctl enable --now connaio
  logs="journalctl -u connaio"
else
  install -m 755 "$dir/connaio.init" /etc/init.d/connaio
  /etc/init.d/connaio start >/dev/null
  logs="tail /var/log/connaio.log"
fi
i=0
while [ ! -s "$data/web-token" ] && [ $i -lt 30 ]; do sleep 1; i=$((i + 1)); done
[ -s "$data/web-token" ] || fail "服务未能启动，请查看日志：$logs"

# the user who ran sudo gets the token too, in their own config directory (where connaio's commands
# look after the service's), so the commands work for them without sudo
cli=connaio # run as root itself, as in a container: no sudo needed (nor there, often)
if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ]; then cli="sudo connaio"; fi
if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ] && command -v getent >/dev/null 2>&1; then
  home=$(getent passwd "$SUDO_USER" | cut -d: -f6)
  if [ -n "$home" ] && [ -d "$home" ] && sudo -u "$SUDO_USER" mkdir -p "$home/.config/Connaio"; then
    install -m 600 -o "$SUDO_USER" "$data/web-token" "$home/.config/Connaio/web-token" && cli=connaio
  fi
fi

# the first install, run on a terminal: a subscription asked for, so it starts with a profile in use
# (curl | sh reads the script on stdin: answers come from /dev/tty, and CI has none)
# (the token is written before the UI answers: asked until it does, else a reinstall would ask too)
i=0
until profiles=$("$dir/connaio" profiles --data-dir "$data" 2>/dev/null); do
  profiles=unknown
  [ $i -lt 30 ] || break
  sleep 1; i=$((i + 1))
done
if [ -z "$profiles" ] && (: </dev/tty) 2>/dev/null; then
  echo
  echo "【导入配置】"
  echo "当前没有任何配置。可在此输入订阅地址，也可稍后在网页界面中添加。"
  while :; do
    printf "订阅地址（留空跳过）："
    read -r url </dev/tty || url=
    [ -n "$url" ] || break
    printf "配置名称（留空则使用订阅域名）："
    read -r name </dev/tty || name=
    echo "正在下载订阅……"
    # no name, no argument: the CLI names it after the host
    if "$dir/connaio" profile add --data-dir "$data" "$url" ${name:+"$name"}; then
      echo "订阅已添加并启用。"
      break
    fi
    echo "订阅添加失败，请检查地址后重新输入。"
  done
fi

# row prints a command and what it does, aligned: a CJK character is 3 bytes but 2 columns wide
row() {
  w=$(printf %s "$1" | LC_ALL=C tr -d '\000-\177' | wc -c)
  printf "  %-$((46 + w / 3))s%s\n" "$1" "$2"
}
echo
if [ -n "$systemd" ]; then
  echo "Connaio 安装完成，已作为 systemd 服务 connaio 运行。"
else
  echo "Connaio 安装完成，已在后台运行（当前系统未使用 systemd，服务由 /etc/init.d/connaio 管理）。"
fi
echo
echo "【网页界面】"
echo "  访问地址：http://127.0.0.1:9090/?token=$(cat "$data/web-token")"
echo "  远程访问：在本地电脑执行 ssh -L 9090:127.0.0.1:9090 <服务器>，然后在浏览器中打开上述地址。"
echo
echo "【常用命令】"
row "$cli" "打开管理菜单"
row "$cli status" "查看运行状态"
row "$cli profile add <订阅地址> [名称]" "添加订阅"
row "$cli profiles" "查看配置列表"
row "$cli profile use|update <名称>" "切换或更新配置"
row "$cli mode rule|global|direct" "切换出站模式"
row "$cli groups" "查看策略组及当前节点"
row "$cli select <策略组> <节点>" "切换节点"
row "$cli enhanced on|off" "开启或关闭增强模式（TUN）"
row "$cli url" "显示网页界面地址"
row "$cli help" "查看全部命令"
echo
echo "【服务管理】"
if [ -n "$systemd" ]; then
  row "systemctl start|stop|restart connaio" "启动、停止或重启服务"
  row "systemctl status connaio" "查看服务状态"
  row "journalctl -u connaio -f" "查看日志"
  echo "  卸载（保留 $data 中的配置数据）："
  echo "    systemctl disable --now connaio && rm -rf $dir /etc/systemd/system/connaio.service"
else
  row "/etc/init.d/connaio start|stop|restart" "启动、停止或重启服务"
  row "/etc/init.d/connaio status" "查看服务状态"
  row "tail -f /var/log/connaio.log" "查看日志"
  echo "  卸载（保留 $data 中的配置数据）："
  echo "    /etc/init.d/connaio stop && rm -rf $dir /etc/init.d/connaio"
  echo
  echo "注意：容器重启后服务不会自动启动，请将 /etc/init.d/connaio start 加入容器的启动命令。"
fi
echo
echo "再次运行本安装命令即可更新 Connaio。"
