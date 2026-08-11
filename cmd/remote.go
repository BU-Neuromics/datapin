package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
)

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

The URL is probed to verify it answers like an InvenioRDM instance
(--no-verify skips the probe, e.g. when offline). Pass --token to store
an API token for the remote at the same time; tokens can also be
supplied per-run via the DATAPIN_TOKEN_<NAME> environment variable.

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
		bk, err := newArchiveBackend(config.Remote{Name: name, Kind: remoteAddKind, URL: url}, remoteAddToken)
		if err != nil {
			return err
		}
		if !remoteAddNoVerify {
			log.Infof("probing %s", url)
			p, ok := bk.(pingable)
			if !ok {
				return fmt.Errorf("kind %q cannot be probed", remoteAddKind)
			}
			if err := p.Ping(cmd.Context()); err != nil {
				return fmt.Errorf("%w\n(use --no-verify to add it anyway)", err)
			}
		}

		if err := config.AddRemote(config.Remote{Name: name, Kind: remoteAddKind, URL: url}); err != nil {
			return err
		}
		tokenStored := false
		if remoteAddToken != "" {
			if err := config.SaveRemoteToken(name, remoteAddToken, noKeychain); err != nil {
				return fmt.Errorf("remote added, but storing its token failed: %w", err)
			}
			tokenStored = true
		}

		caps := bk.Capabilities()
		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, output.RemoteAddResult{
				Name: name, Kind: remoteAddKind, URL: url,
				Sandbox: caps.Sandbox, TokenStored: tokenStored,
			})
		}
		log.Infof("added remote %q (%s)", name, url)
		if caps.Sandbox {
			log.Infof("this is a sandbox instance — DOIs it mints (prefix 10.5072) do not resolve")
		}
		if !tokenStored && config.LoadRemoteToken(name) == "" {
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
	remoteAddCmd.Flags().StringVar(&remoteAddKind, "kind", "invenio", "Backend kind (invenio, figshare, dataverse)")
	remoteAddCmd.Flags().BoolVar(&remoteAddNoVerify, "no-verify", false, "Skip probing the URL before adding")
	remoteAddCmd.Flags().StringVar(&remoteAddToken, "token-value", "", "API token to store for this remote")
	remoteAddCmd.Flags().BoolVar(&noKeychain, "no-keychain", false, "Store the token in a file instead of the OS keychain")
	remoteCmd.AddCommand(remoteAddCmd, remoteLsCmd, remoteRmCmd)
	rootCmd.AddCommand(remoteCmd)
}
