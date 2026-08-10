// Package update implements a cached, best-effort "a newer datapin release is
// available" check. It never blocks a command meaningfully (short timeout,
// network hit at most once per day) and stays silent on any error.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/output"
)

const (
	defaultAPIURL = "https://api.github.com/repos/BU-Neuromics/datapin/releases/latest"
	releasesURL   = "https://github.com/BU-Neuromics/datapin/releases/latest"
	checkInterval = 24 * time.Hour
	httpTimeout   = 1500 * time.Millisecond
)

// Checker performs the cached release check. Fields are injectable for tests.
type Checker struct {
	Current    string // current version, e.g. "v1.5.0"; "dev"/"" disables comparison
	APIURL     string // GitHub latest-release endpoint
	CachePath  string // JSON file persisting the last check
	HTTPClient *http.Client
	Now        func() time.Time
}

type state struct {
	LastCheck time.Time `json:"last_check"`
	Latest    string    `json:"latest"`
}

// Notify writes a one-line notice to w when a newer release is available. It is
// best-effort: network, cache, and parse errors are all swallowed silently.
func (c *Checker) Notify(ctx context.Context, w io.Writer) {
	latest := c.cachedOrFetch(ctx)
	if latest == "" || !newerAvailable(c.Current, latest) {
		return
	}
	fmt.Fprintln(w, output.Yellow(fmt.Sprintf(
		"\nA new datapin release is available: %s (you have %s)", latest, c.Current)))
	fmt.Fprintf(w, "  %s\n", releasesURL)
}

// cachedOrFetch returns the latest version, hitting the network at most once per
// checkInterval and falling back to any cached value on error.
func (c *Checker) cachedOrFetch(ctx context.Context) string {
	now := c.Now()
	st, _ := readState(c.CachePath)
	if st.Latest != "" && now.Sub(st.LastCheck) < checkInterval {
		return st.Latest
	}
	latest, err := fetchLatest(ctx, c.HTTPClient, c.APIURL)
	if err != nil || latest == "" {
		return st.Latest
	}
	_ = writeState(c.CachePath, state{LastCheck: now, Latest: latest})
	return latest
}

// MaybeNotify is the CLI entry point: it gates on the environment and, when
// appropriate, runs the cached check writing to stderr. Called once per command.
func MaybeNotify(current string, quiet, jsonMode bool) {
	disabled := os.Getenv("DATAPIN_NO_UPDATE_CHECK") != ""
	stderrTTY := term.IsTerminal(int(os.Stderr.Fd()))
	if !shouldNotify(current, quiet, jsonMode, stderrTTY, disabled) {
		return
	}
	dir, err := config.ConfigDir()
	if err != nil {
		return
	}
	c := &Checker{
		Current:    current,
		APIURL:     defaultAPIURL,
		CachePath:  filepath.Join(dir, "update_check.json"),
		HTTPClient: &http.Client{Timeout: httpTimeout},
		Now:        time.Now,
	}
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()
	c.Notify(ctx, os.Stderr)
}

// shouldNotify reports whether the check should run at all. Pure/testable.
func shouldNotify(current string, quiet, jsonMode, stderrTTY, disabled bool) bool {
	if disabled || quiet || jsonMode || !stderrTTY {
		return false
	}
	// A dev/unversioned build has nothing meaningful to compare against.
	return parseSemver(current) != nil
}

// newerAvailable reports whether latest is a higher semver than current.
func newerAvailable(current, latest string) bool {
	c := parseSemver(current)
	l := parseSemver(latest)
	if c == nil || l == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseSemver parses "vX.Y.Z" (or "X.Y.Z") into [3]int, ignoring any
// pre-release/build suffix. Returns nil if it is not a plain release version.
func parseSemver(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

func fetchLatest(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("release API status %d", resp.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.TagName, nil
}

func readState(path string) (state, error) {
	var st state
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(data, &st)
	return st, err
}

func writeState(path string, st state) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
