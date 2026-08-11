package config

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// reset puts global state (keyring + config dir) into a clean, isolated state.
func reset(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OSF_TOKEN", "")
}

func TestLoadToken_FlagWins(t *testing.T) {
	reset(t)
	t.Setenv("OSF_TOKEN", "from-env")
	if err := InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	if err := writeTokenToFile("from-file"); err != nil {
		t.Fatalf("writeTokenToFile: %v", err)
	}
	_ = keyring.Set(keychainService, keychainUser, "from-keychain")

	if got := LoadToken("from-flag"); got != "from-flag" {
		t.Errorf("LoadToken = %q, want from-flag", got)
	}
}

func TestLoadToken_EnvBeatsFileAndKeychain(t *testing.T) {
	reset(t)
	t.Setenv("OSF_TOKEN", "from-env")
	if err := InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	if err := writeTokenToFile("from-file"); err != nil {
		t.Fatalf("writeTokenToFile: %v", err)
	}
	_ = keyring.Set(keychainService, keychainUser, "from-keychain")

	if got := LoadToken(""); got != "from-env" {
		t.Errorf("LoadToken = %q, want from-env", got)
	}
}

func TestLoadToken_FileBeatsKeychain(t *testing.T) {
	reset(t)
	if err := InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	if err := writeTokenToFile("from-file"); err != nil {
		t.Fatalf("writeTokenToFile: %v", err)
	}
	_ = keyring.Set(keychainService, keychainUser, "from-keychain")

	if got := LoadToken(""); got != "from-file" {
		t.Errorf("LoadToken = %q, want from-file", got)
	}
}

func TestLoadToken_KeychainFallback(t *testing.T) {
	reset(t)
	_ = keyring.Set(keychainService, keychainUser, "from-keychain")

	if got := LoadToken(""); got != "from-keychain" {
		t.Errorf("LoadToken = %q, want from-keychain", got)
	}
}

func TestLoadToken_NoneReturnsEmpty(t *testing.T) {
	reset(t)
	if got := LoadToken(""); got != "" {
		t.Errorf("LoadToken = %q, want empty", got)
	}
}

func TestSaveToken_Keychain(t *testing.T) {
	reset(t)
	if err := SaveToken("stored-tok", false); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	got, err := keyring.Get(keychainService, keychainUser)
	if err != nil {
		t.Fatalf("keyring.Get: %v", err)
	}
	if got != "stored-tok" {
		t.Errorf("keychain token = %q, want stored-tok", got)
	}
}

func TestSaveToken_FileFallback(t *testing.T) {
	reset(t)
	if err := InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	if err := SaveToken("file-tok", true); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	// noKeychain=true must not write to the keychain.
	if _, err := keyring.Get(keychainService, keychainUser); err == nil {
		t.Error("expected keychain to be empty when --no-keychain used")
	}
	// Token must be in the dedicated token file.
	if got := readTokenFromFile(); got != "file-tok" {
		t.Errorf("token file = %q, want file-tok", got)
	}
	// Token must NOT appear in config.toml.
	cfgPath, err := configFilePath()
	if err != nil {
		t.Fatalf("configFilePath: %v", err)
	}
	data, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(data), "file-tok") {
		t.Error("token must not appear in config.toml")
	}
	// LoadToken must find it.
	if got := LoadToken(""); got != "file-tok" {
		t.Errorf("LoadToken after file save = %q, want file-tok", got)
	}
}

func TestSaveToken_TokenNotInViperConfig(t *testing.T) {
	reset(t)
	if err := InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	if err := SaveToken("secret-tok", true); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	cfgPath, err := configFilePath()
	if err != nil {
		t.Fatalf("configFilePath: %v", err)
	}
	data, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(data), "token") {
		t.Errorf("'token' key must not appear in config.toml; got:\n%s", data)
	}
}

func TestSaveToken_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits not meaningful on Windows")
	}
	reset(t)
	if err := InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	if err := SaveToken("perm-tok", true); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	p, err := tokenFilePath()
	if err != nil {
		t.Fatalf("tokenFilePath: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("token file perms = %04o, want 0600", got)
	}
}

func TestDeleteToken_ClearsKeychainAndFile(t *testing.T) {
	reset(t)
	if err := InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	_ = keyring.Set(keychainService, keychainUser, "from-keychain")
	if err := writeTokenToFile("from-file"); err != nil {
		t.Fatalf("writeTokenToFile: %v", err)
	}

	warning, err := DeleteToken()
	if err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if warning != "" {
		t.Errorf("unexpected keychain warning: %q", warning)
	}
	if _, err := keyring.Get(keychainService, keychainUser); err == nil {
		t.Error("expected keychain token to be deleted")
	}
	if got := readTokenFromFile(); got != "" {
		t.Errorf("token file = %q, want empty after delete", got)
	}
	if got := LoadToken(""); got != "" {
		t.Errorf("LoadToken after delete = %q, want empty", got)
	}
}

func TestDeleteToken_NoFileIsOK(t *testing.T) {
	reset(t)
	// No token file written — delete should still succeed.
	if _, err := DeleteToken(); err != nil {
		t.Errorf("DeleteToken with no token file = %v, want nil", err)
	}
}

// TestDeleteToken_WarnsButSucceedsOnKeychainError verifies that a keychain
// failure (e.g. a locked keychain on headless/HPC) is a non-fatal warning: the
// token file is still removed and logout succeeds. This is what keeps
// `datapin auth logout` working where the keychain is unavailable.
func TestDeleteToken_WarnsButSucceedsOnKeychainError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OSF_TOKEN", "")
	keyring.MockInitWithError(fmt.Errorf("keychain is locked"))
	defer keyring.MockInit()

	if err := InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	if err := writeTokenToFile("from-file"); err != nil {
		t.Fatalf("writeTokenToFile: %v", err)
	}

	warning, err := DeleteToken()
	if err != nil {
		t.Fatalf("DeleteToken should not fail on a keychain error, got: %v", err)
	}
	if warning == "" {
		t.Error("expected a non-empty keychain warning")
	}
	if got := readTokenFromFile(); got != "" {
		t.Errorf("token file should be removed despite the keychain error, got %q", got)
	}
}

func TestTokenSource(t *testing.T) {
	reset(t)
	if got := TokenSource("flag-val"); got != "--token flag" {
		t.Errorf("flag source = %q", got)
	}

	reset(t)
	t.Setenv("OSF_TOKEN", "x")
	if got := TokenSource(""); got != "OSF_TOKEN environment variable" {
		t.Errorf("env source = %q", got)
	}

	reset(t)
	if err := InitViper(); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	if err := writeTokenToFile("x"); err != nil {
		t.Fatalf("writeTokenToFile: %v", err)
	}
	if got := TokenSource(""); got != "token file" {
		t.Errorf("token file source = %q, want \"token file\"", got)
	}

	reset(t)
	_ = keyring.Set(keychainService, keychainUser, "x")
	if got := TokenSource(""); got != "OS keychain" {
		t.Errorf("keychain source = %q", got)
	}

	reset(t)
	if got := TokenSource(""); got != "" {
		t.Errorf("empty source = %q, want empty", got)
	}
}
