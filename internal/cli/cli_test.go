package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fake is a running instance's front: it answers calls by method name and records them.
func fake(t *testing.T, answers map[string]string) (addr string, calls *[]string) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("connaio_token"); err != nil || c.Value != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Args struct {
				MethodName string `json:"methodName"`
				Args       []any  `json:"args"`
			} `json:"args"`
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &req); err != nil {
			t.Errorf("request %s: %v", b, err)
		}
		m := strings.TrimPrefix(req.Args.MethodName, svc)
		a, _ := json.Marshal(req.Args.Args)
		got = append(got, m+string(a))
		answer, ok := answers[m]
		switch {
		case !ok:
			io.WriteString(w, "{}")
		case strings.HasPrefix(answer, "text:"):
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, strings.TrimPrefix(answer, "text:"))
		case strings.HasPrefix(answer, "error:"):
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, `{"message":"`+strings.TrimPrefix(answer, "error:")+`"}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, answer)
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), &got
}

func main(t *testing.T, addr string, args ...string) (string, error) {
	var out strings.Builder
	err := Main(append(args, "--addr", addr, "--token", "tok"), &out) // the flags after the arguments
	return out.String(), err
}

func TestStatus(t *testing.T) {
	addr, _ := fake(t, map[string]string{
		"CoreService.Status":         `{"running":true,"version":"1.19.32"}`,
		"CoreService.Mode":           "text:rule",
		"EnhancedService.Mode":       "text:tun",
		"ProfileService.Profiles":    `{"current":"a","profiles":[{"id":"a","name":"订阅"}]}`,
		"CoreService.ReverseTunnels": `[{"name":"OLA-4090","state":"running"}]`,
	})
	out, err := main(t, addr, "status")
	if err != nil {
		t.Fatal(err)
	}
	// values start at one display column (the widest label, 隧道 OLA-4090, is 13 wide, then 2 spaces)
	// the rules as wide as the table: the label column, 2 spaces, the address
	rule := strings.Repeat("-", 13+2+len("http://"+addr)) + "\n"
	want := rule + "内核           运行中（1.19.32）\n出站模式       规则\nTUN 模式       开启（全部流量）\n配置           订阅\n" + rule +
		"隧道 OLA-4090  运行中\n" + rule + "网页界面       http://" + addr + "\n" + rule
	if out != want {
		t.Errorf("status:\n%s\nwant:\n%s", out, want)
	}
}

func TestCommandsCall(t *testing.T) {
	addr, calls := fake(t, map[string]string{
		"ProfileService.Profiles": `{"profiles":[{"id":"p1","name":"家里"}]}`,
		"CoreService.Select":      "error:no group nope",
	})
	for _, args := range [][]string{{"mode", "全局"}, {"tunnel", "stop", "vps"}, {"enhanced", "on"}, {"profile", "use", "家里"}} {
		if _, err := main(t, addr, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	want := []string{`CoreService.SetMode["global"]`, `ProfileService.SetTunnelDisabled["vps",true]`, `EnhancedService.SetFull[true]`,
		`ProfileService.Profiles[]`, `ProfileService.Select["p1"]`}
	if strings.Join(*calls, " ") != strings.Join(want, " ") {
		t.Errorf("calls %v\nwant  %v", *calls, want)
	}
	if _, err := main(t, addr, "select", "nope", "x"); err == nil || err.Error() != "no group nope" {
		t.Errorf("a failed call: %v", err)
	}
	for _, args := range [][]string{{"bogus"}, {"mode", "fast"}, {"profile", "use", "nope"}, {"select", "only-one"}} {
		if _, err := main(t, addr, args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

// TestToken: a wrong token says so, rather than reading as a core that isn't running; with none
// given, the data directory's is read.
func TestToken(t *testing.T) {
	addr, _ := fake(t, map[string]string{"CoreService.Status": `{"running":true}`})
	var out strings.Builder
	if err := Main([]string{"status", "--addr", addr, "--token", "wrong"}, &out); err == nil || !strings.Contains(err.Error(), "令牌") {
		t.Errorf("wrong token: %v %q", err, out.String())
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "web-token"), []byte("tok\n"), 0o600)
	out.Reset()
	if err := Main([]string{"url", "--addr", addr, "--data-dir", dir}, &out); err != nil || out.String() != "http://"+addr+"/?token=tok\n" {
		t.Errorf("token from the data directory: %q %v", out.String(), err)
	}
}

func TestListenAddr(t *testing.T) {
	dir := t.TempDir()
	for content, want := range map[string]string{
		"": "127.0.0.1:9090",
		"# a comment\nCONNAIO_LISTEN=0.0.0.0:9091\n": "127.0.0.1:9091",
		`CONNAIO_LISTEN="[::]:9092"`:                 "127.0.0.1:9092",
		"CONNAIO_LISTEN=:9093":                       "127.0.0.1:9093",
		"CONNAIO_LISTEN=10.0.0.5:9094":               "10.0.0.5:9094",
		"CONNAIO_LISTEN=nonsense":                    "127.0.0.1:9090",
	} {
		f := filepath.Join(dir, "connaio")
		os.WriteFile(f, []byte(content), 0o644)
		if got := listenAddr(f); got != want {
			t.Errorf("%q: %s, want %s", content, got, want)
		}
	}
	if got := listenAddr(filepath.Join(dir, "missing")); got != "127.0.0.1:9090" {
		t.Errorf("no file: %s", got)
	}
}

func TestDisplayWidth(t *testing.T) {
	for s, w := range map[string]int{"abc": 3, "内核": 4, "隧道 OLA": 8, "规则匹配": 8} {
		if displayWidth(s) != w {
			t.Errorf("%q: %d, want %d", s, displayWidth(s), w)
		}
	}
}

// TestMenu: a node and a mode chosen by number reach the same calls as the commands; an invalid
// choice and the end of the input leave the menu.
func TestMenu(t *testing.T) {
	addr, calls := fake(t, map[string]string{
		"CoreService.Status": `{"running":true,"version":"1.19.32"}`,
		"CoreService.Groups": `[{"name":"GLOBAL"},{"name":"Proxy","now":"A","all":[{"name":"A","delay":80},{"name":"B"}]}]`,
	})
	t.Setenv("CONNAIO_ADDR", addr)
	t.Setenv("CONNAIO_TOKEN", "tok")
	var out strings.Builder
	// 8 切换节点 → group 1 → node 2; 7 出站模式 → 全局; 99 invalid; then the input ends
	if err := Menu(strings.NewReader("8\n1\n2\n\n7\n2\n\n99\n"), &out); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range *calls {
		if c != "CoreService.Status[]" {
			got = append(got, c)
		}
	}
	want := []string{`CoreService.Groups[]`, `CoreService.Select["Proxy","B"]`, `CoreService.SetMode["global"]`}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("calls %v\nwant  %v", got, want)
	}
	for _, s := range []string{"Connaio 管理菜单", "运行中（1.19.32）", "Proxy  → A", "*  A  80 ms", "   B  —", "无效的选项"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("menu lacks %q:\n%s", s, out.String())
		}
	}
}
