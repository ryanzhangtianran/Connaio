#!/bin/sh
# Installs Connaio's headless build on Linux (amd64, arm64), driven by its command line: the latest
# release into /opt/connaio, run as the systemd service connaio on 127.0.0.1:9090 (without systemd, as
# in a container, by /etc/init.d/connaio). Again, it updates it, keeping the data in /var/lib/connaio-web.
#   curl -fsSL https://connaio-dl.ryanzhangtianran.top/install.sh | sudo sh
# From the site: its latest names the version (v1.2.3), <tag>/ holds that version's tarballs.
# Without sudo, it installs for this user alone: into ~/.local/share, run by systemctl --user (or in the
# background where there is none); no TUN mode, which is root's.
# CONNAIO_TARBALL=<path> installs that tarball instead of downloading one.
# CONNAIO_LISTEN=0.0.0.0:9090 takes the command line's calls on every address (kept in
# /etc/default/connaio, which a reinstall without it keeps).
set -eu
site=https://connaio-dl.ryanzhangtianran.top
if [ "$(id -u)" = 0 ]; then
  root=1 dir=/opt/connaio data=/var/lib/connaio-web conf=/etc/default/connaio
else
  # conf's directory is where connaio's commands look for the token and the listen address
  root= dir=$HOME/.local/share/connaio data=$HOME/.local/share/connaio-web conf=${XDG_CONFIG_HOME:-$HOME/.config}/Connaio/connaio.env
fi

# colours on a terminal only: CI and pipes get plain text
if [ -t 1 ] && [ "${TERM:-dumb}" != dumb ]; then
  esc=$(printf '\033')
  B="$esc[1m" A="$esc[36m" D="$esc[90m" G="$esc[32m" Y="$esc[33m" R="$esc[31m" N="$esc[0m"
else
  esc= B= A= D= G= Y= R= N=
