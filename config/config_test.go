package config

import (
	"io/ioutil"
	"os"
	"testing"
)

const testEngineName = "config-unit-test"

func prepareConfigDir(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(GetConfigDir(testEngineName), 0777); err != nil {
		t.Skipf("config dir is not writable: %v", err)
	}
	t.Cleanup(func() {
		os.Remove(GetConfigPath(testEngineName))
		os.Remove(GetConfigPath(testEngineName) + ".tmp")
	})
}

func TestSaveConfigRoundTrip(t *testing.T) {
	prepareConfigDir(t)
	cfg := DefaultCfg()
	cfg.InputMethod = "VNI"
	cfg.DefaultInputMode = SurroundingTextIM
	cfg.InputModeMapping["firefox"] = BackspaceForwardingIM

	SaveConfig(&cfg, testEngineName)

	if _, err := os.Stat(GetConfigPath(testEngineName) + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("the temporary file should be gone after a save, stat err = %v", err)
	}
	loaded := LoadConfig(testEngineName)
	if loaded.InputMethod != "VNI" || loaded.DefaultInputMode != SurroundingTextIM {
		t.Errorf("loaded config = %s/%d, want VNI/%d", loaded.InputMethod, loaded.DefaultInputMode, SurroundingTextIM)
	}
	if loaded.InputModeMapping["firefox"] != BackspaceForwardingIM {
		t.Errorf("loaded mapping = %v", loaded.InputModeMapping)
	}
}

func TestLoadConfigFallsBackToDefaults(t *testing.T) {
	prepareConfigDir(t)
	cases := []struct {
		name    string
		content string
	}{
		{"truncated json", `{"InputMethod": "VNI", "DefaultInputMode": `},
		{"wrong type", `{"InputMethod": "VNI", "DefaultInputMode": "two"}`},
	}
	want := DefaultCfg()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ioutil.WriteFile(GetConfigPath(testEngineName), []byte(tc.content), 0644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			loaded := LoadConfig(testEngineName)
			if loaded.InputMethod != want.InputMethod || loaded.DefaultInputMode != want.DefaultInputMode {
				t.Errorf("loaded config = %s/%d, want defaults %s/%d",
					loaded.InputMethod, loaded.DefaultInputMode, want.InputMethod, want.DefaultInputMode)
			}
		})
	}

	os.Remove(GetConfigPath(testEngineName))
	if loaded := LoadConfig(testEngineName); loaded.InputMethod != want.InputMethod {
		t.Errorf("a missing config should load defaults, got %s", loaded.InputMethod)
	}
	if us := LoadConfig("bamboous"); us.DefaultInputMode != UsIM {
		t.Errorf("bamboous default mode = %d, want %d", us.DefaultInputMode, UsIM)
	}
}
