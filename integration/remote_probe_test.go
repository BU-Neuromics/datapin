//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configTOML reads the env's datapin config.toml.
func (e *invenioEnv) configTOML(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.dir, ".config", "datapin", "config.toml"))
	if err != nil {
		t.Fatalf("reading config.toml: %v", err)
	}
	return string(data)
}

// `remote add` probes the instance and persists what it declared, so no
// later command has to re-probe (issue #20).
func TestRemoteAdd_PersistsProbedCaps(t *testing.T) {
	e := newInvenioEnv(t)
	e.srv.ResourceTypes = []string{"dataset", "image-photo", "software"}

	stdout, stderr, code := e.run("remote", "add", e.srv.URL(),
		"--name", "inst", "--token-value", invenioTestToken, "--no-keychain", "--output=json")
	if code != 0 {
		t.Fatalf("remote add failed (%d): %s%s", code, stdout, stderr)
	}
	var res struct {
		Name string `json:"name"`
		Caps *struct {
			ResourceTypes     []string `json:"resource_types"`
			MaxFilesPerRecord int      `json:"max_files_per_record"`
			MaxFileSize       int64    `json:"max_file_size"`
		} `json:"caps"`
		ProbeNotes []string `json:"probe_notes"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("remote add JSON: %v\n%s", err, stdout)
	}
	if res.Caps == nil || len(res.Caps.ResourceTypes) != 3 {
		t.Fatalf("probed caps missing from JSON: %s", stdout)
	}
	// The API exposes no limits, so the JSON must not claim any.
	if res.Caps.MaxFilesPerRecord != 0 || res.Caps.MaxFileSize != 0 {
		t.Errorf("probe invented limits: %+v", res.Caps)
	}
	if len(res.ProbeNotes) == 0 {
		t.Error("probe notes should explain what was defaulted")
	}

	cfg := e.configTOML(t)
	if !strings.Contains(cfg, "image-photo") {
		t.Errorf("probed vocabulary not persisted:\n%s", cfg)
	}
	if strings.Contains(cfg, "max_files_per_record") {
		t.Errorf("config.toml records a limit the API never stated:\n%s", cfg)
	}
}

// --no-verify skips the probe entirely: no caps stored, driver defaults
// apply, and nothing about the instance is claimed.
func TestRemoteAdd_NoVerifyStoresNoCaps(t *testing.T) {
	e := newInvenioEnv(t)
	if _, stderr, code := e.run("remote", "add", e.srv.URL(), "--name", "inst", "--no-verify"); code != 0 {
		t.Fatalf("remote add --no-verify failed (%d): %s", code, stderr)
	}
	if cfg := e.configTOML(t); strings.Contains(cfg, "caps") {
		t.Errorf("--no-verify stored caps:\n%s", cfg)
	}
	// With no probed vocabulary, an odd resource_type is nobody's business.
	e.setupDatasetNoRemote(t, `resource_type = "not-a-real-type"`)
	stdout, _, code := e.run("check", "--output=json")
	if code != 0 {
		t.Fatalf("check must not fail on an unprobed remote (%d):\n%s", code, stdout)
	}
	if strings.Contains(stdout, "resource-type") {
		t.Errorf("check invented a vocabulary for an unprobed remote:\n%s", stdout)
	}
}

// The probed vocabulary is what `datapin check` validates resource_type
// against — publishing an id the instance does not offer would be rejected.
func TestCheck_ResourceTypeAgainstProbedVocabulary(t *testing.T) {
	e := newInvenioEnv(t)
	e.srv.ResourceTypes = []string{"dataset", "software"}
	if _, stderr, code := e.run("remote", "add", e.srv.URL(),
		"--name", "sandbox", "--token-value", invenioTestToken, "--no-keychain"); code != 0 {
		t.Fatalf("remote add: %s", stderr)
	}
	e.setupDatasetNoRemote(t, `resource_type = "dataste"`)

	stdout, _, code := e.run("check", "--output=json")
	if code != 1 {
		t.Fatalf("check with an off-vocabulary resource_type must exit 1, got %d:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "resource_type") || !strings.Contains(stdout, "software") {
		t.Errorf("check should name the field and the instance vocabulary:\n%s", stdout)
	}

	// A vocabulary id passes.
	e.setupDatasetNoRemote(t, `resource_type = "software"`)
	if stdout, _, code := e.run("check", "--output=json"); code != 0 {
		t.Fatalf("check with a vocabulary id must pass, got %d:\n%s", code, stdout)
	}
}

// Stored caps are what runtime uses — including hand-written overrides, the
// escape hatch for a limit the API does not expose.
func TestCheck_HonorsStoredCapsOverride(t *testing.T) {
	e := newInvenioEnv(t)
	if _, stderr, code := e.run("remote", "add", e.srv.URL(),
		"--name", "sandbox", "--token-value", invenioTestToken, "--no-keychain"); code != 0 {
		t.Fatalf("remote add: %s", stderr)
	}
	e.setupDatasetNoRemote(t, "")

	// Two files in the dataset; a hand-edited cap of 1 must be honored.
	// `remote add` already wrote a caps table (the probed vocabulary), so
	// the override goes inside it — as a user editing config.toml would.
	p := filepath.Join(e.dir, ".config", "datapin", "config.toml")
	cfg := e.configTOML(t)
	const header = "[remotes.sandbox.caps]"
	if strings.Contains(cfg, header) {
		cfg = strings.Replace(cfg, header, header+"\nmax_files_per_record = 1", 1)
	} else {
		cfg += "\n" + header + "\nmax_files_per_record = 1\n"
	}
	if err := os.WriteFile(p, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := e.run("check", "--output=json")
	if code != 1 {
		t.Fatalf("check must honor the configured file cap, got %d:\n%s%s\nconfig:\n%s", code, stdout, stderr, cfg)
	}
	if !strings.Contains(stdout, "record cap of 1") {
		t.Errorf("expected the configured cap in the message:\n%s\nconfig:\n%s", stdout, cfg)
	}
}

// `datapin remote probe <name>` re-probes an existing remote in place, the
// way a vocabulary that grew after `remote add` is picked up.
func TestRemoteProbe_RefreshesStoredCaps(t *testing.T) {
	e := newInvenioEnv(t)
	if _, stderr, code := e.run("remote", "add", e.srv.URL(), "--name", "inst", "--no-verify"); code != 0 {
		t.Fatalf("remote add --no-verify: %s", stderr)
	}
	e.srv.ResourceTypes = []string{"dataset", "workflow"}

	stdout, stderr, code := e.run("remote", "probe", "inst", "--output=json")
	if code != 0 {
		t.Fatalf("remote probe failed (%d): %s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "workflow") {
		t.Errorf("probe result should carry the vocabulary:\n%s", stdout)
	}
	if cfg := e.configTOML(t); !strings.Contains(cfg, "workflow") {
		t.Errorf("probe did not persist the refreshed vocabulary:\n%s", cfg)
	}
	// A remote that is not configured, and an instance that does not answer
	// like InvenioRDM, both fail loudly.
	if _, _, code := e.run("remote", "probe", "nope"); code == 0 {
		t.Error("probing an unknown remote must fail")
	}
	e.srv.VocabularyStatus = 404
	if _, stderr, code := e.run("remote", "probe", "inst"); code == 0 {
		t.Errorf("probing a non-InvenioRDM host must fail: %s", stderr)
	}
	// The failed re-probe must not have clobbered the stored caps.
	if cfg := e.configTOML(t); !strings.Contains(cfg, "workflow") {
		t.Errorf("failed probe dropped the previous caps:\n%s", cfg)
	}
}

// An instance whose vocabulary endpoint does not answer is not InvenioRDM:
// `remote add` refuses and points at --no-verify.
func TestRemoteAdd_UnprobeableInstanceRefused(t *testing.T) {
	e := newInvenioEnv(t)
	e.srv.VocabularyStatus = 404
	_, stderr, code := e.run("remote", "add", e.srv.URL(), "--name", "inst")
	if code == 0 {
		t.Fatal("remote add against a non-InvenioRDM host must fail")
	}
	if !strings.Contains(stderr, "no-verify") {
		t.Errorf("error should point at --no-verify: %s", stderr)
	}
}
