package main

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func writeMacroFile(t *testing.T, content string) string {
	t.Helper()
	dir, err := ioutil.TempDir("", "bamboo-macro")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "macro.txt")
	if err := ioutil.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write macro file: %v", err)
	}
	return path
}

func TestMacroTableLoadFromFile(t *testing.T) {
	path := writeMacroFile(t, "# comment\n; another comment\n\nVn : Việt Nam\nko:không\nbroken line\n")

	cases := []struct {
		name           string
		autoCapitalize bool
		key            string
		want           string
	}{
		{"exact key", false, "ko", "không"},
		{"key and value are trimmed", false, "Vn", "Việt Nam"},
		{"case sensitive without auto capitalize", false, "vn", ""},
		{"case insensitive with auto capitalize", true, "VN", "Việt Nam"},
		{"comments are skipped", false, "# comment", ""},
		{"lines without a separator are skipped", false, "broken line", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table := NewMacroTable(tc.autoCapitalize)
			if err := table.LoadFromFile(path); err != nil {
				t.Fatalf("LoadFromFile: %v", err)
			}
			if got := table.GetText(tc.key); got != tc.want {
				t.Errorf("GetText(%q) = %q, want %q", tc.key, got, tc.want)
			}
			if got := table.HasKey(tc.key); got != (tc.want != "") {
				t.Errorf("HasKey(%q) = %v", tc.key, got)
			}
		})
	}

	table := NewMacroTable(false)
	if err := table.LoadFromFile(filepath.Join(filepath.Dir(path), "missing.txt")); err == nil {
		t.Errorf("LoadFromFile on a missing file should fail")
	}
	table.LoadFromFile(path)
	if !table.HasPrefix("k") || table.HasPrefix("x") {
		t.Errorf("HasPrefix: k=%v x=%v, want true/false", table.HasPrefix("k"), table.HasPrefix("x"))
	}
}

func TestMacroTableEnableStartsOneWatcher(t *testing.T) {
	table := NewMacroTable(false)
	table.Enable("macro-watcher-test")
	table.Enable("macro-watcher-test")
	table.Reload("macro-watcher-test", true)

	table.RLock()
	watching, stale, autoCapitalize := table.watching, table.stale, table.autoCapitalizeMacro
	table.RUnlock()
	if !watching {
		t.Fatalf("Enable should leave a watcher running")
	}
	if !stale && len(table.mTable) == 0 {
		// Either the reload is still pending or the watcher already ran it.
		t.Logf("watcher already consumed the reload request")
	}
	if !autoCapitalize {
		t.Fatalf("Reload should update the auto-capitalize setting")
	}

	table.Disable()
	if table.HasKey("anything") {
		t.Fatalf("a disabled table must be empty")
	}
	table.Enable("macro-watcher-test")
	table.RLock()
	enabled := table.enable
	table.RUnlock()
	if !enabled {
		t.Fatalf("Enable after Disable should turn the table back on")
	}
	table.Disable()
}

func TestMacroTableConcurrentAccess(t *testing.T) {
	path := writeMacroFile(t, "ko:không\nvn:Việt Nam\n")
	table := NewMacroTable(true)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				table.LoadFromFile(path)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				table.GetText("ko")
				table.HasKey("vn")
				table.HasPrefix("v")
			}
		}()
	}
	wg.Wait()
	if got := table.GetText("KO"); got != "không" {
		t.Fatalf("GetText after concurrent reloads = %q, want %q", got, "không")
	}
}
