package main

import (
	"reflect"
	"testing"

	"ibus-bamboo/config"
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