fi
# on: the install runs on a screen of its own (the alternate screen, as vim's), redrawn after each
# step and when the window is resized; the summary is printed on the terminal once it is left
on= cur= q= rp= cp=
# say prints a progress line: a coloured icon and a two-character verb, so the messages line up
say() {
  if [ -n "$on" ]; then
    printf '%s\n' "$1$2 $3$N $4" >>"$tmp/steps"
    cur=
    draw
  else
    printf '%s\n' "$1$2 $3$N $4"
  fi
}
# doing shows the step under way on the screen, until the next say; off it, only a download's,
# above its progress bar
doing() {
  if [ -n "$on" ]; then
    cur="$A› $1$N $2"
    draw
  elif [ "$1" = 下载 ]; then
    say "$A" › "$1" "$2"
  fi
}
# more continues say's message on the next line, under it
more() {
  if [ -n "$on" ]; then
    printf '       %s\n' "$1" >>"$tmp/steps"
    draw
  else
    printf '       %s\n' "$1"
  fi
}
fail() {
  leave
  [ -z "${tmp:-}" ] || cat "$tmp/steps" 2>/dev/null || :
  say "$R" ✗ 错误 "$*" >&2
  exit 1
}
[ "$(uname -s)" = Linux ] || fail "仅支持 Linux"
# width is how many columns $1 takes on a terminal: colour codes none, the icons and the meter one, every other
# non-ASCII character here is CJK, 3 bytes and 2 columns
width() {
  set -- "$(printf %s "$1" | sed "s/✓/x/g;s/✗/x/g;s/›/x/g;s/━/x/g;s/─/x/g${esc:+;s/$esc\\[[0-9;]*m//g}")"
  b=$(printf %s "$1" | wc -c) n=$(printf %s "$1" | LC_ALL=C tr -d '\000-\177' | wc -c)
  echo $((b - n + n / 3 * 2))
}
# size is the window's: rows and cols; 24 by 80 without a terminal
size() {
  set -- $(stty size 2>/dev/null </dev/tty)
  rows=${1:-24} cols=${2:-80}
}
# meter is a bar 20 wide, filled to $1 percent, then the percent
meter() {
  f=$(($1 / 5))
  printf '%s%s%s%s%s %3d%%' "$A" "$(printf "%${f}s" "" | sed 's/ /━/g')" "$D" "$(printf "%$((20 - f))s" "" | sed 's/ /─/g')" "$N" "$1"
}
# sp prints $1 spaces
sp() { [ "$1" -le 0 ] || printf "%$1s" ""; }
# bl adds lines to the box box prints next
bl() { printf '%s\n' "$@" >>"$tmp/box"; }
# inner is how many columns a box holds in this window: 20 at least
inner() {
  size
  echo $((cols - 4 < 20 ? 20 : cols - 4))
}
# row adds a command and what it does, the descriptions in one column; in a narrow window, the
# description on a line of its own under the command
row() {
  if [ $((48 + $(width "$2"))) -le "$(inner)" ]; then
    bl "$1$(sp $((48 - $(width "$1"))))$D$2$N"
  else
    bl "$1" "    $D$2$N"
  fi
}
# kv adds a label and its value, the values in one column; in a narrow window, the value under it
kv() {
  if [ $((10 + $(width "$2"))) -le "$(inner)" ]; then
    bl "$D$1$N$(sp $((10 - $(width "$1"))))$2"
  else
    [ -z "$1" ] || bl "$D$1$N"
    bl "  $2"
  fi
}
# box frames bl's lines under a bold title, as wide as the window: a longer line of ASCII alone (a
# command, an address) folded onto the next, any other left to overflow; $2 pads it to that many lines
box() {
  bw=$(inner)
  bn=$(wc -l <"$tmp/box")
  while [ "$bn" -lt "${2:-0}" ]; do
    echo >>"$tmp/box"
    bn=$((bn + 1))
  done
  bar=$(printf "%$((bw + 2))s" "" | sed 's/ /─/g')
  printf '%s╭%s╮%s\n' "$D" "$bar" "$N"
  { printf '%s\n\n' "$B$A$1$N"; cat "$tmp/box"; } | while IFS= read -r l; do
    if [ "$(width "$l")" -gt "$bw" ] && [ "$(printf %s "$l" | LC_ALL=C tr -d '\000-\177')" = "" ]; then
      printf '%s\n' "$l" | fold -w "$bw"
    else
      printf '%s\n' "$l"
    fi
  done | while IFS= read -r l; do
    printf '%s│%s %s%s %s│%s\n' "$D" "$N" "$l" "$(sp $((bw - $(width "$l"))))" "$D" "$N"
  done
  printf '%s╰%s╯%s\n' "$D" "$bar" "$N"
  rm -f "$tmp/box"
}
# draw redraws the screen: the steps done, the one under way, and a question with its explanation;
# the last line is left for a download's progress bar
draw() {
  [ -n "$on" ] || return 0
  size
  {
    cat "$tmp/steps"
    [ -z "$cur" ] || printf '%s\n' "$cur"
    [ ! -s "$tmp/ask" ] || { echo; cat "$tmp/ask"; }
    [ -z "$q" ] || printf '%s\n' "$q"
  } | tail -n $((rows - 5)) >"$tmp/box"
  k=$(wc -l <"$tmp/box")
  printf '%s[H%s[2J' "$esc" "$esc"
  box "Connaio 安装" $((rows - 5))
  # the cursor at the question's end, for the answer
  if [ -n "$q" ]; then
    printf '%s[%d;%dH%s[?25h' "$esc" $((3 + k)) $((3 + $(width "$q"))) "$esc"
  else
    printf '%s[?25l' "$esc"
  fi
}
# enter goes to the install's screen, on a terminal; leave back, its steps left behind
enter() {
  [ -n "$esc" ] && (: </dev/tty) 2>/dev/null || return 0
  on=1
  : >"$tmp/steps"
  printf '%s[?1049h' "$esc"
  trap draw WINCH 2>/dev/null || :
  draw
}
leave() {
  [ -n "$on" ] || return 0
  on=
  trap - WINCH 2>/dev/null || :
  printf '%s[?25h%s[?1049l' "$esc" "$esc"
}
# explain puts a question's title and explanation ($2...) above it, until done; off the screen, in a box
explain() {
  t=$1
  shift
  if [ -n "$on" ]; then
    printf '%s\n' "$B$t$N" "$@" >"$tmp/ask"
  else
    for l; do bl "$l"; done
    box "$t"
  fi
}
# ask asks $1 and reads the answer from the terminal into $answer
ask() {
  q="$A?$N $1"
  if [ -n "$on" ]; then draw; else printf '%s' "$q"; fi
  # read in the background: a resize's trap runs only once wait returns, which it does for a signal,
  # where a read would carry on
  : >"$tmp/answer"
  (IFS= read -r a </dev/tty && printf '%s' "$a" >"$tmp/answer") &
  rp=$!
  while :; do
    st=0
    wait "$rp" || st=$?
    [ "$st" -gt 128 ] || break
  done
  rp=
  answer=$(cat "$tmp/answer")
  q=
  [ -z "$on" ] || printf '%s[?25l' "$esc"
}
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "不支持的架构：$(uname -m)" ;;
esac

tmp=$(mktemp -d)
# a question's read in the background ended too: left, it would take the next line typed in the shell
trap '[ -z "$rp" ] || kill "$rp" 2>/dev/null; [ -z "$cp" ] || kill "$cp" 2>/dev/null; leave; rm -rf "$tmp"' EXIT
trap 'exit 130' INT TERM HUP
enter
tarball=${CONNAIO_TARBALL:-}
if [ -z "$tarball" ]; then
  doing 查询 "最新版本"
  tag=$(curl -fsSL "$site/latest" | tr -d '[:space:]')
  case "$tag" in v*) ;; *) fail "无法获取最新版本" ;; esac
  doing 下载 "Connaio ${tag#v}（linux-$arch）"
  url="$site/$tag/Connaio-${tag#v}-linux-$arch.tar.gz"
  if [ -n "$on" ]; then
    # the progress in the box, on the step's line: curl's own bar can't be drawn in it, and would sit
    # on the screen's last line, apart from the step; read from what curl prints, every half second
    curl -fL --progress-bar -o "$tmp/connaio.tar.gz" "$url" 2>"$tmp/progress" &
    cp=$!
    while kill -0 "$cp" 2>/dev/null; do
      pct=$(tr '\r' '\n' <"$tmp/progress" | grep -o '[0-9]*\.[0-9]%' | tail -n 1)
      pct=${pct%%.*}
      doing 下载 "Connaio ${tag#v}（linux-$arch）  $(meter "${pct:-0}")"
      sleep 0.5
    done
    wait "$cp" || { cp= && fail "下载失败：$(tr '\r' '\n' <"$tmp/progress" | grep -v '#' | grep . | tail -n 1)"; }
    cp=
  else
    curl -fL --progress-bar -o "$tmp/connaio.tar.gz" "$url" || fail "下载失败"
  fi
  say "$G" ✓ 下载 "Connaio ${tag#v}（linux-$arch）"
  tarball=$tmp/connaio.tar.gz
