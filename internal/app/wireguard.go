package app

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// relayFS holds connaio-relay for the server, gzipped, one per Linux architecture:
// relay/connaio-relay-linux-<arch>.gz, built by wails3 task common:build:relay.
//
//go:embed all:relay
var relayFS embed.FS

// relayBinary is connaio-relay for arch, gzipped.
var relayBinary = func(arch string) ([]byte, error) {
	b, err := relayFS.ReadFile("relay/connaio-relay-linux-" + arch + ".gz")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("此版本未包含服务器程序")
	}
	return b, err
}

// SSHLogin is a one-off password login to a tunnel's server, for setting it up; the password isn't kept.
type SSHLogin struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// RelayVersions are the versions of the connaio-relay builds this app carries, as a relay reports
// its own (the sha256 of its executable): a tunnel whose relay reports another runs an outdated one.
func (s *ToolService) RelayVersions() []string {
	var versions []string
	for _, arch := range []string{"amd64", "arm64"} {
		gz, err := relayBinary(arch)
		if err != nil {
			continue
		}
		r, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			continue
		}
		h := sha256.New()
		if _, err := io.Copy(h, r); err == nil {
			versions = append(versions, fmt.Sprintf("%x", h.Sum(nil)))
		}
	}
	return versions
}

// relayConfig is connaio-relay's config (component/relay's Config in connaio-core).
type relayConfig struct {
	Password string   `json:"password"`
	Port     int      `json:"port"`
	Peer     string   `json:"peer"`
	Forwards []string `json:"forwards"`
}

// relayUpload puts the gzipped binary read from stdin in place as ~/.connaio/connaio-relay; a relay
// running from the old one keeps it until it restarts.
const relayUpload = `set -e; d="$HOME/.connaio"; mkdir -p "$d"; gzip -dc > "$d/connaio-relay.new"; chmod 755 "$d/connaio-relay.new"; mv -f "$d/connaio-relay.new" "$d/connaio-relay"`

// relaySetup runs tunnel $1's relay as the login user, its config read from stdin into
// ~/.connaio/$1.json: a systemd user service where the user may linger (it runs while logged out
// and from boot), else a loop in the background, started again from cron at reboot, that restarts
// the relay when it exits and keeps its log under about 1 MB. It prints which, or fails with what
// the relay said when it can't run. No single quotes: it goes inside sh -c '…'.
const relaySetup = `set -e
d="$HOME/.connaio"; n="$1"; unit="connaio-relay-$n"; run="$d/connaio-relay -c $d/$n.json"
umask 077
mkdir -p "$d"
cat > "$d/$n.json"
command -v loginctl >/dev/null 2>&1 && loginctl enable-linger >/dev/null 2>&1 || true
if systemctl --user show-environment >/dev/null 2>&1 && [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" = yes ]; then
  mkdir -p "$HOME/.config/systemd/user"
  printf "[Unit]\nDescription=Connaio relay %s\nAfter=network-online.target\n\n[Service]\nExecStart=%s\nRestart=always\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n" "$n" "$run" > "$HOME/.config/systemd/user/$unit.service"
  systemctl --user daemon-reload
  systemctl --user enable -q "$unit"
  systemctl --user restart "$unit"
  sleep 2
  if ! systemctl --user is-active --quiet "$unit"; then journalctl --user -u "$unit" -n 5 --no-pager -o cat >&2 || true; exit 1; fi
  echo systemd
else
  log="$d/$n.log"
  cat > "$d/$n.sh" <<EOF
#!/bin/sh
while :; do
  [ "\$(wc -c < "$log" 2>/dev/null || echo 0)" -gt 1048576 ] && mv -f "$log" "$log.1"
  $run >> "$log" 2>&1
  sleep 5
done
EOF
  pkill -f "$d/$n.sh" >/dev/null 2>&1 || true
  pkill -f "$run" >/dev/null 2>&1 || true
  sleep 1
  if ! out=$($run -check 2>&1); then echo "$out" >&2; exit 1; fi
  nohup sh "$d/$n.sh" >/dev/null 2>&1 < /dev/null &
  if command -v crontab >/dev/null 2>&1; then
    { crontab -l 2>/dev/null | grep -vF "$d/$n.sh" || true; echo "@reboot sh $d/$n.sh >/dev/null 2>&1"; } | crontab -
    echo cron
  else
    echo nohup
  fi
fi`

// relayArch maps the server's `uname -sm` to the relay built for it.
func relayArch(uname string) (string, error) {
	switch f := strings.Fields(uname); {
	case len(f) == 2 && f[0] == "Linux" && f[1] == "x86_64":
		return "amd64", nil
	case len(f) == 2 && f[0] == "Linux" && (f[1] == "aarch64" || f[1] == "arm64"):
		return "arm64", nil
	}
	return "", fmt.Errorf("服务器系统不受支持：%s", strings.TrimSpace(uname))
}

