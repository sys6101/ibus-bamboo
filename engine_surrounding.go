/*
 * Bamboo - A Vietnamese Input method editor
 * Copyright (C) 2018 Luong Thanh Lam <ltlam93@gmail.com>
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package main

import (
	"ibus-bamboo/config"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BambooEngine/bamboo-core"
	ibus "github.com/BambooEngine/goibus"
	"github.com/godbus/dbus/v5"
)

// After the cursor moves (arrow keys, Home, End...) the composition buffer
// no longer matches the text before the cursor. In the backspace input modes
// it is rebuilt from the surrounding text the application reports.
//
// Applications report that text right before each key event, so the report
// to trust is the one sent between the previous key event and the current
// one. IBus delivers every D-Bus call on its own goroutine, hence reports and
// key events may be handled out of order; their message serials tell the
// real order. Using whichever report came first after a movement key (as the
// engine used to) often picked one from before the cursor had moved.

// defaultOutputCharset is the charset in which the committed text equals the
// composition buffer.
const defaultOutputCharset = "Unicode"

const (
	surroundingWaitPolls    = 20
	surroundingWaitInterval = time.Millisecond
)

// surroundingText is the latest text-before-cursor report. It has its own
// lock so that storing a report never waits for key handling.
type surroundingText struct {
	mu sync.Mutex
	// silent is set when the application did not report its text in time;
	// waiting is skipped until it reports again.
	silent bool
	before []rune // text before the cursor
	serial uint32 // D-Bus serial of the report
	seen   bool   // a report was received
	usable bool   // the report has a plain cursor (no selection)
}

// serialAfter reports whether serial a was issued after serial b,
// tolerating wrap-around.
func serialAfter(a, b uint32) bool {
	return int32(a-b) > 0
}

// surroundingString extracts the string of an IBusText variant, which
// arrives as the struct (name, attachments, text, attributes).
func surroundingString(text dbus.Variant) (str string, ok bool) {
	defer func() {
		if recover() != nil {
			str, ok = "", false
		}
	}()
	fields := reflect.ValueOf(text.Value())
	if fields.Kind() != reflect.Slice || fields.Len() < 3 {
		return "", false
	}
	str, ok = fields.Index(2).Interface().(string)
	return str, ok
}

func (e *IBusBambooEngine) setSurroundingText(text dbus.Variant, cursorPos uint32, anchorPos uint32, serial uint32) {
	str, ok := surroundingString(text)
	if ok {
		atomic.StoreInt32(&e.surroundingReported, 1)
	}
	var runes = []rune(str)
	ok = ok && cursorPos == anchorPos && int(cursorPos) <= len(runes)
	var before []rune
	if ok {
		before = runes[:cursorPos]
	}

	e.surrounding.mu.Lock()
	defer e.surrounding.mu.Unlock()
	if e.surrounding.seen && serialAfter(e.surrounding.serial, serial) {
		return // an older report handled late
	}
	e.surrounding.silent = false
	e.surrounding.before = before
	e.surrounding.serial = serial
	e.surrounding.seen = true
	e.surrounding.usable = ok
}

// freshSurroundingText returns the report sent for the key event described
// by arrival, if it has been received.
func (e *IBusBambooEngine) freshSurroundingText(arrival keyArrival) (before []rune, usable bool, fresh bool) {
	return e.reportBetween(arrival.prevSerial, arrival.serial)
}

// reportBetween returns the latest report if its serial is after `after`
// and before `before`.
func (e *IBusBambooEngine) reportBetween(after, before uint32) (text []rune, usable bool, ok bool) {
	e.surrounding.mu.Lock()
	defer e.surrounding.mu.Unlock()
	s := &e.surrounding
	if !s.seen || !serialAfter(s.serial, after) || serialAfter(s.serial, before) {
		return nil, false, false
	}
	return append([]rune(nil), s.before...), s.usable, true
}

// waitForReport polls get for a short while, with the engine lock released.
// The caller holds the lock.
func (e *IBusBambooEngine) waitForReport(get func() ([]rune, bool, bool)) ([]rune, bool, bool) {
	text, usable, ok := get()
	if ok || e.capabilities&IBusCapSurroundingText == 0 {
		return text, usable, ok
	}
	e.Unlock()
	defer e.Lock()
	for i := 0; i < surroundingWaitPolls && !ok; i++ {
		time.Sleep(surroundingWaitInterval)
		text, usable, ok = get()
	}
	return text, usable, ok
}

// syncWithSurroundingText makes the composition buffer match the text
// before the cursor when the cursor may have moved. The caller holds the
// engine lock; it is released while waiting for a report.
//
// A movement is known from a movement key; the report sent for the next key
// then shows the new position. A Reset may mean a mouse click, but
// applications differ: GTK and Qt report the text after the click, while
// browsers report only after the text changes, so the latest report may
// predate the click and still be accurate. Some applications (Qt) do not
// reset on a click at all, so after a pause in typing the buffer is also
// checked against the reported text.
//
// Whenever the position is unknown, the buffer is dropped: the key then
// starts a new word instead of rewriting text it cannot see.
func (e *IBusBambooEngine) syncWithSurroundingText(arrival keyArrival) {
	var verify = !e.needSurroundingSync && arrival.afterPause && e.getRawKeyLen() > 0
	if !e.needSurroundingSync && !verify {
		return
	}
	var afterReset, resetSerial = e.syncAfterReset, e.resetSerial
	e.needSurroundingSync = false
	e.syncAfterReset = false
	if arrival.queueWasBusy || atomic.LoadInt32(&e.pendingKeys) > 0 {
		return // queued keys are still changing the buffer
	}
	// The buffer can only be compared with the screen when it is shown
	// as is.
	var comparable = e.config.OutputCharset == defaultOutputCharset
	var word = e.getPreeditString()

	var before []rune
	var usable, ok bool
	if afterReset {
		before, usable, ok = e.waitForReport(func() ([]rune, bool, bool) {
			return e.reportBetween(resetSerial, arrival.serial)
		})
		if !ok {
			// No report since the reset: fall back to the latest one.
			before, usable, ok = e.reportBetween(arrival.serial-1<<31, arrival.serial)
			if !ok || !usable || !comparable || !strings.HasSuffix(string(before), word) {
				e.preeditor.Reset()
				return
			}
		}
	} else {
		before, usable, ok = e.waitForReport(func() ([]rune, bool, bool) {
			return e.freshSurroundingText(arrival)
		})
		if verify && !comparable {
			return
		}
	}
	if !ok || !usable {
		if !verify {
			e.preeditor.Reset()
		}
		return
	}
	if (verify || afterReset) && comparable && strings.HasSuffix(string(before), word) {
		return // the cursor is still right after the word being typed
	}
	debugf("Surrounding Text: %s", string(before))
	e.preeditor.Reset()
	for i := len(before) - 1; i >= 0; i-- {
		// workaround for spell checking
		if bamboo.IsPunctuationMark(before[i]) && e.preeditor.CanProcessKey(before[i]) {
			before[i] = ' '
		}
		e.preeditor.ProcessKey(before[i], bamboo.EnglishMode|bamboo.InReverseOrder)
	}
}

// expectedText tracks the end of the text before the cursor as it should be
// once the application has applied every commit and deletion sent to it.
type expectedText struct {
	tail  []rune
	known bool
	// pendingCommit is set after a commit, until the application has been
	// seen to apply it or the engine stopped waiting for that.
	pendingCommit bool
}

// expectedTailLen bounds how much of the text is tracked.
const expectedTailLen = 64

func (t *expectedText) committed(s []rune) {
	t.tail = append(t.tail, s...)
	if len(t.tail) > expectedTailLen {
		t.tail = append([]rune(nil), t.tail[len(t.tail)-expectedTailLen:]...)
	}
	t.known = true
	t.pendingCommit = true
}

func (t *expectedText) deleted(n int) {
	if !t.known || n > len(t.tail) {
		t.forget()
		return
	}
	t.tail = t.tail[:len(t.tail)-n]
	if len(t.tail) == 0 {
		t.known = false
	}
}

// lose marks the text as unknown, as after a key the application handled
// itself, but keeps a commit that may still be in flight.
func (t *expectedText) lose() {
	t.tail = nil
	t.known = false
}

func (t *expectedText) forget() {
	t.tail = nil
	t.known = false
	t.pendingCommit = false
}

const (
	screenWaitPolls    = 30
	screenWaitInterval = 2 * time.Millisecond
	pokeEveryPolls     = 3
)

// waitForScreen waits, for a short bounded time, until the application has
// reported the text the engine sent last, before sending more.
//
// Several commits or deletions that reach an application before it has
// applied the previous one can be lost: with text-input-v3 (GNOME on
// Wayland) only the last commit of a batch is applied, and deletions are
// measured against the last text reported. Fast typing then lost whole
// words in the backspace input modes.
//
// The caller holds the engine lock; it is released while waiting.
func (e *IBusBambooEngine) waitForScreen() {
	// Only a commit can be overtaken: a deletion is applied together with
	// the commit that follows it.
	if !e.screen.pendingCommit {
		return
	}
	e.screen.pendingCommit = false
	// XTestFakeKeyEvent mode only works on X11, where the problem does not
	// occur; it keeps its own timing.
	if e.checkInputMode(config.XTestFakeKeyEventIM) {
		return
	}
	// GNOME Shell turns the capability off on every reset: whether the
	// application has reported its text tells better.
	if e.capabilities&IBusCapSurroundingText == 0 && atomic.LoadInt32(&e.surroundingReported) == 0 {
		return
	}
	if !e.screen.known || e.config.OutputCharset != defaultOutputCharset {
		e.keepCommitsApart()
		return
	}
	var want = string(e.screen.tail)
	var silent bool
	var check = func() bool {
		e.surrounding.mu.Lock()
		defer e.surrounding.mu.Unlock()
		silent = e.surrounding.silent
		return e.surrounding.usable && strings.HasSuffix(string(e.surrounding.before), want)
	}
	if check() {
		return
	}
	if silent {
		e.keepCommitsApart()
		return
	}
	e.Unlock()
	defer e.Lock()
	for i := 0; i < screenWaitPolls; i++ {
		time.Sleep(screenWaitInterval)
		// Firefox on GNOME Wayland reports its text when it receives
		// the next commit, and takes that report before applying it: the
		// text reported always lags one change behind, and Mutter measures
		// deletions against it. An empty commit makes it report again.
		if i%pokeEveryPolls == pokeEveryPolls-1 && forwardedKeysUnreliable() {
			e.CommitText(ibus.NewText(""))
		}
		if check() {
			return
		}
	}
	// The application does not report every change: do not wait for it
	// again until it reports.
	e.surrounding.mu.Lock()
	e.surrounding.silent = true
	e.surrounding.mu.Unlock()
}

// commitGap is the time left between two commits that the application has
// not confirmed. GNOME Shell sends the text of each commit to the
// application at once but applies it with a "done" event sent once its
// main loop is idle, so two commits that reach it together collapse into
// the last one.
const commitGap = 20 * time.Millisecond

// keepCommitsApart pauses until commitGap has passed since the last commit.
func (e *IBusBambooEngine) keepCommitsApart() {
	var elapsed = time.Duration(time.Now().UnixNano() - e.lastCommitText)
	if elapsed >= 0 && elapsed < commitGap {
		e.pause(commitGap - elapsed)
	}
}
