package cmd

import (
	"errors"
	"fmt"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/log"
)

// friendlyAuthError converts a raw 401/403 from a read command (ls/info/pull/
// status/versions) into an actionable message. These commands attempt the fetch
// unauthenticated first — public projects need no token — so a 401/403 means the
// resource is private and a token is required.
func friendlyAuthError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403) {
		return fmt.Errorf("access denied (HTTP %d) — this project is private or the token is invalid; "+
			"run 'datapin auth login' or set OSF_TOKEN to authenticate", apiErr.StatusCode)
	}
	return err
}

// friendlyAPIError maps the API failures a user can do something about into
// actionable messages. It supersedes friendlyAuthError by also handling
// throttling, which needs to know whether the run was authenticated: OSF allows
// roughly 100 requests/hour anonymously against 10,000/day with a token, and a
// user who believes they are logged in but is not gets no other signal
// (issue #86).
func friendlyAPIError(err error, authenticated bool) error {
	if err == nil {
		return nil
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	if apiErr.StatusCode == 429 {
		if !authenticated {
			return fmt.Errorf("OSF rate limit reached (HTTP 429) — this run was unauthenticated, "+
				"which OSF limits to about 100 requests per hour. Run 'datapin auth login' (or set "+
				"OSF_TOKEN) for a far higher allowance, then try again.\n  (%s)", apiErr.Message)
		}
		return fmt.Errorf("OSF rate limit reached (HTTP 429) — datapin waited and retried, but the "+
			"limit is still in force. Wait for the quota to reset and try again; "+
			"'--jobs=1' spreads a large scan out more gently.\n  (%s)", apiErr.Message)
	}

	return friendlyAuthError(err)
}

// shouldWarnUnauthenticated reports whether a manifest-scanning run should warn
// that it is anonymous. config.LoadToken returns "" for a locked keychain or a
// mistyped variable just as it does for a deliberate anonymous run, so without
// this a user can burn the 100/hour allowance never knowing why. An empty
// manifest issues no requests worth warning about.
func shouldWarnUnauthenticated(token string, trackedEntries int) bool {
	return token == "" && trackedEntries > 0
}

// warnUnauthenticated emits that warning once for a run.
func warnUnauthenticated(token string, trackedEntries int) {
	if shouldWarnUnauthenticated(token, trackedEntries) {
		log.Warnf("running unauthenticated — OSF limits anonymous use to about 100 requests/hour, " +
			"which a manifest scan can exhaust; run 'datapin auth login' or set OSF_TOKEN")
	}
}
