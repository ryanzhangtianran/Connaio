package app

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// webCookie holds the access token in the browser, once given; webFlag tells the page it runs in a
// browser, not the app's window (format.ts web), so it leaves the desktop's parts out.
const (
	webCookie = "connaio_token"
	webFlag   = "connaio_web"
)

// webHandler is connaio-server's front: every request, WebSockets included, needs the access token,
// from the cookie a first visit with ?token= (the address printed at start) or the login form sets,
// and goes on to the Wails server at upstream.
func webHandler(upstream, token string) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: upstream}) // the Host header passes through: Wails' WebSocket origin check sees the browser's
	valid := func(t string) bool { return t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		given := r.URL.Query().Get("token")
		if r.Method == http.MethodPost && r.URL.Path == "/login" {
			given = r.PostFormValue("token")
		}
		if given != "" {
			if !valid(given) {
				loginPage(w, "访问令牌不正确")
				return
			}
			for _, c := range []*http.Cookie{
				{Name: webCookie, Value: token, HttpOnly: true},
				{Name: webFlag, Value: "1"},
			} {
				c.Path, c.MaxAge, c.SameSite, c.Secure = "/", 400*24*3600, http.SameSiteStrictMode, r.TLS != nil
				http.SetCookie(w, c)
			}
			http.Redirect(w, r, "/", http.StatusSeeOther) // the token out of the address bar
			return
		}
		if c, err := r.Cookie(webCookie); err == nil && valid(c.Value) {
			proxy.ServeHTTP(w, r)
			return
		}
		loginPage(w, "")
	})
}

// loginPage asks for the access token, with problem when the last one given was wrong.
func loginPage(w http.ResponseWriter, problem string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	note := "访问令牌见 connaio-server 启动时的输出，或数据目录中的 web-token 文件。"
	if problem != "" {
		note = problem
	}
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Connaio</title>
<style>body{font:14px -apple-system,system-ui,sans-serif;display:grid;place-items:center;height:90vh;color:#1d1d1f}form{display:grid;gap:10px;width:300px}
input,button{height:32px;padding:0 10px;border:1px solid #ccc;border-radius:8px;font:inherit}button{background:#0a84ff;color:#fff;border:0}p{color:#86868b;margin:0}</style>
<form method="post" action="/login"><b>Connaio</b><input name="token" type="password" placeholder="访问令牌" autofocus><button>进入</button><p>` + note + `</p></form>`))
}

// webToken is the access token: the given one, else the one kept in dir/web-token, made on first use.
func webToken(dir, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	path := filepath.Join(dir, "web-token")
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b)), nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	t := rand.Text()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return t, os.WriteFile(path, []byte(t+"\n"), 0o600)
}