// RelayInstall is what InstallRelay sets up on a tunnel's server.
type RelayInstall struct {
	Login    SSHLogin `json:"login"`
	HostKey  string   `json:"hostKey"` // the server's key fingerprint as the user confirmed it (HostFingerprint): no other is logged in to
	Tunnel   string   `json:"tunnel"`
	Password string   `json:"password"` // the tunnel's
	Peer     string   `json:"peer"`     // Connaio's tunnel address
	Port     int      `json:"port"`     // the UDP port the relay takes the handshake on
	Forwards []string `json:"forwards"` // where the relay listens on the server
	Admin    bool     `json:"admin"`    // also run adminSetup once, as root (the login, or sudo with its password)
}

// HostFingerprint is the SSH server's key fingerprint, read in the handshake before any login: the
// user confirms it before a password is sent.
func (s *ToolService) HostFingerprint(host string, port int) (string, error) {
	var got string
	cfg := &ssh.ClientConfig{Timeout: 10 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		got = ssh.FingerprintSHA256(key)
		return errors.New("done")
	}}
	_, err := ssh.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(cmp.Or(port, 22))), cfg)
	// connected, then cut off before SSH's first words: EOF, or a failed read (a reset, on any OS)
	var opErr *net.OpError
	if got == "" && (errors.Is(err, io.EOF) || errors.As(err, &opErr) && opErr.Op == "read") {
		return "", fmt.Errorf("端口 %d 上不是 SSH 服务，或连接被拒绝，请检查 SSH 端口", cmp.Or(port, 22))
	}
	if got == "" {
		return "", fmt.Errorf("无法读取服务器指纹：%w", err)
	}
	return got, nil
}

// adminSetup is the one step that needs root, run once and leaving nothing that keeps it: BBR, in a
// file of its own (/etc/sysctl.d/90-connaio-bbr.conf, $1 the directory) so it is told apart and
// removed, where the kernel has it; then the tunnel's UDP port $2 and the public forwards' TCP ports
// (the rest) opened in ufw or firewalld when one runs. A firewall that isn't running, or raw
// iptables, is left alone. BBR failing doesn't stop the firewall step; fq, which a container can't
// set (it is the host's), is only a bonus: BBR paces by itself since Linux 4.13. It prints a line
// per step. No single quotes: it goes inside sh -c '…'.
const adminSetup = `set -e
dir="$1"; udp="$2"; shift 2
bbr() {
  [ "$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)" = bbr ] && { echo bbr:already; return; }
  modprobe tcp_bbr >/dev/null 2>&1 || true
  sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null | grep -qw bbr || { echo bbr:unsupported; return; }
  sysctl -q -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1 || { echo bbr:failed; return; }
  conf="net.ipv4.tcp_congestion_control = bbr"
  sysctl -q -w net.core.default_qdisc=fq >/dev/null 2>&1 && conf="net.core.default_qdisc = fq
$conf"
  mkdir -p "$dir" && printf "%s\n" "$conf" > "$dir/90-connaio-bbr.conf" || { echo bbr:unsaved; return; }
  echo bbr:on
}
bbr
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  ufw allow "$udp/udp" comment connaio >/dev/null
  for p in "$@"; do ufw allow "$p/tcp" comment connaio >/dev/null; done
  echo fw:ufw
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  firewall-cmd -q --permanent --add-port="$udp/udp"
  for p in "$@"; do firewall-cmd -q --permanent --add-port="$p/tcp"; done
  firewall-cmd -q --reload
  echo fw:firewalld
else
  echo fw:none
fi`

// sysctlDir is where adminSetup puts the BBR setting.
var sysctlDir = "/etc/sysctl.d"

// publicPorts are the TCP ports of the forwards the relay listens on for others than the server
// itself: a loopback one (the proxy port's) stays closed in the firewall.
func publicPorts(forwards []string) ([]string, error) {
	var ports []string
	for _, f := range forwards {
		ap, err := netip.ParseAddrPort(f)
		if err != nil {
			return nil, fmt.Errorf("转发地址无效：%s", f)
		}
		if p := strconv.Itoa(int(ap.Port())); !ap.Addr().IsLoopback() && ap.Port() != 0 && !slices.Contains(ports, p) {
			ports = append(ports, p)
		}
	}
	return ports, nil
}

