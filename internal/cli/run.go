package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
)

// runProxied runs command with the proxy variables pointed at the running instance's mixed-port,
// for the programs that read them (curl, git, pip, apt…): no export, the shell left as it is.
func runProxied(command []string) error {
	if len(command) == 0 {
		return errors.New("用法：connaio run <命令> [参数]")
	}
	c, err := connect(defaultAddr(), os.Getenv("CONNAIO_TOKEN"), "")
	if err != nil {
		return err
	}
	port, err := c.proxyPort()
	if err != nil {
		return err
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = proxyEnv(os.Environ(), port)
	signal.Ignore(os.Interrupt) // Ctrl-C is the command's: this waits for it and passes its status on
	err = cmd.Run()
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	return err
}

// proxyPort is the running instance's mixed-port.
func (c *client) proxyPort() (int, error) {
	var lan struct{ Port int }
	if err := c.call(&lan, "DeviceService.Lan"); err != nil {
		return 0, err
	}
	if lan.Port == 0 {
		return 0, errors.New("当前配置没有设置 mixed-port，没有可用的代理端口")
	}
	return lan.Port, nil
}

// proxyKeys are the variables proxyEnv sets, in lower case; each is set in upper case too.
var proxyKeys = []string{"http_proxy", "https_proxy", "all_proxy", "no_proxy"}

// exportLines is shell code exporting proxyEnv's variables, for eval "$(connaio enable)": a
// program can't change the environment of the shell that runs it.
func exportLines(env []string, port int) string {
	var b strings.Builder
	for _, kv := range proxyEnv(env, port) {
		k, v, _ := strings.Cut(kv, "=")
		if slices.Contains(proxyKeys, strings.ToLower(k)) {
			fmt.Fprintf(&b, "export %s='%s'\n", k, strings.ReplaceAll(v, "'", `'\''`))
		}
	}
	return b.String()
}

// unsetLines is shell code unsetting the proxy variables, for eval "$(connaio disable)".
func unsetLines() string {
	var b strings.Builder
	for _, k := range proxyKeys {
		fmt.Fprintf(&b, "unset %s %s\n", k, strings.ToUpper(k))
	}
	return b.String()
}

// proxyEnv is env with the proxy variables, in both cases, pointed at 127.0.0.1:port; a no_proxy
// already set is kept, else loopback goes direct.
func proxyEnv(env []string, port int) []string {
	proxy := fmt.Sprintf("http://127.0.0.1:%d", port)
	set := map[string]string{"http_proxy": proxy, "https_proxy": proxy, "all_proxy": fmt.Sprintf("socks5h://127.0.0.1:%d", port)}
	noProxy := "localhost,127.0.0.1,::1"
	var out []string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch k = strings.ToLower(k); {
		case set[k] != "":
			continue
		case k == "no_proxy":
			noProxy = v
			continue
		}
		out = append(out, kv)
	}
	set["no_proxy"] = noProxy
	for k, v := range set {
		out = append(out, k+"="+v, strings.ToUpper(k)+"="+v)
	}
	return out
}