fi

sc=systemctl
[ -n "$root" ] || sc="systemctl --user"
systemd=
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ] && $sc show-environment >/dev/null 2>&1; then systemd=1; fi
# pids is the user install's instance where there is no systemd --user: found by its command line
pids() {
  for p in /proc/[0-9]*; do
    case "$(tr '\0' ' ' 2>/dev/null <"$p/cmdline")" in "$dir/connaio --listen "*) echo "${p#/proc/}" ;; esac
  done
}
doing 安装 "$(printf %s "$dir" | sed "s|^$HOME/|~/|")"
if [ -n "$systemd" ]; then
  $sc stop connaio 2>/dev/null || true
elif [ -n "$root" ] && [ -x /etc/init.d/connaio ]; then
  /etc/init.d/connaio stop >/dev/null
elif [ -z "$root" ] && [ -n "$(pids)" ]; then
  kill $(pids)
  sleep 1
fi
mkdir -p "$dir" "$data"
chmod 700 "$data"
tar -xzf "$tarball" -C "$dir" || fail "解压失败：$tarball"
say "$G" ✓ 安装 "$(printf %s "$dir" | sed "s|^$HOME/|~/|")"
# connaio enable|disable export or unset the proxy variables in the shell itself, which a program
# can't: a function, so the shell evals what they print
shfn='connaio() { case "$1" in enable | disable) eval "$(command connaio "$@")" ;; *) command connaio "$@" ;; esac; }'
if [ -n "$root" ]; then
  # a desktop install's /usr/bin/connaio is the app with a window: left as the command it is
  # shortcut: profile.d is read by login shells only (as over ssh); a non-login bash reads ~/.bashrc
  [ -e /usr/bin/connaio ] || { mkdir -p /usr/local/bin && ln -sf "$dir/connaio" /usr/local/bin/connaio && echo "$shfn" >/etc/profile.d/connaio.sh; }
