package main

import (
	"ibus-bamboo/config"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func ibusText(text string) dbus.Variant {
	return dbus.MakeVariant([]interface{}{"IBusText", map[string]dbus.Variant{}, text, dbus.MakeVariant(0)})
}

// surroundingScript drives an engine with explicit D-Bus serials, so a test
// can deliver reports and key events in any handling order.
type surroundingScript struct {
	t *testing.T
	e *IBusBambooEngine
}

func newSurroundingScript(t *testing.T, mode int) *surroundingScript {
	return &surroundingScript{t: t, e: newTestEngine(mode)}
}

func (s *surroundingScript) key(serial uint32, keyVal uint32) {
	s.e.processKeyEvent(keyVal, 0, 0, serial)
}

func (s *surroundingScript) keys(firstSerial uint32, keys string) {
	for i, key := range keys {
		s.key(firstSerial+uint32(i), uint32(key))
	}
}

func (s *surroundingScript) report(serial uint32, text string, cursor uint32) {
	s.e.setSurroundingText(ibusText(text), cursor, cursor, serial)
}

func (s *surroundingScript) wantBuffer(want string) {
	s.t.Helper()
	if got := s.e.getPreeditString(); got != want {
		s.t.Errorf("composition buffer = %q, want %q", got, want)
	}
}

// "bạn", Left, Left, "x" must give "bxạn": the tone key lands between b and
// ạ, where there is no vowel before it. The engine used to rebuild its
// buffer from a report sent before the cursor had finished moving ("xin bạ")
// and turned the word into "bãn".
func TestCursorMoveUsesTheReportSentForTheNextKey(t *testing.T) {
	s := newSurroundingScript(t, config.BackspaceForwardingIM)
	s.keys(10, "banj")
	s.report(20, "xin bạn", 7)
	s.key(21, IBusLeft)
	s.key(23, IBusLeft)
	s.report(22, "xin bạ", 6) // sent before the second Left, handled after it
	s.report(30, "xin b", 5)
	s.key(31, 'x')
	s.wantBuffer("bx")
}

func TestCursorMoveWaitsForALateReport(t *testing.T) {
	s := newSurroundingScript(t, config.BackspaceForwardingIM)
	s.e.capabilities = IBusCapSurroundingText
	s.keys(10, "banj")
	s.key(21, IBusLeft)
	s.report(20, "xin bạn", 7)
	go func() {
		time.Sleep(3 * surroundingWaitInterval)
		s.report(30, "xin b", 5) // sent before the key below, handled after it
	}()
	s.key(31, 'x')
	s.wantBuffer("bx")
}

func TestCursorMoveWithoutATrustworthyReportStartsANewWord(t *testing.T) {
	cases := []struct {
		name         string
		capabilities uint32
		report       func(s *surroundingScript)
	}{
		{"no report at all", 0, func(s *surroundingScript) {}},
		{"client never reports", IBusCapSurroundingText, func(s *surroundingScript) {}},
		{"only a report from before the move", 0, func(s *surroundingScript) { s.report(20, "xin bạn", 7) }},
		{"a selection is active", 0, func(s *surroundingScript) {
			s.e.setSurroundingText(ibusText("xin bạn"), 7, 4, 30)
		}},
		{"cursor beyond the text", 0, func(s *surroundingScript) { s.report(30, "xin", 9) }},
		{"malformed text", 0, func(s *surroundingScript) {
			s.e.setSurroundingText(dbus.MakeVariant("xin bạn"), 7, 7, 30)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSurroundingScript(t, config.BackspaceForwardingIM)
			s.e.capabilities = tc.capabilities
			s.keys(10, "banj")
			s.key(21, IBusLeft)
			tc.report(s)
			s.key(31, 'x')
			s.wantBuffer("x")
		})
	}
}

// After End the word before the cursor is picked up again, and a space then
// ends it, so the next word gets its tone ("nhes" -> "nhé").
func TestCursorMoveThenSpaceStartsACleanWord(t *testing.T) {
	s := newSurroundingScript(t, config.BackspaceForwardingIM)
	s.keys(10, "banj")
	s.key(21, IBusEnd)
	s.report(30, "xin bạn", 7)
	s.key(31, ' ')
	s.keys(40, "nhes")
	s.wantBuffer("nhé")
}

func TestOlderReportDoesNotReplaceANewerOne(t *testing.T) {
	s := newSurroundingScript(t, config.BackspaceForwardingIM)
	s.report(30, "xin b", 5)
	s.report(22, "xin bạ", 6)
	if got := string(s.e.surrounding.before); got != "xin b" {
		t.Errorf("stored report = %q, want the newer one", got)
	}
}

// Text typed in a backspace mode is already in the application. Moving to a
// window that uses pre-edit must not commit it a second time.
func TestFocusChangeDoesNotRecommitBackspaceModeText(t *testing.T) {
	e := newTestEngine(config.PreeditIM)
	out := e.IEngine.(*fakeEngine)
	e.wmClasses = "editor"
	e.config.InputModeMapping["editor"] = config.BackspaceForwardingIM
	typeKeys(e, "abc")
	committed := out.committedText()

	e.checkWmClass("browser")

	if got := out.committedText(); got != committed {
		t.Errorf("committed text after the focus change = %q, want %q unchanged", got, committed)
	}
	if e.getRawKeyLen() != 0 {
		t.Errorf("the composition buffer should be empty after a focus change")
	}
}

// A Reset that is not caused by typing may come from a mouse click. The
// next key checks where the cursor is before touching the buffer.
func TestResetAfterAClick(t *testing.T) {
	longAgo := time.Now().Add(-10 * resetQuietPeriod).UnixNano()
	cases := []struct {
		name       string
		mode       int
		quiet      bool                       // no key activity before the Reset
		reports    func(s *surroundingScript) // reports sent around the Reset (serial 20)
		wantBuffer string
	}{
		{"click elsewhere, reported after the reset (GTK)", config.BackspaceForwardingIM, true,
			func(s *surroundingScript) { s.report(19, "xin ban", 7); s.report(21, "x", 1) }, "xj"},
		{"click elsewhere, the new report is handled late", config.BackspaceForwardingIM, true,
			func(s *surroundingScript) {
				s.report(19, "xin ban", 7)
				go func() { time.Sleep(3 * surroundingWaitInterval); s.report(21, "x", 1) }()
			}, "xj"},
		{"click that kept the cursor, no new report (browsers)", config.BackspaceForwardingIM, true,
			func(s *surroundingScript) { s.report(19, "xin ban", 7) }, "bạn"},
		{"latest report shows another position", config.BackspaceForwardingIM, true,
			func(s *surroundingScript) { s.report(19, "xin b", 5) }, "j"},
		{"no report at all", config.BackspaceForwardingIM, true, func(s *surroundingScript) {}, "j"},
		{"reset caused by a key just handled", config.BackspaceForwardingIM, false,
			func(s *surroundingScript) { s.report(21, "x", 1) }, "bạn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSurroundingScript(t, tc.mode)
			s.e.capabilities = IBusCapSurroundingText
			s.keys(10, "ban")
			if tc.quiet {
				atomic.StoreInt64(&s.e.lastKeyActivity, longAgo)
			}
			tc.reports(s)
			s.e.reset(20)
			s.key(30, 'j')
			s.wantBuffer(tc.wantBuffer)
		})
	}

	t.Run("pre-edit mode always resets", func(t *testing.T) {
		s := newSurroundingScript(t, config.PreeditIM)
		s.keys(10, "ban")
		s.e.reset(20)
		s.wantBuffer("")
	})

	t.Run("keys still being handled", func(t *testing.T) {
		s := newSurroundingScript(t, config.BackspaceForwardingIM)
		s.keys(10, "ban")
		atomic.StoreInt64(&s.e.lastKeyActivity, longAgo)
		atomic.AddInt32(&s.e.pendingKeys, 1)
		s.e.reset(20)
		atomic.AddInt32(&s.e.pendingKeys, -1)
		if s.e.needSurroundingSync {
			t.Errorf("a Reset while keys are queued must not request a sync")
		}
	})
}

