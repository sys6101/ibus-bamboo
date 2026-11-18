package main

import (
	"ibus-bamboo/config"
	"reflect"
	"sync/atomic"
	"testing"
)

func setGnomeWayland(t *testing.T, on bool) {
	t.Helper()
	oldWayland, oldGnome := isWayland, isGnome
	isWayland, isGnome = on, on
	t.Cleanup(func() { isWayland, isGnome = oldWayland, oldGnome })
}

func typeWithHandler(e *IBusBambooEngine, keys string) {
	e.Lock()
	defer e.Unlock()
	for _, key := range keys {
		e.keyPressHandler(uint32(key), 0, 0)
	}
}

func forwardedPresses(f *fakeEngine) []uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint32(nil), f.forwardedPresses...)
}

// GNOME Shell on Wayland drops the modifiers of forwarded keys, so Shift+Left
// would only move the cursor and the new letter would land before the old one.
func TestShiftLeftModeOnGnomeWayland(t *testing.T) {
	tests := []struct {
		name         string
		gnomeWayland bool
		want         []uint32
	}{
		{"X11", false, []uint32{IBusLeft}},
		{"GNOME Wayland", true, []uint32{IBusLeft, IBusDelete}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setGnomeWayland(t, tc.gnomeWayland)
			e, out := newQueuedEngine(config.ShiftLeftForwardingIM)
			e.Destroy()
			typeWithHandler(e, "aa")
			if got := forwardedPresses(out); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("forwarded keys = %#x, want %#x", got, tc.want)
			}
			if got := out.committedText(); got != "aâ" {
				t.Errorf("committed text = %q, want %q", got, "aâ")
			}
		})
	}
}

// GNOME Shell on Wayland maps forwarded keycodes with the keymap, so letters
// forwarded with keycode 0 never reach the application.
func TestForwardAsCommitModeOnGnomeWayland(t *testing.T) {
	tests := []struct {
		name         string
		gnomeWayland bool
		wantKeys     []uint32
		wantCommit   string
	}{
		{"X11", false, []uint32{'a', IBusBackSpace, 'â'}, ""},
		{"GNOME Wayland", true, []uint32{IBusBackSpace}, "aâ"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setGnomeWayland(t, tc.gnomeWayland)
			e, out := newQueuedEngine(config.ForwardAsCommitIM)
			e.Destroy()
			typeWithHandler(e, "aa")
			if got := forwardedPresses(out); !reflect.DeepEqual(got, tc.wantKeys) {
				t.Errorf("forwarded keys = %#x, want %#x", got, tc.wantKeys)
			}
			if got := out.committedText(); got != tc.wantCommit {
				t.Errorf("committed text = %q, want %q", got, tc.wantCommit)
			}
		})
	}
}

// Mutter 50.0 to 50.3 drops forwarded keys, so on GNOME Wayland the modes
// that forward keys delete through the surrounding text when the
// application reports it. Applications that do not report it, such as
// Chromium on X11, ignore deletions through it.
func TestForwardingModesUseSurroundingTextOnGnomeWayland(t *testing.T) {
	tests := []struct {
		name         string
		gnomeWayland bool
		reported     bool
		mode         int
		want         int
	}{
		{"X11", false, true, config.BackspaceForwardingIM, config.BackspaceForwardingIM},
		{"no surrounding text", true, false, config.BackspaceForwardingIM, config.BackspaceForwardingIM},
		{"backspace forwarding", true, true, config.BackspaceForwardingIM, config.SurroundingTextIM},
		{"Shift+Left", true, true, config.ShiftLeftForwardingIM, config.SurroundingTextIM},
		{"forward as commit", true, true, config.ForwardAsCommitIM, config.SurroundingTextIM},
		{"pre-edit", true, true, config.PreeditIM, config.PreeditIM},
		{"XTest", true, true, config.XTestFakeKeyEventIM, config.XTestFakeKeyEventIM},
		{"surrounding text, reported", false, true, config.SurroundingTextIM, config.SurroundingTextIM},
		{"surrounding text, not reported", false, false, config.SurroundingTextIM, config.BackspaceForwardingIM},
		{"surrounding text, GNOME Wayland, not reported", true, false, config.SurroundingTextIM, config.BackspaceForwardingIM},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setGnomeWayland(t, tc.gnomeWayland)
			e := newTestEngine(tc.mode)
			atomic.StoreInt32(&e.surroundingReported, 0)
			if tc.reported {
				e.SetSurroundingText(ibusText("abc"), 3, 3)
			}
			if got := e.effectiveInputMode(); got != tc.want {
				t.Errorf("effective mode = %d, want %d", got, tc.want)
			}
		})
	}
}

// A key the application handles itself changes the text in a way the engine
// does not know, but a commit sent before it may still be in flight.
func TestPassedKeyKeepsThePendingCommit(t *testing.T) {
	var screen expectedText
	screen.committed([]rune("ab"))
	screen.lose()
	if screen.known || len(screen.tail) != 0 {
		t.Errorf("text still known after a key the application handled: %q", string(screen.tail))
	}
	if !screen.pendingCommit {
		t.Error("the commit sent before the key is no longer pending")
	}
}
