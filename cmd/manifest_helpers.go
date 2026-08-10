package cmd

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"os"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

// computeLocalMD5 returns the lowercase hex MD5 of the file at path.
// Returns "" (not an error) if the file does not exist.
func computeLocalMD5(path string) (string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("computing MD5 of %s: %w", path, err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// localFileMatches reports whether the file at path exists and its MD5 equals
// remoteMD5. Used to make transfers idempotent: an identical local copy means
// there is nothing to download (or upload). A missing file, unreadable file, or
// empty remoteMD5 all report false (i.e. "not known-identical, proceed").
func localFileMatches(path, remoteMD5 string) bool {
	if remoteMD5 == "" {
		return false
	}
	localMD5, err := computeLocalMD5(path)
	if err != nil || localMD5 == "" {
		return false
	}
	return localMD5 == remoteMD5
}

// fileVersionsToRemote converts client.FileVersion slice to manifest.RemoteVersion slice.
func fileVersionsToRemote(versions []client.FileVersion) []manifest.RemoteVersion {
	if versions == nil {
		return nil
	}
	out := make([]manifest.RemoteVersion, len(versions))
	for i, v := range versions {
		out[i] = manifest.RemoteVersion{
			Version: v.Number(),
			MD5:     v.Attributes.Extra.Hashes.MD5,
		}
	}
	return out
}

// latestRemoteVersion returns the highest version number in the slice, or 0 if empty.
func latestRemoteVersion(versions []manifest.RemoteVersion) int {
	max := 0
	for _, v := range versions {
		if v.Version > max {
			max = v.Version
		}
	}
	return max
}

// canSkipVersionHistory reports whether the remote's latest version (its MD5 and
// number, both available from a directory listing) is sufficient to classify an
// entry — i.e. classification provably cannot depend on any older version, so a
// per-file GetFileVersions call can be skipped.
//
// ClassifyFile only inspects older versions in its BEHIND/DIVERGED branches,
// which are reachable only when local content matches neither the remote latest
// nor the pinned baseline. So the fetch is skippable when:
//   - local content equals the remote latest (→ IN_SYNC / REMOTE_NEWER / PIN_ONLY,
//     all decided by latest MD5 + number), or
//   - the entry is pinned and local still equals its baseline (L==B → IN_SYNC or
//     REMOTE_NEWER, decided by the latest number alone).
//
// Requires a known latest version number (latestNum > 0); OSF supplies it as
// attributes.current_version in listings, but older/edge responses may omit it,
// in which case we fall back to the full versions fetch.
func canSkipVersionHistory(localMD5, latestMD5 string, latestNum int, entry manifest.Entry) bool {
	if latestNum <= 0 || localMD5 == "" {
		return false
	}
	if localMD5 == latestMD5 {
		return true
	}
	return entry.Version > 0 && localMD5 == entry.MD5
}

// fetchRemoteState resolves an entry's remote file and returns it along with the
// remote versions needed to classify it. It fetches the full version history
// only when necessary: when wantVersions is true and the latest-version fast
// path (canSkipVersionHistory) does not apply. Resolution and version fetches go
// through the (cached) resolver/client, so repeated directory listings collapse
// to one network call.
//
// A remote path that genuinely does not exist returns (nil, nil, nil) — that is
// a valid classification input meaning "nothing on the remote to compare".
// Anything else (throttling, an unreachable API, a permission error) is
// returned as an error rather than folded into the same answer: treating a 429
// as "the file is not there" classifies an unpinned entry as NOT_PUSHED, and
// since #81 that makes sync upload a file that was already on the remote
// (issue #86).
func fetchRemoteState(ctx context.Context, res *resolver.Resolver, osf *client.OSFClient, proj string, entry manifest.Entry, localMD5 string, wantVersions bool) (*client.FileItem, []manifest.RemoteVersion, error) {
	item, err := res.Resolve(ctx, proj, entry.Remote)
	if err != nil {
		if resolver.IsNotFound(err) {
			log.Debugf("%s: not on the remote", entry.Remote)
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("checking %s on the remote: %w", entry.Remote, err)
	}
	if !wantVersions {
		return &item, nil, nil
	}

	latestMD5 := item.Attributes.Extra.Hashes.MD5
	latestNum := item.Attributes.CurrentVersion
	if canSkipVersionHistory(localMD5, latestMD5, latestNum, entry) {
		log.Debugf("%s: classified from listing (remote latest v%d), skipping version history", entry.Local, latestNum)
		return &item, []manifest.RemoteVersion{{Version: latestNum, MD5: latestMD5}}, nil
	}

	fvs, err := osf.GetFileVersions(ctx, item.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching versions of %s: %w", entry.Remote, err)
	}
	log.Debugf("%s: %d remote version(s)", entry.Local, len(fvs))
	return &item, fileVersionsToRemote(fvs), nil
}
