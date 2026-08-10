package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

// fakeLister drives fetchRemoteState without a network.
type fakeLister struct {
	items []client.FileItem
	err   error
}

func (l fakeLister) ListFiles(ctx context.Context, nodeID string) ([]client.FileItem, error) {
	return l.items, l.err
}
func (l fakeLister) ListFilesFromURL(ctx context.Context, url string) ([]client.FileItem, error) {
	return l.items, l.err
}

// A path that is genuinely absent yields "no remote state" with no error — that
// is a legitimate classification input (NOT_PUSHED).
func TestFetchRemoteState_AbsentPathIsNotAnError(t *testing.T) {
	res := resolver.New(fakeLister{items: []client.FileItem{
		{Attributes: client.FileAttributes{Name: "other.csv", Kind: "file"}},
	}})
	entry := manifest.Entry{Local: "a.csv", Remote: "/a.csv"}

	item, versions, err := fetchRemoteState(context.Background(), res, nil, "abc12", entry, "abc", true)
	if err != nil {
		t.Fatalf("an absent remote path is not an error: %v", err)
	}
	if item != nil || versions != nil {
		t.Errorf("absent path should yield no state, got item=%v versions=%v", item, versions)
	}
}

// The bug: a throttled request used to be swallowed and reported as "not on the
// remote". For an unpinned entry that classifies NOT_PUSHED, and since #81 sync
// uploads NOT_PUSHED entries — so a transient 429 could mint a redundant
// version of a file that was already there. It must fail the scan instead.
func TestFetchRemoteState_ThrottledRequestFailsInsteadOfLookingAbsent(t *testing.T) {
	throttled := &client.APIError{StatusCode: 429, Message: "Request was throttled."}
	res := resolver.New(fakeLister{err: throttled})
	entry := manifest.Entry{Local: "a.csv", Remote: "/a.csv"}

	item, versions, err := fetchRemoteState(context.Background(), res, nil, "abc12", entry, "abc", true)
	if err == nil {
		t.Fatal("a throttled scan must fail, not silently classify the file as absent")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 {
		t.Errorf("the 429 must survive to the caller, got %v", err)
	}
	if !strings.Contains(err.Error(), entry.Remote) {
		t.Errorf("the error should name the path it was scanning, got %v", err)
	}
	if item != nil || versions != nil {
		t.Error("a failed scan must not return partial state")
	}
}

// The same distinction end-to-end: a 429 anywhere in a scan aborts it rather
// than producing a plan built on wrong states.
func TestScanEntries_PropagatesThrottling(t *testing.T) {
	throttled := &client.APIError{StatusCode: 429, Message: "Request was throttled."}
	res := resolver.New(fakeLister{err: throttled})
	m := &manifest.Manifest{
		Project: manifest.ProjectConfig{ID: "abc12"},
		Files: []manifest.Entry{
			{Local: "a.csv", Remote: "/a.csv"},
			{Local: "b.csv", Remote: "/b.csv"},
		},
	}

	_, err := scanEntries(context.Background(), m, t.TempDir(), res, nil, 2, false)
	if err == nil {
		t.Fatal("a throttled scan must fail the run")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 {
		t.Errorf("want the 429 to reach the caller, got %v", err)
	}
}
