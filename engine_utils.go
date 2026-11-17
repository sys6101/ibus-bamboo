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
	"fmt"
	"ibus-bamboo/config"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BambooEngine/bamboo-core"
	ibus "github.com/BambooEngine/goibus"
	"github.com/godbus/dbus/v5"
)

// Lookup data shared by every engine in the process. Readers and the lazy
// loaders may run on different engines at once, hence the lock.
var (
	lookupDataMu sync.RWMutex
	dictionary   = map[string]bool{}
	emojiTrie    = NewTrie()
)

func getEmojiTrie() *TrieNode {
	lookupDataMu.RLock()
	defer lookupDataMu.RUnlock()
	return emojiTrie
}

func inDictionary(word string) bool {
	lookupDataMu.RLock()
	defer lookupDataMu.RUnlock()
	return dictionary[word]
}

// ensureEmojiTrie loads the emoji data on first use. A missing data file
// only disables emoji input; it must not take the input method down.
func ensureEmojiTrie() {
	lookupDataMu.Lock()
	defer lookupDataMu.Unlock()
	if emojiTrie == nil || len(emojiTrie.Children) > 0 {
		return
	}
	trie, err := loadEmojiOne(DictEmojiOne)
	if err != nil {
		log.Printf("failed to load emoji data from %s: %s", DictEmojiOne, err)
		return
	}
	emojiTrie = trie
}

// ensureDictionary loads the spelling dictionary on first use.
func ensureDictionary() {
	lookupDataMu.RLock()
	loaded := len(dictionary) > 0
	lookupDataMu.RUnlock()
	if !loaded {
		reloadDictionary()
	}
}

// sessionInputMode is the mode chosen while the focused window class is
// unavailable. It lasts for this ibus-bamboo process and is kept out of
// Config.InputModeMapping, which stores per-application modes.
var sessionInputMode int32

// engineDBusMethods maps Go methods to the names exported on D-Bus. The
// *Msg variants receive the D-Bus message as well, so they are the ones
// IBus must reach; the plain ones stay for direct callers.
var engineDBusMethods = map[string]string{
	"ResetMsg":              "Reset",
	"Reset":                 "ResetDirect",
	"ProcessKeyEventMsg":    "ProcessKeyEvent",
	"ProcessKeyEvent":       "ProcessKeyEventDirect",
	"SetSurroundingTextMsg": "SetSurroundingText",
	"SetSurroundingText":    "SetSurroundingTextDirect",
}

func GetIBusEngineCreator() func(*dbus.Conn, string) dbus.ObjectPath {
	return func(conn *dbus.Conn, ngName string) dbus.ObjectPath {
		var ngGroupName = strings.Split(ngName, "::")[0]
		var engineName = strings.ToLower(ngGroupName)
		debugf("Got engine name: %s", engineName)
		var cfg = config.LoadConfig(engineName)
		var objectPath = dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/IBus/Engine/%s/%d", engineName, time.Now().UnixNano()))
		var inputMethod = bamboo.ParseInputMethod(cfg.InputMethodDefinitions, cfg.InputMethod)
		baseEngine := ibus.BaseEngine(conn, objectPath)
		var engine = NewIbusBambooEngine(engineName, cfg, &baseEngine, bamboo.NewEngine(inputMethod, cfg.Flags))
		engine.propList = GetPropListByConfig(cfg)
		engine.shouldEnqueuKeyStrokes = true
		ibus.PublishEngine(conn, objectPath, engine)
		if err := conn.ExportWithMap(engine, engineDBusMethods, objectPath, ibus.IBUS_IFACE_ENGINE); err != nil {
			log.Printf("failed to export the engine methods: %s", err)
		}
		go engine.init()

		return objectPath
	}
}

const KeypressDelayMs = 10

func (e *IBusBambooEngine) isShortcutKeyEnable(ski uint) bool {
	if int(ski+2) > len(e.config.Shortcuts) {
		return false
	}
	l := e.config.Shortcuts[ski : ski+2]
	return l[1] > 0
}

