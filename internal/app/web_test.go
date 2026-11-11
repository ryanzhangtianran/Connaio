package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWebHandler: nothing reaches the app without the token; with it, the command line's calls
// pass, and nothing else (no web UI).
func TestWebHandler(t *testing.T) {
	upstream := httptest.NewServer(innerOnly("inner")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Errorf("the cookie reached the app: %q", r.Header.Get("Cookie"))
		}
		io.WriteString(w, "app")
	})))
	defer upstream.Close()
	if resp, err := http.Post(upstream.URL+"/wails/runtime", "", nil); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the app reached without the front: %v %v", resp, err) // any local user could
	}
	front := httptest.NewServer(webHandler(strings.TrimPrefix(upstream.URL, "http://"), "secret", "inner"))
	defer front.Close()
	call := func(method, path, token string) (int, string) {
		req, _ := http.NewRequest(method, front.URL+path, nil)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: webCookie, Value: token})
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, token := range []string{"", "wrong"} {
		if code, _ := call("POST", "/wails/runtime", token); code != http.StatusUnauthorized {
			t.Errorf("token %q: %d", token, code)
		}
	}
	if code, body := call("POST", "/wails/runtime", "secret"); code != 200 || body != "app" {
		t.Errorf("a call: %d %q", code, body)
	}
	for _, path := range []string{"/", "/index.html", "/wails/events", "/?token=secret"} {
		if code, _ := call("GET", path, "secret"); code != http.StatusNotFound {
			t.Errorf("%s served: %d", path, code)
		}
	}
}

func TestWebToken(t *testing.T) {
	dir := t.TempDir()
	a, err := webToken(dir, "")
	if err != nil || len(a) < 20 {
		t.Fatalf("%q %v", a, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "web-token")); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	if b, _ := webToken(dir, ""); b != a {
		t.Error("the kept token changed")
	}
	if g, _ := webToken(dir, "given"); g != "given" {
		t.Error("the given token")
	}
}
