#!/bin/sh
# Installs Connaio's headless build on Linux (amd64, arm64), its UI in a browser: the latest release
# into /opt/connaio, run as the systemd service connaio on 127.0.0.1:9090 (without systemd, as in a
# container, by /etc/init.d/connaio), and prints the address to sign in with. Again, it updates it, keeping the data in /var/lib/connaio-web.
#   curl -fsSL https://connaio-dl.ryanzhangtianran.top/install.sh | sudo sh
# From the site: its latest names the version (v1.2.3), <tag>/ holds that version's tarballs.
# CONNAIO_TARBALL=<path> installs that tarball instead of downloading one.
# CONNAIO_LISTEN=0.0.0.0:9090 serves the UI on every address, CONNAIO_DOMAIN=example.com over HTTPS
# on :443 too, a certificate from Let's Encrypt (both kept in /etc/default/connaio, which a reinstall
# without them keeps; CONNAIO_DOMAIN= with nothing turns HTTPS off).
set -eu
site=https://connaio-dl.ryanzhangtianran.top
dir=/opt/connaio
data=/var/lib/connaio-web
conf=/etc/default/connaio

fail() {
  echo "connaio: $*" >&2
  exit 1
}
[ "$(uname -s)" = Linux ] || fail "仅支持 Linux"
[ "$(id -u)" = 0 ] || fail "需要 root：curl -fsSL $site/install.sh | sudo sh"
# line is a rule 72 wide; the text between rules is kept within it
line() { echo "------------------------------------------------------------------------"; }
# opening is the rule above a section, but for one closed right above it: no two rules in a row
opening() { [ -n "${closed:-}" ] || line; closed=; }
closing() { line; closed=1; }
# domain_ok says $1 is a domain name only: the init script sources the file it goes into, and a
# scheme or path never matches the certificate
domain_ok() { case "$1" in *[!A-Za-z0-9.-]*) return 1 ;; esac; }
# checked before anything changes
domain_ok "${CONNAIO_DOMAIN:-}" || fail "域名格式无效：${CONNAIO_DOMAIN}，只填写域名，如 connaio.example.com"
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
  echo "下载 Connaio ${tag#v}（linux-${arch}）…"
  curl -fL --progress-bar -o "$tmp/connaio.tar.gz" "$site/$tag/Connaio-${tag#v}-linux-$arch.tar.gz"
  tarball=$tmp/connaio.tar.gz
fi

systemd=
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then systemd=1; fi
if [ -n "$systemd" ]; then
  systemctl stop connaio 2>/dev/null || true
elif [ -x /etc/init.d/connaio ]; then /etc/init.d/connaio stop >/dev/null; fi
mkdir -p "$dir" "$data"
chmod 700 "$data"
tar -xzf "$tarball" -C "$dir"
# a desktop install's /usr/bin/connaio is the app with a window: left as the command it is
[ -e /usr/bin/connaio ] || { mkdir -p /usr/local/bin && ln -sf "$dir/connaio" /usr/local/bin/connaio; }

# the listen address and the HTTPS domain: the ones given, else the ones kept; this machine only
kept() { sed -n "s/^$1=//p" "$conf" 2>/dev/null | tr -d "\"'"; }
listen=${CONNAIO_LISTEN:-$(kept CONNAIO_LISTEN)}
listen=${listen:-127.0.0.1:9090}
domain=${CONNAIO_DOMAIN-$(kept CONNAIO_DOMAIN)}
# never asked (no CONNAIO_DOMAIN line kept, even an empty one), on a terminal, none given: asked
# whether to serve HTTPS (curl | sh reads the script on stdin: answers come from /dev/tty)
if ! grep -q '^CONNAIO_DOMAIN=' "$conf" 2>/dev/null && [ -z "${CONNAIO_DOMAIN+set}" ] && (: </dev/tty) 2>/dev/null; then
  opening
  echo "HTTPS 访问"
  line
  echo "如需在浏览器中通过公网访问网页界面，请输入已解析到本机的域名，"
  echo "将自动申请 HTTPS 证书（需放行 443 端口）。"
  echo "没有域名时，可填写 <本机公网 IP>.sslip.io。"
  echo "留空则仅允许本机访问，可通过 SSH 隧道使用。"
  while :; do
    printf "域名（留空跳过）："
    read -r domain </dev/tty || domain=
    domain_ok "$domain" && break
    echo "域名格式无效，只填写域名，如 connaio.example.com。"
  done
  closing
fi
mkdir -p "${conf%/*}"
cat >"$conf" <<EOF
# Connaio 的网页界面设置，修改后重启服务生效。
# 监听地址：0.0.0.0:9090 表示允许从其他电脑访问
CONNAIO_LISTEN=$listen
# HTTPS 域名：设置后在 443 端口提供 HTTPS，证书自动从 Let's Encrypt 申请；需先将域名解析到本机
CONNAIO_DOMAIN=$domain
EOF
port=${listen##*:} host=${listen%:*}
case "$host" in "" | 0.0.0.0 | "[::]") public=1 ;; *) public= ;; esac
case "$host" in "" | 0.0.0.0 | "[::]" | 127.0.0.1 | localhost | "[::1]") host=127.0.0.1 ;; esac

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
while [ ! -s "$data/web-token" ] && [ $i -lt 30 ]; do
  sleep 1
  i=$((i + 1))
