package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// runningApps lists the processes that have a window, as the user-facing apps. Exec is the image
// name with its extension, which is how the core's PROCESS-ANCESTOR sees Windows processes.
func runningApps() []ProcessChoice {
	out, _ := powershell("Get-Process | Where-Object MainWindowTitle | Select-Object -ExpandProperty ProcessName -Unique")
	return parseApps(out)
}

func parseApps(out string) []ProcessChoice {
	var apps []ProcessChoice
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			apps = append(apps, ProcessChoice{Name: name, Exec: name + ".exe", Source: "app"})
		}
	}
	slices.SortFunc(apps, func(a, b ProcessChoice) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return apps
}

// PickApp lets the user choose a program and returns its image name.
func (s *SettingsService) PickApp() (ProcessChoice, error) {
	path, err := application.Get().Dialog.OpenFile().
		SetTitle("选择应用").
		CanChooseFiles(true).
		AddFilter("应用程序", "*.exe").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return ProcessChoice{}, err
	}
	exe := filepath.Base(path)
	return ProcessChoice{Name: strings.TrimSuffix(exe, filepath.Ext(exe)), Exec: exe, Source: "app"}, nil
}

const windowsLogo = "data:image/svg+xml," +
	"%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 23 23'%3E" +
	"%3Cpath fill='%23f25022' d='M1 1h10v10H1z'/%3E%3Cpath fill='%237fba00' d='M12 1h10v10H12z'/%3E" +
	"%3Cpath fill='%2300a4ef' d='M1 12h10v10H1z'/%3E%3Cpath fill='%23ffb900' d='M12 12h10v10H12z'/%3E%3C/svg%3E"

var winIcons, winNames sync.Map // executable path -> icon data URL / its description, "" when none

// AppIcon is the icon of the executable at path, as a PNG data URL; "" when it has none, and the
// window shows its glyph.
func (s *SettingsService) AppIcon(path string) string {
	if path == "system" { // the system's services, folded into one client (clientOf): the four squares
		return windowsLogo
	}
	if v, ok := winIcons.Load(path); ok {
		return v.(string)
	}
	url := ""
	if h := w32.ExtractIcon(path, 0); h > 1 { // 0: no icon, 1: not an executable
		url = iconPNG(h)
		w32.DestroyIcon(h)
	}
	winIcons.Store(path, url)
	return url
}

// iconPNG is an icon as a PNG data URL, by way of a temporary file, which is how w32 converts one.
func iconPNG(h w32.HICON) string {
	f, err := os.CreateTemp("", "connaio-icon-*.png")
	if err != nil {
		return ""
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)
	if w32.SaveHIconAsPNG(h, name) != nil {
		return ""
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
}

// appName is what the executable at path calls itself ("Google Chrome" for chrome.exe), as Linux
// names an app from its .desktop entry; "" when it says nothing.
func appName(path string) string {
	if v, ok := winNames.Load(path); ok {
		return v.(string)
	}
	name := fileDescription(path)
	winNames.Store(path, name)
	return name
}

// fileDescription reads FileDescription from an executable's version resource.
func fileDescription(path string) string {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 {
		return ""
	}
	buf := make([]byte, size)
	block := unsafe.Pointer(&buf[0])
	if windows.GetFileVersionInfo(path, 0, size, block) != nil {
		return ""
	}
	var lang *[2]uint16 // the first language and code page the strings are in
	var n uint32
	if windows.VerQueryValue(block, `\VarFileInfo\Translation`, unsafe.Pointer(&lang), &n) != nil || n < 4 {
		return ""
	}
	var text *uint16
	key := fmt.Sprintf(`\StringFileInfo\%04x%04x\FileDescription`, lang[0], lang[1])
	if windows.VerQueryValue(block, key, unsafe.Pointer(&text), &n) != nil || n == 0 {
		return ""
	}
	return strings.TrimSpace(windows.UTF16PtrToString(text))
}

// procTable reads the process table: the windowed processes are the apps, as runningApps lists
// them; one script, so the table is read once.
func procTable() map[int]proc {
	out, _ := powershell(`$w = @{}; Get-Process | Where-Object MainWindowTitle | ForEach-Object { $w[($_.ProcessName + '.exe').ToLower()] = 1 }; ` +
		`Get-CimInstance Win32_Process | ForEach-Object { "$($_.ProcessId) $($_.ParentProcessId) $(if ($w[$_.Name.ToLower()]) {1} else {0}) $($_.Name)" }`)
	return windowsProcs(out)
}

// windowsProcs reads "pid ppid isapp name" lines.
func windowsProcs(out string) map[int]proc {
	procs := map[int]proc{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		procs[pid] = proc{ppid: ppid, comm: strings.Join(f[3:], " "), app: f[2] == "1"}
	}
	return procs
}
