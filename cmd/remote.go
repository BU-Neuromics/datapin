package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
)

// probeError marks a failure of the pre-add URL probe — the one error class
// a caller may sensibly override (remote add's --no-verify, onboard's
// "add it anyway?" confirm).
type probeError struct{ err error }

func (e probeError) Error() string { return e.err.Error() }
func (e probeError) Unwrap() error { return e.err }

// archiveRemoteAdd reports what addArchiveRemote did: the backend's sandbox
// capability, whether a token was stored, and what the instance declared
// about itself when probed (Caps is nil when the probe was skipped or
// learned nothing — issue #20, D55).
type archiveRemoteAdd struct {
	Sandbox     bool
	TokenStored bool
	Caps        *config.RemoteCaps
	ProbeNotes  []string
}

// addArchiveRemote constructs the driver (validating the kind), probes the
// URL when verify is set, persists the remote together with whatever the
// probe learned, and stores its token when one is given. Shared by
// `remote add` and `onboard`.
func addArchiveRemote(ctx context.Context, r config.Remote, token string, verify, noKeychain bool) (archiveRemoteAdd, error) {
	out := archiveRemoteAdd{}
	bk, err := newArchiveBackend(r, token)
	if err != nil {
		return out, err
	}
	out.Sandbox = bk.Capabilities().Sandbox
	if verify {
		log.Infof("probing %s", r.URL)
		// A driver that can describe its instance does so (and its probe
		// subsumes Ping); the rest only answer Ping. Prober comes first: the
		// invenio driver satisfies both.
		switch p := bk.(type) {
		case backend.Prober:
			res, perr := p.Probe(ctx)
			if perr != nil {
				return out, probeError{perr}
			}
			out.Caps = config.FromProbe(res, time.Now().UTC().Format(time.RFC3339))
			out.ProbeNotes = res.Notes
		case pingable:
			if pingErr := p.Ping(ctx); pingErr != nil {
				return out, probeError{pingErr}
			}
		default:
			return out, fmt.Errorf("kind %q cannot be probed", r.Kind)
		}
	}
	r.Caps = out.Caps
	if err := config.AddRemote(r); err != nil {
		return out, err
	}
	if token != "" {
		if err := config.SaveRemoteToken(r.Name, token, noKeychain); err != nil {
			return out, fmt.Errorf("remote added, but storing its token failed: %w", err)
		}
		out.TokenStored = true
	}
	return out, nil
}

var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Manage archive backend remotes (Zenodo/InvenioRDM)",
	Long: `Configure named archive remotes that datasets publish to.

A remote is a backend instance (e.g. https://zenodo.org or
https://sandbox.zenodo.org) stored in ~/.config/datapin/config.toml.
Tokens are stored separately per remote — in the OS keychain, or a
token file on headless systems — never in config.toml. The token
lookup order is: DATAPIN_TOKEN_<NAME> env var, keychain, token file.`,
}

var (
	remoteAddName     string
	remoteAddKind     string
	remoteAddNoVerify bool
	remoteAddToken    string
)