else
  mkdir -p "$HOME/.local/bin" && ln -sf "$dir/connaio" "$HOME/.local/bin/connaio"
  grep -qF "$shfn" "$HOME/.bashrc" 2>/dev/null || printf '\n# Connaio: connaio enable|disable\n%s\n' "$shfn" >>"$HOME/.bashrc"
fi

# the listen address: the one given, else the one kept; this machine only
kept() { sed -n "s/^$1=//p" "$conf" 2>/dev/null | tr -d "\"'"; }
listen=${CONNAIO_LISTEN:-$(kept CONNAIO_LISTEN)}
listen=${listen:-127.0.0.1:9090}
mkdir -p "${conf%/*}"
cat >"$conf" <<EOF
# Connaio 的服务设置，修改后重启服务生效。
# 监听地址：命令行经此控制 Connaio；0.0.0.0:9090 表示允许从其他电脑访问
CONNAIO_LISTEN=$listen
EOF
port=${listen##*:} host=${listen%:*}
case "$host" in "" | 0.0.0.0 | "[::]") public=1 ;; *) public= ;; esac

doing 启动 "服务"
if [ -n "$root" ] && [ -n "$systemd" ]; then
  install -m 644 "$dir/connaio.service" /etc/systemd/system/connaio.service
elif [ -n "$systemd" ]; then
  # the shipped unit has root's paths: this user's is written here, and an update leaves it be
  mkdir -p "$HOME/.config/systemd/user"
  cat >"$HOME/.config/systemd/user/connaio.service" <<EOF
[Unit]
Description=Connaio (this user's)

[Service]
Environment=CONNAIO_LISTEN=127.0.0.1:9090
EnvironmentFile=-$conf
ExecStart=$dir/connaio --listen \${CONNAIO_LISTEN} --data-dir $data
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
fi
if [ -n "$systemd" ]; then
  $sc daemon-reload
  $sc enable --now connaio >/dev/null 2>&1 || $sc enable --now connaio
  logs="journalctl -u connaio"
  [ -n "$root" ] || logs="journalctl --user-unit connaio"
elif [ -n "$root" ]; then
  install -m 755 "$dir/connaio.init" /etc/init.d/connaio
  /etc/init.d/connaio start >/dev/null
  logs="tail /var/log/connaio.log"
else
  setsid "$dir/connaio" --listen "$listen" --data-dir "$data" >>"$data/connaio.log" 2>&1 </dev/null &
  logs="tail $data/connaio.log"
fi
i=0
while [ ! -s "$data/web-token" ] && [ $i -lt 30 ]; do
  sleep 1
  i=$((i + 1))
done
[ -s "$data/web-token" ] || fail "服务未能启动，请查看日志：$logs"

# the user who ran sudo gets the token too, in their own config directory (where connaio's commands
# look after the service's), so the commands work for them without sudo
cli=connaio # run as root itself, as in a container, or this user's own: no sudo needed
if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ]; then cli="sudo connaio"; fi
[ -n "$root" ] || install -m 600 "$data/web-token" "${conf%/*}/web-token"
if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ] && command -v getent >/dev/null 2>&1; then
  home=$(getent passwd "$SUDO_USER" | cut -d: -f6)
  if [ -n "$home" ] && [ -d "$home" ] && sudo -u "$SUDO_USER" mkdir -p "$home/.config/Connaio"; then
    install -m 600 -o "$SUDO_USER" "$data/web-token" "$home/.config/Connaio/web-token" && cli=connaio
  fi
