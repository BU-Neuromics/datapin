package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/BU-Neuromics/datapin/internal/config"
)

func setupRemotesEnv(t *testing.T) string {
	t.Helper()
	keyring.MockInit()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	return root
}

func TestAddRemote_ListAndGet(t *testing.T) {
	setupRemotesEnv(t)
	r := config.Remote{Name: "sandbox", Kind: "invenio", URL: "https://sandbox.zenodo.org"}
	if err := config.AddRemote(r); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	remotes, err := config.Remotes()
	if err != nil {
		t.Fatalf("Remotes: %v", err)
	}
	if len(remotes) != 1 || remotes[0] != r {
		t.Errorf("Remotes = %+v, want [%+v]", remotes, r)
	}
	got, ok := config.GetRemote("sandbox")
	if !ok || got != r {
		t.Errorf("GetRemote = %+v/%v", got, ok)
	}
	if _, ok := config.GetRemote("nope"); ok {
		t.Error("GetRemote(nope) must report absence")
	}
}

func TestAddRemote_DuplicateNameRejected(t *testing.T) {
	setupRemotesEnv(t)
	r := config.Remote{Name: "sandbox", Kind: "invenio", URL: "https://sandbox.zenodo.org"}
	if err := config.AddRemote(r); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	if err := config.AddRemote(r); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("second AddRemote = %v, want already-exists error", err)
	}
}

func TestAddRemote_PreservesUnrelatedConfig(t *testing.T) {
	root := setupRemotesEnv(t)
	dir := filepath.Join(root, "datapin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("existing_key = \"keep-me\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.AddRemote(config.Remote{Name: "z", Kind: "invenio", URL: "https://zenodo.org"}); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "keep-me") {
		t.Errorf("AddRemote clobbered unrelated config:\n%s", data)
	}
}

func TestRemoveRemote(t *testing.T) {
	setupRemotesEnv(t)
	if err := config.AddRemote(config.Remote{Name: "z", Kind: "invenio", URL: "https://zenodo.org"}); err != nil {
		t.Fatal(err)
	}
	if err := config.RemoveRemote("z"); err != nil {
		t.Fatalf("RemoveRemote: %v", err)
	}
	if _, ok := config.GetRemote("z"); ok {
		t.Error("remote still present after RemoveRemote")
	}
	if err := config.RemoveRemote("z"); err == nil {
		t.Error("RemoveRemote of a missing remote must error")
	}
}

func TestRemoteToken_EnvWins(t *testing.T) {
	setupRemotesEnv(t)
	t.Setenv("DATAPIN_TOKEN_SANDBOX", "from-env")
	if err := config.SaveRemoteToken("sandbox", "from-store", true); err != nil {
		t.Fatal(err)
	}
	if got := config.LoadRemoteToken("sandbox"); got != "from-env" {
		t.Errorf("LoadRemoteToken = %q, want env to win", got)
	}
}

func TestRemoteToken_HyphenatedNameMapsToUnderscoreEnv(t *testing.T) {
	setupRemotesEnv(t)
	t.Setenv("DATAPIN_TOKEN_MY_LAB", "from-env")
	if got := config.LoadRemoteToken("my-lab"); got != "from-env" {
		t.Errorf("LoadRemoteToken(my-lab) = %q, want DATAPIN_TOKEN_MY_LAB honored", got)
	}
}

func TestRemoteToken_FileFallbackAndDelete(t *testing.T) {
	root := setupRemotesEnv(t)
	if err := config.SaveRemoteToken("sandbox", "stored-token", true /* noKeychain */); err != nil {
		t.Fatalf("SaveRemoteToken: %v", err)
	}
	p := filepath.Join(root, "datapin", "tokens", "sandbox")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("token file missing: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("token file mode = %v, want 0600", info.Mode().Perm())
	}
	if got := config.LoadRemoteToken("sandbox"); got != "stored-token" {
		t.Errorf("LoadRemoteToken = %q", got)
	}
	if _, err := config.DeleteRemoteToken("sandbox"); err != nil {
		t.Fatalf("DeleteRemoteToken: %v", err)
	}
	if got := config.LoadRemoteToken("sandbox"); got != "" {
		t.Errorf("token survives delete: %q", got)
	}
}

func TestRemoteToken_Keychain(t *testing.T) {
	setupRemotesEnv(t)
	if err := config.SaveRemoteToken("sandbox", "keychain-token", false); err != nil {
		t.Fatalf("SaveRemoteToken: %v", err)
	}
	if got := config.LoadRemoteToken("sandbox"); got != "keychain-token" {
		t.Errorf("LoadRemoteToken = %q", got)
	}
	if _, err := config.DeleteRemoteToken("sandbox"); err != nil {
		t.Fatalf("DeleteRemoteToken: %v", err)
	}
	if got := config.LoadRemoteToken("sandbox"); got != "" {
		t.Errorf("keychain token survives delete: %q", got)
	}
}