var remoteAddCmd = &cobra.Command{
	Use:   "add <url>",
	Short: "Add a named archive remote",
	Long: `Register a backend instance as a named remote.

The URL is probed to verify it answers like an InvenioRDM instance and to
learn what that instance declares about itself — today its resource-type
vocabulary, which 'datapin check' validates a dataset's resource_type
against. Probed values are stored under [remotes.<name>.caps] in
config.toml so no later command re-probes; anything the API does not
expose (per-record file/size limits, transfer types) keeps datapin's
documented default and can be overridden by editing that table.
--no-verify skips the probe entirely (e.g. when offline) and leaves every
capability at its default. Pass --token-value to store an API token for
the remote at the same time; tokens can also be supplied per-run via the
DATAPIN_TOKEN_<NAME> environment variable.

Zenodo sandbox and production are separate services with separate
accounts and tokens — register them as two remotes:

  datapin remote add https://sandbox.zenodo.org --name sandbox
  datapin remote add https://zenodo.org --name zenodo`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		url := args[0]
		name := remoteAddName
		if name == "" {
			return fmt.Errorf("--name is required (e.g. --name sandbox)")
		}
		sandbox := false
		var probed *config.RemoteCaps
		var probeNotes []string
		if isWorkspaceKind(remoteAddKind) {
			if !remoteAddNoVerify {
				log.Infof("probing %s", url)
				ws, closer, err := newWorkspace(config.Remote{Name: name, Kind: remoteAddKind, URL: url}, remoteAddToken)
				if err != nil {
					return fmt.Errorf("%w\n(use --no-verify to add it anyway)", err)
				}
				if _, err := ws.List(cmd.Context()); err != nil {
					if closer != nil {
						_ = closer()
					}
					return fmt.Errorf("%w\n(use --no-verify to add it anyway)", err)
				}
				if closer != nil {
					_ = closer()
				}
			}
			if err := config.AddRemote(config.Remote{Name: name, Kind: remoteAddKind, URL: url}); err != nil {
				return err
			}
			if remoteAddToken != "" {
				if err := config.SaveRemoteToken(name, remoteAddToken, noKeychain); err != nil {
					return fmt.Errorf("remote added, but storing its token failed: %w", err)
				}
			}
		} else {
			res, err := addArchiveRemote(cmd.Context(),
				config.Remote{Name: name, Kind: remoteAddKind, URL: url},
				remoteAddToken, !remoteAddNoVerify, noKeychain)
			var pe probeError
			if errors.As(err, &pe) {
				return fmt.Errorf("%w\n(use --no-verify to add it anyway)", err)
			}
			if err != nil {
				return err
			}
			sandbox, probed, probeNotes = res.Sandbox, res.Caps, res.ProbeNotes
		}
		tokenStored := remoteAddToken != ""

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, output.RemoteAddResult{
				Name: name, Kind: remoteAddKind, URL: url,
				Sandbox: sandbox, TokenStored: tokenStored,
				Caps: capsResult(probed), ProbeNotes: probeNotes,
			})
		}
		log.Infof("added remote %q (%s)", name, url)
		logProbeResult(probed, probeNotes)
		if sandbox {
			log.Infof("this is a sandbox instance — DOIs it mints (prefix 10.5072) do not resolve")
		}
		if isWorkspaceKind(remoteAddKind) {
			log.Infof("workspace remote — datasets push/pull here with journal versioning; publishing needs an archive remote")
		} else if !tokenStored && config.LoadRemoteToken(name) == "" {
			log.Infof("no token stored — set DATAPIN_TOKEN_%s or re-add with --token before publishing", envSuffix(name))
		}
		return nil
	},
}

var remoteLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List configured remotes",
	RunE: func(cmd *cobra.Command, args []string) error {
		remotes, err := config.Remotes()
		if err != nil {
			return err
		}
		if flagOutput == "json" {
			rows := make([]output.RemoteListEntry, 0, len(remotes))
			for _, r := range remotes {
				rows = append(rows, output.RemoteListEntry{
					Name: r.Name, Kind: r.Kind, URL: r.URL,
					HasToken: config.LoadRemoteToken(r.Name) != "",
				})
			}
			return output.PrintJSON(os.Stdout, rows)
		}
		if len(remotes) == 0 {
			log.Infof("no remotes configured — add one with: datapin remote add <url> --name <name>")
			return nil
		}
		rows := make([][]output.Cell, 0, len(remotes))
		for _, r := range remotes {
			token := "no token"
			style := output.Yellow
			if config.LoadRemoteToken(r.Name) != "" {
				token = "token set"
				style = output.Green
			}
			rows = append(rows, []output.Cell{
				{Text: r.Name, Style: output.Bold},
				{Text: r.Kind},
				{Text: r.URL},
				{Text: token, Style: style},
			})
		}
		output.RenderTable(os.Stdout, []string{"NAME", "KIND", "URL", "AUTH"}, rows)
		return nil
	},
}