// Qt does not reset the input context on a mouse click. After a pause, the
// buffer is checked against the text before the cursor instead.
func TestPauseThenMismatchingTextResyncsTheBuffer(t *testing.T) {
	longAgo := time.Now().Add(-10 * resetQuietPeriod).UnixNano()
	cases := []struct {
		name       string
		pause      bool
		report     string
		cursor     uint32
		wantBuffer string
	}{
		{"cursor moved to another place", true, "x", 1, "xj"},
		{"cursor still after the word", true, "xin ban", 7, "bạn"},
		{"no pause: the report may predate our last commit", false, "x", 1, "bạn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSurroundingScript(t, config.BackspaceForwardingIM)
			s.keys(10, "ban")
			if tc.pause {
				atomic.StoreInt64(&s.e.lastKeyActivity, longAgo)
			}
			s.report(30, tc.report, tc.cursor)
			s.key(31, 'j')
			s.wantBuffer(tc.wantBuffer)
		})
	}

	t.Run("other output charsets are left alone", func(t *testing.T) {
		s := newSurroundingScript(t, config.BackspaceForwardingIM)
		s.e.config.OutputCharset = "VNI Windows"
		s.keys(10, "ban")
		atomic.StoreInt64(&s.e.lastKeyActivity, longAgo)
		s.report(30, "x", 1)
		s.key(31, 'j')
		s.wantBuffer("bạn")
	})
}

