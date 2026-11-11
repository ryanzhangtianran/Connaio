//go:build server

package app

import (
	"crypto/rand"
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

	"github.com/ryanzhangtianran/Connaio/internal/cli"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Run is Connaio's: the app with no window, tray or desktop, driven by the command line through
// --listen behind the access token (web.go), the core always this process's own child.
func Run(fs.FS, []byte) error {
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
	listen := flag.String("listen", "127.0.0.1:9090", "address the command line calls; 0.0.0.0:9090 to reach it from other machines")
	dataDir := flag.String("data-dir", "", "data directory: profiles, override, settings (default: Connaio in the user's config directory)")
	given := flag.String("token", "", "access token of the command line (default: made once, kept in the data directory as web-token)")
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
	secret := rand.Text()

	app := application.New(application.Options{
		Name:         "Connaio",
		Description:  "Rule-based proxy with reverse tunnels",
		MarshalError: marshalError,
		Services:     sv.list(),
		Assets:       application.AssetOptions{Handler: http.NotFoundHandler(), Middleware: innerOnly(secret)}, // the services alone, at /wails/runtime
		Server:       application.ServerOptions{Host: "127.0.0.1", Port: port},
	})
	handler := webHandler(fmt.Sprintf("127.0.0.1:%d", port), token, secret)
	// on the internet with --listen 0.0.0.0: a client stalling its headers is dropped
	go func() {
		log.Fatal((&http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}).Serve(front))
	}()
	// the token itself never in the log: the journal is readable by more than root
	log.Printf("Connaio listening for the command line on %s, its access token in %s", *listen, filepath.Join(*dataDir, "web-token"))
	return app.Run()
}
