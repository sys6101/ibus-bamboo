package main

import (
	"ibus-bamboo/config"
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
