package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/zalando/go-keyring"
)

// Remote is one configured backend remote (plan §4.6). Tokens are stored
// separately (keychain or token file), never in config.toml — the config
// stays safe to commit to a dotfiles repo.
type Remote struct {
	Name string `toml:"-"`
	Kind string `toml:"kind"` // "invenio" (Figshare, Dataverse later)
	URL  string `toml:"url"`
	// Caps are the per-instance capabilities probed at `remote add` time
	// (issue #20, D53), nil when the remote was never probed (--no-verify)
	// or the probe learned nothing. Hand-editable overrides live here too.
	Caps *RemoteCaps `toml:"caps,omitempty"`
}

// remotesFile mirrors the [remotes.<name>] tables in config.toml.
type remotesFile struct {
	Remotes map[string]Remote `toml:"remotes"`
}

// readConfigFile parses config.toml into raw (whole file, preserving
// unrelated keys) — the remotes functions read the file directly rather
// than through viper so a just-written remote is immediately visible.
func readConfigRaw() (map[string]any, string, error) {
	p, err := configFilePath()
	if err != nil {
		return nil, "", err
	}
	raw := map[string]any{}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return raw, p, nil
		}
		return nil, "", err
	}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, "", fmt.Errorf("parsing %s: %w", p, err)
	}
	return raw, p, nil
}

func writeConfigRaw(raw map[string]any, path string) error {
	data, err := toml.Marshal(raw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.tmp.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0600); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// Remotes lists configured remotes, sorted by name.
func Remotes() ([]Remote, error) {
	p, err := configFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var rf remotesFile
	if err := toml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	names := make([]string, 0, len(rf.Remotes))
	for name := range rf.Remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Remote, 0, len(names))
	for _, name := range names {
		r := rf.Remotes[name]
		r.Name = name
		out = append(out, r)
	}
	return out, nil
}

// GetRemote returns the named remote.
func GetRemote(name string) (Remote, bool) {
	remotes, err := Remotes()
	if err != nil {
		return Remote{}, false
	}
	for _, r := range remotes {
		if r.Name == name {
			return r, true
		}
	}
	return Remote{}, false
}

// AddRemote records a remote in config.toml, preserving unrelated keys.
func AddRemote(r Remote) error {
	if r.Name == "" {
		return fmt.Errorf("remote name cannot be blank")
	}
	raw, path, err := readConfigRaw()
	if err != nil {
		return err
	}
	remotes, _ := raw["remotes"].(map[string]any)
	if remotes == nil {
		remotes = map[string]any{}
	}
	if _, dup := remotes[r.Name]; dup {
		return fmt.Errorf("remote %q already exists — remove it first with: datapin remote rm %s", r.Name, r.Name)
	}
	entry := map[string]any{"kind": r.Kind, "url": r.URL}
	if !r.Caps.IsEmpty() {
		entry["caps"] = capsTable(r.Caps)
	}
	remotes[r.Name] = entry
	raw["remotes"] = remotes
	return writeConfigRaw(raw, path)
}

// RemoveRemote deletes a remote from config.toml (its stored token, if
// any, is the caller's to delete via DeleteRemoteToken).
func RemoveRemote(name string) error {
	raw, path, err := readConfigRaw()
	if err != nil {
		return err
	}
	remotes, _ := raw["remotes"].(map[string]any)
	if _, ok := remotes[name]; !ok {
		return fmt.Errorf("no remote named %q", name)
	}
	delete(remotes, name)
	raw["remotes"] = remotes
	return writeConfigRaw(raw, path)
}

// remoteEnvVar maps a remote name to its token env var:
// "my-lab" → DATAPIN_TOKEN_MY_LAB.
func remoteEnvVar(name string) string {
	up := strings.ToUpper(name)
	up = strings.NewReplacer("-", "_", ".", "_").Replace(up)
	return "DATAPIN_TOKEN_" + up
}

func remoteTokenPath(name string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tokens", name), nil
}

// LoadRemoteToken returns the token for a named remote:
// DATAPIN_TOKEN_<NAME> env > OS keychain > token file. Empty if none.
func LoadRemoteToken(name string) string {
	if t := os.Getenv(remoteEnvVar(name)); t != "" {
		return t
	}
	if t, err := keyring.Get(keychainService, "remote-"+name); err == nil && t != "" {
		return t
	}
	if p, err := remoteTokenPath(name); err == nil {
		if data, err := os.ReadFile(p); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

// SaveRemoteToken stores a remote's token in the OS keychain, falling back
// to a 0600 token file (~/.config/datapin/tokens/<name>) when the keychain
// is unavailable or noKeychain is set.
func SaveRemoteToken(name, token string, noKeychain bool) error {
	if !noKeychain {
		if err := keyring.Set(keychainService, "remote-"+name, token); err == nil {
			return nil
		}
	}
	p, err := remoteTokenPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(token), 0600)
}

// DeleteRemoteToken removes a remote's token from every store. Keychain
// trouble is a warning (headless systems), a failed file removal an error.
func DeleteRemoteToken(name string) (warning string, err error) {
	if kerr := keyring.Delete(keychainService, "remote-"+name); kerr != nil && kerr != keyring.ErrNotFound {
		warning = fmt.Sprintf("could not remove token from OS keychain: %v", kerr)
	}
	p, perr := remoteTokenPath(name)
	if perr != nil {
		return warning, perr
	}
	if rmErr := os.Remove(p); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return warning, fmt.Errorf("removing token file: %w", rmErr)
	}
	return warning, nil
}
