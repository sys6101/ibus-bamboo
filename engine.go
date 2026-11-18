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
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BambooEngine/bamboo-core"
	ibus "github.com/BambooEngine/goibus"
	"github.com/godbus/dbus/v5"

	"ibus-bamboo/config"
	"ibus-bamboo/ui"
)

type IBusBambooEngine struct {
	sync.Mutex
	IEngine
	preeditor            bamboo.IEngine
	engineName           string
	config               *config.Config
	propList             *ibus.PropList
	englishMode          bool
	macroTable           *MacroTable
	wmClasses            string
	isInputModeLTOpened  bool
	isEmojiLTOpened      bool
	isInHexadecimal      bool
	emojiLookupTable     *ibus.LookupTable
	inputModeLookupTable *ibus.LookupTable
	capabilities         uint32
	// surroundingReported is set (atomically) once the application focused
	// last has reported its surrounding text. GNOME Shell turns the
	// capability on and off as it resets, so the capability cannot tell.
	surroundingReported  int32
	keyPressDelay        int
	nFakeBackSpace       int32
	isFirstTimeSendingBS bool
	emoji                *EmojiEngine
	// needSurroundingSync is set when the cursor may have moved: the next
	// key first rebuilds the composition buffer from the text before the
	// cursor (see syncWithSurroundingText).
	needSurroundingSync bool
	// screen is what the engine expects to be right before the cursor once
	// the application has applied everything sent to it.
	screen expectedText
	// syncAfterReset tells that the pending sync comes from a Reset with
	// the D-Bus serial resetSerial, rather than from a movement key.
	syncAfterReset   bool
	resetSerial      uint32
	surrounding      surroundingText
	lastKeySerial    uint32 // serial of the previous key event, atomic
	localSerial      uint32 // serials for calls that carry none, atomic
	activeKeys       int32  // key events currently inside processKeyEvent, atomic
	lastKeyActivity  int64  // UnixNano of the last key event or key handling, atomic
	lastKeyWithShift bool
	lastCommitText   int64
	// restore key strokes by pressing Shift + Space
	shouldRestoreKeyStrokes bool
	// enqueue key strokes to process later
	shouldEnqueuKeyStrokes bool
	// Each engine owns its key queue and the goroutine draining it, so two
	// engines (Bamboo and BambooUs, or a re-created engine) can never
	// handle each other's keystrokes.
	keyPressChan chan [3]uint32
	// pendingKeys counts keys that are queued or being handled by the
	// queue goroutine. It is read without the engine lock.
	pendingKeys int32
	done        chan struct{}
	destroyOnce sync.Once
	// keyGate admits key events one at a time, in arrival order.
	keyGate *fifoGate
}

// fifoGate lets goroutines through one at a time in the order they arrived.
// A sync.Mutex does not promise that order: with several key events waiting
// behind the queue goroutine, a later key could get in first and the typed
// text came out scrambled.
type fifoGate struct {
	mu      sync.Mutex
	turn    *sync.Cond
	next    uint64 // ticket handed to the next arrival
	serving uint64 // ticket currently allowed through
}

func newFifoGate() *fifoGate {
	g := &fifoGate{}
	g.turn = sync.NewCond(&g.mu)
	return g
}

func (g *fifoGate) enter() {
	g.mu.Lock()
	ticket := g.next
	g.next++
	for g.serving != ticket {
		g.turn.Wait()
	}
	g.mu.Unlock()
}

func (g *fifoGate) leave() {
	g.mu.Lock()
	g.serving++
	g.mu.Unlock()
	g.turn.Broadcast()
}

// Locking: IBus calls the methods below from one goroutine per D-Bus message,
// and the key queue goroutine runs alongside them. The embedded mutex guards
// all engine state. Exported IBus methods take it; every other method expects
// the caller to hold it. Blocking work (window class lookup, the settings GUI,
// waiting for the key queue) is done with the lock released.

