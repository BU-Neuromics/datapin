package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage OSF authentication",
}

// --- auth login ---

var noKeychain bool

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Save an OSF personal access token",
	Long: `Prompts for an OSF personal access token and stores it securely.

To create a token, visit: https://osf.io/settings/tokens/

The token is stored in the OS keychain by default.
On headless/HPC systems, use --no-keychain to write to a token file instead.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLogin(cmd.Context(), noKeychain)
	},
}

// runLogin reads a token (interactively or from stdin), validates it against the
// API, and stores it. Shared by `datapin auth login` and `datapin onboard`.
func runLogin(ctx context.Context, noKeychain bool) error {
	token, err := readToken()
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("no token provided")
	}

	// Validate by calling the API.
	log.Infof("validating token")
	c := client.New(token)
	user, err := c.GetCurrentUser(ctx)
	if err != nil {
		if apiErr, ok := err.(*client.APIError); ok && apiErr.StatusCode == 401 {
			return fmt.Errorf("invalid token: authentication failed")
		}
		return fmt.Errorf("validating token: %w", err)
	}

	if err := config.SaveToken(token, noKeychain); err != nil {
		return fmt.Errorf("saving token: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Logged in as %s (%s)\n", user.Attributes.FullName, user.ID)
	return nil
}

func readToken() (string, error) {
	// If stdin is a terminal, read interactively without echo.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Enter your OSF personal access token: ")
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr) // newline after hidden input
		if err != nil {
			return "", fmt.Errorf("reading token: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	// Non-interactive: read from stdin (e.g. piped from a script).
	var token string
	_, err := fmt.Fscan(os.Stdin, &token)
	if err != nil {
		return "", fmt.Errorf("reading token from stdin: %w", err)
	}
	return strings.TrimSpace(token), nil
}

// --- auth status ---

var authStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Show current authentication status",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		token := config.LoadToken(flagToken)
		if token == "" {
			fmt.Fprintln(os.Stdout, "Not logged in (unauthenticated mode — public projects only)")
			return nil
		}

		c := client.New(token)
		user, err := c.GetCurrentUser(cmd.Context())
		if err != nil {
			if apiErr, ok := err.(*client.APIError); ok && apiErr.StatusCode == 401 {
				return fmt.Errorf("stored token is invalid; run 'datapin auth login' to re-authenticate")
			}
			return fmt.Errorf("checking auth: %w", err)
		}

		src := config.TokenSource(flagToken)
		fmt.Fprintf(os.Stdout, "Logged in as: %s (%s)\n", user.Attributes.FullName, user.ID)
		if user.Attributes.EmailPrimary != "" {
			fmt.Fprintf(os.Stdout, "Email:        %s\n", user.Attributes.EmailPrimary)
		}
		if src != "" {
			fmt.Fprintf(os.Stdout, "Token from:   %s\n", src)
		}
		return nil
	},
}

// --- auth logout ---

var authLogoutCmd = &cobra.Command{
	Use:          "logout",
	Short:        "Remove stored OSF token",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		warning, err := config.DeleteToken()
		if err != nil {
			return fmt.Errorf("removing token: %w", err)
		}
		if warning != "" {
			log.Warnf("%s", warning)
		}
		fmt.Fprintln(os.Stdout, "Logged out.")
		return nil
	},
}

func init() {
	authLoginCmd.Flags().BoolVar(&noKeychain, "no-keychain", false, "Store token in config file instead of OS keychain")
	authCmd.AddCommand(authLoginCmd, authStatusCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}