func (e *IBusBambooEngine) init() {
	// The engine still works with defaults when the config directory or
	// the sample macro file is unavailable (read-only or missing home).
	if err := initConfigFiles(e.engineName); err != nil {
		log.Printf("failed to prepare config files: %s", err)
	}
	e.Lock()
	macroEnabled := e.config.IBflags&config.IBmacroEnabled != 0
	e.Unlock()
	if macroEnabled {
		e.macroTable.Enable(e.engineName)
	}
}

func initConfigFiles(engineName string) error {
	configDir := config.GetConfigDir(engineName)
	if sta, err := os.Stat(configDir); err != nil || !sta.IsDir() {
		if err := os.MkdirAll(configDir, 0777); err != nil {
			return fmt.Errorf("create config dir %s: %w", configDir, err)
		}
	}
	macroPath := config.GetMacroPath(engineName)
	if _, err := os.Stat(macroPath); os.IsNotExist(err) {
		sample, err := ioutil.ReadFile(getEngineSubFile(sampleMactabFile))
		if err != nil {
			return fmt.Errorf("read sample macro file: %w", err)
		}
		if err := ioutil.WriteFile(macroPath, sample, 0644); err != nil {
			return fmt.Errorf("write macro file %s: %w", macroPath, err)
		}
	}
	return nil
}

// reloadDictionary loads the spelling dictionary, keeping the current one
// when the data file cannot be read.
func reloadDictionary() {
	dict, err := loadDictionary(DictVietnameseCm)
	if err != nil {
		log.Printf("failed to load dictionary from %s: %s", DictVietnameseCm, err)
		return
	}
	lookupDataMu.Lock()
	dictionary = dict
	lookupDataMu.Unlock()
}

// keyPressCapturing handles this engine's queued keys one at a time, in the
// order they were typed. It takes the engine lock per key, never while
// waiting on the channel.
func (e *IBusBambooEngine) keyPressCapturing() {
	for {
		select {
		case <-e.done:
			return
		case keyEvents := <-e.keyPressChan:
			e.Lock()
			e.keyPressForwardHandler(keyEvents[0], keyEvents[1], keyEvents[2])
			e.Unlock()
			e.touchKeyActivity()
			atomic.AddInt32(&e.pendingKeys, -1)
		}
	}
}

// enqueueKeyPress queues a key for keyPressCapturing. The caller holds the
// engine lock; it is released only if the queue is full, since the queue
// goroutine needs it to make room.
func (e *IBusBambooEngine) enqueueKeyPress(keyVal, keyCode, state uint32) {
	var keyEvents = [3]uint32{keyVal, keyCode, state}
	atomic.AddInt32(&e.pendingKeys, 1)
	select {
	case e.keyPressChan <- keyEvents:
		return
	default:
	}
	e.Unlock()
	defer e.Lock()
	select {
	case e.keyPressChan <- keyEvents:
	case <-e.done:
		atomic.AddInt32(&e.pendingKeys, -1)
	}
}

// dequeueKeyPress takes the next queued key, if any, without blocking.
func (e *IBusBambooEngine) dequeueKeyPress() ([3]uint32, bool) {
	select {
	case keyEvents := <-e.keyPressChan:
		atomic.AddInt32(&e.pendingKeys, -1)
		return keyEvents, true
	default:
		return [3]uint32{}, false
	}
}

const (
	queueDrainPolls    = 400
	queueDrainInterval = 5 * time.Millisecond
)

// waitForQueueDrain waits, for a bounded time, until the queue goroutine has
// finished the keys typed before the current one. Keys that are not queued
// themselves (cursor movement, BackSpace, Enter...) go back to the
// application, so they must not overtake text that is still being sent.
// The caller holds the engine lock; it is released while waiting because
// that goroutine needs it.
func (e *IBusBambooEngine) waitForQueueDrain() {
	if atomic.LoadInt32(&e.pendingKeys) <= 0 {
		return
	}
	e.Unlock()
	defer e.Lock()
	for i := 0; i < queueDrainPolls && atomic.LoadInt32(&e.pendingKeys) > 0; i++ {
		time.Sleep(queueDrainInterval)
	}
}

