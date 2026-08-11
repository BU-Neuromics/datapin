package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

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

// addArchiveRemote constructs the driver (validating the kind), probes the
// URL when verify is set, persists the remote, and stores its token when one
// is given. Shared by `remote add` and `onboard`. sandbox reports the
// backend's sandbox capability.
func addArchiveRemote(ctx context.Context, r config.Remote, token string, verify, noKeychain bool) (sandbox, tokenStored bool, err error) {
	bk, err := newArchiveBackend(r, token)
	if err != nil {
		return false, false, err
	}
	sandbox = bk.Capabilities().Sandbox
	if verify {
		log.Infof("probing %s", r.URL)
		p, ok := bk.(pingable)
		if !ok {
			return sandbox, false, fmt.Errorf("kind %q cannot be probed", r.Kind)
		}
		if pingErr := p.Ping(ctx); pingErr != nil {
			return sandbox, false, probeError{pingErr}
		}
	}
	if err := config.AddRemote(r); err != nil {
		return sandbox, false, err
	}
	if token != "" {
		if err := config.SaveRemoteToken(r.Name, token, noKeychain); err != nil {
			return sandbox, false, fmt.Errorf("remote added, but storing its token failed: %w", err)
		}
		tokenStored = true
	}
	return sandbox, tokenStored, nil
}

var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Manage the archive and workspace remotes datasets use",
	Long: `Configure named remotes that datasets publish or sync to.

The kind implies the role:
  invenio, figshare, dataverse   archive remotes — publishing mints a DOI
  dir, s3, sftp                  workspace remotes — journal-versioned, no DOI

A remote is a backend instance (e.g. https://zenodo.org,
https://sandbox.zenodo.org, s3://minio.lab:9000/bucket) stored in
~/.config/datapin/config.toml. Tokens are stored separately per remote — in
the OS keychain, or a token file on headless systems — never in config.toml.
The token lookup order is: DATAPIN_TOKEN_<NAME> env var, keychain, token file.`,
}

var (
	remoteAddName     string
	remoteAddKind     string
	remoteAddNoVerify bool
	remoteAddToken    string
)

var remoteAddCmd = &cobra.Command{
	Use:   "add <url>",
	Short: "Add a named archive or workspace remote",
	Long: `Register a backend instance as a named remote.

The URL is probed before the remote is added — an archive kind must answer
like that API, a workspace kind must be listable (--no-verify skips the
probe, e.g. when offline). Pass --token-value to store the remote's
credential at the same time; it can also be supplied per-run via the
DATAPIN_TOKEN_<NAME> environment variable.

Archive kinds mint DOIs when a dataset publishes:

  datapin remote add https://sandbox.zenodo.org --name sandbox
  datapin remote add https://zenodo.org --name zenodo
  datapin remote add https://demo.dataverse.org/dataverse/mylab --name dv --kind dataverse
  datapin remote add https://api.figshare.com --name fig --kind figshare

Zenodo sandbox and production are separate services with separate accounts
and tokens — register them as two remotes and rehearse on the sandbox.

Workspace kinds sync mutable intermediate results with journal versioning
and no DOI:

  datapin remote add /mnt/lab-share --name nas --kind dir
  datapin remote add s3://minio.lab:9000/bucket/prefix --name obj --kind s3 \
      --token-value ACCESSKEY:SECRETKEY
  datapin remote add sftp://user@cluster/scratch/proj --name hpc --kind sftp`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		url := args[0]
		name := remoteAddName
		if name == "" {
			return fmt.Errorf("--name is required (e.g. --name sandbox)")
		}
		sandbox := false
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
			var err error
			sandbox, _, err = addArchiveRemote(cmd.Context(),
				config.Remote{Name: name, Kind: remoteAddKind, URL: url},
				remoteAddToken, !remoteAddNoVerify, noKeychain)
			var pe probeError
			if errors.As(err, &pe) {
				return fmt.Errorf("%w\n(use --no-verify to add it anyway)", err)
			}
			if err != nil {
				return err
			}
		}
		tokenStored := remoteAddToken != ""

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, output.RemoteAddResult{
				Name: name, Kind: remoteAddKind, URL: url,
				Sandbox: sandbox, TokenStored: tokenStored,
			})
		}
		log.Infof("added remote %q (%s)", name, url)
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
	remoteAddCmd.Flags().StringVar(&remoteAddKind, "kind", "invenio", "Remote kind: archive (invenio, figshare, dataverse) or workspace (dir, s3, sftp)")
	remoteAddCmd.Flags().BoolVar(&remoteAddNoVerify, "no-verify", false, "Skip probing the URL before adding")
	remoteAddCmd.Flags().StringVar(&remoteAddToken, "token-value", "", "Credential to store for this remote (s3: ACCESSKEY:SECRETKEY)")
	remoteAddCmd.Flags().BoolVar(&noKeychain, "no-keychain", false, "Store the token in a file instead of the OS keychain")
	remoteCmd.AddCommand(remoteAddCmd, remoteLsCmd, remoteRmCmd)
	rootCmd.AddCommand(remoteCmd)
}
