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
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BambooEngine/bamboo-core"
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

const (
	surroundingWaitPolls    = 20
	surroundingWaitInterval = time.Millisecond
)

// surroundingText is the latest text-before-cursor report. It has its own
// lock so that storing a report never waits for key handling.
type surroundingText struct {
	mu     sync.Mutex
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
	e.surrounding.before = before
	e.surrounding.serial = serial
	e.surrounding.seen = true
	e.surrounding.usable = ok
}

// freshSurroundingText returns the report sent for the key event described
// by arrival, if it has been received.
func (e *IBusBambooEngine) freshSurroundingText(arrival keyArrival) (before []rune, usable bool, fresh bool) {
	e.surrounding.mu.Lock()
	defer e.surrounding.mu.Unlock()
	s := &e.surrounding
	if !s.seen || !serialAfter(s.serial, arrival.prevSerial) || serialAfter(s.serial, arrival.serial) {
		return nil, false, false
	}
	return append([]rune(nil), s.before...), s.usable, true
}

// syncWithSurroundingText rebuilds the composition buffer after a cursor
// movement. Without a trustworthy report the buffer stays empty, so the key
// starts a new word instead of changing text it cannot see. The caller holds
// the engine lock; it is released while waiting for a report.
func (e *IBusBambooEngine) syncWithSurroundingText(arrival keyArrival) {
	if !e.needSurroundingSync {
		return
	}
	e.needSurroundingSync = false
	if arrival.queueWasBusy || atomic.LoadInt32(&e.pendingKeys) > 0 {
		return // queued keys are still changing the buffer
	}
	before, usable, fresh := e.freshSurroundingText(arrival)
	if !fresh && e.capabilities&IBusCapSurroundingText != 0 {
		// The report for this key may still be on its way.
		e.Unlock()
		for i := 0; i < surroundingWaitPolls && !fresh; i++ {
			time.Sleep(surroundingWaitInterval)
			before, usable, fresh = e.freshSurroundingText(arrival)
		}
		e.Lock()
	}
	if !fresh || !usable {
		return
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
