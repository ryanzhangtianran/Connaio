package app

import (
	"os"
	"testing"
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
	if pub, _ := os.ReadFile(a.Path + ".pub"); string(pub) != a.Public+"\n" {
		t.Fatalf(".pub %q, want %q", pub, a.Public)
	}
	if b, _ := s.KeyPair("vps"); b.Private != a.Private {
		t.Fatal("an existing key pair was replaced")
	}
	if _, err := s.KeyPair("../x"); err == nil {
		t.Fatal("path in name accepted")
	}
}
