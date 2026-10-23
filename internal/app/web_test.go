package app

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWebHandler: nothing reaches the app without the token; the token sets the cookie, which then
// passes requests and WebSocket upgrades through.
func TestWebHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" { // answers the upgrade, then echoes
			c, rw, _ := w.(http.Hijacker).Hijack()
			defer c.Close()
			rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			rw.Flush()
			io.Copy(c, rw)
			return
		}
		io.WriteString(w, "app "+r.Host)
	}))
	defer upstream.Close()
	front := httptest.NewServer(webHandler(strings.TrimPrefix(upstream.URL, "http://"), "secret"))
	defer front.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, path := range []string{"/", "/wails/runtime", "/?token=wrong"} {
		resp, err := noRedirect.Get(front.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without the token: %d", path, resp.StatusCode)
		}
	}
	login := func(resp *http.Response, err error) string {
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		for _, c := range resp.Cookies() {
			if c.Name == webCookie {
				if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
					t.Errorf("cookie %+v", c)
				}
				return c.Value
			}
		}
		t.Fatalf("no cookie: %d", resp.StatusCode)
		return ""
	}
	if login(noRedirect.PostForm(front.URL+"/login", url.Values{"token": {"secret"}})) != "secret" {
		t.Fatal("login form")
	}
	cookie := login(noRedirect.Get(front.URL + "/?token=secret"))

	req, _ := http.NewRequest("GET", front.URL+"/page", nil)
	req.AddCookie(&http.Cookie{Name: webCookie, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "app "+strings.TrimPrefix(front.URL, "http://") {
		t.Errorf("proxied: %d %q (the browser's Host, for Wails' origin check)", resp.StatusCode, body)
	}

	c, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "GET /wails/events HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nCookie: "+webCookie+"="+cookie+"\r\n\r\n")
	r := bufio.NewReader(c)
	status, _ := r.ReadString('\n')
	if !strings.Contains(status, "101") {
		t.Fatalf("upgrade: %q", status)
	}
	for line, _ := r.ReadString('\n'); line != "\r\n" && line != ""; line, _ = r.ReadString('\n') {
	}
	io.WriteString(c, "ping")
	got := make([]byte, 4)
	if _, err := io.ReadFull(r, got); err != nil || string(got) != "ping" {
		t.Fatalf("through the upgrade: %q %v", got, err)
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
