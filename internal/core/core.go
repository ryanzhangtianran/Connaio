// Package core runs connaio-core as a child process and talks to its REST API over a Unix socket
// (a named pipe on Windows).
package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ryanzhangtianran/Connaio/internal/run"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Core is the connaio-core the GUI talks to: its own child process, or an already running root
// daemon (enhanced mode). Dir is the GUI's data dir: config.yaml, core.log for the child.
type Core struct {
	Dir  string
	http *http.Client

	life    sync.Mutex // serialises Start, Stop and Attach
	mu      sync.Mutex
	socket  string
	logPath string
	daemon  bool
	cmd     *exec.Cmd
	exited  chan struct{}
}

func New(dir string) *Core {
	c := &Core{Dir: dir}
	c.http = client(c.Socket)
	return c
}

// client talks HTTP to the core at addr(); connections aren't kept, as the socket changes when
// switching between child and daemon.
func client(addr func() string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return dialControl(ctx, addr()) },
		DisableKeepAlives: true,
	}}
}

func (c *Core) Socket() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.socket
}

// Daemon reports whether the GUI is attached to the root daemon rather than its own child.
func (c *Core) Daemon() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.daemon
}

// Attach talks to a core that is already running on socket, e.g. the root daemon, whose log is at
// logPath. The switch is made only once that socket answers; until then the GUI keeps talking to
// whatever it had.
func (c *Core) Attach(ctx context.Context, socket, logPath string) error {
	c.life.Lock()
	defer c.life.Unlock()
	if err := c.wait(ctx, nil, logPath, socket); err != nil {
		return err
	}
	c.mu.Lock()
	c.socket, c.daemon, c.logPath = socket, true, logPath
	c.mu.Unlock()
	return nil
}

// wait polls socket until its API answers, the process exits or 15s pass.
func (c *Core) wait(ctx context.Context, exited <-chan struct{}, logPath, socket string) error {
	deadline := time.After(15 * time.Second)
	for {
		if err := c.probe(ctx, socket); err == nil {
			return nil
		}
		select {
		case <-exited:
			return fmt.Errorf("内核已退出：%s", problems(tail(logPath))) // its log's problem lines; the path is no help shown
		case <-deadline:
			return errors.New("内核 15 秒内未响应")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Binary finds connaio-core: $CONNAIO_CORE for development, else next to the app executable.
func Binary() (string, error) {
	if p := os.Getenv("CONNAIO_CORE"); p != "" {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	p := filepath.Join(filepath.Dir(exe), coreExe)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("connaio-core not found next to %s (set CONNAIO_CORE): %w", exe, err)
	}
	return p, nil
}

// Start launches the core and waits until its API answers; a core that never does is stopped
// again, and the GUI keeps whatever it was attached to.
func (c *Core) Start(ctx context.Context) error {
	c.life.Lock()
	defer c.life.Unlock()
	bin, err := Binary()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(c.ConfigPath()); err != nil {
		return fmt.Errorf("no config to start with: %w", err)
	}
	pidFile := filepath.Join(c.Dir, "core.pid")
	stopStale(pidFile) // a core left by a crashed or force-quit GUI still holds the ports

	logPath := filepath.Join(c.Dir, "core.log")
	_ = os.Rename(logPath, filepath.Join(c.Dir, "core.prev.log")) // keep the last run's log for diagnosis
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	socket := controlAddr(c.Dir)

	cmd := run.Command(bin, append([]string{"-d", c.Dir, "-exit-with-parent"}, controlArgs(socket)...)...) // a crashed GUI takes its core down with it
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		logFile.Close()
		_ = os.Remove(pidFile)
		close(exited)
	}()
	if err := c.wait(ctx, exited, logPath, socket); err != nil {
		stop(cmd, exited)
		return err
	}
	c.mu.Lock()
	c.socket, c.logPath, c.daemon, c.cmd, c.exited = socket, logPath, false, cmd, exited
	c.mu.Unlock()
	return nil
}

// Exited reports whether the GUI's own child is gone (never started, stopped or crashed).
// Pid is the child core's process id, 0 for none (or the daemon).
func (c *Core) Pid() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *Core) Exited() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil {
		return true
	}
	select {
	case <-c.exited:
		return true
	default:
		return false
	}
}

// tail is the last 256 KB of a log file: enough for the latest start, not the whole history.
func tail(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	const n = 256 << 10
	if st, err := f.Stat(); err == nil && st.Size() > n {
		_, _ = f.Seek(-n, io.SeekEnd)
	}
	b, _ := io.ReadAll(f)
	return string(b)
}