// pause waits with the engine lock released. Handling one key in the
// backspace modes takes tens of milliseconds of deliberate waiting; keys
// typed meanwhile must still be queued and answered right away, because
// clients order the text they receive by when those answers arrive (GTK4
// applied a delete after the commit that followed it when answers were
// held back). The caller holds the engine lock and must not rely on engine
// state being unchanged across the call.
func (e *IBusBambooEngine) pause(d time.Duration) {
	e.Unlock()
	time.Sleep(d)
	e.Lock()
}

// consumeFakeBackspace reports whether a BackSpace key event is one of the
// backspaces this engine injected through XTest, and counts it off.
func (e *IBusBambooEngine) consumeFakeBackspace() bool {
	for {
		n := atomic.LoadInt32(&e.nFakeBackSpace)
		if n <= 0 {
			return false
		}
		if atomic.CompareAndSwapInt32(&e.nFakeBackSpace, n, n-1) {
			return true
		}
	}
}

func (e *IBusBambooEngine) resetBuffer() {
	if e.getRawKeyLen() == 0 {
		return
	}
	if e.checkInputMode(config.PreeditIM) {
		e.commitPreeditAndReset(e.getPreeditString())
	} else {
		e.preeditor.Reset()
	}
}

func (e *IBusBambooEngine) checkWmClass(newId string) {
	if e.wmClasses != newId {
		// Flush with the input mode of the window being left. Looking the
		// mode up after the switch could treat text already committed in a
		// backspace mode as pending preedit and commit it again into the
		// window that just got the focus.
		e.resetBuffer()
		e.resetFakeBackspace()
		e.needSurroundingSync = false
		e.syncAfterReset = false
		e.wmClasses = newId
	}
}

func (e *IBusBambooEngine) isShortcutKeyPressed(keyVal, state uint32, shortcut uint) bool {
	if !e.isShortcutKeyEnable(shortcut) {
		return false
	}
	realState := state & IBusDefaultModMask
	lowerKey := uint32(unicode.ToLower(rune(keyVal)))
	shortcuts := e.config.Shortcuts[shortcut : shortcut+2]
	ret := shortcuts[0] == realState && shortcuts[1] == lowerKey
	// fmt.Println("...isShortcutKeyPressed=", ret, ret && !e.lastKeyWithShift, shortcuts)
	if realState == 1 && shortcut == KSViEnSwitch {
		return ret && !e.lastKeyWithShift
	}
	return ret
}

