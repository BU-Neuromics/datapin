// Package backend defines the adapter interface archive repositories
// implement (plan §4.2). File addresses are flat keys that may contain "/";
// hierarchical backends map keys onto real paths. Workspace backends (OSF
// today) are not yet behind this interface — see docs/decisions.md D12.
package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DraftID identifies an open draft on a backend.
type DraftID string

// RecordID identifies a published record. For InvenioRDM this is the
// per-version record id; the concept (parent) id lives on Record.
type RecordID string

// Checksum is an algorithm-qualified digest, e.g. {Algo: "md5", Hex: "…"}.
type Checksum struct {
	Algo string
	Hex  string
}

// String renders the backend wire form, "md5:<hex>".
func (c Checksum) String() string { return c.Algo + ":" + c.Hex }

// ParseChecksum parses the "algo:hex" wire form used by InvenioRDM
// (e.g. "md5:9695a27bc7b9b0dc3c12b4dd4af31c82").
func ParseChecksum(s string) (Checksum, error) {
	algo, hex, ok := strings.Cut(s, ":")
	if !ok || algo == "" || hex == "" {
		return Checksum{}, fmt.Errorf("malformed checksum %q: want \"algo:hex\"", s)
	}
	return Checksum{Algo: strings.ToLower(algo), Hex: strings.ToLower(hex)}, nil
}

// Creator is one dataset author. Name is "Family, Given" display form;
// ORCID, when present, is the bare identifier (not a URL).
type Creator struct {
	FamilyName  string
	GivenName   string
	ORCID       string
	Affiliation string
}

// Metadata is the DataCite-floor metadata a draft carries (D16 — the fuller
// internal/meta model serializes into this).
type Metadata struct {
	Title           string
	Description     string
	PublicationDate string // YYYY-MM-DD
	Publisher       string
	License         string // SPDX id, e.g. "CC0-1.0"
	Keywords        []string
	ResourceType    string // backend vocabulary id, e.g. "dataset"
	// ContactEmail is the dataset's point-of-contact e-mail. Dataverse
	// requires it (datasetContactEmail, live-verified); other backends
	// ignore it.
	ContactEmail string
	Creators     []Creator
	Version      string // optional human-readable version label
}

// FileInfo describes one file on a draft or published record.
type FileInfo struct {
	Key      string
	Size     int64
	Checksum Checksum
	Pending  bool // registered but not committed — blocks publish
}

// VersionInfo is one entry in a record's version chain, oldest first.
type VersionInfo struct {
	ID         RecordID
	Index      int // 0-based position in the chain
	DOI        string
	IsLatest   bool
	IsDraft    bool
	Created    string // RFC3339, as reported by the backend
	FilesCount int
}

// Record is a published record (or an unpublished concept with a draft).
type Record struct {
	ID         RecordID
	ConceptID  string // parent/concept identifier grouping all versions
	DOI        string // version DOI, "" if none minted
	ConceptDOI string // concept DOI resolving to latest, "" if none
	Title      string
	Published  bool
	Versions   []VersionInfo
}

// PublishResult reports a completed publish action.
type PublishResult struct {
	RecordID   RecordID
	DOI        string // version DOI ("10.5281/zenodo.x"); "" if none minted
	ConceptDOI string
	Pending    bool // curation-gated backends: submitted, not yet public
}

// ValidationError is a structured 400 from the backend, preserving the
// per-field messages (e.g. publish without metadata.publisher).
type ValidationError struct {
	Message string
	Fields  map[string][]string
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return e.Message
	}
	parts := make([]string, 0, len(e.Fields))
	for f, msgs := range e.Fields {
		parts = append(parts, f+": "+strings.Join(msgs, "; "))
	}
	// Sort-free join keeps this simple; callers wanting order use Fields.
	return e.Message + " (" + strings.Join(parts, " / ") + ")"
}

// NotFoundError distinguishes "the record/draft is not there" from transport
// failure — the same rule as resolver.NotFoundError ("throttling is never
// mistaken for absence").
type NotFoundError struct {
	What string
}

func (e *NotFoundError) Error() string { return e.What + " not found" }

// IsNotFound reports whether err is a NotFoundError.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

// Caps declare what a configured remote can do. They are probed or
// configured per remote, not hardcoded per backend type (plan §4.2).
type Caps struct {
	MintsDOI bool
	// PerVersionDOI: each published version gets its own DOI (Zenodo,
	// Figshare .vN). False when one DOI covers all versions (Dataverse).
	PerVersionDOI     bool
	ReserveDOI        bool
	PIDKind           string // "doi" | "swhid" | "none"
	SyncPublish       bool   // false: curation-gated (Dryad-style)
	MutablePublished  bool   // true only for workspace-style backends
	ImportsPrevious   bool   // InvenioRDM files-import
	MultipartUpload   bool
	MaxFileSize       int64 // 0 = unknown/unlimited
	MaxFilesPerRecord int   // 0 = unknown/unlimited
	ChecksumAlgo      string
	Sandbox           bool // test instance minting non-resolving DOIs (10.5072)
}

// Backend is the least-common-denominator surface every archive repository
// implements (plan §4.2).
type Backend interface {
	Capabilities() Caps

	// Draft lifecycle
	CreateDraft(ctx context.Context, meta Metadata) (DraftID, error)
	UpdateMetadata(ctx context.Context, id DraftID, meta Metadata) error
	UploadFile(ctx context.Context, id DraftID, key string, r io.Reader, size int64, sum Checksum) (FileInfo, error)
	DeleteDraftFile(ctx context.Context, id DraftID, key string) error
	ListDraftFiles(ctx context.Context, id DraftID) ([]FileInfo, error)
	ImportPreviousFiles(ctx context.Context, id DraftID) error
	ReserveDOI(ctx context.Context, id DraftID) (string, error)
	Publish(ctx context.Context, id DraftID) (PublishResult, error)
	Discard(ctx context.Context, id DraftID) error

	// Published records
	NewVersion(ctx context.Context, rec RecordID) (DraftID, error)
	GetRecord(ctx context.Context, rec RecordID) (Record, error)
	ListFiles(ctx context.Context, rec RecordID) ([]FileInfo, error)
	DownloadFile(ctx context.Context, rec RecordID, key string, w io.Writer) error
}