// InstallRelay logs in to the tunnel's server by SSH, only with the key the user confirmed, and sets
// up connaio-relay there as the login user: its binary for the server's architecture, its config,
// and running it (relaySetup). Doing it again replaces both and restarts the relay. With Admin it
// then runs adminSetup as root: directly for root, else through sudo, the password on its stdin
// (never on a command line). The password isn't kept.
func (s *ToolService) InstallRelay(r RelayInstall) (string, error) {
	if !regexp.MustCompile(`^[\w.-]+$`).MatchString(r.Tunnel) { // it goes into the server's shell
		return "", errors.New("隧道名称只能用字母、数字、. _ -")
	}
	if r.Port < 1 || r.Port > 65535 {
		return "", errors.New("端口无效")
	}
	ports, err := publicPorts(r.Forwards)
	if err != nil {
		return "", err
	}
	if r.HostKey == "" {
		return "", errors.New("请先确认服务器指纹")
	}
	conf, err := json.Marshal(relayConfig{r.Password, r.Port, r.Peer, r.Forwards})
	if err != nil {
		return "", err
	}
	cfg := &ssh.ClientConfig{User: r.Login.User, Auth: []ssh.AuthMethod{ssh.Password(r.Login.Password)}, Timeout: 15 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if ssh.FingerprintSHA256(key) != r.HostKey {
				return errors.New("服务器指纹与确认的不一致")
			}
			return nil
		}}
	client, err := ssh.Dial("tcp", net.JoinHostPort(r.Login.Host, strconv.Itoa(cmp.Or(r.Login.Port, 22))), cfg)
	if err != nil {
		return "", fmt.Errorf("SSH 登录失败：%w", err)
	}
	defer client.Close()
	defer time.AfterFunc(2*time.Minute, func() { client.Close() }).Stop() // a server that stalls

	uname, err := onServer(client, "uname -sm", nil)
	if err != nil {
		return "", err
	}
	arch, err := relayArch(uname)
	if err != nil {
		return "", err
	}
	bin, err := relayBinary(arch)
	if err != nil {
		return "", err
	}
	if _, err := onServer(client, "sh -c '"+relayUpload+"'", bytes.NewReader(bin)); err != nil {
		return "", err
	}
	how, err := onServer(client, "sh -c '"+relaySetup+"' sh "+r.Tunnel, strings.NewReader(string(conf)))
	if err != nil {
		return "", err
	}
	started := map[string]string{"systemd": "以用户服务运行，开机自启", "cron": "已在后台运行，开机由 cron 启动", "nohup": "已在后台运行，服务器重启后需重新安装"}[lastLine(how)]
	msg := "服务器程序" + cmp.Or(started, "已启动") + "。"
	if !r.Admin {
		return msg, nil
	}
	cmd := "sh -c '" + adminSetup + "' sh '" + sysctlDir + "' " + strconv.Itoa(r.Port) + " " + strings.Join(ports, " ")
	var stdin io.Reader
	if r.Login.User != "root" {
		cmd, stdin = "sudo -S -p '' "+cmd, strings.NewReader(r.Login.Password+"\n")
	}
	out, err := onServer(client, cmd, stdin)
	if err != nil {
		return "", fmt.Errorf("%s但管理员设置未完成：%w", msg, err)
	}
	return msg + adminText(out, r.Port, ports), nil
}

// adminText says what adminSetup did, from the lines it printed.
func adminText(out string, udp int, tcp []string) string {
	open := fmt.Sprintf("UDP %d", udp)
	if len(tcp) > 0 {
		open += "、TCP " + strings.Join(tcp, "、")
	}
	text := map[string]string{
		"bbr:on": "已开启 BBR。", "bbr:already": "BBR 原已开启。", "bbr:unsupported": "服务器内核不支持 BBR。",
		"bbr:failed": "未能开启 BBR。", "bbr:unsaved": "已开启 BBR，但未能写入设置，服务器重启后失效。",
		"fw:ufw": "已在 ufw 中放行 " + open + "。", "fw:firewalld": "已在 firewalld 中放行 " + open + "。",
		"fw:none": "未检测到运行中的 ufw 或 firewalld，防火墙未改动。",
	}
	var b strings.Builder
	for _, line := range strings.Split(out, "\n") {
		b.WriteString(text[strings.TrimSpace(line)])
	}
	return b.String()
}

// onServer runs cmd on the server with stdin and returns its output; a failure carries what it printed.
func onServer(client *ssh.Client, cmd string, stdin io.Reader) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	session.Stdin = stdin
	out, err := session.CombinedOutput(cmd)
	if err != nil {
		return "", fmt.Errorf("安装失败：%s", cmp.Or(strings.TrimSpace(string(out)), err.Error()))
	}
	return string(out), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
