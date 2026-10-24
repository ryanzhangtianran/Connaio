package app

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"1.0.1", "1.0.0", true}, {"1.0.10", "1.0.9", true}, {"1.0.0", "1.0.0", false}, {"1.0", "1.0.1", false}, {"2", "1.9.9", true},
		{"1.0.0", "1.0.0-rc.1", true}, {"1.0.0-rc.1", "1.0.0", false}, {"1.0.0-rc.10", "1.0.0-rc.9", true}, {"1.0.0-rc.1", "0.1.38", true}, {"1.0.0-rc.1", "1.0.0-rc.1", false}} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestFetchToChecksTheDigest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("package")) }))
	defer srv.Close()
	sum := sha256.Sum256([]byte("package"))
	path := filepath.Join(t.TempDir(), "p")
	if err := fetchTo(srv.Client(), srv.URL, path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := fetchTo(srv.Client(), srv.URL, path, "00"); err == nil {
		t.Fatal("a package whose digest doesn't match must be refused")
	}
}