const keyQueueSize = 100

func NewIbusBambooEngine(name string, cfg *config.Config, base IEngine, preeditor bamboo.IEngine) *IBusBambooEngine {
	e := &IBusBambooEngine{
		engineName: name,
		IEngine:    base,
		preeditor:  preeditor,
		config:     cfg,
		// Created here, not in the asynchronous init, so an early key or
		// focus event can never see a nil emoji engine or macro table.
		emoji:      NewEmojiEngine(),
		macroTable: NewMacroTable(cfg.IBflags&config.IBautoCapitalizeMacro != 0),

		keyPressChan: make(chan [3]uint32, keyQueueSize),
		done:         make(chan struct{}),
		keyGate:      newFifoGate(),
	}
	go e.keyPressCapturing()
	return e
}

// Destroy stops the key queue goroutine before the engine is unexported.
func (e *IBusBambooEngine) Destroy() *dbus.Error {
	e.destroyOnce.Do(func() { close(e.done) })
	return e.IEngine.Destroy()
}

/*
*
Implement IBus.Engine's process_key_event default signal handler.

Args:

	keyval - The keycode, transformed through a keymap, stays the
		same for every keyboard
	keycode - Keyboard-dependant key code
	modifiers - The state of IBus.ModifierType keys like
		Shift, Control, etc.

Return:

	True - if successfully process the keyevent
	False - otherwise. The keyevent will be passed to X-Client

This function gets called whenever a key is pressed.
*/
func (e *IBusBambooEngine) ProcessKeyEvent(keyVal uint32, keyCode uint32, state uint32) (bool, *dbus.Error) {
	return e.processKeyEvent(keyVal, keyCode, state, e.nextLocalSerial())
}

// ProcessKeyEventMsg is ProcessKeyEvent as exported on D-Bus: it also takes
// the message, whose serial orders the call against SetSurroundingText.
func (e *IBusBambooEngine) ProcessKeyEventMsg(keyVal uint32, keyCode uint32, state uint32, msg dbus.Message) (bool, *dbus.Error) {
	return e.processKeyEvent(keyVal, keyCode, state, msg.Serial())
}

// keyArrival describes the moment a key event reached the engine.
type keyArrival struct {
	// queueWasBusy tells whether earlier keys were still being handled.
	queueWasBusy bool
	// serial and prevSerial are the D-Bus serials of this key event and of
	// the one before it.
	serial, prevSerial uint32
	// afterPause tells whether typing had stopped for a while before this
	// key, long enough for the cursor to have been moved with the mouse.
	afterPause bool
}

func (e *IBusBambooEngine) processKeyEvent(keyVal, keyCode, state, serial uint32) (bool, *dbus.Error) {
	if state&IBusReleaseMask != 0 {
		// Releases are not part of the key order used to date surrounding
		// text reports: browsers report the text after handling a press,
		// often after its release has been sent.
		return false, nil
	}
	var arrival = keyArrival{serial: serial, prevSerial: atomic.SwapUint32(&e.lastKeySerial, serial)}
	arrival.afterPause = e.isResetFromOutside()
	atomic.AddInt32(&e.activeKeys, 1)
	defer func() {
		e.touchKeyActivity()
		atomic.AddInt32(&e.activeKeys, -1)
	}()
	if keyVal == IBusBackSpace && e.consumeFakeBackspace() {
		// One of our own XTest backspaces coming back. It is acknowledged
		// without the engine lock: the queue goroutine holds that lock
		// while it waits for exactly these events to pass through.
		return false, nil
	}
	// A key that arrives while earlier keys are still being handled must
	// follow them through the queue. Deciding that only after waiting for
	// the lock would let it take the direct path right behind them, and
	// its reply would race the text they commit.
	arrival.queueWasBusy = atomic.LoadInt32(&e.pendingKeys) > 0
	// Keys take a ticket first so they are handled in the order typed, then
	// the engine lock, which they may release while waiting for the queue.
	e.keyGate.enter()
	defer e.keyGate.leave()
	e.Lock()
	defer e.Unlock()
	debugf(">>>>ProcessKeyEvent >  %d | state %d keyVal 0x%04x | %c <<<<", atomic.LoadInt32(&e.pendingKeys), state, keyVal, rune(keyVal))
	if ret, retValue := e.processShortcutKey(keyVal, keyCode, state); ret {
		return retValue, nil
	}
	if e.inBackspaceWhiteList() {
		handled, err := e.bsProcessKeyEvent(keyVal, keyCode, state, arrival)
		if !handled {
			// The application handles the key itself.
			e.screen.lose()
		}
		return handled, err
	}
	return e.preeditProcessKeyEvent(keyVal, keyCode, state)
}

