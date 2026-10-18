package daemon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework ServiceManagement
#import <Foundation/Foundation.h>
#import <ServiceManagement/ServiceManagement.h>
#include <stdlib.h>

#pragma clang diagnostic ignored "-Wunguarded-availability-new" // SMAppService is macOS 13; the app needs 15

static SMAppService *service(void) {
	return [SMAppService daemonServiceWithPlistName:@"com.ryanzhangtianran.connaio.core.plist"];
}

// 0 not registered, 1 enabled, 2 waiting for the user's approval, 3 not found in the bundle
static int serviceStatus(void) { return (int)service().status; }

// serviceRegister and serviceUnregister return NULL on success, else a malloc'd message.
static char *serviceRegister(void) {
	NSError *err = nil;
	return [service() registerAndReturnError:&err] ? NULL : strdup(err.localizedDescription.UTF8String);
}
static char *serviceUnregister(void) {
	NSError *err = nil;
	return [service() unregisterAndReturnError:&err] ? NULL : strdup(err.localizedDescription.UTF8String);
}
static void openLoginItems(void) { [SMAppService openSystemSettingsLoginItems]; }
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"
)

const (
	Label   = "com.ryanzhangtianran.connaio.core"
	Dir     = "/Library/Application Support/Connaio Core"
	Socket  = Dir + "/core.sock"
	LogPath = "/Library/Logs/Connaio Core.log" // launchd writes the daemon's output here (build/darwin/daemon.plist)

	// the LaunchDaemon of 0.1.3 and before: the core copied to a root-owned helper, so each update that
	// changed its bytes (each one, as signing does) asked for the administrator password again
	legacyPlist = "/Library/LaunchDaemons/" + Label + ".plist"
)

var legacyHelpers = []string{"/Library/PrivilegedHelperTools/Connaio Core", "/Library/PrivilegedHelperTools/" + Label}

const (
	notRegistered = iota
	enabled
	requiresApproval
)

func status() int { return int(C.serviceStatus()) }

func legacy() bool {
	_, err := os.Stat(legacyPlist)
	return err == nil
}

// InBundle says the daemon runs the core inside the app (SMAppService), not a copy: an app update
// brings it a new core, which it picks up once restarted.
const InBundle = true

// Installed reports whether the daemon is set up: registered and allowed, or the older
// LaunchDaemon still in place (Stale moves it over).
func Installed() bool { return status() == enabled || legacy() }

// Stale reports whether the older LaunchDaemon is still installed; Install moves it to the one run
// from the app. The app's own core needs no reinstalling: see InBundle.
func Stale(string) bool { return legacy() }

// Install registers the daemon in the app's bundle (build/darwin/daemon.plist) with launchd, after
// removing the older LaunchDaemon (its last administrator prompt). The first time, macOS asks the
// user to allow it in System Settings, which is opened for them; this waits up to two minutes.
func Install(string) error {
	if legacy() {
		if err := asAdmin(legacyRemoval(), "Connaio 要把 root 内核换成随 App 更新的方式，以后更新不再需要输入密码。"); err != nil {
			return err
		}
	}
	if status() != enabled {
		if msg := C.serviceRegister(); msg != nil {
			defer C.free(unsafe.Pointer(msg))
			if status() != requiresApproval {
				return fmt.Errorf("注册 root 内核失败：%s", C.GoString(msg))
			}
		}
	}
	if status() == requiresApproval {
		C.openLoginItems()
		for deadline := time.Now().Add(2 * time.Minute); status() == requiresApproval && time.Now().Before(deadline); {
			time.Sleep(time.Second)
		}
	}
	if status() != enabled {
		return errors.New("请在「系统设置 → 通用 → 登录项与扩展」中允许 Connaio 在后台运行，然后再试一次")
	}
	return nil
}

// Uninstall stops and unregisters the daemon, and removes the older LaunchDaemon if it is there; its
// data dir is kept.
func Uninstall() error {
	if legacy() {
		if err := asAdmin(legacyRemoval(), "Connaio 需要移除 root 权限。"); err != nil {
			return err
		}
	}
	if status() == notRegistered {
		return nil
	}
	if msg := C.serviceUnregister(); msg != nil {
		defer C.free(unsafe.Pointer(msg))
		return fmt.Errorf("移除 root 内核失败：%s", C.GoString(msg))
	}
	return nil
}

// legacyRemoval stops the older LaunchDaemon and deletes it and its helper; it runs as root.
func legacyRemoval() string {
	files := []string{shellQuote(legacyPlist)}
	for _, h := range legacyHelpers {
		files = append(files, shellQuote(h))
	}
	return "launchctl bootout system/" + Label + " 2>/dev/null || true\nrm -f " + strings.Join(files, " ")
}

// asAdmin runs a shell script as root through the standard macOS administrator prompt.
func asAdmin(script, prompt string) error {
	msg := runAppleScript(fmt.Sprintf("do shell script %s with prompt %s with administrator privileges", appleQuote(script), appleQuote(prompt)))
	switch {
	case msg == "":
		return nil
	case strings.HasPrefix(msg, "-128 "): // the user pressed Cancel
		return fmt.Errorf("已取消授权")
	default:
		return fmt.Errorf("授权失败：%s", msg)
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// RunAsAdmin runs a shell script after the administrator prompt, for network repairs.
func RunAsAdmin(script, prompt string) error { return asAdmin(script, prompt) }
