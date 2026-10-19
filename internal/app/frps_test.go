package app

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestKeyPairKept(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var s ToolService
	a, err := s.KeyPair("vps")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(a.Path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private key file: %v %v", info, err)
	}
	if pub, _ := os.ReadFile(a.Path + ".pub"); !strings.HasPrefix(string(pub), "ssh-ed25519 ") {
		t.Fatalf(".pub %q", pub)
	}
	if b, _ := s.KeyPair("vps"); b.Private != a.Private {
		t.Fatal("an existing key pair was replaced")
	}
	if _, err := s.KeyPair("../x"); err == nil {
		t.Fatal("path in name accepted")
	}
	if keys, _ := s.KeyPairs(); len(keys) != 1 || keys[0].Name != "vps" || keys[0].Private != a.Private {
		t.Fatalf("KeyPairs: %+v", keys)
	}
}

// TestTunnelTest logs in to an SSH server here that takes only the tunnel's key.
func TestTunnelTest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var s ToolService
	key, _ := s.KeyPair("vps")
	client, _ := ssh.ParsePrivateKey([]byte(key.Private))
	_, hostPriv, _ := ed25519.GenerateKey(nil)
	host, _ := ssh.NewSignerFromKey(hostPriv)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if bytes.Equal(k.Marshal(), client.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, errors.New("not the tunnel's key")
	}}
	cfg.AddHostKey(host)
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
			go func() { _, _, _, _ = ssh.NewServerConn(c, cfg) }()
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port
	hostLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host.PublicKey())))

	if msg, err := s.TestTunnel(TunnelTest{Type: "ssh", Host: "127.0.0.1", Port: port, User: "u", Key: key.Private, HostKey: hostLine}); err != nil || !strings.Contains(msg, ssh.FingerprintSHA256(host.PublicKey())) {
		t.Fatalf("key login: %q %v", msg, err)
	}
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	other, _ := ssh.NewSignerFromKey(otherPriv)
	if _, err := s.TestTunnel(TunnelTest{Type: "ssh", Host: "127.0.0.1", Port: port, User: "u", Key: key.Private, HostKey: string(ssh.MarshalAuthorizedKey(other.PublicKey()))}); err == nil {
		t.Fatal("a server whose key differs from the tunnel's was accepted")
	}
	if _, err := s.TestTunnel(TunnelTest{Type: "frp", Host: "127.0.0.1", Port: port}); err != nil {
		t.Fatalf("frp port open: %v", err)
	}
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := closed.Addr().(*net.TCPAddr).Port
	closed.Close()
	if _, err := s.TestTunnel(TunnelTest{Type: "frp", Host: "127.0.0.1", Port: closedPort}); err == nil || !strings.Contains(err.Error(), strconv.Itoa(closedPort)) {
		t.Fatalf("frp port closed: %v", err)
	}
}
