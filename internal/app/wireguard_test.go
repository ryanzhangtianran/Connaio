package app

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestFingerprintNotSSH: a port that closes at once, not SSH, says so.
func TestFingerprintNotSSH(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	var s ToolService
	if _, err := s.HostFingerprint("127.0.0.1", l.Addr().(*net.TCPAddr).Port); err == nil || !strings.Contains(err.Error(), "不是 SSH 服务") {
		t.Fatalf("%v", err)
	}
}

func TestRelayArch(t *testing.T) {
	for uname, want := range map[string]string{"Linux x86_64\n": "amd64", "Linux aarch64": "arm64", "Linux arm64": "arm64", "Darwin arm64": "", "Linux armv7l": ""} {
		if got, err := relayArch(uname); got != want || (want == "") != (err != nil) {
			t.Errorf("%q: %q %v", uname, got, err)
		}
	}
}

// TestInstallRelay installs over SSH on a server here whose uname, systemctl, loginctl, crontab and
// the like are stubs: as a systemd user service where the user may linger, else from cron.
func TestInstallRelay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the server runs sh")
	}
	relay := "#!/bin/sh\n[ \"$3\" = -check ] && exit 0\nexec /bin/sleep 3\n" // passes -check, then stays up past the setup's check
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write([]byte(relay))
	w.Close()
	defer func(f func(string) ([]byte, error)) { relayBinary = f }(relayBinary)
	relayBinary = func(arch string) ([]byte, error) {
		if arch != "amd64" { // the server's, by its uname stub
			return nil, errors.New("not carried")
		}
		return gz.Bytes(), nil
	}

	defer func(d string) { sysctlDir = d }(sysctlDir)
	for _, linger := range []string{"yes", "no"} {
		stubs, home := t.TempDir(), t.TempDir()
		log := filepath.Join(stubs, "log")
		for name, body := range map[string]string{
			"uname":      `echo "Linux x86_64"`,
			"systemctl":  `echo "systemctl $*" >> "$LOG"`,
			"loginctl":   `if [ "$1" = show-user ]; then echo "$LINGER"; else echo "loginctl $*" >> "$LOG"; fi`,
			"crontab":    `if [ "$1" = -l ]; then cat "$STUBS/cron" 2>/dev/null; else cat > "$STUBS/cron"; fi`,
			"sleep":      ``,
			"journalctl": ``,
			"pkill":      ``,
			"sudo":       `read -r pw; echo "sudo $pw" >> "$LOG"; shift 3; exec "$@"`, // sudo -S -p '' <command>
			"modprobe":   ``,
			// as in a container: fq is the host's to set
			"sysctl": `case "$*" in "-n net.ipv4.tcp_congestion_control") echo cubic;; "-n net.ipv4.tcp_available_congestion_control") echo "reno cubic bbr";; *default_qdisc*) exit 1;; *) echo "sysctl $*" >> "$LOG";; esac`,
			"ufw":    `if [ "$1" = status ]; then echo "Status: active"; else echo "ufw $*" >> "$LOG"; fi`,
		} {
			if err := os.WriteFile(filepath.Join(stubs, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		addr, fingerprint := startShellServer(t, []string{"PATH=" + stubs + ":/usr/bin:/bin", "HOME=" + home, "LOG=" + log, "STUBS=" + stubs, "LINGER=" + linger})
		host, port, _ := net.SplitHostPort(addr)
		p, _ := strconv.Atoi(port)
		login := SSHLogin{Host: host, Port: p, User: "admin", Password: "pw"}
		var s ToolService
		if fp, err := s.HostFingerprint(host, p); err != nil || fp != fingerprint {
			t.Fatalf("fingerprint %q %v, want %q", fp, err, fingerprint)
		}
		sysctlDir = t.TempDir()
		install := RelayInstall{Login: login, HostKey: fingerprint, Tunnel: "vps", Password: "correct horse battery", Peer: "10.8.0.2", Port: 51820,
			Forwards: []string{"0.0.0.0:8080", "127.0.0.1:1080"}, Admin: linger == "yes"}

		msg, err := s.InstallRelay(install)
		if err != nil {
			t.Fatalf("linger %s: %q %v", linger, msg, err)
		}
		d := filepath.Join(home, ".connaio")
		if b, _ := os.ReadFile(filepath.Join(d, "connaio-relay")); string(b) != relay {
			t.Errorf("relay binary %q", b)
		}
		var conf relayConfig
		b, _ := os.ReadFile(filepath.Join(d, "vps.json"))
		if json.Unmarshal(b, &conf) != nil || conf.Password != "correct horse battery" || conf.Port != 51820 || conf.Peer != "10.8.0.2" || len(conf.Forwards) != 2 {
			t.Errorf("config %s", b)
		}
		if info, _ := os.Stat(filepath.Join(d, "vps.json")); info.Mode().Perm() != 0o600 {
			t.Errorf("config mode %v", info.Mode().Perm())
		}
		calls, _ := os.ReadFile(log)
		run := d + "/connaio-relay -c " + d + "/vps.json"
		if linger == "yes" {
			unit, _ := os.ReadFile(filepath.Join(home, ".config/systemd/user/connaio-relay-vps.service"))
			if !strings.Contains(string(unit), "ExecStart="+run) || !strings.Contains(msg, "开机自启") {
				t.Errorf("unit %s, message %q", unit, msg)
			}
			for _, want := range []string{"loginctl enable-linger", "systemctl --user enable -q connaio-relay-vps", "systemctl --user restart connaio-relay-vps",
				"sudo pw", "sysctl -q -w net.ipv4.tcp_congestion_control=bbr", "ufw allow 51820/udp comment connaio", "ufw allow 8080/tcp comment connaio"} {
				if !strings.Contains(string(calls), want) {
					t.Errorf("missing %q in calls:\n%s", want, calls)
				}
			}
			if strings.Contains(string(calls), "1080") {
				t.Errorf("the loopback forward's port opened:\n%s", calls)
			}
			if b, _ := os.ReadFile(filepath.Join(sysctlDir, "90-connaio-bbr.conf")); string(b) != "net.ipv4.tcp_congestion_control = bbr\n" { // no fq: it wasn't set
				t.Errorf("sysctl file %q", b)
			}
			if !strings.Contains(msg, "已开启 BBR") || !strings.Contains(msg, "ufw 中放行 UDP 51820、TCP 8080") {
				t.Errorf("message %q", msg)
			}
			// another key at the server's address: refused before any password goes out
			install.HostKey = "SHA256:other"
			if _, err := s.InstallRelay(install); err == nil || !strings.Contains(err.Error(), "指纹") {
				t.Errorf("another server key: %v", err)
			}
			install.HostKey = ""
			if _, err := s.InstallRelay(install); err == nil {
				t.Error("an unconfirmed server key accepted")
			}
			continue
		}
		// again: the cron line is replaced, not added twice
		if _, err := s.InstallRelay(install); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(calls), "sudo") {
			t.Errorf("sudo without Admin:\n%s", calls)
		}
		cron, _ := os.ReadFile(filepath.Join(stubs, "cron"))
		if strings.Count(string(cron), "@reboot sh "+d+"/vps.sh") != 1 || !strings.Contains(msg, "cron") {
			t.Errorf("crontab %q, message %q", cron, msg)
		}
		if loop, _ := os.ReadFile(filepath.Join(d, "vps.sh")); !strings.Contains(string(loop), run+" >> \""+d+"/vps.log\"") || !strings.Contains(string(loop), `"$(wc -c < "`+d+`/vps.log"`) {
			t.Errorf("restart loop:\n%s", loop)
		}
		// a relay that can't run (its port taken) fails the install with what it said
		relay = "#!/bin/sh\necho port 8080 taken; exit 1\n"
		gz.Reset()
		w = gzip.NewWriter(&gz)
		w.Write([]byte(relay))
		w.Close()
		if _, err := s.InstallRelay(install); err == nil || !strings.Contains(err.Error(), "port 8080 taken") {
			t.Errorf("a relay that can't run: %v", err)
		}
	}

	var s ToolService
	if v := s.RelayVersions(); len(v) != 1 || len(v[0]) != 64 { // the stub's amd64 only
		t.Errorf("relay versions %v", v)
	}
	for name, bad := range map[string]RelayInstall{
		"shell in name":    {HostKey: "k", Tunnel: "a;b", Port: 1},
		"port":             {HostKey: "k", Tunnel: "vps", Port: 70000},
		"shell in forward": {HostKey: "k", Tunnel: "vps", Port: 1, Forwards: []string{"0.0.0.0:80;reboot"}},
	} {
		if _, err := s.InstallRelay(bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// startShellServer runs an SSH server here that takes the password pw and runs each exec request
// with sh under env, and returns its address and its key's fingerprint.
func startShellServer(t *testing.T, env []string) (string, string) {
	_, priv, _ := ed25519.GenerateKey(nil)
	host, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if string(pw) == "pw" {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	cfg.AddHostKey(host)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					ch, chReqs, _ := nc.Accept()
					go func() {
						defer ch.Close()
						for r := range chReqs {
							if r.Type != "exec" {
								r.Reply(false, nil)
								continue
							}
							r.Reply(true, nil)
							cmd := exec.Command("sh", "-c", string(r.Payload[4:])) // an SSH string: length, then the command
							cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, ch, ch, ch.Stderr()
							status := uint32(0)
							if cmd.Run() != nil {
								status = 1
							}
							ch.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, status))
							return
						}
					}()
				}
			}()
		}
	}()
	return l.Addr().String(), ssh.FingerprintSHA256(host.PublicKey())
}
