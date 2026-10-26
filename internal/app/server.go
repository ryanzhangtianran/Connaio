//go:build server

package app

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/ryanzhangtianran/Connaio/internal/cli"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Run is Connaio's: the app with no window, tray or desktop, its UI served to a browser at
// --listen behind the access token (web.go), the core always this process's own child.
func Run(assets fs.FS, _ []byte) error {
	// any first word is a command to the running instance, an unknown one an error: never a second
	// instance started on the default data directory by a typo
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		if err := cli.Main(os.Args[1:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "connaio:", err)
			os.Exit(1)
		}
		return nil
	}
	// bare in a terminal, as x-ui: the menu; the service always starts with --listen
	if fi, err := os.Stdin.Stat(); err == nil && len(os.Args) == 1 && fi.Mode()&os.ModeCharDevice != 0 {
		return cli.Menu(os.Stdin, os.Stdout)
	}
	listen := flag.String("listen", "127.0.0.1:9090", "address of the web UI; 0.0.0.0:9090 to reach it from other machines")
	dataDir := flag.String("data-dir", "", "data directory: profiles, override, settings (default: Connaio in the user's config directory)")
	given := flag.String("token", "", "access token of the web UI (default: made once, kept in the data directory as web-token)")
	domain := flag.String("domain", os.Getenv("CONNAIO_DOMAIN"), "serve the web UI over HTTPS on :443 too, for this domain, its certificate from Let's Encrypt")
	flag.Parse()
	if *dataDir == "" { // asked only now: a systemd service has no $HOME, and gives --data-dir
		configDir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("give --data-dir: %w", err)
		}
		*dataDir = filepath.Join(configDir, "Connaio")
	}

	sv, err := newServices(*dataDir)
	if err != nil {
		return err
	}
	token, err := webToken(*dataDir, *given)
	if err != nil {
		return err
	}
	front, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	// Wails on a loopback port of its own, reached only through the front
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := inner.Addr().(*net.TCPAddr).Port
	inner.Close()

	app := application.New(application.Options{
		Name:         "Connaio",
		Description:  "Rule-based proxy with reverse tunnels",
		MarshalError: marshalError,
		Services:     sv.list(),
		Assets:       application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Server:       application.ServerOptions{Host: "127.0.0.1", Port: port},
	})
	handler := webHandler(fmt.Sprintf("127.0.0.1:%d", port), token)
	go func() { log.Fatal(http.Serve(front, handler)) }()
	if *domain != "" {
		// the certificate got and renewed on the TLS handshake (TLS-ALPN-01: no port 80), kept in the data directory
		// shortcut: :443 fixed, a flag for it when someone needs another port
		m := &autocert.Manager{Prompt: autocert.AcceptTOS, HostPolicy: autocert.HostWhitelist(*domain), Cache: autocert.DirCache(filepath.Join(*dataDir, "acme"))}
		// a port taken (another web server) costs HTTPS only: the UI on --listen stays, to say so
		if tls, err := net.Listen("tcp", ":443"); err != nil {
			notify("error", "HTTPS 未启动，443 端口无法使用：%v", err)
		} else {
			// on the internet: a client stalling its handshake or headers is dropped (this bounds the
			// TLS handshake too); no write timeout, the UI's WebSocket stays open
			srv := &http.Server{Handler: handler, TLSConfig: m.TLSConfig(), ReadHeaderTimeout: 10 * time.Second}
			go func() { log.Fatal(srv.ServeTLS(tls, "", "")) }()
			log.Printf("Connaio web UI on https://%s", *domain)
		}
	}
	log.Printf("Connaio web UI on http://%s — open http://%s/?token=%s once to sign in", *listen, *listen, token)
	return app.Run()
}
