package app

import (
	"cmp"
	"crypto/ed25519"
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

	"golang.org/x/crypto/ssh"
)

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
		return ssh.InsecureIgnoreHostKey(), nil // ponytail: trust on first use
	}
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(hostKey))
	if err != nil {
		return nil, fmt.Errorf("服务器公钥无法解析：%w", err)
	}
	return ssh.FixedHostKey(k), nil
}

// KeyPair is an SSH key pair kept in ~/.ssh for a tunnel.
type KeyPair struct {
	Name    string `json:"name"`    // what it was generated for: ~/.ssh/<name>_id_ed25519
	Private string `json:"private"` // OpenSSH private key text, for the tunnel's private-key
	Path    string `json:"path"`    // the private key's file; the public one is beside it with .pub
}

// keySuffix ends a tunnel's key file in ~/.ssh, telling it from the user's own id_ed25519.
const keySuffix = "_id_ed25519"

// keysDir is ~/.ssh, where the tunnels' key pairs are kept beside the user's, for ssh -i too.
func keysDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh"), nil
}

// KeyPairs lists the key pairs in ~/.ssh, by name: the only keys a tunnel takes, so each one
// is generated here and its public half is at hand to install on a server.
func (s *ToolService) KeyPairs() ([]KeyPair, error) {
	dir, err := keysDir()
	if err != nil {
		return nil, err
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*"+keySuffix))
	var keys []KeyPair
	for _, p := range paths {
		if text, err := os.ReadFile(p); err == nil {
			keys = append(keys, KeyPair{Name: strings.TrimSuffix(filepath.Base(p), keySuffix), Private: string(text), Path: p})
		}
	}
	return keys, nil
}

// KeyPair loads ~/.ssh/<name>_id_ed25519, generating it (ed25519, no passphrase) only when missing,
// so a key already installed on a server is never replaced. The name is lowercased with - as _, as
// key files are named.
func (s *ToolService) KeyPair(tunnel string) (KeyPair, error) {
	name := strings.ReplaceAll(strings.ToLower(tunnel), "-", "_")
	if name == "" || strings.ContainsAny(name, `/\`) {
		return KeyPair{}, errors.New("先填写隧道名称")
	}
	dir, err := keysDir()
	if err != nil {
		return KeyPair{}, err
	}
	path := filepath.Join(dir, name+keySuffix)
	text, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		_, key, _ := ed25519.GenerateKey(nil)
		host, _ := os.Hostname()
		comment := host + "@" + tunnel
		block, err := ssh.MarshalPrivateKey(key, comment)
		if err != nil {
			return KeyPair{}, err
		}
		text = pem.EncodeToMemory(block)
		signer, _ := ssh.NewSignerFromKey(key)
		pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " " + comment + "\n"
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
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Key      string `json:"key,omitempty"`
	HostKey  string `json:"hostKey,omitempty"`
}

// TestTunnel tries a tunnel's server from here, directly: it logs in with the password or key, and
// checks the server's key when one is given.
func (s *ToolService) TestTunnel(t TunnelTest) (string, error) {
	if t.Host == "" || t.Port == 0 {
		return "", errors.New("先填写服务器和端口")
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
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

// takeOverScript kills the sshd sessions (sshd, or sshd-session since OpenSSH 9.8) listening on the
// ports given as arguments and prints their pids. ss shows only the login user's own processes
// unless it is root, so another user's session, or a program that isn't sshd, is left alone.
// shortcut: Linux servers only (ss); add an lsof fallback if a tunnel's server is a Mac or BSD.
const takeOverScript = `for p in "$@"; do ss -Hltnp "sport = :$p"; done | grep -o '"sshd[^"]*",pid=[0-9]*' | sed 's/.*pid=//' | sort -u | while read -r pid; do kill "$pid" && echo "$pid"; done`

// TakeOverTunnel ends the SSH sessions on an SSH tunnel's server that hold its forwards' remote
// ports: the same tunnel opened from another machine. This one's core, already retrying, gets them
// within its retry interval, before the other's, which waits that whole interval after losing its
// session, then finds the ports taken and keeps retrying: they don't take turns until someone takes
// over again. The server is reached from here directly, as TestTunnel does.
func (s *ToolService) TakeOverTunnel(t TunnelTest, remotes []string) (int, error) {
	var ports []string
	for _, r := range remotes {
		_, p, err := net.SplitHostPort(r)
		if _, perr := strconv.Atoi(p); err != nil || perr != nil {
			return 0, fmt.Errorf("远端地址无法解析：%s", r)
		}
		ports = append(ports, p)
	}
	auth, err := sshAuth(t.Password, t.Key)
	if err != nil {
		return 0, err
	}
	check, err := hostKeyCheck(t.HostKey)
	if err != nil {
		return 0, err
	}
	cfg := &ssh.ClientConfig{User: t.User, Auth: auth, HostKeyCallback: check, Timeout: 10 * time.Second}
	client, err := ssh.Dial("tcp", net.JoinHostPort(t.Host, strconv.Itoa(cmp.Or(t.Port, 22))), cfg)
	if err != nil {
		return 0, fmt.Errorf("SSH 登录失败：%w", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return 0, err
	}
	defer session.Close()
	session.Stdin = strings.NewReader(takeOverScript)
	out, err := session.CombinedOutput("sh -s -- " + strings.Join(ports, " "))
	if err != nil {
		return 0, fmt.Errorf("结束占用端口的会话失败：%s", cmp.Or(strings.TrimSpace(string(out)), err.Error()))
	}
	n := len(strings.Fields(string(out)))
	if n == 0 {
		return 0, errors.New("服务器上没有本用户的 SSH 会话占用这些端口")
	}
	return n, nil
}
