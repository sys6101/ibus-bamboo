package main

import (
	"errors"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	gnomeFocusWindowScript = "global.get_window_actors().find(window => !Main.overview.visible && window.meta_window.has_focus()).get_meta_window().get_wm_class()"
	gnomeOverviewScript    = "Main.overview.visible"
	// gnomeEvalRetryInterval is how long Shell.Eval is left alone after it
	// turned out to be unavailable.
	gnomeEvalRetryInterval = 5 * time.Minute
)

var errGnomeEvalUnavailable = errors.New("org.gnome.Shell.Eval is not available")

// gnomeEvalBlockedUntil is the time before which Shell.Eval is not called.
var (
	gnomeEvalMu           sync.Mutex
	gnomeEvalBlockedUntil time.Time
)

// gnomeShellEval runs a script in GNOME Shell. It is a variable so tests can
// replace it. dbus.SessionBus returns a connection shared by the whole
// process, so it is neither re-initialized nor closed here.
var gnomeShellEval = func(script string) (ok bool, result string, err error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false, "", err
	}
	obj := conn.Object("org.gnome.Shell", "/org/gnome/Shell")
	err = obj.Call("org.gnome.Shell.Eval", 0, script).Store(&ok, &result)
	return ok, result, err
}

// gnomeGetFocusWindowClass asks GNOME Shell for the focused window class.
//
// GNOME 41 and later refuse Eval unless the shell runs in unsafe mode: the
// call then fails, or returns (false, ""). This is asked on every focus
// change, so once Eval is found unavailable it is skipped for a while
// instead of paying D-Bus round trips for nothing each time.
func gnomeGetFocusWindowClass() (string, error) {
	gnomeEvalMu.Lock()
	blocked := time.Now().Before(gnomeEvalBlockedUntil)
	gnomeEvalMu.Unlock()
	if blocked {
		return "", errGnomeEvalUnavailable
	}

	ok, wmClass, err := gnomeShellEval(gnomeFocusWindowScript)
	if err != nil || (!ok && wmClass == "") {
		gnomeEvalMu.Lock()
		gnomeEvalBlockedUntil = time.Now().Add(gnomeEvalRetryInterval)
		gnomeEvalMu.Unlock()
		if err == nil {
			err = errGnomeEvalUnavailable
		}
		return "", err
	}
	if ok {
		return wmClass, nil
	}
	// The script threw: no window has the focus, e.g. in the overview.
	if isGnomeOverviewVisible() {
		return "org.gnome.Overview", nil
	}
	return "", errors.New(wmClass)
}

func isGnomeOverviewVisible() bool {
	ok, visible, err := gnomeShellEval(gnomeOverviewScript)
	return err == nil && ok && visible == "true"
}
