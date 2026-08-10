//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BU-Neuromics/datapin/internal/client"
)

// uniqueWikiPage returns a namespaced page name and registers cleanup that
// deletes it after the test (best effort), so live runs leave no residue.
func uniqueWikiPage(e *liveEnv) string {
	page := fmt.Sprintf("gosf-ci-%d-%d", time.Now().UnixNano(), os.Getpid())
	e.t.Cleanup(func() {
		e.runEventually("wiki", "rm", e.project+":"+page, "--yes", "--quiet")
	})
	return page
}

// TestLive_WikiCanonicalRoundTrip is the load-bearing live check on content
// fidelity. OSF does NOT store wiki content byte-for-byte: it normalizes line
// endings to LF and trims surrounding whitespace. gosf therefore guarantees a
// *canonical* round trip (client.CanonicalizeWikiContent), not a byte-exact one.
// This pushes content with CRLF, interior trailing spaces, and a trailing
// newline, then verifies OSF returns exactly the canonical form — and that a
// re-push of the same file is idempotent despite that difference (the
// regression that motivated the canonical-comparison fix).
func TestLive_WikiCanonicalRoundTrip(t *testing.T) {
	e := requireLive(t)
	page := uniqueWikiPage(e)

	local := "# Heading\r\nCRLF line\nLF line\n\ntrailing spaces   \nfinal newline\n"
	want := string(client.CanonicalizeWikiContent([]byte(local)))
	e.writeFile("page.md", local)

	if _, stderr, code := e.runEventually("wiki", "push", "page.md", e.project+":"+page, "--quiet"); code != 0 {
		t.Fatalf("wiki push exit %d; stderr=%s", code, stderr)
	}

	var got string
	for i := 0; i < 40; i++ {
		out, _, code := e.run("wiki", "get", e.project+":"+page)
		if code == 0 {
			got = out
			break
		}
	}
	if got != want {
		t.Errorf("wiki content after round trip:\n got  %q\n want %q (canonical)", got, want)
	}

	// Interior trailing spaces must survive (only CRLF and surrounding
	// whitespace are normalized) — guards against over-aggressive canonicalization.
	if !strings.Contains(got, "trailing spaces   \n") {
		t.Errorf("interior trailing spaces were not preserved: %q", got)
	}

	// Re-pushing the same local file is idempotent: canonical(local) already
	// equals what OSF stored, so no redundant version is minted.
	out, stderr, code := e.runEventually("wiki", "push", "page.md", e.project+":"+page, "--output=json")
	if code != 0 {
		t.Fatalf("idempotent re-push exit %d; stderr=%s", code, stderr)
	}
	var r struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("push json: %v\n%s", err, out)
	}
	if r.Action != "skip" {
		t.Errorf("re-push action = %q, want skip (canonical content unchanged)", r.Action)
	}
}

// TestLive_WikiCreateVersionRenameDelete exercises the full write lifecycle.
func TestLive_WikiCreateVersionRenameDelete(t *testing.T) {
	e := requireLive(t)
	page := uniqueWikiPage(e)

	// Create.
	e.writeFile("p.md", "first\n")
	if _, stderr, code := e.runEventually("wiki", "push", "p.md", e.project+":"+page, "--quiet"); code != 0 {
		t.Fatalf("create exit %d; stderr=%s", code, stderr)
	}

	// New version.
	e.writeFile("p.md", "second\n")
	if _, stderr, code := e.runEventually("wiki", "push", "p.md", e.project+":"+page, "--quiet"); code != 0 {
		t.Fatalf("new version exit %d; stderr=%s", code, stderr)
	}

	// Versions reflect two entries.
	var nums []int
	for i := 0; i < 40; i++ {
		out, _, code := e.run("wiki", "versions", e.project+":"+page, "--output=json")
		if code != 0 {
			continue
		}
		var vr struct {
			Versions []struct {
				Version int `json:"version"`
			} `json:"versions"`
		}
		if json.Unmarshal([]byte(out), &vr) != nil {
			continue
		}
		nums = nil
		for _, v := range vr.Versions {
			nums = append(nums, v.Version)
		}
		if len(nums) >= 2 {
			break
		}
	}
	if len(nums) < 2 || nums[0] != 2 {
		t.Fatalf("expected >=2 versions newest-first, got %v", nums)
	}

	// Rename, then register cleanup for the new name too.
	renamed := page + "-renamed"
	e.t.Cleanup(func() {
		e.runEventually("wiki", "rm", e.project+":"+renamed, "--yes", "--quiet")
	})
	if _, stderr, code := e.runEventually("wiki", "mv", e.project+":"+page, renamed, "--quiet"); code != 0 {
		t.Fatalf("rename exit %d; stderr=%s", code, stderr)
	}

	// Delete.
	if _, stderr, code := e.runEventually("wiki", "rm", e.project+":"+renamed, "--yes", "--quiet"); code != 0 {
		t.Fatalf("delete exit %d; stderr=%s", code, stderr)
	}
}

// TestLive_WikiIdempotentPush confirms a re-push of identical content mints no
// new version against real OSF.
func TestLive_WikiIdempotentPush(t *testing.T) {
	e := requireLive(t)
	page := uniqueWikiPage(e)

	e.writeFile("p.md", "stable content\n")
	if _, stderr, code := e.runEventually("wiki", "push", "p.md", e.project+":"+page, "--quiet"); code != 0 {
		t.Fatalf("create exit %d; stderr=%s", code, stderr)
	}

	out, stderr, code := e.runEventually("wiki", "push", "p.md", e.project+":"+page, "--output=json")
	if code != 0 {
		t.Fatalf("idempotent push exit %d; stderr=%s", code, stderr)
	}
	var r struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("push json: %v\n%s", err, out)
	}
	if r.Action != "skip" {
		t.Errorf("identical re-push action = %q, want skip", r.Action)
	}
}