fi

# the first install, run on a terminal: a subscription asked for, so it starts with a profile in use
# (curl | sh reads the script on stdin: answers come from /dev/tty, and CI has none)
# (the token is written before the service answers: asked until it does, else a reinstall would ask too)
i=0
until profiles=$("$dir/connaio" profiles --data-dir "$data" 2>/dev/null); do
  profiles=unknown
  [ $i -lt 30 ] || break
  sleep 1
  i=$((i + 1))
done
say "$G" ✓ 启动 "服务"
if [ -z "$profiles" ] && (: </dev/tty) 2>/dev/null; then
  explain "导入配置" "当前没有任何配置。" "可输入订阅地址，或稍后用 connaio profile add 添加。"
  while :; do
    ask "订阅地址（留空跳过）："
    url=$answer
    [ -n "$url" ] || break
    ask "配置名称（留空则使用订阅域名）："
    name=$answer
    doing 下载 "订阅"
    # no name, no argument: the CLI names it after the host; its output kept off the screen
    if out=$("$dir/connaio" profile add --data-dir "$data" "$url" ${name:+"$name"} 2>&1); then
      say "$G" ✓ 订阅 "已添加并启用"
      break
    fi
    # stored before the core failed to load it: asking again would add it twice
    if [ -n "$("$dir/connaio" profiles --data-dir "$data" 2>/dev/null)" ]; then
      say "$Y" ! 注意 "订阅已保存，但内核加载配置失败"
      more "请稍后执行 $cli status 查看，或查看日志：$logs"
      break
    fi
    say "$R" ✗ 失败 "订阅添加失败，请检查地址后重新输入"
    more "$(printf %s "$out" | tail -n 1)"
  done
  rm -f "$tmp/ask"
fi
if [ -n "$on" ]; then
  say "$G" ✓ 完成 "安装完成"
  sleep 1
  leave
  cat "$tmp/steps"
fi

# warn adds a warning to the box, labelled once
warn() {
  kv "${warned-注意}" "$Y$1$N"
  warned=
}
if [ -z "$root" ] && [ -n "$systemd" ]; then
  kv 运行方式 "systemd 用户服务 connaio"
elif [ -z "$root" ]; then
  kv 运行方式 "后台运行"
elif [ -n "$systemd" ]; then
  kv 运行方式 "systemd 服务 connaio"
else
  kv 运行方式 "后台运行（/etc/init.d/connaio）"
fi
kv 监听地址 "$listen"
kv 设置文件 "$(printf %s "$conf" | sed "s|^$HOME/|~/|")"
[ -z "$public" ] || warn "$port 端口对其他电脑开放，连接未加密"
if [ -z "$root" ]; then
  warn "无 root，不支持 TUN 模式"
  if [ -n "$systemd" ]; then
    warn "退出登录后继续运行：loginctl enable-linger $(id -un)（需管理员）"
  else
    warn "重启后需再次运行安装命令启动"
  fi
  case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) warn "请将 ~/.local/bin 加入 PATH" ;; esac
elif [ -z "$systemd" ]; then
  warn "容器重启后需执行 /etc/init.d/connaio start"
fi
# a desktop install's connaio is the app with a window, which fails with no display
if [ -n "$root" ] && [ -e /usr/bin/connaio ]; then
  warn "本机的 connaio 是桌面版，请改用 $dir/connaio"
fi
bl ""
# the menu has the rest (the service, its log, an update), help every command, the README the uninstall
row "$cli" "打开管理菜单：节点、配置、VPN、服务、日志与更新"
row "$cli help" "查看全部命令"
box "Connaio 安装完成"
