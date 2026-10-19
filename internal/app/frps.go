package app

import (
	"bufio"
	"cmp"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/crypto/ssh"
)

func init() {
	application.RegisterEvent[string]("frps-deploy") // one line of the deploy's progress
}

// FrpsDeploy is where and how to log in to set frps up.
type FrpsDeploy struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	Key      string `json:"key,omitempty"` // private key text
	BindPort int    `json:"bindPort"`      // frps listens here for frpc
}

// Frps is a deployed frps, for the tunnel form.
type Frps struct {
	Token    string `json:"token"`
	BindPort int    `json:"bindPort"`
}

// frpsScript installs the latest frps release for the server's architecture as a systemd service
// with token auth. %[1]d is the bind port, %[2]s the token. Needs Linux, systemd and curl; root,
// or sudo without a password.
const frpsScript = `set -e
SUDO=""; [ "$(id -u)" = 0 ] || SUDO="sudo -n"
case "$(uname -m)" in x86_64|amd64) a=amd64;; aarch64|arm64) a=arm64;; armv7l|armv6l) a=arm;; *) echo "不支持的架构 $(uname -m)"; exit 1;; esac
echo "==> 查询 frp 最新版本"
v=$(curl -fsSL --max-time 60 https://api.github.com/repos/fatedier/frp/releases/latest | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -1)
[ -n "$v" ] || { echo "无法获取 frp 版本，服务器能访问 GitHub 吗？"; exit 1; }
echo "==> 下载 frp $v（linux/$a）"
t=$(mktemp -d); cd "$t"
curl -fsSL --max-time 300 -o frp.tgz "https://github.com/fatedier/frp/releases/download/v$v/frp_${v}_linux_$a.tar.gz"
tar xzf frp.tgz
echo "==> 安装 /usr/local/bin/frps"
$SUDO install -m 755 "frp_${v}_linux_$a/frps" /usr/local/bin/frps
$SUDO mkdir -p /etc/frp
printf 'bindPort = %[1]d\nauth.method = "token"\nauth.token = "%[2]s"\n' | $SUDO tee /etc/frp/frps.toml >/dev/null
printf '[Unit]\nDescription=frps\nAfter=network.target\n\n[Service]\nExecStart=/usr/local/bin/frps -c /etc/frp/frps.toml\nRestart=always\n\n[Install]\nWantedBy=multi-user.target\n' | $SUDO tee /etc/systemd/system/frps.service >/dev/null
echo "==> 启动 frps 服务"
$SUDO systemctl daemon-reload
$SUDO systemctl enable frps >/dev/null 2>&1
$SUDO systemctl restart frps
sleep 1
rm -rf "$t"
$SUDO systemctl is-active --quiet frps || { echo "frps 未能启动，可在服务器上运行 journalctl -u frps 查看原因"; exit 1; }
echo "==> frps 正在端口 %[1]d 上运行"
`

// DeployFRPS logs in over SSH and installs frps with a fresh token, streaming progress lines.
// ponytail: the host key is accepted on first use and only shown; pin it if this grows past setup.
func (s *ToolService) DeployFRPS(d FrpsDeploy) (Frps, error) {
	if d.Port == 0 {
		d.Port = 22
	}
	if d.BindPort == 0 {
		d.BindPort = 7000
	}
	auth, err := sshAuth(d.Password, d.Key)
	if err != nil {
		return Frps{}, err
	}
	cfg := &ssh.ClientConfig{
		User: d.User, Auth: auth, Timeout: 15 * time.Second,
		HostKeyCallback: func(host string, _ net.Addr, key ssh.PublicKey) error {
			emit("frps-deploy", "==> 服务器指纹 "+ssh.FingerprintSHA256(key))
			return nil
		},
	}
	emit("frps-deploy", "==> 连接 "+d.User+"@"+d.Host)
	client, err := ssh.Dial("tcp", net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), cfg)
	if err != nil {
		return Frps{}, fmt.Errorf("SSH 连接失败：%w", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return Frps{}, err
	}
	defer session.Close()

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	session.Stdin = strings.NewReader(fmt.Sprintf(frpsScript, d.BindPort, token))
	out, err := session.StdoutPipe()
	if err != nil {
		return Frps{}, err
	}
	if err := session.Start("sh -s 2>&1"); err != nil {
		return Frps{}, err
	}
	sc := bufio.NewScanner(out)
	var last string
	for sc.Scan() {
		last = sc.Text()
		emit("frps-deploy", last)
	}
	if err := session.Wait(); err != nil {
		return Frps{}, fmt.Errorf("部署失败：%s", strings.TrimSpace(last))
	}
	return Frps{Token: token, BindPort: d.BindPort}, nil
}