// Browsers report the surrounding text after handling a key press, which
// can come after the release of that key. Releases must not move the window
// in which reports are accepted.
func TestReportSentAfterAKeyReleaseIsUsed(t *testing.T) {
	s := newSurroundingScript(t, config.BackspaceForwardingIM)
	s.keys(10, "ban")
	s.e.processKeyEvent('n', 0, IBusReleaseMask, 14)
	s.report(13, "xin ban", 7) // sent after the press of n, handled after its release
	atomic.StoreInt64(&s.e.lastKeyActivity, time.Now().Add(-10*resetQuietPeriod).UnixNano())
	s.e.reset(15) // a click that did not move the cursor
	s.key(20, 'j')
	s.wantBuffer("bạn")
}

func TestExpectedText(t *testing.T) {
	var screen expectedText
	screen.committed([]rune("tie"))
	screen.deleted(1)
	screen.committed([]rune("ên"))
	if !screen.known || string(screen.tail) != "tiên" {
		t.Fatalf("tail = %q known=%v, want \"tiên\"", string(screen.tail), screen.known)
	}
	screen.deleted(9)
	if screen.known {
		t.Fatalf("deleting more than is tracked must make the tail unknown")
	}
	long := []rune(strings.Repeat("a", 2*expectedTailLen))
	screen.committed(long)
	if len(screen.tail) != expectedTailLen {
		t.Fatalf("tail length = %d, want %d", len(screen.tail), expectedTailLen)
	}
}

// With text-input-v3, a commit sent before the application applied the
// previous one replaces it. The engine waits for the report of its last
// change before sending the next one.
func TestWaitForScreenBeforeTheNextChange(t *testing.T) {
	newEngine := func() *IBusBambooEngine {
		e := newTestEngine(config.SurroundingTextIM)
		e.capabilities = IBusCapSurroundingText
		e.screen.committed([]rune("xin ti"))
		return e
	}

	t.Run("waits until the application reports the last change", func(t *testing.T) {
		e := newEngine()
		e.setSurroundingText(ibusText("xin t"), 5, 5, 1)
		go func() {
			time.Sleep(5 * screenWaitInterval)
			e.setSurroundingText(ibusText("xin ti"), 6, 6, 2)
		}()
		start := time.Now()
		e.Lock()
		e.waitForScreen()
		e.Unlock()
		if waited := time.Since(start); waited < 4*screenWaitInterval {
			t.Errorf("returned after %v, before the report arrived", waited)
		}
	})

	t.Run("an application that does not report is waited for once", func(t *testing.T) {
		e := newEngine()
		e.setSurroundingText(ibusText("xin t"), 5, 5, 1)
		e.Lock()
		e.waitForScreen()
		e.screen.committed([]rune("n"))
		start := time.Now()
		e.waitForScreen()
		e.Unlock()
		if waited := time.Since(start); waited > screenWaitInterval {
			t.Errorf("second wait took %v, want none after a timeout", waited)
		}
	})

	t.Run("no wait without surrounding text support", func(t *testing.T) {
		e := newEngine()
		e.capabilities = 0
		start := time.Now()
		e.Lock()
		e.waitForScreen()
		e.Unlock()
		if waited := time.Since(start); waited > screenWaitInterval {
			t.Errorf("waited %v", waited)
		}
	})
}

func TestNoWaitWithoutAPendingCommit(t *testing.T) {
	e := newTestEngine(config.SurroundingTextIM)
	e.capabilities = IBusCapSurroundingText
	e.screen.committed([]rune("xin ti"))
	e.setSurroundingText(ibusText("xin t"), 5, 5, 1)
	e.Lock()
	e.waitForScreen() // waits once for the commit
	start := time.Now()
	e.waitForScreen() // e.g. the commit after a deletion
	e.Unlock()
	if waited := time.Since(start); waited > screenWaitInterval {
		t.Errorf("waited %v with no commit pending", waited)
	}
}
