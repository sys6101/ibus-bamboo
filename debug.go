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
	"log"
	"os"
)

// debugEnabled turns on verbose logging, which includes every key pressed
// and every string committed. It is off by default so that typed text never
// ends up in a log; set IBUS_BAMBOO_DEBUG=1 or pass -debug to enable it.
var debugEnabled = os.Getenv("IBUS_BAMBOO_DEBUG") != ""

func debugf(format string, args ...interface{}) {
	if debugEnabled {
		log.Printf(format, args...)
	}
}