// HostKey is the SSH server's public key as "type base64", read in the handshake before any login.
func (s *ToolService) HostKey(host string, port int) (string, error) {
	var got ssh.PublicKey
	cfg := &ssh.ClientConfig{Timeout: 10 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		got = key
		return errors.New("done")
	}}
	_, err := ssh.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(cmp.Or(port, 22))), cfg)
	if got == nil {
		return "", fmt.Errorf("无法读取服务器公钥：%w", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(got))), nil
}

// sshAuth is a login by key, by password, or by both, the key tried first.
func sshAuth(password, key string) ([]ssh.AuthMethod, error) {
	var auth []ssh.AuthMethod
	if key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(key))
		if err != nil {
			return nil, fmt.Errorf("私钥无法解析：%w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if password != "" {
		auth = append(auth, ssh.Password(password))
	}
	if len(auth) == 0 {
		return nil, errors.New("需要密码或私钥")
	}
	return auth, nil
}

// hostKeyCheck checks the server against hostKey ("type base64") when given; otherwise it trusts
// the server on first use.
func hostKeyCheck(hostKey string) (ssh.HostKeyCallback, error) {
	if hostKey == "" {
		return ssh.InsecureIgnoreHostKey(), nil // ponytail: trust on first use, as DeployFRPS
	}
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(hostKey))
	if err != nil {
		return nil, fmt.Errorf("服务器公钥无法解析：%w", err)
	}
	return ssh.FixedHostKey(k), nil
}

// KeyPair is an SSH key pair kept in ~/.connaio for a tunnel.
type KeyPair struct {
	Name    string `json:"name"`    // what it was generated for: ~/.connaio/<name>_ed25519
	Private string `json:"private"` // OpenSSH private key text, for the tunnel's private-key
	Path    string `json:"path"`    // the private key's file; the public one is beside it with .pub
}

// keysDir is ~/.connaio, where the tunnels' key pairs are kept.
func keysDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".connaio"), nil
}

// KeyPairs lists the key pairs in ~/.connaio, by name: the only keys a tunnel takes, so each one
// is generated here and its public half is at hand to install on a server.
func (s *ToolService) KeyPairs() ([]KeyPair, error) {
	dir, err := keysDir()
	if err != nil {
		return nil, err
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*_ed25519"))
	var keys []KeyPair
	for _, p := range paths {
		if text, err := os.ReadFile(p); err == nil {
			keys = append(keys, KeyPair{Name: strings.TrimSuffix(filepath.Base(p), "_ed25519"), Private: string(text), Path: p})
		}
	}
	return keys, nil
}

// KeyPair loads ~/.connaio/<name>_ed25519, generating it (ed25519, no passphrase) only when missing,
// so a key already installed on a server is never replaced.
func (s *ToolService) KeyPair(name string) (KeyPair, error) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return KeyPair{}, errors.New("先填写隧道名称")
	}
	dir, err := keysDir()
	if err != nil {
		return KeyPair{}, err
	}
	path := filepath.Join(dir, name+"_ed25519")
	text, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		_, key, _ := ed25519.GenerateKey(nil)
		block, err := ssh.MarshalPrivateKey(key, "connaio-"+name)
		if err != nil {
			return KeyPair{}, err
		}
		text = pem.EncodeToMemory(block)
		signer, _ := ssh.NewSignerFromKey(key)
		pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " connaio-" + name + "\n"
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return KeyPair{}, err
		}
		if err := os.WriteFile(path, text, 0o600); err != nil {
			return KeyPair{}, err
		}
		if err := os.WriteFile(path+".pub", []byte(pub), 0o644); err != nil {
			return KeyPair{}, err
		}
	} else if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Name: name, Private: string(text), Path: path}, nil
}

