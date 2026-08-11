package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"

	"github.com/BU-Neuromics/datapin/internal/log"
)

const (
	keychainService = "datapin"
	keychainUser    = "token"

	// legacyKeychainService is the pre-rename service name; entries stored
	// by gosf are still read (and removed on logout) for migration.
	legacyKeychainService = "gosf"
)

// ConfigDir returns the datapin config directory path (~/.config/datapin).
func ConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "datapin"), nil
}

// legacyConfigDir returns the pre-rename config directory (~/.config/gosf),
// which is read (never written) for migration.
func legacyConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gosf"), nil
}

func configFilePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// tokenFilePath returns the path to the dedicated token file (~/.config/datapin/token).
// The token is stored here rather than in config.toml so that config.toml
// remains safe to commit to version control (e.g. in a dotfiles repository).
func tokenFilePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "token"), nil
}

// InitViper configures viper to read from ~/.config/datapin/config.toml.
// Missing config file is not an error.
func InitViper() error {
	dir, err := ConfigDir()
	if err != nil {
		return fmt.Errorf("resolving config dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	viper.AddConfigPath(dir)
	// Read-only migration fallback: a pre-rename ~/.config/gosf/config.toml
	// is still honored when no datapin config exists (earlier paths win).
	if legacyDir, lerr := legacyConfigDir(); lerr == nil {
		viper.AddConfigPath(legacyDir)
	}
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("reading config: %w", err)
		}
	}
	return nil
}

// LoadToken returns the token using the priority chain:
// flagToken > OSF_TOKEN env > token file > OS keychain.
// Within the file and keychain tiers the datapin store wins, falling back to
// the pre-rename gosf store (read-only, with a migration warning).
// Returns empty string if none found (unauthenticated mode).
func LoadToken(flagToken string) string {
	if flagToken != "" {
		return flagToken
	}
	if t := os.Getenv("OSF_TOKEN"); t != "" {
		return t
	}
	if t := readTokenFromFile(); t != "" {
		return t
	}
	if t := readLegacyTokenFromFile(); t != "" {
		log.Warnf("using token from legacy ~/.config/gosf/token — run 'datapin auth login' to migrate it")
		return t
	}
	if t, err := keyring.Get(keychainService, keychainUser); err == nil {
		return t
	}
	if t, err := keyring.Get(legacyKeychainService, keychainUser); err == nil {
		log.Warnf("using token from the legacy gosf keychain entry — run 'datapin auth login' to migrate it")
		return t
	}
	return ""
}

// SaveToken stores the token. It tries the OS keychain first unless
// noKeychain is true, falling back to the dedicated token file (~/.config/datapin/token).
func SaveToken(token string, noKeychain bool) error {
	if !noKeychain {
		if err := keyring.Set(keychainService, keychainUser, token); err == nil {
			return nil
		}
		// Keychain unavailable (headless/HPC) — fall through to token file.
	}
	return writeTokenToFile(token)
}

// DeleteToken removes the stored token from the token file and, best-effort,
// from the OS keychain. The token file is the store datapin controls directly, so a
// keychain error (e.g. a locked/unavailable keychain on a headless/HPC system)
// is returned as a non-fatal warning rather than failing logout — the file is
// still removed. Only a genuine file-removal failure is a hard error. A missing
// token file or a "not found" keychain entry are treated as already-clean.
func DeleteToken() (warning string, err error) {
	if kerr := keyring.Delete(keychainService, keychainUser); kerr != nil && kerr != keyring.ErrNotFound {
		warning = fmt.Sprintf("could not remove token from OS keychain: %v", kerr)
	}
	// Also clear the pre-rename gosf stores: a legacy token left behind would
	// keep authenticating runs after an apparently successful logout.
	if kerr := keyring.Delete(legacyKeychainService, keychainUser); kerr != nil && kerr != keyring.ErrNotFound && warning == "" {
		warning = fmt.Sprintf("could not remove token from OS keychain: %v", kerr)
	}

	p, perr := tokenFilePath()
	if perr != nil {
		return warning, perr
	}
	if rmErr := os.Remove(p); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return warning, fmt.Errorf("removing token file: %w", rmErr)
	}
	if legacyDir, lerr := legacyConfigDir(); lerr == nil {
		legacyToken := filepath.Join(legacyDir, "token")
		if rmErr := os.Remove(legacyToken); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return warning, fmt.Errorf("removing legacy token file: %w", rmErr)
		}
	}
	return warning, nil
}

// writeTokenToFile writes the token to ~/.config/datapin/token with 0600 permissions.
func writeTokenToFile(token string) error {
	p, err := tokenFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	return os.WriteFile(p, []byte(token), 0600)
}

// readTokenFromFile reads the token from ~/.config/datapin/token.
// Returns empty string if the file does not exist or cannot be read.
func readTokenFromFile() string {
	p, err := tokenFilePath()
	if err != nil {
		return ""
	}
	return readTokenFile(p)
}

// readLegacyTokenFromFile reads the token from the pre-rename
// ~/.config/gosf/token (read-only migration fallback).
func readLegacyTokenFromFile() string {
	dir, err := legacyConfigDir()
	if err != nil {
		return ""
	}
	return readTokenFile(filepath.Join(dir, "token"))
}

func readTokenFile(p string) string {
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// TokenSource returns a human-readable description of where the active
// token came from, for display in `auth status`.
func TokenSource(flagToken string) string {
	if flagToken != "" {
		return "--token flag"
	}
	if os.Getenv("OSF_TOKEN") != "" {
		return "OSF_TOKEN environment variable"
	}
	if readTokenFromFile() != "" {
		return "token file"
	}
	if readLegacyTokenFromFile() != "" {
		return "legacy gosf token file"
	}
	if _, err := keyring.Get(keychainService, keychainUser); err == nil {
		return "OS keychain"
	}
	if _, err := keyring.Get(legacyKeychainService, keychainUser); err == nil {
		return "legacy gosf OS keychain entry"
	}
	return ""
}
