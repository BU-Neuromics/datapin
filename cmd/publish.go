package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
)

var (
	publishYes     bool
	publishForce   bool
	publishDryRun  bool
	publishReserve bool
)

var publishCmd = &cobra.Command{
	Use:   "publish [<slug>]",
	Short: "Publish a dataset to its archive remote, minting a DOI",
	Long: `Promote a dataset's current files to a published, immutable,
DOI-carrying record on its archive remote (Zenodo/InvenioRDM).

Publishing is PERMANENT and PUBLIC: a published version cannot be edited
or deleted, and its DOI resolves forever. Rehearse against a sandbox
remote (https://sandbox.zenodo.org) first.

The first publish creates the record; later publishes open a new version
draft, carry unchanged files over server-side (no re-upload), transfer
only changed content, and mint a new version DOI. The concept DOI stays
stable and always resolves to the latest version.

With no slug, every dataset with publishable changes is processed.
--reserve uploads everything and reserves the DOI but does NOT publish —
the DOI can go into a manuscript before the data is final; run publish
again (without --reserve) to make it live.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPublish(cmd.Context(), args)
	},
}

// publishFileJSON is one file row in the JSON result.
type publishFileJSON struct {
	Key    string `json:"key"`
	Action string `json:"action"`
	MD5    string `json:"md5,omitempty"`
}

// publishResultJSON is emitted per dataset under --output=json.
type publishResultJSON struct {
	Slug        string            `json:"slug"`
	State       string            `json:"state"`
	Record      string            `json:"record,omitempty"`
	Version     int               `json:"version,omitempty"`
	DOI         string            `json:"doi,omitempty"`
	ConceptDOI  string            `json:"concept_doi,omitempty"`
	ReservedDOI string            `json:"reserved_doi,omitempty"`
	Files       []publishFileJSON `json:"files,omitempty"`
	DryRun      bool              `json:"dry_run"`
}

func runPublish(ctx context.Context, args []string) error {
	manifestPath, repoRoot, err := manifest.FindManifest()
	if err != nil {
		return err
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return err
	}

	var targets []*manifest.Dataset
	if len(args) == 1 {
		ds := m.FindDataset(args[0])
		if ds == nil {
			return fmt.Errorf("no dataset with slug %q in the manifest", args[0])
		}
		targets = append(targets, ds)
	} else {
		for i := range m.Datasets {
			targets = append(targets, &m.Datasets[i])
		}
		if len(targets) == 0 {
			return fmt.Errorf("no [[datasets]] in the manifest — declare one, then: datapin publish <slug>")
		}
	}

	// JSON mode publishes are destructive-and-permanent: --force (or
	// --yes) is mandatory, mirroring `datapin rm` (no interactive prompt).
	if flagOutput == "json" && !publishYes && !publishForce && !publishDryRun && !publishReserve {
		return fmt.Errorf("publish in --output=json mode requires --yes (publishing is permanent)")
	}

	var results []publishResultJSON
	for _, ds := range targets {
		res, err := publishOne(ctx, m, manifestPath, repoRoot, ds)
		if err != nil {
			return fmt.Errorf("dataset %q: %w", ds.Slug, err)
		}
		results = append(results, res)
	}

	if flagOutput == "json" {
		return output.PrintJSON(os.Stdout, results)
	}
	return nil
}

// publishOne runs the full publish decision + transaction for one dataset.
func publishOne(ctx context.Context, m *manifest.Manifest, manifestPath, repoRoot string, ds *manifest.Dataset) (publishResultJSON, error) {
	zero := publishResultJSON{Slug: ds.Slug, DryRun: publishDryRun}

	if err := publishPreflight(ds); err != nil {
		return zero, err
	}

	localMD5, sizes, missing, err := datasetLocalState(repoRoot, ds)
	if err != nil {
		return zero, err
	}
	if len(missing) > 0 {
		return zero, fmt.Errorf("cannot publish with missing local files: %v", missing)
	}

	bk, remote, err := resolveArchive(ds, m)
	if err != nil {
		return zero, err
	}

	rec, remoteFiles, latestNum, err := remoteDatasetState(ctx, bk, ds)
	if err != nil {
		return zero, err
	}
	published := ds.Record != ""

	// Gate: someone published a version this manifest has not seen.
	if published && latestNum > ds.Version && !publishForce {
		return zero, fmt.Errorf(
			"the remote has version %d but the manifest pins version %d — pull the newer version first (datapin pull %s --latest) or --force to publish on top of it",
			latestNum, ds.Version, ds.Slug)
	}

	plan := planDataset(ds.Files, localMD5, remoteFiles)
	if published && !planHasWork(plan) {
		// Content already matches the remote latest: at most re-pin.
		if repinDataset(ds, rec, latestNum, localMD5) {
			if err := manifest.Save(m, manifestPath); err != nil {
				return zero, err
			}
			log.Infof("dataset %q: content already published as version %d — pins refreshed, nothing to publish", ds.Slug, latestNum)
		} else {
			log.Infof("dataset %q: in sync with version %d — nothing to publish", ds.Slug, latestNum)
		}
		zero.State = "IN_SYNC"
		zero.Record = ds.Record
		zero.Version = ds.Version
		zero.DOI = ds.VersionDOI
		zero.ConceptDOI = ds.ConceptDOI
		return zero, nil
	}

	printPublishPlan(ds, remote.Name, remote.URL, plan, sizes, published, latestNum, bk.Capabilities().Sandbox)

	if publishDryRun {
		zero.State = "DRY_RUN"
		zero.Files = planToJSON(plan)
		return zero, nil
	}
	if !publishYes && !publishForce {
		if !isInteractive() {
			return zero, fmt.Errorf("refusing to publish without --yes on a non-interactive run (publishing is permanent)")
		}
		if !confirm(fmt.Sprintf("Publish dataset %q%s? This is permanent", ds.Slug,
			map[bool]string{true: "", false: " and mints a DOI"}[publishReserve])) {
			return zero, fmt.Errorf("publish aborted")
		}
	}

	res, reservedDOI, newVersionNum, err := publishTransaction(ctx, bk, ds, plan, repoRoot, published, latestNum)
	if err != nil {
		return zero, err
	}

	if publishReserve {
		log.Infof("dataset %q: draft ready with reserved DOI %s — run 'datapin publish %s' to make it live", ds.Slug, reservedDOI, ds.Slug)
		zero.State = "RESERVED"
		zero.ReservedDOI = reservedDOI
		zero.Files = planToJSON(plan)
		return zero, nil
	}

	// Re-pin: the new version is the pinned baseline.
	conceptID := rec.ConceptID
	if got, err := bk.GetRecord(ctx, res.RecordID); err == nil {
		conceptID = got.ConceptID
	}
	ds.Record = string(res.RecordID)
	ds.Concept = conceptID
	ds.Version = newVersionNum
	ds.VersionDOI = res.DOI
	ds.ConceptDOI = res.ConceptDOI
	for i := range ds.Files {
		ds.Files[i].MD5 = localMD5[ds.Files[i].Local]
	}
	if err := manifest.Save(m, manifestPath); err != nil {
		return zero, fmt.Errorf("published as %s but updating the manifest failed: %w", res.DOI, err)
	}

	log.Infof("✓ published dataset %q as version %d", ds.Slug, newVersionNum)
	if flagOutput != "json" {
		printCitation(os.Stdout, ds, res)
	}

	zero.State = "PUBLISHED"
	zero.Record = ds.Record
	zero.Version = ds.Version
	zero.DOI = res.DOI
	zero.ConceptDOI = res.ConceptDOI
	zero.Files = planToJSON(plan)
	return zero, nil
}

// publishTransaction executes the plan: open a draft (create or new
// version), import previous files, apply per-key actions, refresh
// metadata, preflight, and publish (or reserve). Failure before publish
// discards the draft — except under --reserve, whose whole point is a
// surviving draft.
func publishTransaction(ctx context.Context, bk backend.Backend, ds *manifest.Dataset, plan []datasetPlanEntry, repoRoot string, published bool, latestNum int) (backend.PublishResult, string, int, error) {
	meta := datasetBackendMetadata(ds)

	var draft backend.DraftID
	var err error
	if published {
		draft, err = bk.NewVersion(ctx, backend.RecordID(ds.Record))
		if err != nil {
			return backend.PublishResult{}, "", 0, err
		}
		// files-import needs an empty draft. A draft left over from a
		// crashed run may already hold files — clear and start fresh so
		// the transaction is re-runnable.
		if err := bk.ImportPreviousFiles(ctx, draft); err != nil {
			existing, listErr := bk.ListDraftFiles(ctx, draft)
			if listErr != nil {
				return backend.PublishResult{}, "", 0, err
			}
			for _, f := range existing {
				if delErr := bk.DeleteDraftFile(ctx, draft, f.Key); delErr != nil {
					return backend.PublishResult{}, "", 0, fmt.Errorf("clearing stale draft file %s: %w", f.Key, delErr)
				}
			}
			if err := bk.ImportPreviousFiles(ctx, draft); err != nil {
				return backend.PublishResult{}, "", 0, fmt.Errorf("importing previous version's files: %w", err)
			}
		}
	} else {
		draft, err = bk.CreateDraft(ctx, meta)
		if err != nil {
			return backend.PublishResult{}, "", 0, err
		}
	}

	discard := func(cause error) error {
		if publishReserve {
			return cause
		}
		if derr := bk.Discard(ctx, draft); derr != nil {
			log.Warnf("could not discard draft %s after failure: %v — remove it in the web UI", draft, derr)
		}
		return cause
	}

	for _, e := range plan {
		switch e.Action {
		case actionKeep:
			continue
		case actionRemove, actionReplace:
			if err := bk.DeleteDraftFile(ctx, draft, e.Key); err != nil && !backend.IsNotFound(err) {
				return backend.PublishResult{}, "", 0, discard(fmt.Errorf("removing %s from draft: %w", e.Key, err))
			}
		}
		if e.Action == actionReplace || e.Action == actionUpload {
			if err := uploadPlanEntry(ctx, bk, draft, repoRoot, e); err != nil {
				return backend.PublishResult{}, "", 0, discard(err)
			}
		}
	}

	if err := bk.UpdateMetadata(ctx, draft, meta); err != nil {
		return backend.PublishResult{}, "", 0, discard(fmt.Errorf("updating metadata: %w", err))
	}

	// Preflight: pending entries (crashed uploads) block publish.
	files, err := bk.ListDraftFiles(ctx, draft)
	if err != nil {
		return backend.PublishResult{}, "", 0, discard(err)
	}
	for _, f := range files {
		if f.Pending {
			if err := bk.DeleteDraftFile(ctx, draft, f.Key); err != nil {
				return backend.PublishResult{}, "", 0, discard(fmt.Errorf("clearing pending file %s: %w", f.Key, err))
			}
			log.Warnf("cleared pending (uncommitted) draft file %s", f.Key)
		}
	}

	if publishReserve {
		doi, err := bk.ReserveDOI(ctx, draft)
		if err != nil {
			return backend.PublishResult{}, "", 0, err
		}
		return backend.PublishResult{}, doi, 0, nil
	}

	res, err := bk.Publish(ctx, draft)
	if err != nil {
		// The draft survives a failed publish action deliberately: bytes
		// are uploaded and the failure may be metadata-fixable.
		return backend.PublishResult{}, "", 0, err
	}
	return res, "", latestNum + 1, nil
}

func uploadPlanEntry(ctx context.Context, bk backend.Backend, draft backend.DraftID, repoRoot string, e datasetPlanEntry) error {
	p := filepath.Join(repoRoot, e.Local)
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	log.Infof("↑ uploading %s → %s (%s)", e.Local, e.Key, output.FormatSize(info.Size()))
	_, err = bk.UploadFile(ctx, draft, e.Key, f, info.Size(), backend.Checksum{Algo: "md5", Hex: e.LocalMD5})
	if err != nil {
		return fmt.Errorf("uploading %s: %w", e.Key, err)
	}
	return nil
}

// repinDataset refreshes stale pins when content already matches the
// remote latest. Reports whether anything changed.
func repinDataset(ds *manifest.Dataset, rec backend.Record, latestNum int, localMD5 map[string]string) bool {
	changed := false
	var latest backend.VersionInfo
	for _, v := range rec.Versions {
		if v.Index+1 == latestNum {
			latest = v
		}
	}
	if latest.ID != "" && ds.Record != string(latest.ID) {
		ds.Record = string(latest.ID)
		ds.VersionDOI = latest.DOI
		ds.Version = latestNum
		changed = true
	}
	for i := range ds.Files {
		if sum := localMD5[ds.Files[i].Local]; sum != "" && ds.Files[i].MD5 != sum {
			ds.Files[i].MD5 = sum
			changed = true
		}
	}
	return changed
}

// printPublishPlan renders the loud, DOI-consequence-explicit plan.
func printPublishPlan(ds *manifest.Dataset, remoteName, remoteURL string, plan []datasetPlanEntry, sizes map[string]int64, published bool, latestNum int, sandbox bool) {
	if flagOutput == "json" {
		return
	}
	w := os.Stderr
	fmt.Fprintf(w, "\nDataset %q → remote %q (%s)\n", ds.Slug, remoteName, remoteURL)
	if published {
		fmt.Fprintf(w, "  action: publish version %d (a NEW version DOI will be minted)\n", latestNum+1)
	} else {
		fmt.Fprintf(w, "  action: first publish (a concept DOI and version DOI will be minted)\n")
	}
	fmt.Fprintf(w, "  %s\n", output.Bold("PUBLIC AND PERMANENT: published versions cannot be edited or deleted."))
	if sandbox {
		fmt.Fprintf(w, "  (sandbox remote — DOIs will use the non-resolving 10.5072 test prefix)\n")
	}
	var total int64
	for _, e := range plan {
		switch e.Action {
		case actionKeep:
			fmt.Fprintf(w, "    = %-30s unchanged (carried over, no re-upload)\n", e.Key)
		case actionRemove:
			fmt.Fprintf(w, "    - %-30s removed from this version\n", e.Key)
		default:
			sz := sizes[e.Local]
			total += sz
			verb := map[datasetAction]string{actionUpload: "new", actionReplace: "changed"}[e.Action]
			fmt.Fprintf(w, "    ↑ %-30s %s, %s, md5 %s\n", e.Key, verb, output.FormatSize(sz), e.LocalMD5)
		}
	}
	fmt.Fprintf(w, "  transfer: %s\n\n", output.FormatSize(total))
}

// printCitation prints the DOI and a paste-ready citation — the loud,
// useful end of every publish (plan §5).
func printCitation(w *os.File, ds *manifest.Dataset, res backend.PublishResult) {
	year := datasetBackendMetadata(ds).PublicationDate[:4]
	var authors []string
	for _, c := range ds.Metadata.Creators {
		authors = append(authors, c.Name)
	}
	fmt.Fprintf(w, "DOI: https://doi.org/%s\n", res.DOI)
	if res.ConceptDOI != "" {
		fmt.Fprintf(w, "Concept DOI (always latest): https://doi.org/%s\n", res.ConceptDOI)
	}
	fmt.Fprintf(w, "Cite as: %s (%s). %s (Version %d) [Data set]. https://doi.org/%s\n",
		joinAuthors(authors), year, ds.Metadata.Title, ds.Version, res.DOI)
}

func joinAuthors(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		out := ""
		for i, n := range names {
			switch {
			case i == 0:
				out = n
			case i == len(names)-1:
				out += " & " + n
			default:
				out += "; " + n
			}
		}
		return out
	}
}

func planToJSON(plan []datasetPlanEntry) []publishFileJSON {
	out := make([]publishFileJSON, 0, len(plan))
	for _, e := range plan {
		out = append(out, publishFileJSON{Key: e.Key, Action: string(e.Action), MD5: e.LocalMD5})
	}
	return out
}

func init() {
	publishCmd.Flags().BoolVar(&publishYes, "yes", false, "Skip the confirmation prompt")
	publishCmd.Flags().BoolVar(&publishForce, "force", false, "Publish even when the remote has a newer version than the manifest pin")
	publishCmd.Flags().BoolVar(&publishDryRun, "dry-run", false, "Show the plan without publishing")
	publishCmd.Flags().BoolVar(&publishReserve, "reserve", false, "Upload and reserve the DOI but do not publish (draft survives)")
	rootCmd.AddCommand(publishCmd)
}