// SSHLogin is a password login, used once to put a key on a server.
type SSHLogin struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	HostKey  string `json:"hostKey,omitempty"` // "type base64"; checked when given
}

// installKeyScript appends the key line read from stdin to ~/.ssh/authorized_keys unless its
// type and key are already there, like ssh-copy-id.
const installKeyScript = `umask 077; read -r k; set -- $k; mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && { grep -qF "$1 $2" ~/.ssh/authorized_keys || echo "$k" >> ~/.ssh/authorized_keys; }`

// InstallKey logs in with the password and authorizes privateKey's public half for that user.
func (s *ToolService) InstallKey(l SSHLogin, privateKey string) error {
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return fmt.Errorf("私钥无法解析：%w", err)
	}
	hostKey, err := hostKeyCheck(l.HostKey)
	if err != nil {
		return err
	}
	cfg := &ssh.ClientConfig{User: l.User, Auth: []ssh.AuthMethod{ssh.Password(l.Password)}, HostKeyCallback: hostKey, Timeout: 15 * time.Second}
	client, err := ssh.Dial("tcp", net.JoinHostPort(l.Host, strconv.Itoa(cmp.Or(l.Port, 22))), cfg)
	if err != nil {
		return fmt.Errorf("SSH 连接失败：%w", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	session.Stdin = strings.NewReader(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if out, err := session.CombinedOutput("sh -c '" + installKeyScript + "'"); err != nil {
		return fmt.Errorf("安装公钥失败：%s", cmp.Or(strings.TrimSpace(string(out)), err.Error()))
	}
	return nil
}

// TunnelTest is a tunnel's server as the dialog has it, to try before saving.
type TunnelTest struct {
	Type     string `json:"type"` // "ssh" or "frp"
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Key      string `json:"key,omitempty"`
	HostKey  string `json:"hostKey,omitempty"`
}

// TestTunnel tries a tunnel's server from here, directly: an SSH tunnel logs in with its password or
// key (and checks the server's key when one is given); an FRP tunnel's server port is reached, its
// token checked only when the tunnel connects.
func (s *ToolService) TestTunnel(t TunnelTest) (string, error) {
	if t.Host == "" || t.Port == 0 {
		return "", errors.New("先填写服务器和端口")
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	if t.Type == "frp" {
		c, err := net.DialTimeout("tcp", addr, 8*time.Second)
		if err != nil {
			return "", fmt.Errorf("连不上 %s：%w", addr, err)
		}
		c.Close()
		return "frps 端口可达，token 在隧道连接时校验", nil
	}
	auth, err := sshAuth(t.Password, t.Key)
	if err != nil {
		return "", err
	}
	check, err := hostKeyCheck(t.HostKey)
	if err != nil {
		return "", err
	}
	var fingerprint string
	cfg := &ssh.ClientConfig{User: t.User, Auth: auth, Timeout: 10 * time.Second, HostKeyCallback: func(host string, remote net.Addr, key ssh.PublicKey) error {
		fingerprint = ssh.FingerprintSHA256(key)
		return check(host, remote, key)
	}}
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return "", fmt.Errorf("SSH 登录失败：%w", err)
	}
	client.Close()
	return "SSH 登录成功，服务器指纹 " + fingerprint, nil
}
