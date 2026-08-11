package output

// This file defines the structured result types emitted by commands when
// --output=json is used. Keeping them in one place makes the JSON contract
// that scripting clients depend on explicit and testable.

// OpenResult is emitted by `datapin open --output=json`.
type OpenResult struct {
	URL string `json:"url"`
}

// RemoveResult is emitted by `datapin rm --output=json`.
type RemoveResult struct {
	Node   string `json:"node"`
	Path   string `json:"path"`
	Kind   string `json:"kind"` // "file" or "folder"
	DryRun bool   `json:"dry_run"`
}

// TransferItem describes one file moved by pull or push.
type TransferItem struct {
	Path   string `json:"path"`
	Size   int64  `json:"size,omitempty"`
	Action string `json:"action,omitempty"` // push: upload|overwrite|rename|skip
}

// PullResult is emitted by `datapin pull --output=json`.
type PullResult struct {
	Downloaded []TransferItem `json:"downloaded"`
	DryRun     bool           `json:"dry_run"`
}

// NewPullResult returns a PullResult with a non-nil slice so it serialises as
// [] rather than null when empty.
func NewPullResult(dryRun bool) *PullResult {
	return &PullResult{Downloaded: []TransferItem{}, DryRun: dryRun}
}

// Add appends a downloaded file to the result.
func (r *PullResult) Add(path string, size int64) {
	r.Downloaded = append(r.Downloaded, TransferItem{Path: path, Size: size})
}

// PushResult is emitted by `datapin push --output=json`.
type PushResult struct {
	Uploaded []TransferItem `json:"uploaded"`
	DryRun   bool           `json:"dry_run"`
}

// NewPushResult returns a PushResult with a non-nil slice so it serialises as
// [] rather than null when empty.
func NewPushResult(dryRun bool) *PushResult {
	return &PushResult{Uploaded: []TransferItem{}, DryRun: dryRun}
}

// Add appends an uploaded file (with the action taken) to the result.
func (r *PushResult) Add(path, action string) {
	r.Uploaded = append(r.Uploaded, TransferItem{Path: path, Action: action})
}

// AddEntry describes one file staged by `datapin add`.
type AddEntry struct {
	Local   string `json:"local"`
	Remote  string `json:"remote"`
	Project string `json:"project"`
	Version int    `json:"version"`
	MD5     string `json:"md5"`
}

// AddResult is emitted by `datapin add --output=json`.
type AddResult struct {
	Entries         []AddEntry `json:"entries"`
	ManifestCreated bool       `json:"manifest_created"`
}

// StatusItem describes one manifest entry's state, emitted by `datapin status --output=json`.
type StatusItem struct {
	Path                string `json:"path"`
	Kind                string `json:"kind"` // "file" or "wiki"
	State               string `json:"state"`
	DeclaredVersion     int    `json:"declared_version"`
	RemoteLatestVersion int    `json:"remote_latest_version,omitempty"`
}

// SyncItem describes the action taken for one manifest entry, emitted by `datapin sync --output=json`.
type SyncItem struct {
	Path                string `json:"path"`
	Kind                string `json:"kind"` // "file" or "wiki"
	State               string `json:"state"`
	DeclaredVersion     int    `json:"declared_version"`
	RemoteLatestVersion int    `json:"remote_latest_version,omitempty"`
	ActionTaken         string `json:"action_taken"`
}

// VersionItem describes one version in the versions list.
type VersionItem struct {
	Version     int    `json:"version"`
	DateCreated string `json:"date_created"`
	Size        int64  `json:"size"`
	Contributor string `json:"contributor"`
}

// VersionsResult is emitted by `datapin versions --output=json`.
type VersionsResult struct {
	Versions []VersionItem `json:"versions"`
}

// NewVersionsResult returns a VersionsResult with a non-nil slice so it
// serialises as [] rather than null when empty.
func NewVersionsResult() *VersionsResult {
	return &VersionsResult{Versions: []VersionItem{}}
}

// MvResult is emitted by `datapin mv --output=json`.
type MvResult struct {
	Src    string `json:"src"`
	Dest   string `json:"dest"`
	DryRun bool   `json:"dry_run"`
}

// CpResult is emitted by `datapin cp --output=json`.
type CpResult struct {
	Src    string `json:"src"`
	Dest   string `json:"dest"`
	DryRun bool   `json:"dry_run"`
}