done
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

# row prints a command and what it does, aligned: a CJK character is 3 bytes but 2 columns wide
row() {
  w=$(printf %s "$1" | LC_ALL=C tr -d '\000-\177' | wc -c)
  printf "  %-$((46 + w / 3))s%s\n" "$1" "$2"
}

# the first install, run on a terminal: a subscription asked for, so it starts with a profile in use
# (curl | sh reads the script on stdin: answers come from /dev/tty, and CI has none)
# (the token is written before the UI answers: asked until it does, else a reinstall would ask too)
i=0
until profiles=$("$dir/connaio" profiles --data-dir "$data" 2>/dev/null); do
  profiles=unknown
  [ $i -lt 30 ] || break
  sleep 1
  i=$((i + 1))
done
if [ -z "$profiles" ] && (: </dev/tty) 2>/dev/null; then
  opening
  echo "导入配置"
  line
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
    # stored before the core failed to load it: asking again would add it twice
    if [ -n "$("$dir/connaio" profiles --data-dir "$data" 2>/dev/null)" ]; then
      echo "订阅已保存，但内核加载配置失败。"
      echo "请稍后执行 $cli status 查看，或查看日志：$logs"
      break
    fi
    echo "订阅添加失败，请检查地址后重新输入。"
  done
  closing
fi

opening
if [ -n "$systemd" ]; then
  echo "Connaio 安装完成，已作为 systemd 服务 connaio 运行。"
else
  echo "Connaio 安装完成，已在后台运行（由 /etc/init.d/connaio 管理）。"
fi
line
echo "网页界面"
token=$(cat "$data/web-token")
if [ -n "$domain" ]; then
  echo "  访问地址：https://$domain/?token=$token"
  echo "  注意：需将域名解析到本机并放行 443 端口，首次访问时自动申请证书。"
  if [ -n "$public" ]; then
    echo "  注意：$port 端口仍以明文 HTTP 对外开放，建议将监听地址改回本机。"
  fi
elif [ -n "$public" ]; then
  echo "  访问地址：http://<服务器 IP>:$port/?token=$token"
  echo "  注意：需放行 $port 端口。连接未加密，建议设置域名以启用 HTTPS。"
elif [ "$host" = 127.0.0.1 ]; then
  echo "  访问地址：http://127.0.0.1:$port/?token=$token"
  echo "  远程访问：在本地电脑执行 ssh -L $port:127.0.0.1:$port <服务器>，"
  echo "            再在浏览器中打开上述地址。"
else
  echo "  访问地址：http://$host:$port/?token=$token"
fi
echo "  设置文件：${conf}（监听地址与域名，修改后重启服务生效）"
line
echo "常用命令"
# a desktop install's connaio is the app with a window, which fails with no display
if [ -e /usr/bin/connaio ]; then
  echo "  注意：本机的 connaio 是桌面版，请改用 $dir/connaio。"
fi
row "$cli" "打开管理菜单"
row "$cli status" "查看运行状态"
row "$cli profile add <订阅地址> [名称]" "添加订阅"
row "$cli profiles" "查看配置列表"
row "$cli profile use|update <名称>" "切换或更新配置"
row "$cli mode rule|global|direct" "切换出站模式"
row "$cli groups" "查看策略组及当前节点"
row "$cli select <策略组> <节点>" "切换节点"
row "$cli enhanced on|off" "开启或关闭 TUN 模式"
row "$cli url" "显示网页界面地址"
row "$cli run <命令>" "经代理运行该命令"
row "$cli help" "查看全部命令"
line
echo "服务管理"
if [ -n "$systemd" ]; then
  row "systemctl start|stop|restart connaio" "启动、停止或重启服务"
  row "systemctl status connaio" "查看服务状态"
  row "journalctl -u connaio -f" "查看日志"
  echo "  卸载（保留 $data 中的配置数据）："
  echo "    systemctl disable --now connaio"
  echo "    rm -rf $dir $conf"
  echo "    rm -f /etc/systemd/system/connaio.service"
else
  row "/etc/init.d/connaio start|stop|restart" "启动、停止或重启服务"
  row "/etc/init.d/connaio status" "查看服务状态"
  row "tail -f /var/log/connaio.log" "查看日志"
  echo "  卸载（保留 $data 中的配置数据）："
  echo "    /etc/init.d/connaio stop"
  echo "    rm -rf $dir $conf /etc/init.d/connaio"
  echo "  注意：容器重启后服务不会自动启动，"
  echo "        请将 /etc/init.d/connaio start 加入容器的启动命令。"
fi
line
echo "再次运行本安装命令即可更新 Connaio。"
line