func (e *IBusBambooEngine) nextLocalSerial() uint32 {
	return atomic.AddUint32(&e.localSerial, 1)
}

func (e *IBusBambooEngine) FocusIn() *dbus.Error {
	debugf("FocusIn.")
	atomic.StoreInt32(&e.surroundingReported, 0)
	// The window class lookup talks to X11 or D-Bus; keep it off the lock.
	var latestWm = e.getLatestWmClass()
	e.Lock()
	e.checkWmClass(latestWm)
	var propList = e.propList
	var needEmoji = e.isShortcutKeyEnable(KSEmojiDialog)
	var needDictionary = e.config.IBflags&config.IBspellCheckWithDicts != 0
	e.Unlock()

	e.RegisterProperties(propList)
	e.RequireSurroundingText()
	if needEmoji {
		ensureEmojiTrie()
	}
	if needDictionary {
		ensureDictionary()
	}
	debugf("WM_CLASS=(%s)", latestWm)
	return nil
}

func (e *IBusBambooEngine) FocusOut() *dbus.Error {
	debugf("FocusOut.")
	return nil
}

func (e *IBusBambooEngine) Reset() *dbus.Error {
	e.reset(e.nextLocalSerial())
	return nil
}

// ResetMsg is Reset as exported on D-Bus; the message serial dates the reset
// against the surrounding text reports.
func (e *IBusBambooEngine) ResetMsg(msg dbus.Message) *dbus.Error {
	e.reset(msg.Serial())
	return nil
}

func (e *IBusBambooEngine) reset(serial uint32) {
	debugf("Reset.")
	e.Lock()
	defer e.Unlock()
	if e.checkInputMode(config.PreeditIM) {
		e.preeditor.Reset()
	} else if e.inBackspaceWhiteList() && e.isResetFromOutside() {
		// The cursor may have been moved without a key, typically by a
		// mouse click. The next key checks where the cursor is now.
		e.resetFakeBackspace()
		e.needSurroundingSync = true
		e.screen.forget()
		e.syncAfterReset = true
		e.resetSerial = serial
	}
}

// resetQuietPeriod is how long after the last key a Reset is still blamed on
// that key.
const resetQuietPeriod = 150 * time.Millisecond

// isResetFromOutside tells whether a Reset comes from something other than
// typing. Applications also reset the input context while they handle keys,
// including the backspaces this engine sends to rewrite a word; honoring
// those would empty the buffer in the middle of a word.
func (e *IBusBambooEngine) isResetFromOutside() bool {
	if atomic.LoadInt32(&e.activeKeys) > 0 || atomic.LoadInt32(&e.pendingKeys) > 0 {
		return false
	}
	return time.Now().UnixNano()-atomic.LoadInt64(&e.lastKeyActivity) > int64(resetQuietPeriod)
}

func (e *IBusBambooEngine) touchKeyActivity() {
	atomic.StoreInt64(&e.lastKeyActivity, time.Now().UnixNano())
}

func (e *IBusBambooEngine) Enable() *dbus.Error {
	debugf("Enable.")
	e.RequireSurroundingText()
	return nil
}

