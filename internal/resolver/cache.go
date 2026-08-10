package resolver

import (
	"context"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/BU-Neuromics/datapin/internal/client"
)

// cachingLister wraps a FileLister and memoizes directory listings for the
// lifetime of the wrapper (one per command invocation). A bulk scan resolves
// many files that share directories; without caching, resolver.Resolve re-lists
// the same folders once per file. singleflight collapses concurrent identical
// fetches so a parallel scan issues each listing exactly once.
type cachingLister struct {
	inner  FileLister
	sf     singleflight.Group
	mu     sync.RWMutex
	byNode map[string][]client.FileItem
	byURL  map[string][]client.FileItem
}

// NewCachingLister returns a FileLister that memoizes ListFiles/ListFilesFromURL
// results. It is safe for concurrent use. Cached entries never expire, so scope
// one instance to a single command run (the remote is assumed stable for the
// duration of a scan).
func NewCachingLister(inner FileLister) FileLister {
	return &cachingLister{
		inner:  inner,
		byNode: make(map[string][]client.FileItem),
		byURL:  make(map[string][]client.FileItem),
	}
}

func (c *cachingLister) ListFiles(ctx context.Context, nodeID string) ([]client.FileItem, error) {
	c.mu.RLock()
	if v, ok := c.byNode[nodeID]; ok {
		c.mu.RUnlock()
		return v, nil
	}
	c.mu.RUnlock()

	v, err, _ := c.sf.Do("node:"+nodeID, func() (any, error) {
		// Re-check under the flight: a prior leader may have populated the
		// cache between our RLock miss and joining singleflight.
		c.mu.RLock()
		if v, ok := c.byNode[nodeID]; ok {
			c.mu.RUnlock()
			return v, nil
		}
		c.mu.RUnlock()
		items, err := c.inner.ListFiles(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.byNode[nodeID] = items
		c.mu.Unlock()
		return items, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]client.FileItem), nil
}

func (c *cachingLister) ListFilesFromURL(ctx context.Context, url string) ([]client.FileItem, error) {
	c.mu.RLock()
	if v, ok := c.byURL[url]; ok {
		c.mu.RUnlock()
		return v, nil
	}
	c.mu.RUnlock()

	v, err, _ := c.sf.Do("url:"+url, func() (any, error) {
		c.mu.RLock()
		if v, ok := c.byURL[url]; ok {
			c.mu.RUnlock()
			return v, nil
		}
		c.mu.RUnlock()
		items, err := c.inner.ListFilesFromURL(ctx, url)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.byURL[url] = items
		c.mu.Unlock()
		return items, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]client.FileItem), nil
}