func (e *IBusBambooEngine) processShortcutKey(keyVal, keyCode, state uint32) (bool, bool) {
	if keyVal == IBusCapsLock {
		return true, false
	}
	// fmt.Println("===Process shortcut for emoji selector")
	if e.isShortcutKeyPressed(keyVal, state, KSEmojiDialog) &&
		!e.isEmojiLTOpened {
		e.resetBuffer()
		e.isEmojiLTOpened = true
		e.lastKeyWithShift = true
		e.openEmojiList()
		return true, true
	}
	if e.isEmojiLTOpened {
		return true, e.emojiProcessKeyEvent(keyVal, keyCode, state)
	}
	// fmt.Println("====== Process hexadecimal key pressed")
	if e.isShortcutKeyPressed(keyVal, state, KSHexadecimal) {
		e.resetBuffer()
		e.isInHexadecimal = true
		e.setupHexadecimalProcessKeyEvent()
		return true, true
	}
	if e.isInHexadecimal {
		if e.isShortcutKeyPressed(keyVal, state, KSHexadecimal) {
			e.closeHexadecimalInput()
			e.updateLastKeyWithShift(keyVal, state)
			return true, false
		}
		return true, e.hexadecimalProcessKeyEvent(keyVal, keyCode, state)
	}

	if e.config.DefaultInputMode == config.UsIM {
		return true, false
	}
	if e.isShortcutKeyPressed(keyVal, state, KSRestoreKeyStrokes) {
		// fmt.Println("===== Process restoring key strokes")
		e.shouldRestoreKeyStrokes = true
		return false, false
	}
	// fmt.Println("===Process shortcut for input method switcher")
	if e.isShortcutKeyPressed(keyVal, state, KSViEnSwitch) {
		e.englishMode = !e.englishMode
		notify(e.englishMode)
		e.resetBuffer()
		return true, true
	}
	// fmt.Println("====== Process shortcut for input mode switch")
	// Open the table even when the window class is empty. On current GNOME,
	// org.gnome.Shell.Eval is blocked, so getWmClass() stays empty and the
	// shortcut would otherwise never run. Per-application lookup still
	// requires a class; see getInputMode.
	if e.isInputModeLTOpened {
		return e.ltProcessKeyEvent(keyVal, keyCode, state)
	} else if e.isShortcutKeyPressed(keyVal, state, KSInputModeSwitch) {
		e.resetBuffer()
		e.isInputModeLTOpened = true
		e.lastKeyWithShift = true
		e.openLookupTable()
		return true, true
	}

	if keyVal == IBusShiftL || keyVal == IBusShiftR {
		return true, false
	}
	if e.checkInputMode(config.UsIM) {
		if e.isInputModeLTOpened && e.isShortcutKeyPressed(keyVal, state, KSInputModeSwitch) {
			return false, false
		}
		return true, false
	}
	if e.englishMode {
		e.updateLastKeyWithShift(keyVal, state)
		return true, false
	}
	return false, false
}

func (e *IBusBambooEngine) toUpper(keyRune rune) rune {
	var keyMapping = map[rune]rune{
		'[': '{',
		']': '}',
		'{': '[',
		'}': ']',
	}

	if upperSpecialKey, found := keyMapping[keyRune]; found && inKeyList(e.preeditor.GetInputMethod().AppendingKeys, keyRune) {
		keyRune = upperSpecialKey
	}
	return keyRune
}

func (e *IBusBambooEngine) updateLastKeyWithShift(keyVal, state uint32) {
	if e.preeditor.CanProcessKey(rune(keyVal)) {
		e.lastKeyWithShift = state&IBusShiftMask != 0
	} else {
		e.lastKeyWithShift = false
	}
}

func (e *IBusBambooEngine) getRawKeyLen() int {
	return len(e.preeditor.GetProcessedString(bamboo.EnglishMode | bamboo.FullText))
}

func (e *IBusBambooEngine) runeCount() int {
	return utf8.RuneCountInString(e.getPreeditString())
}

func (e *IBusBambooEngine) getInputMode() int {
	if e.getWmClass() != "" {
		if im, ok := e.config.InputModeMapping[e.getWmClass()]; ok && config.ImLookupTable[im] != "" {
			return im
		}
	} else if im := int(atomic.LoadInt32(&sessionInputMode)); config.ImLookupTable[im] != "" {
		return im
	}
	if _, ok := config.ImLookupTable[e.config.DefaultInputMode]; ok {
		return e.config.DefaultInputMode
	}
	return config.PreeditIM
}

func (e *IBusBambooEngine) openLookupTable() {
	var wmClasses = strings.Split(e.getWmClass(), ":")
	var wmClass = e.getWmClass()
	if len(wmClasses) == 2 {
		wmClass = wmClasses[1]
	}

	e.UpdateAuxiliaryText(ibus.NewText("Nhấn (1/2/3/4/5/6/7) để lưu tùy chọn của bạn"), true)

	lt := ibus.NewLookupTable()
	lt.PageSize = uint32(len(config.ImLookupTable))
	lt.Orientation = IBusOrientationVertical
	var activeMode = e.getInputMode()
	for im := 1; im <= len(config.ImLookupTable); im++ {
		if activeMode == im {
			lt.AppendLabel("*")
		} else {
			lt.AppendLabel(strconv.Itoa(im))
		}
		if im == config.UsIM {
			label := config.ImLookupTable[im]
			if e.getInputMode() == config.UsIM {
				label = config.UsIMRemoveLabel
			}
			if wmClass != "" {
				label += " (" + wmClass + ")"
			}
			lt.AppendCandidate(label)
		} else {
			lt.AppendCandidate(config.ImLookupTable[im])
		}
	}
	// The cursor can only be placed once the candidates exist.
	lt.SetCursorPos(uint32(activeMode - 1))
	e.inputModeLookupTable = lt
	e.UpdateLookupTable(lt, true)
}

