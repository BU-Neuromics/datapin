package resolver

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
)

// countingLister records how many times each backing call is made.
type countingLister struct {
	mu       sync.Mutex
	nodeHits map[string]int
	urlHits  map[string]int
	// gate, when non-nil, blocks the backend call until the test closes it.
	gate chan struct{}
	// started is closed when the first backend call is entered, so a test can
	// deterministically know the singleflight leader is in-flight.
	started   chan struct{}
	startOnce sync.Once
}

func newCountingLister() *countingLister {
	return &countingLister{nodeHits: map[string]int{}, urlHits: map[string]int{}}
}

func (c *countingLister) ListFiles(_ context.Context, nodeID string) ([]client.FileItem, error) {
	if c.started != nil {
		c.startOnce.Do(func() { close(c.started) })
	}
	if c.gate != nil {
		<-c.gate
	}
	c.mu.Lock()
	c.nodeHits[nodeID]++
	c.mu.Unlock()
	return []client.FileItem{{ID: "n-" + nodeID}}, nil
}

func (c *countingLister) ListFilesFromURL(_ context.Context, url string) ([]client.FileItem, error) {
	c.mu.Lock()
	c.urlHits[url]++
	c.mu.Unlock()
	return []client.FileItem{{ID: "u-" + url}}, nil
}

func TestCachingLister_MemoizesRepeatCalls(t *testing.T) {
	inner := newCountingLister()
	c := NewCachingLister(inner)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := c.ListFiles(ctx, "abc12"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ListFilesFromURL(ctx, "https://x/folder1"); err != nil {
			t.Fatal(err)
		}
	}
	// A different key is fetched independently.
	if _, err := c.ListFiles(ctx, "zzz99"); err != nil {
		t.Fatal(err)
	}

	if inner.nodeHits["abc12"] != 1 {
		t.Errorf("ListFiles(abc12) hit backend %d times, want 1", inner.nodeHits["abc12"])
	}
	if inner.urlHits["https://x/folder1"] != 1 {
		t.Errorf("ListFilesFromURL hit backend %d times, want 1", inner.urlHits["https://x/folder1"])
	}
	if inner.nodeHits["zzz99"] != 1 {
		t.Errorf("distinct key should be fetched once, got %d", inner.nodeHits["zzz99"])
	}
}

// Concurrent identical requests collapse into a single backend call.
func TestCachingLister_DedupesConcurrent(t *testing.T) {
	inner := newCountingLister()
	inner.gate = make(chan struct{})
	inner.started = make(chan struct{})
	c := NewCachingLister(inner)

	var errs int64
	call := func() {
		if _, e := c.ListFiles(context.Background(), "abc12"); e != nil {
			atomic.AddInt64(&errs, 1)
		}
	}

	// Leader: enters the backend and blocks on the gate.
	go call()
	<-inner.started // leader is now in-flight, holding the singleflight key

	// Followers: launched while the leader is blocked, so they all join the
	// same in-flight call rather than starting new ones.
	const followers = 19
	var wg sync.WaitGroup
	wg.Add(followers)
	for i := 0; i < followers; i++ {
		go func() { defer wg.Done(); call() }()
	}

	close(inner.gate) // release the leader; everyone shares its result
	wg.Wait()

	if atomic.LoadInt64(&errs) != 0 {
		t.Fatalf("%d concurrent calls errored", errs)
	}
	if inner.nodeHits["abc12"] != 1 {
		t.Errorf("concurrent identical calls hit backend %d times, want 1", inner.nodeHits["abc12"])
	}
}