func (e *IBusBambooEngine) Disable() *dbus.Error {
	debugf("Disable.")
	return nil
}

// @method(in_signature="vuu")
func (e *IBusBambooEngine) SetSurroundingText(text dbus.Variant, cursorPos uint32, anchorPos uint32) *dbus.Error {
	e.setSurroundingText(text, cursorPos, anchorPos, e.nextLocalSerial())
	return nil
}

// SetSurroundingTextMsg is SetSurroundingText as exported on D-Bus.
func (e *IBusBambooEngine) SetSurroundingTextMsg(text dbus.Variant, cursorPos uint32, anchorPos uint32, msg dbus.Message) *dbus.Error {
	e.setSurroundingText(text, cursorPos, anchorPos, msg.Serial())
	return nil
}

func (e *IBusBambooEngine) PageUp() *dbus.Error {
	e.Lock()
	defer e.Unlock()
	e.pageUp()
	return nil
}

func (e *IBusBambooEngine) PageDown() *dbus.Error {
	e.Lock()
	defer e.Unlock()
	e.pageDown()
	return nil
}

func (e *IBusBambooEngine) CursorUp() *dbus.Error {
	e.Lock()
	defer e.Unlock()
	e.cursorUp()
	return nil
}

func (e *IBusBambooEngine) CursorDown() *dbus.Error {
	e.Lock()
	defer e.Unlock()
	e.cursorDown()
	return nil
}

func (e *IBusBambooEngine) pageUp() {
	if e.isEmojiLTOpened && e.emojiLookupTable.PageUp() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.PageUp() {
		e.updateInputModeLT()
	}
}

func (e *IBusBambooEngine) pageDown() {
	if e.isEmojiLTOpened && e.emojiLookupTable.PageDown() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.PageDown() {
		e.updateInputModeLT()
	}
}

func (e *IBusBambooEngine) cursorUp() {
	if e.isEmojiLTOpened && e.emojiLookupTable.CursorUp() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.CursorUp() {
		e.updateInputModeLT()
	}
}

