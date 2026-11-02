package app

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/unix"
)

// runningApps lists the running apps' main executables, from the process table.
func runningApps() []ProcessChoice {
	var paths []string
	for _, p := range procTable() {
		paths = append(paths, p.comm)
	}
	return parseApps(paths)
}

// parseApps keeps the main executables of top-level app bundles among executable paths.
func parseApps(paths []string) []ProcessChoice {
	var apps []ProcessChoice
	seen := map[string]bool{}
	for _, path := range paths {
		app, ok := appOfPath(path)
		if !ok || seen[strings.ToLower(app.Exec)] {
			continue
		}
		seen[strings.ToLower(app.Exec)] = true
		apps = append(apps, app)
	}
	slices.SortFunc(apps, func(a, b ProcessChoice) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return apps
}

// appOfPath is the app a top-level bundle's main executable belongs to; not for helpers (apps
// nested inside another bundle) or the system's own agents (Dock, ControlCenter, the framework
// agents), which nobody routes. /System/Applications (Mail, Messages, App Store) counts.
func appOfPath(path string) (ProcessChoice, bool) {
	bundle := bundleOf(path)
	if bundle == "" || systemDaemon(path) {
		return ProcessChoice{}, false
	}
	exe, ok := strings.CutPrefix(path[len(bundle)+1:], "Contents/MacOS/")
	if !ok || strings.Contains(exe, "/") {
		return ProcessChoice{}, false
	}
	return ProcessChoice{Name: strings.TrimSuffix(filepath.Base(bundle), ".app"), Exec: exe, Source: "app"}, true
}

// procTable reads the process table from the kernel, each process's executable by its path: a
// millisecond or two, where starting ps took tens.
func procTable() map[int]proc {
	kps, _ := unix.SysctlKinfoProcSlice("kern.proc.all")
	procs := make(map[int]proc, len(kps))
	for _, k := range kps {
		pid := int(k.Proc.P_pid)
		p := proc{ppid: int(k.Eproc.Ppid), comm: execPath(pid)}
		if p.comm == "" { // gone, or not this user's to ask: its short name, as ps shows it then
			p.comm = unix.ByteSliceToString(k.Proc.P_comm[:])
		}
		_, p.app = appOfPath(p.comm)
		procs[pid] = p
	}
	return procs
}

// execPath is pid's executable, by proc_pidpath's syscall (as the core's process lookup); "" if unknown.
func execPath(pid int) string {
	const callPIDInfo, pidPathInfo, maxPath = 2, 11, 4 * 1024 // PROC_INFO_CALL_PIDINFO, PROC_PIDPATHINFO, PROC_PIDPATHINFO_MAXSIZE
	buf := make([]byte, maxPath)
	if _, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, callPIDInfo, uintptr(pid), pidPathInfo, 0, uintptr(unsafe.Pointer(&buf[0])), maxPath); errno != 0 {
		return ""
	}
	return unix.ByteSliceToString(buf)
}

// PickApp lets the user choose an app in Finder and returns its executable name.
func (s *SettingsService) PickApp() (ProcessChoice, error) {
	path, err := application.Get().Dialog.OpenFile().
		SetTitle("选择应用").
		SetDirectory("/Applications").
		CanChooseFiles(true).
		AddFilter("应用程序", "*.app").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return ProcessChoice{}, err
	}
	return appExecutable(path)
}

// appExecutable reads a bundle's CFBundleExecutable.
func appExecutable(bundle string) (ProcessChoice, error) {
	out, err := exec.Command("plutil", "-extract", "CFBundleExecutable", "raw", filepath.Join(bundle, "Contents", "Info.plist")).Output()
	if err != nil {
		return ProcessChoice{}, fmt.Errorf("%s 不是应用程序: %w", bundle, err)
	}
	return ProcessChoice{Name: strings.TrimSuffix(filepath.Base(bundle), ".app"), Exec: strings.TrimSpace(string(out)), Source: "app"}, nil
}

// appName names a Linux executable's application; here bundles (bundleOf) already fold processes into apps.
func appName(string) string { return "" }