// InitResult is emitted by `datapin init --output=json`.
type InitResult struct {
	Project string `json:"project"`
	Created bool   `json:"created"`
}

// WikiListItem describes one wiki page, emitted by `datapin wiki ls --output=json`.
type WikiListItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      int    `json:"version"`
	Size         int64  `json:"size"`
	DateModified string `json:"date_modified"`
}

// WikiGetResult is emitted by `datapin wiki get --output=json`.
type WikiGetResult struct {
	Project string `json:"project"`
	Page    string `json:"page"`
	Version int    `json:"version"`
	Size    int64  `json:"size"`
	Content string `json:"content"`
}

// WikiPushResult is emitted by `datapin wiki push --output=json`.
type WikiPushResult struct {
	Project string `json:"project"`
	Page    string `json:"page"`
	Action  string `json:"action"` // create | update | skip
	Version int    `json:"version"`
	DryRun  bool   `json:"dry_run"`
}

// WikiRemoveResult is emitted by `datapin wiki rm --output=json`.
type WikiRemoveResult struct {
	Node   string `json:"node"`
	Page   string `json:"page"`
	DryRun bool   `json:"dry_run"`
}

// WikiMvResult is emitted by `datapin wiki mv --output=json`.
type WikiMvResult struct {
	Node   string `json:"node"`
	From   string `json:"from"`
	To     string `json:"to"`
	DryRun bool   `json:"dry_run"`
}

// WikiAddEntry describes one wiki page staged by `datapin wiki add`.
type WikiAddEntry struct {
	Local   string `json:"local"`
	Page    string `json:"page"`
	Project string `json:"project"`
	Version int    `json:"version"`
	MD5     string `json:"md5"`
}

// WikiAddResult is emitted by `datapin wiki add --output=json`.
type WikiAddResult struct {
	Entries         []WikiAddEntry `json:"entries"`
	ManifestCreated bool           `json:"manifest_created"`
}

// MkdirResult is emitted by `datapin mkdir --output=json`.
type MkdirResult struct {
	Path    string `json:"path"`
	Created bool   `json:"created"`
	DryRun  bool   `json:"dry_run"`
}

// RemoteAddResult is emitted by `datapin remote add --output=json`.
type RemoteAddResult struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	Sandbox     bool   `json:"sandbox"`
	TokenStored bool   `json:"token_stored"`
}

// RemoteListEntry is one row of `datapin remote ls --output=json`.
type RemoteListEntry struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	HasToken bool   `json:"has_token"`
}

// RemoteRmResult is emitted by `datapin remote rm --output=json`.
type RemoteRmResult struct {
	Name string `json:"name"`
}

// MigrateWikiPage is one exported wiki page in a MigrateResult.
type MigrateWikiPage struct {
	Page  string `json:"page"`
	Local string `json:"local"`
}

// MigrateDataset is one scaffolded dataset in a MigrateResult.
type MigrateDataset struct {
	Slug  string `json:"slug"`
	Files int    `json:"files"`
}

// MigrateResult is emitted by `datapin migrate --output=json`.
type MigrateResult struct {
	Mode              string            `json:"mode"`   // "guid" or "manifest"
	Source            string            `json:"source"` // the OSF GUID migrated from
	Dest              string            `json:"dest,omitempty"`
	Manifest          string            `json:"manifest"`
	Downloaded        []TransferItem    `json:"downloaded"`
	Skipped           []TransferItem    `json:"skipped"` // already local and MD5-identical
	WikiPages         []MigrateWikiPage `json:"wiki_pages"`
	Datasets          []MigrateDataset  `json:"datasets"`
	ComponentsSkipped []string          `json:"components_skipped,omitempty"`
	TODOs             []string          `json:"todos"`
	DryRun            bool              `json:"dry_run"`
}

// NewMigrateResult returns a MigrateResult with non-nil slices so they
// serialise as [] rather than null when empty.
func NewMigrateResult(mode, source string, dryRun bool) *MigrateResult {
	return &MigrateResult{
		Mode:       mode,
		Source:     source,
		DryRun:     dryRun,
		Downloaded: []TransferItem{},
		Skipped:    []TransferItem{},
		WikiPages:  []MigrateWikiPage{},
		Datasets:   []MigrateDataset{},
		TODOs:      []string{},
	}
}
