package sysproxy

import (
	"context"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// prefsDir holds preferences.plist, where System Settings and networksetup keep every service's
// proxies; a variable for the test.
var prefsDir = "/Library/Preferences/SystemConfiguration"

// Changes signals after the network settings change (another app's proxy among them), watched with
// kqueue rather than polled: it costs nothing while they stay put. A burst of writes may come as one
// signal. nil if they can't be watched.
func Changes(ctx context.Context) <-chan struct{} {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil
	}
	dir, err := unix.Open(prefsDir, unix.O_EVTONLY, 0)
	if err != nil {
		unix.Close(kq)
		return nil
	}
	// the directory sees a file saved by replacing it; the file itself, one written in place
	watch := func(fd int) error {
		ev := unix.Kevent_t{}
		unix.SetKevent(&ev, fd, unix.EVFILT_VNODE, unix.EV_ADD|unix.EV_CLEAR)
		ev.Fflags = unix.NOTE_WRITE | unix.NOTE_EXTEND | unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_ATTRIB
		_, err := unix.Kevent(kq, []unix.Kevent_t{ev}, nil, nil)
		return err
	}
	file := -1
	watchFile := func() { // again after each change: a replaced file is a new one
		if file >= 0 {
			unix.Close(file) // closing drops its registration
		}
		if file, _ = unix.Open(filepath.Join(prefsDir, "preferences.plist"), unix.O_EVTONLY, 0); file >= 0 {
			watch(file)
		}
	}
	if watch(dir) != nil {
		unix.Close(dir)
		unix.Close(kq)
		return nil
	}
	watchFile()
	changed := make(chan struct{}, 1)
	go func() {
		<-ctx.Done()
		unix.Close(kq) // ends the wait below
	}()
	go func() {
		defer unix.Close(dir)
		events := make([]unix.Kevent_t, 4)
		for {
			if _, err := unix.Kevent(kq, nil, events, nil); err != nil {
				if err == unix.EINTR {
					continue
				}
				if file >= 0 {
					unix.Close(file)
				}
				return
			}
			watchFile()
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}()
	return changed
}
