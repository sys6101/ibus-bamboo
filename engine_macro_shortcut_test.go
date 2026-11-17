package main

import (
	"encoding/json"
	"ibus-bamboo/config"
	"io/ioutil"
	"os"
	"testing"
)

// Issue #459: a shortcut to turn macro expansion on and off.
func TestMacroToggleShortcut(t *testing.T) {
	const ctrlShift = IBusControlMask | IBusShiftMask
	e := newTestEngine(config.PreeditIM)
	e.engineName = "macro-shortcut-test"
	defer os.Remove(config.GetConfigPath(e.engineName))
	e.config.Shortcuts[KSMacroToggle] = ctrlShift
	e.config.Shortcuts[KSMacroToggle+1] = 'm'
	e.macroTable.LoadFromFile(writeMacroFile(t, "ko:không\n"))

	enabled := func() bool { return e.config.IBflags&config.IBmacroEnabled != 0 }
	wasEnabled := enabled()
	for i, want := range []bool{!wasEnabled, wasEnabled} {
		handled, err := e.ProcessKeyEvent('M', 0, ctrlShift)
		if err != nil || !handled {
			t.Fatalf("press %d: handled=%v err=%v, want the shortcut consumed", i+1, handled, err)
		}
		if enabled() != want {
			t.Fatalf("press %d: macro enabled = %v, want %v", i+1, enabled(), want)
		}
		e.macroTable.RLock()
		tableOn := e.macroTable.enable
		e.macroTable.RUnlock()
		if tableOn != want {
			t.Fatalf("press %d: macro table enabled = %v, want %v", i+1, tableOn, want)
		}
		if saved := config.LoadConfig(e.engineName); (saved.IBflags&config.IBmacroEnabled != 0) != want {
			t.Fatalf("press %d: the saved config does not match", i+1)
		}
	}
	e.macroTable.Disable()

	if handled, _ := e.ProcessKeyEvent('m', 0, IBusControlMask); handled {
		t.Errorf("Ctrl+M alone is not the shortcut and must reach the application")
	}
}

func TestMacroToggleShortcutIsOffByDefault(t *testing.T) {
	cfg := config.DefaultCfg()
	if cfg.Shortcuts[KSMacroToggle+1] != 0 {
		t.Errorf("the macro shortcut should be unset by default")
	}
}

// Configs saved before the macro shortcut existed have ten slots.
func TestConfigWithFewerShortcutSlotsLoads(t *testing.T) {
	const name = "old-shortcuts-test"
	defer os.Remove(config.GetConfigPath(name))
	if err := os.MkdirAll(config.GetConfigDir(name), 0777); err != nil {
		t.Skipf("config dir is not writable: %v", err)
	}
	old, _ := json.Marshal(map[string]interface{}{
		"InputMethod": "VNI",
		"Shortcuts":   []uint32{1, 126, 0, 0, 0, 0, 0, 0, 5, 117},
	})
	if err := ioutil.WriteFile(config.GetConfigPath(name), old, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg := config.LoadConfig(name)
	if cfg.InputMethod != "VNI" || cfg.Shortcuts[KSInputModeSwitch+1] != 126 || cfg.Shortcuts[KSHexadecimal+1] != 117 {
		t.Errorf("old config not loaded: %s %v", cfg.InputMethod, cfg.Shortcuts)
	}
	if cfg.Shortcuts[KSMacroToggle+1] != 0 {
		t.Errorf("the new shortcut should be off for an old config, got %v", cfg.Shortcuts)
	}
}
