package resolver_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

// A path that genuinely is not on the remote must be distinguishable from a
// transport failure. Before issue #86 both surfaced as an untyped error, so a
// throttled request was read as "this file does not exist" — and for an
// unpinned entry that means sync uploads a file that was already there.
type errLister struct{ err error }

func (l errLister) ListFiles(ctx context.Context, nodeID string) ([]client.FileItem, error) {
	return nil, l.err
}
func (l errLister) ListFilesFromURL(ctx context.Context, url string) ([]client.FileItem, error) {
	return nil, l.err
}

type okLister struct{ items []client.FileItem }

func (l okLister) ListFiles(ctx context.Context, nodeID string) ([]client.FileItem, error) {
	return l.items, nil
}
func (l okLister) ListFilesFromURL(ctx context.Context, url string) ([]client.FileItem, error) {
	return l.items, nil
}

func TestResolve_MissingPathIsTypedNotFound(t *testing.T) {
	r := resolver.New(okLister{items: []client.FileItem{
		{Attributes: client.FileAttributes{Name: "other.csv", Kind: "file"}},
	}})

	_, err := r.Resolve(context.Background(), "abc12", "/absent.csv")
	if err == nil {
		t.Fatal("resolving an absent path must fail")
	}
	if !resolver.IsNotFound(err) {
		t.Errorf("a genuinely absent path must report IsNotFound, got %v", err)
	}
}

// The case that mattered: a 429 from the API must NOT look like absence.
func TestResolve_APIErrorIsNotNotFound(t *testing.T) {
	throttled := &client.APIError{StatusCode: 429, Message: "Request was throttled."}
	r := resolver.New(errLister{err: throttled})

	_, err := r.Resolve(context.Background(), "abc12", "/data/file.csv")
	if err == nil {
		t.Fatal("a throttled listing must fail")
	}
	if resolver.IsNotFound(err) {
		t.Error("a 429 must not be reported as a missing path — that is what caused spurious uploads")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 {
		t.Errorf("the underlying APIError must survive unwrapping, got %v", err)
	}
}

func TestListDir_MissingPathIsTypedNotFound(t *testing.T) {
	r := resolver.New(okLister{items: []client.FileItem{
		{Attributes: client.FileAttributes{Name: "real", Kind: "folder"}},
	}})
	if _, err := r.ListDir(context.Background(), "abc12", "/nope"); !resolver.IsNotFound(err) {
		t.Errorf("listing an absent directory must report IsNotFound, got %v", err)
	}
}

func TestListDir_APIErrorIsNotNotFound(t *testing.T) {
	r := resolver.New(errLister{err: &client.APIError{StatusCode: 503, Message: "unavailable"}})
	if _, err := r.ListDir(context.Background(), "abc12", "/data"); resolver.IsNotFound(err) {
		t.Error("a 503 must not be reported as a missing path")
	}
}
