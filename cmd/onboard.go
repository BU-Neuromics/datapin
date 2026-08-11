package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/gitutil"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/meta"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/picker"
)

var (
	onboardProject    string
	onboardRemoteBase string
	onboardOSF        bool
)

var onboardCmd = &cobra.Command{
	Use:   "onboard",
	Short: "Guided setup: configure an archive remote and describe your first dataset",
	Long: `Walk through datapin setup interactively: pick the repository your data
will publish to (Zenodo sandbox first — rehearse there, the DOIs are fake),
group local files into a dataset, and fill in the metadata a published
record needs (title, creators with ORCIDs, an explicit license choice).

onboard is resumable — it detects what is already configured (remote,
manifest, datasets) and starts at the first missing piece, so it is safe
to re-run. It stops after writing .datapin/datapin.toml; run
'datapin check <slug>' to lint the metadata and 'datapin publish <slug>'
when you are ready to mint a DOI.

For mutable intermediate results that need versioned sync but no DOI, add
a workspace remote instead: datapin remote add <url> --kind dir|s3|sftp.

--osf runs the legacy OSF workspace wizard (deprecated: OSF is sunsetting;
see 'datapin migrate' to move existing OSF projects off the platform).

Requires an interactive terminal. For scripting, use 'datapin remote add'
and edit .datapin/datapin.toml directly.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runOnboard,
}

// prompter abstracts the interactive prompts so the collect* helpers are
// unit-testable with scripted answers.
type prompter struct {
	line func(prompt, def string) string
	yes  func(prompt string) bool
}

func runOnboard(cmd *cobra.Command, args []string) error {
	if flagOutput == "json" {
		return errors.New("onboard is interactive and unavailable with --output=json; use datapin remote add and edit .datapin/datapin.toml directly")
	}
	if !isInteractive() {
		return errors.New("onboard needs an interactive terminal; use datapin remote add and edit .datapin/datapin.toml for scripting")
	}
	if err := checkLegacyOnboardFlags(onboardOSF, onboardProject, onboardRemoteBase); err != nil {
		return err
	}
	if onboardOSF {
		log.Warnf("--osf runs the legacy OSF workspace flow, which is deprecated (OSF is sunsetting — see 'datapin migrate')")
		return runOnboardOSF(cmd.Context())
	}
	return runOnboardPublish(cmd.Context())
}

// checkLegacyOnboardFlags rejects the OSF-flow flags unless --osf is set:
// they steer the legacy wizard and would be silently ignored otherwise.
func checkLegacyOnboardFlags(osf bool, project, remoteBase string) error {
	if !osf && project != "" {
		return errors.New("--project only applies to the legacy OSF flow; pass --osf (deprecated) to use it")
	}
	if !osf && remoteBase != "" {
		return errors.New("--remote-base only applies to the legacy OSF flow; pass --osf (deprecated) to use it")
	}
	return nil
}

// ---- publish-first flow ----

func runOnboardPublish(ctx context.Context) error {
	p := prompter{line: promptLine, yes: confirm}

	// Phase 1 — ensure a manifest exists (no OSF project needed to publish).
	m, mfPath, repoRoot, err := ensurePublishManifest()
	if err != nil {
		return err
	}

	// Phase 2 — an archive remote to publish to.
	remote, err := ensureArchiveRemote(ctx, p, m, mfPath)
	if err != nil {
		return err
	}

	// Phase 3 — group local files into a dataset with its metadata.
	slug, err := ensureDataset(p, m, mfPath, repoRoot, remote.Kind)
	if err != nil {
		return err
	}

	// Phase 4 — the optional second track: a mutable, DOI-free workspace
	// remote for intermediate results (offered, never assumed).
	if err := offerWorkspaceRemote(ctx, p, m, mfPath); err != nil {
		return err
	}

	// Phase 5 — lint what we wrote and point at check/publish.
	return onboardPublishSummary(m, mfPath, slug)
}

// offerWorkspaceRemote asks whether the project also needs a workspace remote
// (mutable, journal-versioned, no DOIs — the cluster→laptop track) and
// records it as default_workspace. Skipped when one is already configured.
func offerWorkspaceRemote(ctx context.Context, p prompter, m *manifest.Manifest, mfPath string) error {
	if name := m.Project.DefaultWorkspace; name != "" {
		if r, ok := config.GetRemote(name); ok && isWorkspaceKind(r.Kind) {
			fmt.Fprintf(os.Stderr, "%s workspace remote %q (%s) already configured\n", output.Green("✓"), r.Name, r.URL)
			return nil
		}
	}
	fmt.Fprintln(os.Stderr, output.Bold("\nMutable intermediate results (optional)"))
	fmt.Fprintln(os.Stderr, output.Dim("  A workspace remote versions data with a journal but mints no DOIs —"))
	fmt.Fprintln(os.Stderr, output.Dim("  for results still moving between a cluster and a laptop."))
	if !p.yes("Add a workspace remote now?") {
		return nil
	}
	fmt.Fprintln(os.Stderr, "  1) dir   a mounted directory or NAS share")
	fmt.Fprintln(os.Stderr, "  2) s3    S3-compatible bucket (s3://endpoint/bucket/prefix)")
	fmt.Fprintln(os.Stderr, "  3) sftp  SSH host (sftp://host/path)")
	var kind string
	for {
		var ok bool
		if kind, ok = workspaceKindFromChoice(p.line("Workspace kind [1-3]", "")); ok {
			break
		}
	}
	var url string
	for url == "" {
		url = p.line("Workspace URL", "")
	}
	var name string
	for {
		name = p.line("Name for this remote", "workspace")
		if name == "" {
			continue
		}
		if _, exists := config.GetRemote(name); exists {
			fmt.Fprintln(os.Stderr, output.Yellow(fmt.Sprintf("a remote named %q already exists — pick another name", name)))
			continue
		}
		break
	}
	token := ""
	if kind == "s3" {
		token = promptSecret("S3 credentials as ACCESS:SECRET (Enter to skip)")
	}

	r := config.Remote{Name: name, Kind: kind, URL: url}
	if err := probeWorkspace(ctx, r, token); err != nil {
		fmt.Fprintln(os.Stderr, output.Yellow(fmt.Sprintf("could not reach %s: %v", url, err)))
		if !p.yes("Add it anyway (unverified)?") {
			return fmt.Errorf("workspace remote %q not added: %w", name, err)
		}
	}
	if err := config.AddRemote(r); err != nil {
		return err
	}
	if token != "" {
		if err := config.SaveRemoteToken(name, token, noKeychain); err != nil {
			return fmt.Errorf("remote added, but storing its credentials failed: %w", err)
		}
	}
	m.Project.DefaultWorkspace = name
	if err := manifest.Save(m, mfPath); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s added workspace remote %q (%s); datasets push/pull there with 'datapin push <slug>'\n",
		output.Green("✓"), name, url)
	return nil
}

// probeWorkspace verifies a workspace remote answers before it is recorded —
// the same List probe `remote add` performs.
func probeWorkspace(ctx context.Context, r config.Remote, token string) error {
	log.Infof("probing %s", r.URL)
	ws, closer, err := newWorkspace(r, token)
	if err != nil {
		return err
	}
	if closer != nil {
		defer func() { _ = closer() }()
	}
	_, err = ws.List(ctx)
	return err
}

// workspaceKindFromChoice maps the workspace-kind menu answer. There is no
// default: the second track is opt-in.
func workspaceKindFromChoice(ans string) (kind string, ok bool) {
	switch strings.TrimSpace(ans) {
	case "1":
		return "dir", true
	case "2":
		return "s3", true
	case "3":
		return "sftp", true
	}
	return "", false
}

// ensurePublishManifest loads the manifest, creating an empty one in the
// current directory when none exists. Datasets need no OSF project GUID.
func ensurePublishManifest() (*manifest.Manifest, string, string, error) {
	mfPath, repoRoot, findErr := manifest.FindManifest()
	if manifest.IsNotFound(findErr) {
		p, _, err := manifest.Init(".", "")
		if err != nil {
			return nil, "", "", err
		}
		cwd, _ := os.Getwd()
		fmt.Fprintf(os.Stderr, "%s created %s\n", output.Green("✓"), p)
		mfPath, repoRoot = p, cwd
	} else if findErr != nil {
		return nil, "", "", findErr
	}
	m, err := manifest.Load(mfPath)
	if err != nil {
		return nil, "", "", err
	}
	return m, mfPath, repoRoot, nil
}

// ensureArchiveRemote resolves the archive remote datasets will publish to:
// the manifest's default_archive when it is configured, else a pick from the
// configured archive remotes, else a guided `remote add`. The chosen remote
// is recorded as default_archive.
func ensureArchiveRemote(ctx context.Context, p prompter, m *manifest.Manifest, mfPath string) (config.Remote, error) {
	if name := m.Project.DefaultArchive; name != "" {
		if r, ok := config.GetRemote(name); ok && !isWorkspaceKind(r.Kind) {
			fmt.Fprintf(os.Stderr, "%s publishing to remote %q (%s)\n", output.Green("✓"), r.Name, r.URL)
			return r, nil
		}
		fmt.Fprintln(os.Stderr, output.Yellow(fmt.Sprintf("default_archive = %q is not a configured archive remote — let's fix that", name)))
	}

	fmt.Fprintln(os.Stderr, output.Bold("\nChoose where to publish"))
	remotes, err := config.Remotes()
	if err != nil {
		return config.Remote{}, err
	}

	var chosen config.Remote
	if archives := archiveRemotes(remotes); len(archives) > 0 {
		fmt.Fprintln(os.Stderr, "Configured archive remotes:")
		for i, r := range archives {
			fmt.Fprintf(os.Stderr, "  %2d) %s  (%s, %s)\n", i+1, r.Name, r.Kind, r.URL)
		}
		for {
			ans := p.line("Pick a number, or 'n' to add a new remote", "1")
			idx, addNew, ok := pickRemoteAnswer(ans, len(archives))
			if !ok {
				continue
			}
			if !addNew {
				chosen = archives[idx]
				break
			}
			chosen, err = onboardAddRemote(ctx, p)
			if err != nil {
				return config.Remote{}, err
			}
			break
		}
	} else {
		chosen, err = onboardAddRemote(ctx, p)
		if err != nil {
			return config.Remote{}, err
		}
	}

	if m.Project.DefaultArchive != chosen.Name {
		m.Project.DefaultArchive = chosen.Name
		if err := manifest.Save(m, mfPath); err != nil {
			return config.Remote{}, err
		}
		fmt.Fprintf(os.Stderr, "%s datasets publish to %q by default (default_archive in %s)\n",
			output.Green("✓"), chosen.Name, mfPath)
	}
	return chosen, nil
}

// remoteOption is one entry of the "where will you publish?" menu.
type remoteOption struct {
	Label string
	Kind  string
	URL   string // "" = ask for the instance/collection URL
	Name  string // suggested remote name
	Note  string
}

// onboardRemoteOptions lists the archive targets, sandbox rehearsal first
// (issue #25): rehearse the whole publish flow where the DOIs are fake.
func onboardRemoteOptions() []remoteOption {
	return []remoteOption{
		{"Zenodo Sandbox", "invenio", "https://sandbox.zenodo.org", "sandbox",
			"recommended first — rehearse the full publish flow; DOIs are fake and records disposable"},
		{"Zenodo", "invenio", "https://zenodo.org", "zenodo",
			"the production service — published records are permanent"},
		{"Another InvenioRDM instance", "invenio", "", "archive",
			"an institutional repository running InvenioRDM"},
		{"Dataverse", "dataverse", "", "dataverse",
			"a collection URL, e.g. https://demo.dataverse.org/dataverse/<alias>"},
		{"Figshare", "figshare", "https://api.figshare.com", "figshare", ""},
	}
}

// remoteOptionFor maps a menu answer to its option. The empty answer is the
// sandbox default; anything unrecognized is not ok.
func remoteOptionFor(ans string) (remoteOption, bool) {
	opts := onboardRemoteOptions()
	if ans == "" {
		return opts[0], true
	}
	n, err := strconv.Atoi(ans)
	if err != nil || n < 1 || n > len(opts) {
		return remoteOption{}, false
	}
	return opts[n-1], true
}

// pickRemoteAnswer parses the "pick a configured remote" answer: a 1-based
// number (default: the first), or n/N for adding a new remote.
func pickRemoteAnswer(ans string, n int) (idx int, addNew, ok bool) {
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "":
		return 0, false, true
	case "n":
		return 0, true, true
	}
	i, err := strconv.Atoi(ans)
	if err != nil || i < 1 || i > n {
		return 0, false, false
	}
	return i - 1, false, true
}

// archiveRemotes filters the configured remotes down to archive kinds
// (workspace kinds carry the mutable role and cannot mint DOIs — D33).
func archiveRemotes(remotes []config.Remote) []config.Remote {
	var out []config.Remote
	for _, r := range remotes {
		if !isWorkspaceKind(r.Kind) {
			out = append(out, r)
		}
	}
	return out
}

// onboardAddRemote walks through registering a new archive remote: target
// menu (sandbox first), URL where needed, name, token, probe, persist.
func onboardAddRemote(ctx context.Context, p prompter) (config.Remote, error) {
	opts := onboardRemoteOptions()
	fmt.Fprintln(os.Stderr, "Where will you publish?")
	for i, o := range opts {
		line := fmt.Sprintf("  %d) %-28s", i+1, o.Label)
		if o.URL != "" {
			line += " " + o.URL
		}
		fmt.Fprintln(os.Stderr, line)
		if o.Note != "" {
			fmt.Fprintln(os.Stderr, output.Dim("       "+o.Note))
		}
	}

	var opt remoteOption
	for {
		var ok bool
		if opt, ok = remoteOptionFor(p.line(fmt.Sprintf("Repository [1-%d]", len(opts)), "")); ok {
			break
		}
	}
	url := opt.URL
	for url == "" {
		url = p.line("Repository URL", "")
	}
	var name string
	for {
		name = p.line("Name for this remote", opt.Name)
		if name == "" {
			continue
		}
		if _, exists := config.GetRemote(name); exists {
			fmt.Fprintln(os.Stderr, output.Yellow(fmt.Sprintf("a remote named %q already exists — pick another name", name)))
			continue
		}
		break
	}
	token := promptSecret(fmt.Sprintf("API token for %s (Enter to skip; set DATAPIN_TOKEN_%s before publishing)", url, envSuffix(name)))

	r := config.Remote{Name: name, Kind: opt.Kind, URL: url}
	added, err := addArchiveRemote(ctx, r, token, true, noKeychain)
	if err != nil {
		var pe probeError
		if !errors.As(err, &pe) {
			return config.Remote{}, err
		}
		fmt.Fprintln(os.Stderr, output.Yellow(fmt.Sprintf("could not verify %s: %v", url, err)))
		if !p.yes("Add it anyway (unverified)?") {
			return config.Remote{}, fmt.Errorf("remote %q not added: %w", name, err)
		}
		// Unverified: no capabilities were learned, so the remote keeps the
		// driver defaults (issue #20, D55).
		if added, err = addArchiveRemote(ctx, r, token, false, noKeychain); err != nil {
			return config.Remote{}, err
		}
	}
	fmt.Fprintf(os.Stderr, "%s added remote %q (%s)\n", output.Green("✓"), name, url)
	if added.Sandbox {
		fmt.Fprintln(os.Stderr, output.Dim("  sandbox instance — DOIs it mints (prefix 10.5072) do not resolve"))
	}
	if added.Caps != nil && len(added.Caps.ResourceTypes) > 0 {
		fmt.Fprintln(os.Stderr, output.Dim(fmt.Sprintf(
			"  probed %d resource types from the instance vocabulary", len(added.Caps.ResourceTypes))))
	}
	if !added.TokenStored {
		fmt.Fprintf(os.Stderr, "%s\n", output.Yellow(fmt.Sprintf("  no token stored — set DATAPIN_TOKEN_%s or run: datapin remote add %s --name %s --token-value <token>", envSuffix(name), url, name)))
	}
	return r, nil
}

// promptSecret reads a line without echo (tokens must not land in scrollback).
func promptSecret(prompt string) string {
	fmt.Fprintf(os.Stderr, "%s: ", prompt)
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// ensureDataset creates a [[datasets]] entry interactively: file selection,
// slug, and the DataCite floor (title, creators+ORCIDs, license — an explicit
// choice, D37; contact e-mail, required for Dataverse — D38). Returns the new
// dataset's slug, or "" when the phase was skipped.
func ensureDataset(p prompter, m *manifest.Manifest, mfPath, repoRoot, remoteKind string) (string, error) {
	if len(m.Datasets) > 0 {
		var slugs []string
		for _, d := range m.Datasets {
			slugs = append(slugs, d.Slug)
		}
		fmt.Fprintf(os.Stderr, "%s manifest already has %d dataset(s): %s\n",
			output.Green("✓"), len(m.Datasets), strings.Join(slugs, ", "))
		if !p.yes("Add another dataset now?") {
			return "", nil
		}
	}

	fmt.Fprintln(os.Stderr, output.Bold("\nSelect the files this dataset will contain"))
	cands, err := gitutil.Candidates(repoRoot)
	if err != nil {
		return "", fmt.Errorf("scanning local files: %w", err)
	}
	cands = onboardDatasetCandidates(cands, m)
	if len(cands) == 0 {
		fmt.Fprintln(os.Stderr, output.Dim("No untracked local files found — add files under [[datasets.files]] in "+mfPath+" by hand."))
		return "", nil
	}
	selected, err := picker.Run(cands)
	if errors.Is(err, picker.ErrCanceled) {
		fmt.Fprintln(os.Stderr, "Canceled — nothing added.")
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(selected) == 0 {
		fmt.Fprintln(os.Stderr, output.Dim("No files selected."))
		return "", nil
	}

	var slug string
	for {
		slug = p.line("Dataset slug (its local handle for check/publish/pull)", defaultDatasetSlug(repoRoot, m))
		if slug == "" {
			continue
		}
		if m.FindDataset(slug) != nil {
			fmt.Fprintln(os.Stderr, output.Yellow(fmt.Sprintf("a dataset with slug %q already exists — pick another", slug)))
			continue
		}
		break
	}

	fmt.Fprintln(os.Stderr, output.Bold("\nDescribe the dataset")+output.Dim(" (what a published record needs — the DataCite floor)"))
	md := manifest.DatasetMetadata{
		Title:        collectTitle(p),
		Description:  p.line("Description (recommended; Enter to skip)", ""),
		Creators:     collectCreators(p),
		License:      collectLicense(p),
		ContactEmail: collectContactEmail(p, remoteKind),
	}

	m.Datasets = append(m.Datasets, manifest.Dataset{
		Slug:     slug,
		Metadata: md,
		Files:    datasetFilesFor(selected),
	})
	if err := manifest.Save(m, mfPath); err != nil {
		return "", fmt.Errorf("updating %s: %w", mfPath, err)
	}
	fmt.Fprintf(os.Stderr, "%s dataset %q with %d file(s) written to %s\n",
		output.Green("✓"), slug, len(selected), mfPath)
	return slug, nil
}

// onboardDatasetCandidates drops files already tracked anywhere in the
// manifest: in a dataset, a [[files]] entry, or a [[wikis]] entry.
func onboardDatasetCandidates(cands []gitutil.Candidate, m *manifest.Manifest) []gitutil.Candidate {
	tracked := map[string]bool{}
	if m != nil {
		for _, e := range m.Files {
			tracked[filepath.ToSlash(e.Local)] = true
		}
		for _, w := range m.Wikis {
			tracked[filepath.ToSlash(w.Local)] = true
		}
		for _, d := range m.Datasets {
			for _, f := range d.Files {
				tracked[filepath.ToSlash(f.Local)] = true
			}
		}
	}
	var out []gitutil.Candidate
	for _, c := range cands {
		if !tracked[c.Path] {
			out = append(out, c)
		}
	}
	return out
}

// defaultDatasetSlug suggests a slug from the repo directory name, deduped
// against the manifest's existing datasets.
func defaultDatasetSlug(repoRoot string, m *manifest.Manifest) string {
	taken := map[string]bool{}
	for _, d := range m.Datasets {
		taken[d.Slug] = true
	}
	return dedupeSlug(slugify(filepath.Base(repoRoot)), taken)
}

// datasetFilesFor builds the [[datasets.files]] entries: keys are the full
// slash-separated local path (D49 — basenames collide across directories).
func datasetFilesFor(selected []string) []manifest.DatasetFile {
	out := make([]manifest.DatasetFile, 0, len(selected))
	for _, rel := range selected {
		key := filepath.ToSlash(rel)
		out = append(out, manifest.DatasetFile{Local: rel, Key: key})
	}
	return out
}

// collectTitle insists on a title — DataCite-mandatory, and the one field
// with no useful placeholder.
func collectTitle(p prompter) string {
	for {
		if t := p.line("Title", ""); t != "" {
			return t
		}
		fmt.Fprintln(os.Stderr, output.Dim("  a published record needs a title (DataCite mandatory)"))
	}
}

// collectCreators gathers at least one creator; ORCIDs are validated
// (ISO 7064 checksum) and stored in canonical 0000-0000-0000-000X form.
func collectCreators(p prompter) []manifest.DatasetCreator {
	var out []manifest.DatasetCreator
	for {
		var name string
		for {
			if name = p.line(`Creator name ("Family, Given")`, ""); name != "" {
				break
			}
			fmt.Fprintln(os.Stderr, output.Dim("  a published record needs at least one creator (DataCite mandatory)"))
		}
		var orcid string
		for {
			ans := p.line("  ORCID (recommended; Enter to skip)", "")
			if ans == "" {
				break
			}
			if orcid = meta.NormalizeORCID(ans); orcid != "" {
				break
			}
			fmt.Fprintln(os.Stderr, output.Yellow(fmt.Sprintf("  %q is not a valid ORCID (want 0000-0000-0000-000X)", ans)))
		}
		affiliation := p.line("  Affiliation (Enter to skip)", "")
		out = append(out, manifest.DatasetCreator{Name: name, ORCID: orcid, Affiliation: affiliation})
		if !p.yes("Add another creator?") {
			return out
		}
	}
}

// licenseFromChoice maps a license-menu answer. There is deliberately no
// default: the license is an explicit choice (D37) — datapin suggests
// CC0-1.0 (with CC-BY-4.0 the named alternative) but never fills one in.
func licenseFromChoice(ans string) (license string, needsID, ok bool) {
	switch strings.TrimSpace(ans) {
	case "1":
		return "CC0-1.0", false, true
	case "2":
		return "CC-BY-4.0", false, true
	case "3":
		return "", true, true
	case "4":
		return "", false, true
	}
	return "", false, false
}

// collectLicense presents the explicit license choice (D37/D20): CC0-1.0
// suggested, CC-BY-4.0 the alternative, any SPDX id, or a deliberate
// "decide later" (publish will refuse until one is set).
func collectLicense(p prompter) string {
	fmt.Fprintln(os.Stderr, "Choose a license — an explicit choice; datapin never picks one for you:")
	fmt.Fprintln(os.Stderr, "  1) CC0-1.0    public-domain dedication (suggested for research data)")
	fmt.Fprintln(os.Stderr, "  2) CC-BY-4.0  reuse requires attribution (the common alternative)")
	fmt.Fprintln(os.Stderr, "  3) another SPDX license id (e.g. MIT, ODbL-1.0)")
	fmt.Fprintln(os.Stderr, "  4) decide later — 'datapin publish' refuses until a license is set")
	for {
		license, needsID, ok := licenseFromChoice(p.line("License [1-4]", ""))
		if !ok {
			continue
		}
		for needsID && license == "" {
			license = p.line("SPDX license id", "")
		}
		return license
	}
}

// collectContactEmail asks for the dataset's point of contact. Dataverse
// refuses to create a dataset without one (D38), so for a dataverse remote
// the answer is required; elsewhere it is optional.
func collectContactEmail(p prompter, remoteKind string) string {
	required := contactEmailRequired(remoteKind)
	prompt := "Contact e-mail (Enter to skip)"
	if required {
		prompt = "Contact e-mail (required to publish to Dataverse)"
	}
	for {
		email := p.line(prompt, "")
		if email != "" || !required {
			return email
		}
		fmt.Fprintln(os.Stderr, output.Dim("  Dataverse requires a point-of-contact e-mail to create a dataset"))
	}
}

// contactEmailRequired reports whether the remote kind mandates
// contact_email at the publish boundary (D38: only Dataverse does).
func contactEmailRequired(kind string) bool {
	return kind == "dataverse"
}

// onboardPublishSummary lints the freshly written dataset and points at the
// publish workflow (and the optional workspace track).
func onboardPublishSummary(m *manifest.Manifest, mfPath, slug string) error {
	if slug != "" {
		if ds := m.FindDataset(slug); ds != nil {
			issues := meta.Check(ds.Metadata)
			for _, i := range issues {
				style := output.Yellow
				if i.Severity == meta.Error {
					style = output.Red
				}
				fmt.Fprintf(os.Stderr, "  %s %s: %s\n", style(string(i.Severity)), i.Field, i.Message)
			}
			if len(issues) == 0 {
				fmt.Fprintf(os.Stderr, "%s metadata is publish-ready\n", output.Green("✓"))
			}
		}
	}
	checkArg, publishArg := "", "<slug>"
	if slug != "" {
		checkArg, publishArg = " "+slug, slug
	}
	fmt.Fprintln(os.Stderr, output.Bold("\nNext steps"))
	fmt.Fprintf(os.Stderr, "  review:   %s\n", mfPath)
	fmt.Fprintf(os.Stderr, "  lint:     datapin check%s\n", checkArg)
	fmt.Fprintf(os.Stderr, "  publish:  datapin publish %s   (PUBLIC and PERMANENT — mints a DOI)\n", publishArg)
	fmt.Fprintln(os.Stderr, output.Dim("  mutable intermediate results (no DOI)? add a workspace remote:"))
	fmt.Fprintln(os.Stderr, output.Dim("    datapin remote add <url> --kind dir|s3|sftp --name <name>, then datapin push/pull"))
	return nil
}

// ---- legacy OSF workspace flow (behind --osf; deprecated) ----

func runOnboardOSF(ctx context.Context) error {
	// Phase 0 — Auth (offered, not required).
	token := config.LoadToken(flagToken)
	if token == "" {
		fmt.Fprintln(os.Stderr, output.Bold("Authenticate"))
		if confirm("Log in to OSF now? (needed to browse your projects and to push later)") {
			if err := runLogin(ctx, false); err != nil {
				return err
			}
			token = config.LoadToken(flagToken)
		} else {
			fmt.Fprintln(os.Stderr, output.Dim("Continuing unauthenticated — enter a GUID manually; log in before 'datapin sync'."))
		}
	} else {
		fmt.Fprintln(os.Stderr, output.Green("✓")+" authenticated")
	}
	osfClient := client.New(token)

	// Phase 1 — ensure a manifest with a project id.
	m, mfPath, repoRoot, err := ensureManifest(ctx, osfClient, token)
	if err != nil {
		return err
	}

	// Phase 2 — select local files to push.
	fmt.Fprintln(os.Stderr, output.Bold("\nSelect files to push"))
	cands, err := gitutil.Candidates(repoRoot)
	if err != nil {
		return fmt.Errorf("scanning local files: %w", err)
	}
	cands = untrackedCandidates(cands, m)
	if len(cands) == 0 {
		fmt.Fprintln(os.Stderr, output.Dim("No new untracked files to add."))
		return onboardSummary(mfPath)
	}

	selected, err := picker.Run(cands)
	if errors.Is(err, picker.ErrCanceled) {
		fmt.Fprintln(os.Stderr, "Canceled — nothing added.")
		return nil
	}
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		fmt.Fprintln(os.Stderr, output.Dim("No files selected."))
		return onboardSummary(mfPath)
	}

	base := onboardRemoteBase
	if base == "" {
		base = promptLine("Remote base path in OSF Storage", "/")
	}

	added := 0
	for _, rel := range selected {
		if findEntryByLocal(m, rel) >= 0 {
			continue
		}
		m.Files = append(m.Files, manifest.Entry{
			Local:  rel,
			Remote: remotePath(base, rel),
		})
		added++
	}
	if added > 0 {
		if err := manifest.Save(m, mfPath); err != nil {
			return fmt.Errorf("updating %s: %w", mfPath, err)
		}
	}
	fmt.Fprintf(os.Stderr, "%s added %d file(s) to %s\n", output.Green("✓"), added, mfPath)
	return onboardSummary(mfPath)
}

// ensureManifest loads (or creates) the manifest and guarantees a project id,
// prompting to choose one when needed.
func ensureManifest(ctx context.Context, c *client.OSFClient, token string) (*manifest.Manifest, string, string, error) {
	mfPath, repoRoot, findErr := manifest.FindManifest()
	if findErr != nil && !manifest.IsNotFound(findErr) {
		return nil, "", "", findErr
	}

	if manifest.IsNotFound(findErr) {
		fmt.Fprintln(os.Stderr, output.Bold("\nChoose a project"))
		proj, err := chooseProject(ctx, c, token)
		if err != nil {
			return nil, "", "", err
		}
		p, _, err := manifest.Init(".", proj)
		if err != nil {
			return nil, "", "", err
		}
		m, err := manifest.Load(p)
		if err != nil {
			return nil, "", "", err
		}
		cwd, _ := os.Getwd()
		fmt.Fprintf(os.Stderr, "%s created %s (project %s)\n", output.Green("✓"), p, proj)
		return m, p, cwd, nil
	}

	m, err := manifest.Load(mfPath)
	if err != nil {
		return nil, "", "", err
	}
	if m.Project.ID == "" {
		fmt.Fprintln(os.Stderr, output.Bold("\nChoose a project"))
		proj, err := chooseProject(ctx, c, token)
		if err != nil {
			return nil, "", "", err
		}
		m.Project.ID = proj
		if err := manifest.Save(m, mfPath); err != nil {
			return nil, "", "", err
		}
	}
	fmt.Fprintf(os.Stderr, "%s using %s (project %s)\n", output.Green("✓"), mfPath, m.Project.ID)
	return m, mfPath, repoRoot, nil
}

// chooseProject resolves a project GUID: the --project flag, else a numbered
// pick from the user's projects (when authenticated), else a typed GUID.
func chooseProject(ctx context.Context, c *client.OSFClient, token string) (string, error) {
	if onboardProject != "" {
		return onboardProject, nil
	}
	if token != "" {
		if nodes, err := c.GetUserNodes(ctx); err == nil && len(nodes) > 0 {
			fmt.Fprintln(os.Stderr, "Your projects:")
			for i, n := range nodes {
				vis := "private"
				if n.Attributes.Public {
					vis = "public"
				}
				fmt.Fprintf(os.Stderr, "  %2d) %s  (%s, %s)\n", i+1, n.Attributes.Title, n.ID, vis)
			}
			ans := promptLine("Pick a number, or type a project GUID", "")
			if idx, convErr := strconv.Atoi(ans); convErr == nil && idx >= 1 && idx <= len(nodes) {
				return nodes[idx-1].ID, nil
			}
			if ans != "" {
				return ans, nil
			}
			return "", errors.New("no project selected")
		}
	}
	guid := promptLine("Enter your OSF project GUID (the 5-char id in the osf.io URL)", "")
	if guid == "" {
		return "", errors.New("no project GUID provided")
	}
	return guid, nil
}

func onboardSummary(mfPath string) error {
	fmt.Fprintln(os.Stderr, output.Bold("\nNext steps"))
	fmt.Fprintf(os.Stderr, "  review:  %s\n", mfPath)
	fmt.Fprintln(os.Stderr, "  status:  datapin status")
	fmt.Fprintln(os.Stderr, "  push:    datapin sync")
	return nil
}

// untrackedCandidates drops candidates already recorded in the manifest.
func untrackedCandidates(cands []gitutil.Candidate, m *manifest.Manifest) []gitutil.Candidate {
	tracked := map[string]bool{}
	if m != nil {
		for _, e := range m.Files {
			tracked[filepath.ToSlash(e.Local)] = true
		}
	}
	var out []gitutil.Candidate
	for _, c := range cands {
		if !tracked[c.Path] {
			out = append(out, c)
		}
	}
	return out
}

// remotePath maps a repo-relative path to a remote OSF path under base.
func remotePath(base, rel string) string {
	base = "/" + strings.Trim(base, "/")
	rel = strings.TrimLeft(filepath.ToSlash(rel), "/")
	if base == "/" {
		return "/" + rel
	}
	return base + "/" + rel
}

// promptLine reads a trimmed line from stdin, returning def on empty input.
func promptLine(prompt, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", prompt)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		if ans := strings.TrimSpace(scanner.Text()); ans != "" {
			return ans
		}
	}
	return def
}

func init() {
	onboardCmd.Flags().StringVar(&onboardProject, "project", "", "OSF project GUID to attach (legacy --osf flow only)")
	onboardCmd.Flags().StringVar(&onboardRemoteBase, "remote-base", "", "Remote base path for pushed files (legacy --osf flow only)")
	onboardCmd.Flags().BoolVar(&onboardOSF, "osf", false, "Run the legacy OSF workspace wizard (deprecated: OSF is sunsetting — see 'datapin migrate')")
	onboardCmd.Flags().BoolVar(&noKeychain, "no-keychain", false, "Store tokens in a file instead of the OS keychain")
	rootCmd.AddCommand(onboardCmd)
}
