package app

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// desktopApp is an installed application, from its .desktop entry.
type desktopApp struct {
	name, exec, icon string // exec: the executable's base name, as processes are matched
	cmd              string // the program as the Exec line names it
	dir              string // the directory it is installed in, when one of its own (appDir)
}

// desktopDirs hold .desktop entries: the system's, the user's, snaps' and flatpaks'.
func desktopDirs() []string {
	dirs := []string{"/usr/share/applications", "/usr/local/share/applications", "/var/lib/snapd/desktop/applications", "/var/lib/flatpak/exports/share/applications"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local/share/applications"), filepath.Join(home, ".local/share/flatpak/exports/share/applications"))
	}
	return dirs
}

// desktopApps reads the installed applications once.
var desktopApps = sync.OnceValue(func() []desktopApp {
	var apps []desktopApp
	for _, dir := range desktopDirs() {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.desktop"))
		for _, p := range paths {
			if b, err := os.ReadFile(p); err == nil {
				if a := parseDesktop(string(b)); a.exec != "" {
					a.dir = appDir(a.cmd)
					apps = append(apps, a)
				}
			}
		}
	}
	return apps
})

// parseDesktop reads the [Desktop Entry] of a .desktop file; exec is empty for entries that aren't
// launchable applications (hidden, or with no command).
func parseDesktop(text string) desktopApp {
	var a desktopApp
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || section != "[Desktop Entry]" {
			continue
		}
		switch k {
		case "Name":
			a.name = v
		case "Icon":
			a.icon = v
		case "Exec":
			a.cmd = execPath(v)
			a.exec = filepath.Base(a.cmd)
		case "NoDisplay", "Hidden":
			if v == "true" {
				return desktopApp{}
			}
		case "Type":
			if v != "Application" {
				return desktopApp{}
			}
		}
	}
	return a
}

// execPath is the program an Exec line runs, past env and its assignments.
func execPath(cmd string) string {
	for _, f := range strings.Fields(cmd) {
		f = strings.Trim(f, `"'`)
		if f == "env" || strings.Contains(f, "=") {
			continue
		}
		return f
	}
	return ""
}

// sharedBins are directories of many programs: an Exec program in one has no directory of its own.
var sharedBins = []string{"/", "/usr/bin", "/bin", "/usr/sbin", "/sbin", "/usr/local/bin", "/usr/games", "/snap/bin", "/usr/lib", "/usr/lib64", "/usr/libexec", "/usr/local/lib", "/opt"}

// appDir is the directory an application is installed in, when it has one of its own: its Exec
// program followed through PATH and symlinks. Launchers often only start the real binary from
// there: /usr/bin/bytedance-feishu-stable is /opt/bytedance/feishu/bytedance-feishu, a script
// that runs ./feishu. "" for one in a shared bin directory.
func appDir(cmd string) string {
	p, err := exec.LookPath(cmd)
	if err != nil {
		return ""
	}
	if p, err = filepath.EvalSymlinks(p); err != nil {
		return ""
	}
	if dir := filepath.Dir(p); !slices.Contains(sharedBins, dir) {
		return dir
	}
	return ""
}

// appName is the name of the installed application the executable at path belongs to, "" for none.
func appName(path string) string {
	a, _ := appOf(strings.TrimSuffix(path, " (deleted)"))
	return a.name
}

// appOf is the installed application an executable (a path, or a bare name) belongs to: the one
// whose Exec runs it by name, or which is installed in its directory or one below
// (/opt/wemeet/wemeetapp.sh runs /opt/wemeet/bin/wemeetapp).
func appOf(exe string) (desktopApp, bool) {
	base, dir := filepath.Base(exe), filepath.Dir(exe)
	for _, a := range desktopApps() {
		if a.exec == base || a.dir != "" && (a.dir == dir || strings.HasPrefix(dir, a.dir+"/")) {
			return a, true
		}
	}
	return desktopApp{}, false
}