func (e *IBusBambooEngine) ltProcessKeyEvent(keyVal uint32, keyCode uint32, state uint32) (bool, bool) {
	// e.HideLookupTable()
	// e.HideAuxiliaryText()
	if e.isShortcutKeyPressed(keyVal, state, KSInputModeSwitch) {
		e.closeInputModeCandidates()
		return true, false
	}
	var keyRune = rune(keyVal)
	if keyVal == IBusLeft || keyVal == IBusUp {
		e.cursorUp()
		return true, true
	} else if keyVal == IBusRight || keyVal == IBusDown {
		e.cursorDown()
		return true, true
	} else if keyVal == IBusPageUp {
		e.pageUp()
		return true, true
	} else if keyVal == IBusPageDown {
		e.pageDown()
		return true, true
	}
	if keyVal == IBusReturn {
		e.commitInputModeCandidate()
		e.closeInputModeCandidates()
		return true, true
	}
	if keyRune >= '1' && keyRune <= '7' {
		if pos, err := strconv.Atoi(string(keyRune)); err == nil {
			if e.inputModeLookupTable.SetCursorPos(uint32(pos - 1)) {
				e.commitInputModeCandidate()
				e.closeInputModeCandidates()
				return true, true
			} else {
				e.closeInputModeCandidates()
			}
		}
	}
	if keyVal == IBusEscape {
		// Consume Escape: it only dismisses the table and must not also
		// reach the application (it would cancel the focused dialog).
		e.closeInputModeCandidates()
		return true, true
	}
	return true, false
}

func (e *IBusBambooEngine) commitInputModeCandidate() {
	var im = int(e.inputModeLookupTable.CursorPos + 1)
	if config.ImLookupTable[im] == "" {
		return
	}
	if im == config.UsIM && e.getInputMode() == config.UsIM {
		e.removeInputModeExclusion()
	} else if wmClass := e.getWmClass(); wmClass != "" {
		e.config.InputModeMapping[wmClass] = im
		config.SaveConfig(e.config, e.engineName)
	} else {
		// Remember the choice for this process. An empty class must not be
		// written into InputModeMapping: getInputMode ignores that entry,
		// so the selection would be dropped as soon as the table closes.
		atomic.StoreInt32(&sessionInputMode, int32(im))
	}

	e.propList = GetPropListByConfig(e.config)
	e.RegisterProperties(e.propList)
}

// removeInputModeExclusion drops UsIM for the focused app, or for this
// process when the window class is unknown. The next lookup uses the
// default input mode.
func (e *IBusBambooEngine) removeInputModeExclusion() {
	if wmClass := e.getWmClass(); wmClass != "" {
		delete(e.config.InputModeMapping, wmClass)
		config.SaveConfig(e.config, e.engineName)
	}
	if int(atomic.LoadInt32(&sessionInputMode)) == config.UsIM {
		atomic.StoreInt32(&sessionInputMode, 0)
	}
}

func (e *IBusBambooEngine) closeInputModeCandidates() {
	e.inputModeLookupTable = nil
	e.clearLookupTable()
	e.HidePreeditText()
	e.HideLookupTable()
	e.HideAuxiliaryText()
	e.isInputModeLTOpened = false
}

