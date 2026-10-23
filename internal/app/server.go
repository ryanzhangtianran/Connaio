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

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Run is Connaio's: the app with no window, tray or desktop, its UI served to a browser at
// --listen behind the access token (web.go), the core always this process's own child.
func Run(assets fs.FS, _ []byte) error {
	listen := flag.String("listen", "127.0.0.1:9090", "address of the web UI; 0.0.0.0:9090 to reach it from other machines")
	dataDir := flag.String("data-dir", "", "data directory: profiles, override, settings (default: Connaio in the user's config directory)")
	given := flag.String("token", "", "access token of the web UI (default: made once, kept in the data directory as web-token)")
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
	go func() { log.Fatal(http.Serve(front, webHandler(fmt.Sprintf("127.0.0.1:%d", port), token))) }()
	log.Printf("Connaio web UI on http://%s — open http://%s/?token=%s once to sign in", *listen, *listen, token)
	return app.Run()
}
