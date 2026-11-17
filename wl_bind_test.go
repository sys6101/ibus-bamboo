package main

import (
	"bytes"
	"testing"
)

func TestWlStringArgs(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantWords int // 32-bit words after the length
	}{
		{"empty", "", 1},
		{"length 3 mod 4", "abc", 1},
		{"length 0 mod 4", "abcd", 2},
		{"length 1 mod 4", "abcde", 2},
		{"length 2 mod 4", "abcdef", 2},
		{"foreign toplevel manager", "zwlr_foreign_toplevel_manager_v1", 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := wlStringArgs(tc.in)
			if got := args[0].(uint32); got != uint32(len(tc.in)+1) {
				t.Errorf("declared length = %d, want %d (the string and its NUL)", got, len(tc.in)+1)
			}
			if got := len(args) - 1; got != tc.wantWords {
				t.Fatalf("payload = %d words, want %d", got, tc.wantWords)
			}
			var wire []byte
			for _, word := range args[1:] {
				buf := make([]byte, 4)
				wlByteOrder.PutUint32(buf, word.(uint32))
				wire = append(wire, buf...)
			}
			if !bytes.Equal(wire[:len(tc.in)], []byte(tc.in)) {
				t.Errorf("payload = %q, want it to start with %q", wire, tc.in)
			}
			if nul := bytes.IndexByte(wire, 0); nul != len(tc.in) {
				t.Errorf("first NUL at offset %d, want %d", nul, len(tc.in))
			}
			if rest := bytes.Trim(wire[len(tc.in):], "\x00"); len(rest) != 0 {
				t.Errorf("padding contains non-NUL bytes: %q", rest)
			}
		})
	}
}