var remoteProbeCmd = &cobra.Command{
	Use:   "probe <name>",
	Short: "Re-probe a configured archive remote's capabilities",
	Long: `Ask a configured archive remote what it declares about itself and
rewrite its [remotes.<name>.caps] table in config.toml.

Use it when an instance has changed since it was added — a resource type
added to its vocabulary, say — or after adding a remote with --no-verify.
Only what the instance's API actually exposes is stored; anything else
keeps datapin's documented default and is reported as such. A failed probe
leaves the previously stored capabilities untouched.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		r, ok := config.GetRemote(name)
		if !ok {
			return fmt.Errorf("no remote named %q — add it with: datapin remote add <url> --name %s", name, name)
		}
		if isWorkspaceKind(r.Kind) {
			return fmt.Errorf("remote %q is a %s workspace remote — capability probing applies to archive remotes", name, r.Kind)
		}
		bk, err := newArchiveBackend(r, config.LoadRemoteToken(name))
		if err != nil {
			return err
		}
		pr, ok := bk.(backend.Prober)
		if !ok {
			return fmt.Errorf("remote kind %q does not support capability probing yet", r.Kind)
		}
		log.Infof("probing %s", r.URL)
		res, err := pr.Probe(cmd.Context())
		if err != nil {
			return err
		}
		probed := config.FromProbe(res, time.Now().UTC().Format(time.RFC3339))
		if err := config.SetRemoteCaps(name, probed); err != nil {
			return err
		}
		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, output.RemoteProbeResult{
				Name: name, Kind: r.Kind, URL: r.URL,
				Caps: capsResult(probed), ProbeNotes: res.Notes,
			})
		}
		log.Infof("probed remote %q (%s)", name, r.URL)
		logProbeResult(probed, res.Notes)
		return nil
	},
}

// capsResult renders stored caps for --output=json (nil when unprobed).
func capsResult(c *config.RemoteCaps) *output.RemoteCapsResult {
	if c == nil {
		return nil
	}
	return &output.RemoteCapsResult{
		ProbedAt:          c.ProbedAt,
		MaxFilesPerRecord: c.MaxFilesPerRecord,
		MaxFileSize:       c.MaxFileSize,
		MultipartUpload:   c.MultipartUpload,
		ResourceTypes:     c.ResourceTypes,
	}
}

// logProbeResult reports what a probe learned and what it did not, so the
// user can see which capabilities are the instance's own word and which are
// datapin's defaults.
func logProbeResult(c *config.RemoteCaps, notes []string) {
	if c != nil && len(c.ResourceTypes) > 0 {
		log.Infof("probed %d resource types from the instance vocabulary", len(c.ResourceTypes))
	}
	for _, n := range notes {
		log.Debugf("probe: %s", n)
	}
}

var remoteRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a configured remote",
	Long:  "Remove a remote from the config and delete its stored token. Manifests referencing the name will fail to resolve it until it is re-added.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := config.RemoveRemote(name); err != nil {
			return err
		}
		if warning, err := config.DeleteRemoteToken(name); err != nil {
			return fmt.Errorf("remote removed, but deleting its token failed: %w", err)
		} else if warning != "" {
			log.Warnf("%s", warning)
		}
		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, output.RemoteRmResult{Name: name})
		}
		log.Infof("removed remote %q", name)
		return nil
	},
}

// envSuffix renders a remote name as its token env-var suffix.
func envSuffix(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r-'a'+'A')
		case r == '-' || r == '.':
			out = append(out, '_')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

func init() {
	remoteAddCmd.Flags().StringVar(&remoteAddName, "name", "", "Name for the remote (required)")
	remoteAddCmd.Flags().StringVar(&remoteAddKind, "kind", "invenio", "Backend kind (invenio, figshare, dataverse)")
	remoteAddCmd.Flags().BoolVar(&remoteAddNoVerify, "no-verify", false, "Skip probing the URL before adding")
	remoteAddCmd.Flags().StringVar(&remoteAddToken, "token-value", "", "API token to store for this remote")
	remoteAddCmd.Flags().BoolVar(&noKeychain, "no-keychain", false, "Store the token in a file instead of the OS keychain")
	remoteCmd.AddCommand(remoteAddCmd, remoteLsCmd, remoteProbeCmd, remoteRmCmd)
	rootCmd.AddCommand(remoteCmd)
}