// stalePid is the pid a crashed or force-quit GUI left in pidFile, if any.
func stalePid(pidFile string) (int, bool) {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return 0, false
	}
	return pid, true
}

// BindErrors returns the listeners the core failed to open in its latest config load, from its
// log: a taken port doesn't stop the core, it just runs without that listener.
func (c *Core) BindErrors() []string {
	c.mu.Lock()
	path := c.logPath
	c.mu.Unlock()
	text := tail(path)
	if i := strings.LastIndex(text, "Start initial configuration in progress"); i >= 0 {
		text = text[i:]
	}
	return msgs(text, func(line string) bool { return strings.Contains(line, "address already in use") })
}

// ConfigPath is the config the core loads from its data dir.
func (c *Core) ConfigPath() string { return filepath.Join(c.Dir, "config.yaml") }

// Validate runs the core's config test on path, returning its message when the config is rejected.
// Without a deadline it gives up after 30s: the test can download geodata.
func (c *Core) Validate(ctx context.Context, path string) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	bin, err := Binary()
	if err != nil {
		return err
	}
	out, err := run.CommandContext(ctx, bin, "-t", "-d", c.Dir, "-f", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("配置校验失败: %s", problems(string(out)))
	}
	return nil
}

// Reload hands the running core a config. It goes as a payload, not a path, so a root daemon
// never reads files inside the user's home. force=true: without it the core skips inbound settings
// (ports, allow-lan); listeners whose address is unchanged are kept either way.
func (c *Core) Reload(ctx context.Context, config []byte) error {
	return c.Do(ctx, http.MethodPut, "/configs?force=true", map[string]string{"payload": string(config)}, nil)
}

// problems keeps the core's error/fatal lines from `-t` output, or its verdict when there are
// none: "configuration file <path> test failed" only repeats them, with a path.
func problems(out string) string {
	keep := msgs(out, func(line string) bool {
		return strings.Contains(line, "level=error") || strings.Contains(line, "level=fatal")
	})
	if len(keep) == 0 {
		keep = msgs(out, func(line string) bool { return strings.Contains(line, "failed") })
	}
	if len(keep) == 0 {
		return strings.TrimSpace(out)
	}
	return strings.Join(keep, "\n")
}

// msgs returns the log lines matching keep, reduced to their msg="..." text.
func msgs(text string, keep func(string) bool) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if keep(line) {
			if _, msg, ok := strings.Cut(line, `msg=`); ok {
				line = strings.Trim(msg, `"`)
				if u, err := strconv.Unquote(msg); err == nil { // logfmt quotes it: \" inside
					line = u
				}
			}
			out = append(out, line)
		}
	}
	return out
}

// Stop ends the GUI's own child (it restores routes and listeners on SIGTERM), killing it after 5s.
// An attached daemon keeps running.
func (c *Core) Stop() {
	c.life.Lock()
	defer c.life.Unlock()
	c.mu.Lock()
	cmd, exited := c.cmd, c.exited
	c.cmd = nil
	c.mu.Unlock()
	if cmd != nil {
		stop(cmd, exited)
	}
}

// API is the connaio-core controller API level this GUI needs (its constant.ConnaioAPI).
const API = 1

// Info is the core's GET /version: the mihomo release it is based on, and its Connaio API level
// (0 for a core that predates the field).
type Info struct {
	Version string `json:"version"`
	API     int    `json:"connaio"`
}

func (c *Core) Version(ctx context.Context) (Info, error) {
	var v Info
	err := c.Do(ctx, http.MethodGet, "/version", nil, &v)
	return v, err
}

// probe asks the core on socket for its version, whatever socket the GUI is attached to.
func (c *Core) probe(ctx context.Context, socket string) error {
	client := client(func() string { return socket })
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://core/version", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Do sends a JSON request; out may be nil. Non-2xx answers become errors carrying the core's message.
// A context without a deadline gets 10s, so a hung core never hangs the GUI.
func (c *Core) Do(ctx context.Context, method, path string, body, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://core"+path, reader)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, e.Message)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Stream reads a streaming endpoint such as /traffic, calling fn per JSON line until ctx ends or the stream breaks.
func (c *Core) Stream(ctx context.Context, path string, fn func(line []byte)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://core"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		fn(scanner.Bytes())
	}
	return scanner.Err()
}
