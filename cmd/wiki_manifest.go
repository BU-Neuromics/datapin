package cmd

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// wikiScanCache memoizes per-project wiki page listings for the duration of one
// command so the many entries that share a project don't each re-list. It
// mirrors resolver.CachingLister's role for files. singleflight collapses
// concurrent identical fetches so each project lists exactly once.
type wikiScanCache struct {
	c   *client.OSFClient
	mu  sync.Mutex
	sf  map[string]*wikiListCall
	got map[string][]client.Wiki
}

type wikiListCall struct {
	wg   sync.WaitGroup
	list []client.Wiki
	err  error
}

func newWikiScanCache(c *client.OSFClient) *wikiScanCache {
	return &wikiScanCache{c: c, sf: map[string]*wikiListCall{}, got: map[string][]client.Wiki{}}
}

// listWikis returns a project's wiki pages, fetching once and reusing the result.
func (w *wikiScanCache) listWikis(ctx context.Context, nodeID string) ([]client.Wiki, error) {
	w.mu.Lock()
	if cached, ok := w.got[nodeID]; ok {
		w.mu.Unlock()
		return cached, nil
	}
	if call, ok := w.sf[nodeID]; ok {
		w.mu.Unlock()
		call.wg.Wait()
		return call.list, call.err
	}
	call := &wikiListCall{}
	call.wg.Add(1)
	w.sf[nodeID] = call
	w.mu.Unlock()

	call.list, call.err = w.c.ListWikis(ctx, nodeID)
	call.wg.Done()

	w.mu.Lock()
	if call.err == nil {
		w.got[nodeID] = call.list
	}
	delete(w.sf, nodeID)
	w.mu.Unlock()
	return call.list, call.err
}

// md5hex returns the lowercase hex MD5 of b.
func md5hex(b []byte) string {
	h := md5.Sum(b)
	return fmt.Sprintf("%x", h[:])
}

// wikiContentMD5 returns the MD5 of content in its canonical form. Wiki content
// is hashed canonically (not byte-for-byte) because OSF normalizes it on write;
// see client.CanonicalizeWikiContent.
func wikiContentMD5(content []byte) string {
	return md5hex(client.CanonicalizeWikiContent(content))
}

// wikiLocalMD5 returns the canonical-content MD5 of the local markdown file at
// path, or "" (no error) if the file does not exist. This is the wiki analogue
// of computeLocalMD5, but it hashes the canonical form so a local file that
// differs from the remote only in line endings or trailing whitespace still
// classifies as in sync (OSF would store both identically).
func wikiLocalMD5(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return wikiContentMD5(data), nil
}

// canSkipWikiHistory mirrors canSkipVersionHistory for wikis: when local content
// equals the remote latest, or a pinned entry's local still equals its baseline,
// classification cannot depend on older versions, so per-version content fetches
// are unnecessary and a latest-only slice suffices.
func canSkipWikiHistory(localMD5, latestMD5 string, latestNum int, entry manifest.WikiEntry) bool {
	if latestNum <= 0 || localMD5 == "" {
		return false
	}
	if localMD5 == latestMD5 {
		return true
	}
	return entry.Version > 0 && localMD5 == entry.MD5
}

// fetchWikiRemoteState resolves a wiki entry's remote page and returns it along
// with the remote versions (numbers + gosf-computed MD5s) needed to classify it.
// Latest content is fetched once via the content endpoint; older versions are
// hashed only when the latest/baseline fast path does not settle the
// classification. A nil page means the entry's page does not exist remotely.
func fetchWikiRemoteState(ctx context.Context, cache *wikiScanCache, c *client.OSFClient, nodeID string, entry manifest.WikiEntry, localMD5 string) (*client.Wiki, []manifest.RemoteVersion, error) {
	wikis, err := cache.listWikis(ctx, nodeID)
	if err != nil {
		return nil, nil, friendlyWikiError(err, nodeID)
	}
	page, ok := findWikiPage(wikis, entry.Page)
	if !ok {
		return nil, nil, nil
	}

	latestNum := page.Attributes.Extra.Version
	latestContent, err := c.GetWikiContent(ctx, page.ID)
	if err != nil {
		return page, nil, friendlyWikiError(err, nodeID)
	}
	latestMD5 := wikiContentMD5(latestContent)

	if canSkipWikiHistory(localMD5, latestMD5, latestNum, entry) {
		log.Debugf("wiki %q: classified from latest (v%d), skipping history", entry.Page, latestNum)
		return page, []manifest.RemoteVersion{{Version: latestNum, MD5: latestMD5}}, nil
	}

	// Need the full history: hash each older version's content.
	rawVersions, err := c.GetWikiVersions(ctx, page.ID)
	if err != nil {
		return page, []manifest.RemoteVersion{{Version: latestNum, MD5: latestMD5}}, nil
	}
	out := make([]manifest.RemoteVersion, 0, len(rawVersions))
	for _, v := range rawVersions {
		num := v.Number()
		if num == latestNum {
			out = append(out, manifest.RemoteVersion{Version: num, MD5: latestMD5})
			continue
		}
		content, cerr := c.GetWikiVersionContent(ctx, page.ID, strconv.Itoa(num))
		if cerr != nil {
			log.Tracef("wiki %q: version %d content fetch failed: %v", entry.Page, num, cerr)
			continue
		}
		out = append(out, manifest.RemoteVersion{Version: num, MD5: wikiContentMD5(content)})
	}
	log.Debugf("wiki %q: %d remote version(s)", entry.Page, len(out))
	return page, out, nil
}