// runningApps lists the installed applications that are running, by the executables in /proc.
func runningApps() []ProcessChoice {
	var apps []ProcessChoice
	seen := map[string]bool{}
	exes, _ := filepath.Glob("/proc/[0-9]*/exe")
	for _, p := range exes {
		target, err := os.Readlink(p) // other users' processes can't be read: skipped
		if err != nil {
			continue
		}
		if a, ok := appOf(strings.TrimSuffix(target, " (deleted)")); ok && !seen[a.exec] {
			seen[a.exec] = true
			apps = append(apps, ProcessChoice{Name: a.name, Exec: a.exec, Source: "app"})
		}
	}
	slices.SortFunc(apps, func(a, b ProcessChoice) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return apps
}

// PickApp lets the user choose an application's .desktop entry and returns its executable name.
func (s *SettingsService) PickApp() (ProcessChoice, error) {
	path, err := application.Get().Dialog.OpenFile().
		SetTitle("选择应用").
		SetDirectory("/usr/share/applications").
		CanChooseFiles(true).
		AddFilter("应用程序", "*.desktop").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return ProcessChoice{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ProcessChoice{}, err
	}
	a := parseDesktop(string(b))
	if a.exec == "" {
		return ProcessChoice{}, os.ErrInvalid
	}
	return ProcessChoice{Name: a.name, Exec: a.exec, Source: "app"}, nil
}

var appIcons sync.Map // executable path -> data URL ("" when none)

// AppIcon is the icon of the application whose executable is path, from its .desktop entry and
// the icon theme, as a data URL; a command of no application wears the theme's terminal icon, as
// on macOS it wears Finder's for executables. "" when there is none, and the window shows its glyph.
func (s *SettingsService) AppIcon(path string) string {
	if v, ok := appIcons.Load(path); ok {
		return v.(string)
	}
	icon := "utilities-terminal"
	if path == "device" { // a LAN device or bare address, as the window asks for one
		icon = "computer"
	} else if path == "system" { // the system's services, folded into one client (clientOf)
		icon = "preferences-system"
	} else if a, ok := appOf(path); ok && a.icon != "" {
		icon = a.icon
	}
	url := iconURL(icon)
	appIcons.Store(path, url)
	return url
}

// iconTheme is the desktop's icon theme (GNOME's setting), "" when it can't be read.
var iconTheme = sync.OnceValue(func() string {
	out, _ := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "icon-theme").Output()
	return strings.Trim(strings.TrimSpace(string(out)), "'")
})

// iconURL finds a theme icon (or takes an icon path) and reads it as a data URL: the desktop's
// theme first, then Adwaita, then hicolor, where applications install theirs.
func iconURL(icon string) string {
	candidates := []string{icon}
	if !filepath.IsAbs(icon) {
		candidates = nil
		var roots []string
		for _, theme := range []string{iconTheme(), "Adwaita", "hicolor"} {
			if theme != "" {
				roots = append(roots, "/usr/share/icons/"+theme)
			}
		}
		if home, err := os.UserHomeDir(); err == nil {
			roots = append(roots, filepath.Join(home, ".local/share/icons/hicolor"))
		}
		for _, root := range roots {
			for _, size := range []string{"scalable", "256x256", "128x128", "96x96", "64x64", "48x48", "32x32@2x"} {
				for _, kind := range []string{"apps", "mimetypes", "devices"} {
					for _, ext := range []string{".svg", ".png"} {
						candidates = append(candidates, filepath.Join(root, size, kind, icon+ext))
					}
				}
			}
		}
		candidates = append(candidates, "/usr/share/pixmaps/"+icon+".png", "/usr/share/pixmaps/"+icon+".svg")
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		mime := "image/png"
		if strings.HasSuffix(p, ".svg") {
			mime = "image/svg+xml"
		} else {
			b = trimPNG(b)
		}
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
	}
	return ""
}

// trimPNG crops an icon's transparent margin to a square around what is drawn, so icons drawn with
// a wide margin (Yaru's, Ghostty's) show as large as those drawn edge to edge; data unchanged if
// it doesn't decode.
func trimPNG(data []byte) []byte {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	box, b := drawnBox(img, 0x0800), img.Bounds() // ignore the faintest shadow
	side := max(box.Dx(), box.Dy())
	if side == 0 || side >= b.Dx() && side >= b.Dy() {
		return data
	}
	out := image.NewNRGBA(image.Rect(0, 0, side, side))
	draw.Draw(out, image.Rect((side-box.Dx())/2, (side-box.Dy())/2, side, side), img, box.Min, draw.Src)
	var buf bytes.Buffer
	if png.Encode(&buf, out) != nil {
		return data
	}
	return buf.Bytes()
}

// procTable reads the process table from /proc.
func procTable() map[int]proc {
	procs := map[int]proc{}
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		stat, err := os.ReadFile(d + "/stat")
		if err != nil {
			continue
		}
		exe, _ := os.Readlink(d + "/exe") // another user's: unreadable, the stat comm (15 chars) stands in
		pid, _ := strconv.Atoi(filepath.Base(d))
		p := linuxProc(string(stat), strings.TrimSuffix(exe, " (deleted)"))
		_, p.app = appOf(p.comm)
		procs[pid] = p
	}
	return procs
}

// linuxProc reads a /proc/<pid>/stat line, "pid (comm) S ppid …", comm holding any character.
func linuxProc(stat, exe string) proc {
	i := strings.LastIndex(stat, ")")
	if i < 0 {
		return proc{}
	}
	f := strings.Fields(stat[i+1:])
	p := proc{comm: exe}
	if len(f) > 1 {
		p.ppid, _ = strconv.Atoi(f[1])
	}
	if p.comm == "" {
		p.comm = stat[strings.Index(stat, "(")+1 : i]
	}
	return p
}
