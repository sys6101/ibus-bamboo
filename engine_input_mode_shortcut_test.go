package main

import (
	"ibus-bamboo/config"
	"testing"
)

const ibusLockMask = 1 << 1

// Shift+~ must open the input mode table whatever the lock and layout-group
// bits are: CapsLock (Lock) and the second XKB layout (bit 13) are not
// modifiers the user pressed.
func TestInputModeShortcutIgnoresLockAndGroupBits(t *testing.T) {
	for _, extra := range []uint32{0, ibusLockMask, 1 << 4, 1 << 13, ibusLockMask | 1<<13} {
		e := newTestEngine(config.PreeditIM)
		handled, _ := e.ProcessKeyEvent('~', 0, IBusShiftMask|extra)
		if !handled || !e.isInputModeLTOpened {
			t.Errorf("state extra 0x%x: handled=%v opened=%v, want the table opened", extra, handled, e.isInputModeLTOpened)
		}
	}
}

// The panel hides the table when the focus leaves; the engine must forget it
// too, or the next Shift+~ only "closes" a table nobody sees.
func TestInputModeTableClosesOnFocusOut(t *testing.T) {
	e := newTestEngine(config.PreeditIM)
	e.ProcessKeyEvent('~', 0, IBusShiftMask)
	if !e.isInputModeLTOpened {
		t.Fatal("table not opened")
	}
	e.FocusOut()
	if e.isInputModeLTOpened {
		t.Fatal("table still marked open after FocusOut")
	}
	e.ProcessKeyEvent('~', 0, IBusShiftMask)
	if !e.isInputModeLTOpened {
		t.Error("Shift+~ after a focus change did not open the table")
	}
}
