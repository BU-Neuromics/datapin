package config

import (
	"fmt"

	"github.com/BU-Neuromics/datapin/internal/backend"
)

// RemoteCaps is what a remote told us about itself when it was probed
// (`datapin remote add` / `datapin remote probe`, issue #20, D54). It is
// stored under `[remotes.<name>.caps]` in config.toml so runtime never
// re-probes, and it is deliberately hand-editable: a field the instance's
// API does not expose stays absent here and the driver's own profile
// applies, while a value written by hand overrides the driver.
//
// Absent ≠ false. Every field's zero value means "not probed, not
// overridden" — which is why MultipartUpload is a *bool.
//
// Only the invenio driver consumes these today (it is the kind with
// per-instance variation); `remote probe` says so for the others rather
// than pretending a stored value has an effect.
type RemoteCaps struct {
	// ProbedAt is an RFC3339 timestamp, informational only: it tells a
	// user reading config.toml how stale these values are.
	ProbedAt string `toml:"probed_at,omitempty"`
	// MaxFilesPerRecord and MaxFileSize are 0 when the instance does not
	// expose a limit (the common case — see D55).
	MaxFilesPerRecord int   `toml:"max_files_per_record,omitempty"`
	MaxFileSize       int64 `toml:"max_file_size,omitempty"`
	// MultipartUpload is nil when the instance declares nothing about
	// transfer types.
	MultipartUpload *bool `toml:"multipart_upload,omitempty"`
	// ResourceTypes is the instance's resource-type vocabulary ids, used
	// by `datapin check` to validate a dataset's resource_type before
	// publish rejects it.
	ResourceTypes []string `toml:"resource_types,omitempty"`
}

// Apply overlays the probed/overridden values onto a driver's own caps
// profile. A nil receiver, or any zero field, keeps the driver's value.
func (c *RemoteCaps) Apply(base backend.Caps) backend.Caps {
	if c == nil {
		return base
	}
	out := base
	if c.MaxFilesPerRecord > 0 {
		out.MaxFilesPerRecord = c.MaxFilesPerRecord
	}
	if c.MaxFileSize > 0 {
		out.MaxFileSize = c.MaxFileSize
	}
	if c.MultipartUpload != nil {
		out.MultipartUpload = *c.MultipartUpload
	}
	return out
}

// IsEmpty reports whether nothing was probed or overridden — such caps are
// not written to config.toml at all.
func (c *RemoteCaps) IsEmpty() bool {
	return c == nil || (c.MaxFilesPerRecord == 0 && c.MaxFileSize == 0 &&
		c.MultipartUpload == nil && len(c.ResourceTypes) == 0)
}

// FromProbe converts a driver's probe result into the stored form. It
// returns nil when the probe learned nothing worth persisting.
func FromProbe(p backend.ProbeResult, probedAt string) *RemoteCaps {
	c := &RemoteCaps{
		ProbedAt:          probedAt,
		MaxFilesPerRecord: p.MaxFilesPerRecord,
		MaxFileSize:       p.MaxFileSize,
		MultipartUpload:   p.MultipartUpload,
		ResourceTypes:     p.ResourceTypes,
	}
	if c.IsEmpty() {
		return nil
	}
	return c
}

// SetRemoteCaps replaces a configured remote's caps table (a re-probe),
// leaving name/kind/url and the stored token untouched. Passing nil (or
// empty caps) removes the table, restoring the driver defaults.
func SetRemoteCaps(name string, caps *RemoteCaps) error {
	raw, path, err := readConfigRaw()
	if err != nil {
		return err
	}
	remotes, _ := raw["remotes"].(map[string]any)
	entry, ok := remotes[name].(map[string]any)
	if !ok {
		return fmt.Errorf("no remote named %q", name)
	}
	if caps.IsEmpty() {
		delete(entry, "caps")
	} else {
		entry["caps"] = capsTable(caps)
	}
	remotes[name] = entry
	raw["remotes"] = remotes
	return writeConfigRaw(raw, path)
}

// capsTable renders caps as the TOML table AddRemote/SetRemoteCaps write.
// Only non-zero fields appear, so config.toml never shows a limit datapin
// did not actually learn.
func capsTable(c *RemoteCaps) map[string]any {
	t := map[string]any{}
	if c.ProbedAt != "" {
		t["probed_at"] = c.ProbedAt
	}
	if c.MaxFilesPerRecord > 0 {
		t["max_files_per_record"] = c.MaxFilesPerRecord
	}
	if c.MaxFileSize > 0 {
		t["max_file_size"] = c.MaxFileSize
	}
	if c.MultipartUpload != nil {
		t["multipart_upload"] = *c.MultipartUpload
	}
	if len(c.ResourceTypes) > 0 {
		t["resource_types"] = c.ResourceTypes
	}
	return t
}
