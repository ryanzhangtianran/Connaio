#!/bin/sh
# Installs Connaio's headless build on Linux (amd64, arm64), its UI in a browser: the latest release
# into /opt/connaio, run as the systemd service connaio on 127.0.0.1:9090, and prints the address to
# sign in with. Again, it updates it, keeping the data in /var/lib/connaio-web.
#   curl -fsSL https://raw.githubusercontent.com/ryanzhangtianran/Connaio/main/install.sh | sudo sh
# CONNAIO_TARBALL=<path> installs that tarball instead of downloading one.
set -eu
repo=ryanzhangtianran/Connaio
dir=/opt/connaio
data=/var/lib/connaio-web

fail() { echo "connaio: $*" >&2; exit 1; }
[ "$(uname -s)" = Linux ] || fail "仅支持 Linux"
[ "$(id -u)" = 0 ] || fail "需要 root：curl -fsSL https://raw.githubusercontent.com/$repo/main/install.sh | sudo sh"
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "不支持的架构：$(uname -m)" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tarball=${CONNAIO_TARBALL:-}
if [ -z "$tarball" ]; then
  # the latest release's tag, from where GitHub redirects /releases/latest
  tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest" | sed 's|.*/||')
  case "$tag" in v*) ;; *) fail "无法获取最新版本" ;; esac
  echo "下载 Connaio ${tag#v}（linux-$arch）…"
  curl -fL --progress-bar -o "$tmp/connaio.tar.gz" "https://github.com/$repo/releases/download/$tag/Connaio-${tag#v}-linux-$arch.tar.gz"
  tarball=$tmp/connaio.tar.gz
fi

if command -v systemctl >/dev/null 2>&1; then systemctl stop connaio 2>/dev/null || true; fi
mkdir -p "$dir" "$data"
chmod 700 "$data"
tar -xzf "$tarball" -C "$dir"
# a desktop install's /usr/bin/connaio is the app with a window: left as the command it is
[ -e /usr/bin/connaio ] || { mkdir -p /usr/local/bin && ln -sf "$dir/connaio" /usr/local/bin/connaio; }

if ! command -v systemctl >/dev/null 2>&1 || [ ! -d /run/systemd/system ]; then
  echo "已安装到 $dir。没有 systemd，请手动运行：$dir/connaio --listen 127.0.0.1:9090 --data-dir $data"
  exit 0
fi
install -m 644 "$dir/connaio.service" /etc/systemd/system/connaio.service
systemctl daemon-reload
systemctl enable --now connaio >/dev/null 2>&1 || systemctl enable --now connaio
i=0
while [ ! -s "$data/web-token" ] && [ $i -lt 30 ]; do sleep 1; i=$((i + 1)); done
[ -s "$data/web-token" ] || fail "服务没有启动，请查看：journalctl -u connaio"

echo
echo "Connaio 已安装并以服务 connaio 运行。"
echo "  打开：http://127.0.0.1:9090/?token=$(cat "$data/web-token")"
echo "  从其他电脑访问：ssh -L 9090:127.0.0.1:9090 <这台机器>，再在本机浏览器打开上面的地址"
echo "  查看状态：systemctl status connaio；卸载：systemctl disable --now connaio && rm -rf $dir /etc/systemd/system/connaio.service"