// clearLookupTable replaces the panel's table with an empty one (workaround
// for issue #18) without asking the panel to show it: panels that honor the
// visible flag would otherwise keep an empty table on screen (issue #593).
func (e *IBusBambooEngine) clearLookupTable() {
	e.UpdateLookupTable(ibus.NewLookupTable(), false)
}

// lookupTableCursorDown moves the cursor to the next candidate. Unlike
// LookupTable.CursorDown it never moves past the last one, which would leave
// the panel on an empty page and commit a candidate that does not exist.
func lookupTableCursorDown(lt *ibus.LookupTable) bool {
	if int(lt.CursorPos)+1 >= len(lt.Candidates) {
		return false
	}
	lt.CursorPos++
	return true
}

func (e *IBusBambooEngine) updateInputModeLT() {
	var visible = len(e.inputModeLookupTable.Candidates) > 0
	e.UpdateLookupTable(e.inputModeLookupTable, visible)
}

func isValidState(state uint32) bool {
	if state&IBusControlMask != 0 ||
		state&IBusMod1Mask != 0 ||
		state&IBusMod4Mask != 0 ||
		state&IBusIgnoredMask != 0 ||
		state&IBusSuperMask != 0 ||
		state&IBusHyperMask != 0 ||
		state&IBusMetaMask != 0 {
		return false
	}
	return true
}

func (e *IBusBambooEngine) isPrintableKey(state, keyVal uint32) bool {
	return isValidState(state) && e.isValidKeyVal(keyVal)
}

func (e *IBusBambooEngine) getCommitText(keyVal, keyCode, state uint32) (newText string, IsWordBreakSymbol bool) {
	var keyRune = rune(keyVal)
	isPrintableKey := e.isPrintableKey(state, keyVal)
	oldText := e.getPreeditString()
	// restore key strokes by pressing Shift + Space
	if e.shouldRestoreKeyStrokes {
		e.shouldRestoreKeyStrokes = false
		e.preeditor.RestoreLastWord(!bamboo.HasAnyVietnameseRune(oldText))
		return e.getPreeditString(), false
	}
	var keyS string
	if isPrintableKey {
		keyS = string(keyRune)
	}
	if isPrintableKey && e.preeditor.CanProcessKey(keyRune) {
		if state&IBusLockMask != 0 {
			keyRune = e.toUpper(keyRune)
		}
		e.preeditor.ProcessKey(keyRune, e.getBambooInputMode())
		if inKeyList(e.preeditor.GetInputMethod().AppendingKeys, keyRune) {
			var newText string
			if e.shouldFallbackToEnglish(true) {
				newText = e.getProcessedString(bamboo.EnglishMode)
			} else {
				newText = e.getProcessedString(bamboo.VietnameseMode)
			}
			if fullSeq := e.preeditor.GetProcessedString(bamboo.VietnameseMode); len(fullSeq) > 0 && rune(fullSeq[len(fullSeq)-1]) == keyRune {
				// [[ => [
				var ret = e.getPreeditString()
				var lastRune = rune(ret[len(ret)-1])
				var isWordBreakRune = bamboo.IsWordBreakSymbol(lastRune)
				// TODO: THIS IS A HACK
				if isWordBreakRune {
					e.preeditor.RemoveLastChar(false)
					e.preeditor.ProcessKey(' ', bamboo.EnglishMode)
				}
				return ret, isWordBreakRune
			} else if l := []rune(newText); len(l) > 0 && keyRune == l[len(l)-1] {
				// f] => f]
				var isWordBreakRune = bamboo.IsWordBreakSymbol(keyRune)
				if isWordBreakRune {
					e.preeditor.RemoveLastChar(false)
					e.preeditor.ProcessKey(' ', bamboo.EnglishMode)
				}
				return oldText + string(keyRune), isWordBreakRune
			} else {
				// ] => o?
				return e.getPreeditString(), false
			}
		} else if e.config.IBflags&config.IBmacroEnabled != 0 {
			return e.getProcessedString(bamboo.PunctuationMode), false
		} else {
			return e.getPreeditString(), false
		}
	} else if e.config.IBflags&config.IBmacroEnabled != 0 {
		// macro processing
		if isPrintableKey && e.macroTable.HasPrefix(oldText+keyS) {
			e.preeditor.ProcessKey(keyRune, bamboo.EnglishMode)
			return oldText + keyS, false
		}
		if e.macroTable.HasKey(oldText) {
			if isPrintableKey {
				return e.expandMacro(oldText) + keyS, true
			}
			return e.expandMacro(oldText), true
		}
	}
	return e.handleNonVnWord(keyVal, keyCode, state), true
}

