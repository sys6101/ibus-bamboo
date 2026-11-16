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
	"bufio"
	"ibus-bamboo/config"
	"os"
	"strings"
	"sync"
	"time"
)

const macroWatchInterval = 3 * time.Second

// MacroTable is read by the key handlers and reloaded by a watcher goroutine,
// so every field is guarded by the embedded mutex.
type MacroTable struct {
	sync.RWMutex
	enable              bool
	watching            bool // a watcher goroutine is running
	stale               bool // the table must be reloaded even if the file is unchanged
	autoCapitalizeMacro bool
	mTable              map[string]string
}

func NewMacroTable(autoCapitalizeMacro bool) *MacroTable {
	return &MacroTable{autoCapitalizeMacro: autoCapitalizeMacro}
}

func (e *MacroTable) LoadFromFile(macroFileName string) error {
	f, err := os.Open(macroFileName)
	if err != nil {
		return err
	}
	defer f.Close()
	e.RLock()
	autoCapitalize := e.autoCapitalizeMacro
	e.RUnlock()

	table := map[string]string{}
	rd := bufio.NewReader(f)
	for {
		line, _, err := rd.ReadLine()
		if err != nil {
			break
		}
		var s = strings.TrimSpace(string(line))
		if len(line) == 0 || strings.HasPrefix(s, ";") || strings.HasPrefix(s, "#") {
			continue
		}
		var list = strings.Split(s, ":")
		if len(list) == 2 {
			key := strings.TrimSpace(list[0])
			if autoCapitalize {
				key = strings.ToLower(key)
			}
			table[key] = strings.TrimSpace(list[1])
		}
	}
	e.Lock()
	e.mTable = table
	e.Unlock()
	return nil
}

func (e *MacroTable) Reload(engineName string, autoCapitalizeMacro bool) {
	e.Lock()
	e.autoCapitalizeMacro = autoCapitalizeMacro
	e.Unlock()
	e.Enable(engineName)
}

func (e *MacroTable) GetText(key string) string {
	e.RLock()
	defer e.RUnlock()
	if e.autoCapitalizeMacro {
		key = strings.ToLower(key)
	}
	return e.mTable[key]
}

func (e *MacroTable) HasKey(key string) bool {
	return e.GetText(key) != ""
}

func (e *MacroTable) HasPrefix(key string) bool {
	e.RLock()
	defer e.RUnlock()
	if e.mTable[key] != "" {
		return true
	}
	for k := range e.mTable {
		if strings.HasPrefix(k, key) {
			return true
		}
	}
	return false
}

// Enable turns the table on and makes sure exactly one watcher goroutine
// keeps it in sync with the macro file. Calling it again only requests a
// reload; it never starts a second watcher.
func (e *MacroTable) Enable(engineName string) {
	e.Lock()
	e.enable = true
	e.stale = true
	alreadyWatching := e.watching
	e.watching = true
	e.Unlock()
	if alreadyWatching {
		return
	}
	go e.watch(config.GetMacroPath(engineName))
}

func (e *MacroTable) watch(path string) {
	var modTime time.Time
	for {
		e.Lock()
		if !e.enable {
			e.watching = false
			e.Unlock()
			return
		}
		stale := e.stale
		e.stale = false
		e.Unlock()

		if sta, _ := os.Stat(path); sta != nil {
			if newModTime := sta.ModTime(); stale || !newModTime.Equal(modTime) {
				modTime = newModTime
				e.LoadFromFile(path)
			}
		}
		time.Sleep(macroWatchInterval)
	}
}

func (e *MacroTable) Disable() {
	e.Lock()
	defer e.Unlock()
	e.enable = false
	e.mTable = map[string]string{}
}
