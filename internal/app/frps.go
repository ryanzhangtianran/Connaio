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
	var auth []ssh.AuthMethod
	if d.Key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(d.Key))
		if err != nil {
			return Frps{}, fmt.Errorf("私钥无法解析：%w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if d.Password != "" {
		auth = append(auth, ssh.Password(d.Password))
	}
	if len(auth) == 0 {
		return Frps{}, errors.New("需要密码或私钥")
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

// KeyPair is an SSH key pair kept in ~/.connaio for a tunnel.
type KeyPair struct {
	Private string `json:"private"` // OpenSSH private key text, for the tunnel's private-key
	Public  string `json:"public"`  // authorized_keys line
	Path    string `json:"path"`    // the private key's file; the public one is beside it with .pub
}

// KeyPair loads ~/.connaio/<name>_ed25519, generating it (ed25519, no passphrase) only when missing,
// so a key already installed on a server is never replaced.
func (s *ToolService) KeyPair(name string) (KeyPair, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return KeyPair{}, errors.New("先填写隧道名称")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return KeyPair{}, err
	}
	path := filepath.Join(home, ".connaio", name+"_ed25519")
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
	signer, err := ssh.ParsePrivateKey(text)
	if err != nil {
		return KeyPair{}, fmt.Errorf("%s 无法解析：%w", path, err)
	}
	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " connaio-" + name
	return KeyPair{Private: string(text), Public: pub, Path: path}, nil
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
	hostKey := ssh.InsecureIgnoreHostKey() // ponytail: trust on first use, as DeployFRPS
	if l.HostKey != "" {
		k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(l.HostKey))
		if err != nil {
			return fmt.Errorf("服务器公钥无法解析：%w", err)
		}
		hostKey = ssh.FixedHostKey(k)
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