func (e *IBusBambooEngine) handleNonVnWord(keyVal, keyCode, state uint32) string {
	var (
		keyS           string
		keyRune        = rune(keyVal)
		isPrintableKey = e.isPrintableKey(state, keyVal)
		oldText        = e.getPreeditString()
	)
	if isPrintableKey {
		keyS = string(keyRune)
	}
	if bamboo.HasAnyVietnameseRune(oldText) && e.mustFallbackToEnglish() {
		e.preeditor.RestoreLastWord(false)
		newText := e.preeditor.GetProcessedString(bamboo.PunctuationMode|bamboo.EnglishMode) + keyS
		if isPrintableKey {
			e.preeditor.ProcessKey(keyRune, bamboo.EnglishMode)
		}
		return newText
	}
	if isPrintableKey {
		e.preeditor.ProcessKey(keyRune, bamboo.EnglishMode)
		return oldText + keyS
	}
	// Ctrl + A is treasted as a WBS
	return oldText + keyS
}

func (e *IBusBambooEngine) getMacroText() (bool, string) {
	if e.config.IBflags&config.IBmacroEnabled == 0 {
		return false, ""
	}
	var text = e.preeditor.GetProcessedString(bamboo.PunctuationMode)
	if e.macroTable.HasKey(text) {
		return true, e.expandMacro(text)
	}
	return false, ""
}

func (e *IBusBambooEngine) getFakeBackspace() int32 {
	return atomic.LoadInt32(&e.nFakeBackSpace)
}

func (e *IBusBambooEngine) setFakeBackspace(n int32) {
	atomic.StoreInt32(&e.nFakeBackSpace, n)
}

func (e *IBusBambooEngine) isValidKeyVal(keyVal uint32) bool {
	var keyRune = rune(keyVal)
	if keyVal == IBusBackSpace || bamboo.IsWordBreakSymbol(keyRune) {
		return true
	}
	if ok, _ := e.getMacroText(); ok && keyVal == IBusTab {
		return true
	}
	return e.preeditor.CanProcessKey(keyRune)
}

func (e *IBusBambooEngine) inBackspaceWhiteList() bool {
	var inputMode = e.getInputMode()
	for _, im := range config.ImBackspaceList {
		if im == inputMode {
			return true
		}
	}
	return false
}

func (e *IBusBambooEngine) inBrowserList() bool {
	return inStringList(DefaultBrowserList, e.getWmClass())
}

func (e *IBusBambooEngine) getWmClass() string {
	return e.wmClasses
}

func (e *IBusBambooEngine) getLatestWmClass() string {
	var wmClass string
	if isGnome {
		wmClass, _ = gnomeGetFocusWindowClass()
	} else if isWayland {
		wmClass = getWlAppId()
	}
	if wmClass == "" {
		wmClass = x11GetFocusWindowClass()
	}
	wmClass = strings.Replace(wmClass, "\"", "", -1)
	return wmClass
}

func (e *IBusBambooEngine) checkInputMode(im int) bool {
	return e.getInputMode() == im
}

func notify(enMode bool) {
	var title = "Vietnamese"
	var msg = "Press Shortcut keys to switch input language"
	if enMode {
		title = "English"
		msg = "Press Shortcut keys to switch input language"
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		log.Println(err)
		return
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.Notify", 0, "", uint32(281025),
		"", title, msg, []string{}, map[string]dbus.Variant{}, int32(3000))
	if call.Err != nil {
		log.Println(call.Err)
	}
}
