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

	"github.com/wailsapp/wails/v3/pkg/application"
)

// webCookie carries the access token from the command line.
const webCookie = "connaio_token"

// frontHeader carries the front's secret to the Wails server, which answers nothing without it
// (innerOnly): any local user can reach its loopback port, the front alone knows the secret.
const frontHeader = "X-Connaio-Front"

// webHandler is the front the command line calls: POST /wails/runtime, the access token in its
// cookie, goes on to the Wails server at upstream with secret, never the cookie (were another
// process to hold upstream's port, it would get no token). Nothing else is served: no web UI.
func webHandler(upstream, token, secret string) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: upstream})
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Header.Del("Cookie")
		r.Header.Set(frontHeader, secret)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(webCookie); err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/wails/runtime" {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// innerOnly is the Wails server's middleware: a request without the front's secret is refused.
// shortcut: Wails serves its events WebSocket outside the middleware, so a local user can still
// read the events it broadcasts (no calls); a unix socket for Wails when it offers one.
func innerOnly(secret string) application.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get(frontHeader)), []byte(secret)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// webToken is the command line's access token: the given one, else the one kept in dir/web-token, made on first use.
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
