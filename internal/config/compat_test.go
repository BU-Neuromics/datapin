package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"

	"github.com/BU-Neuromics/datapin/internal/config"
)

// ---- legacy ~/.config/gosf compatibility (read-only migration path) ----

// setupCompatEnv isolates the test from the real keychain, config dir, and
// token env var, and returns the temp XDG_CONFIG_HOME root.
func setupCompatEnv(t *testing.T) string {
	t.Helper()
	keyring.MockInit()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("OSF_TOKEN", "")
	return root
}

func writeTokenFile(t *testing.T, root, app, token string) {
	t.Helper()
	dir := filepath.Join(root, app)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadToken_LegacyTokenFileFallback(t *testing.T) {
	root := setupCompatEnv(t)
	writeTokenFile(t, root, "gosf", "legacy-file-token")

	if got := config.LoadToken(""); got != "legacy-file-token" {
		t.Errorf("LoadToken = %q, want the legacy ~/.config/gosf/token to be read", got)
	}
}

func TestLoadToken_NewTokenFileWinsOverLegacy(t *testing.T) {
	root := setupCompatEnv(t)
	writeTokenFile(t, root, "datapin", "new-token")
	writeTokenFile(t, root, "gosf", "legacy-token")

	if got := config.LoadToken(""); got != "new-token" {
		t.Errorf("LoadToken = %q, want the datapin token file to win", got)
	}
}

func TestLoadToken_LegacyKeychainFallback(t *testing.T) {
	setupCompatEnv(t)
	if err := keyring.Set("gosf", "token", "legacy-keychain-token"); err != nil {
		t.Fatal(err)
	}

	if got := config.LoadToken(""); got != "legacy-keychain-token" {
		t.Errorf("LoadToken = %q, want the legacy gosf keychain entry to be read", got)
	}
}

func TestLoadToken_NewKeychainWinsOverLegacy(t *testing.T) {
	setupCompatEnv(t)
	if err := keyring.Set("datapin", "token", "new-keychain-token"); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set("gosf", "token", "legacy-keychain-token"); err != nil {
		t.Fatal(err)
	}

	if got := config.LoadToken(""); got != "new-keychain-token" {
		t.Errorf("LoadToken = %q, want the datapin keychain entry to win", got)
	}
}

func TestSaveToken_WritesOnlyNewLocations(t *testing.T) {
	root := setupCompatEnv(t)

	if err := config.SaveToken("fresh-token", true /* noKeychain */); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "datapin", "token")); err != nil {
		t.Errorf("SaveToken must write ~/.config/datapin/token: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "gosf", "token")); !os.IsNotExist(err) {
		t.Error("SaveToken must never write to the legacy ~/.config/gosf/token")
	}
}

func TestDeleteToken_RemovesLegacyStoresToo(t *testing.T) {
	root := setupCompatEnv(t)
	writeTokenFile(t, root, "gosf", "legacy-token")
	if err := keyring.Set("gosf", "token", "legacy-keychain-token"); err != nil {
		t.Fatal(err)
	}

	if _, err := config.DeleteToken(); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}

	if got := config.LoadToken(""); got != "" {
		t.Errorf("after DeleteToken a legacy store still supplies a token (%q) — logout would silently not log out", got)
	}
}

func TestInitViper_ReadsLegacyConfigFile(t *testing.T) {
	root := setupCompatEnv(t)
	dir := filepath.Join(root, "gosf")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("legacy_marker = \"from-gosf\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	if err := config.InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	if got := viper.GetString("legacy_marker"); got != "from-gosf" {
		t.Errorf("legacy_marker = %q, want the legacy ~/.config/gosf/config.toml to be read", got)
	}
}

func TestInitViper_NewConfigWinsOverLegacy(t *testing.T) {
	root := setupCompatEnv(t)
	for app, val := range map[string]string{"datapin": "from-datapin", "gosf": "from-gosf"} {
		dir := filepath.Join(root, app)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("marker = \""+val+"\"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	if err := config.InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	if got := viper.GetString("marker"); got != "from-datapin" {
		t.Errorf("marker = %q, want the datapin config to win", got)
	}
}