func (e *IBusBambooEngine) cursorDown() {
	if e.isEmojiLTOpened && lookupTableCursorDown(e.emojiLookupTable) {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && lookupTableCursorDown(e.inputModeLookupTable) {
		e.updateInputModeLT()
	}
}

func (e *IBusBambooEngine) CandidateClicked(index uint32, button uint32, state uint32) *dbus.Error {
	e.Lock()
	defer e.Unlock()
	if e.isEmojiLTOpened && e.updateCursorPosInEmojiTable(index) {
		e.commitEmojiCandidate()
		e.closeEmojiCandidates()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.SetCursorPos(index) {
		e.commitInputModeCandidate()
		e.closeInputModeCandidates()
	}
	return nil
}

func (e *IBusBambooEngine) SetCapabilities(cap uint32) *dbus.Error {
	e.Lock()
	e.capabilities = cap
	e.Unlock()
	debugf("SetCapabilities 0x%x (surrounding text: %v)", cap, cap&IBusCapSurroundingText != 0)
	return nil
}

func (e *IBusBambooEngine) SetCursorLocation(x int32, y int32, w int32, h int32) *dbus.Error {
	return nil
}

func (e *IBusBambooEngine) SetContentType(purpose uint32, hints uint32) *dbus.Error {
	return nil
}

// @method(in_signature="su")
func (e *IBusBambooEngine) PropertyActivate(propName string, propState uint32) *dbus.Error {
	if propName == PropKeyAbout {
		exec.Command("xdg-open", HomePage).Start()
		return nil
	}
	if propName == PropKeyVnCharsetConvert {
		exec.Command("xdg-open", CharsetConvertPage).Start()
		return nil
	}
	if propName == PropKeyConfiguration || propName == PropKeyInputModeLookupTableShortcut || propName == PropKeyMacroTable {
		// The settings window blocks until it is closed: keep typing
		// possible meanwhile by leaving the engine unlocked.
		ui.OpenGUI(e.engineName)
		cfg := config.LoadConfig(e.engineName)
		e.Lock()
		e.config = cfg
		e.Unlock()
		return nil
	}
	e.Lock()
	defer e.Unlock()

	turnSpellChecking := func(on bool) {
		if on {
			e.config.IBflags |= config.IBspellCheckEnabled
			e.config.IBflags |= config.IBautoNonVnRestore
			if e.config.IBflags&config.IBspellCheckWithDicts == 0 {
				e.config.IBflags |= config.IBspellCheckWithRules
			}
		} else {
			e.config.IBflags &= ^config.IBspellCheckEnabled
			e.config.IBflags &= ^config.IBautoNonVnRestore
		}
	}

	if propName == PropKeyStdToneStyle {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.Flags |= bamboo.EstdToneStyle
		} else {
			e.config.Flags &= ^bamboo.EstdToneStyle
		}
	}
	if propName == PropKeyFreeToneMarking {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.Flags |= bamboo.EfreeToneMarking
		} else {
			e.config.Flags &= ^bamboo.EfreeToneMarking
		}
	}
	if propName == PropKeyEnableSpellCheck {
		if propState == ibus.PROP_STATE_CHECKED {
			turnSpellChecking(true)
		} else {
			turnSpellChecking(false)
		}
	}
	if propName == PropKeySpellCheckByRules {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBspellCheckWithRules
			turnSpellChecking(true)
		} else {
			e.config.IBflags &= ^config.IBspellCheckWithRules
		}
	}
	if propName == PropKeySpellCheckByDicts {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBspellCheckWithDicts
			turnSpellChecking(true)
			reloadDictionary()
		} else {
			e.config.IBflags &= ^config.IBspellCheckWithDicts
		}
	}
	if propName == PropKeyMacroEnabled {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBmacroEnabled
			e.macroTable.Enable(e.engineName)
		} else {
			e.config.IBflags &= ^config.IBmacroEnabled
			e.macroTable.Disable()
		}
	}
	if propName == PropKeyPreeditInvisibility {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBnoUnderline
		} else {
			e.config.IBflags &= ^config.IBnoUnderline
		}
	}
	if propName == PropKeyPreeditElimination {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBpreeditElimination
		} else {
			e.config.IBflags &= ^config.IBpreeditElimination
		}
	}
	if propName == PropKeyAutoCapitalizeMacro {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBautoCapitalizeMacro
		} else {
			e.config.IBflags &= ^config.IBautoCapitalizeMacro
		}
		if e.config.IBflags&config.IBmacroEnabled != 0 {
			e.macroTable.Reload(e.engineName, e.config.IBflags&config.IBautoCapitalizeMacro != 0)
		}
	}

	var im, foundIm = getValueFromPropKey(propName, "InputMode")
	if foundIm && propState == ibus.PROP_STATE_CHECKED {
		e.config.DefaultInputMode, _ = strconv.Atoi(im)
	}
	var charset, foundCs = getValueFromPropKey(propName, "OutputCharset")
	if foundCs && isValidCharset(charset) && propState == ibus.PROP_STATE_CHECKED {
		e.config.OutputCharset = charset
	}
	if _, found := e.config.InputMethodDefinitions[propName]; found && propState == ibus.PROP_STATE_CHECKED {
		e.config.InputMethod = propName
	}
	if propName != "-" {
		config.SaveConfig(e.config, e.engineName)
	}
	e.propList = GetPropListByConfig(e.config)

	var inputMethod = bamboo.ParseInputMethod(e.config.InputMethodDefinitions, e.config.InputMethod)
	e.preeditor = bamboo.NewEngine(inputMethod, e.config.Flags)
	e.RegisterProperties(e.propList)
	return nil
}
