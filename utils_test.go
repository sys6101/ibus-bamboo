package main

import (
	"os"
	"testing"
)

func TestX11GetFocusWindowClassWithoutDisplay(t *testing.T) {
	display, hadDisplay := os.LookupEnv("DISPLAY")
	if err := os.Unsetenv("DISPLAY"); err != nil {
		t.Fatalf("failed to unset DISPLAY: %v", err)
	}
	defer func() {
		if hadDisplay {
			_ = os.Setenv("DISPLAY", display)
		} else {
			_ = os.Unsetenv("DISPLAY")
		}
	}()

	if wmClass := x11GetFocusWindowClass(); wmClass != "" {
		t.Fatalf("expected no window class without an X display, got %q", wmClass)
	}
}
