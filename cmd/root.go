package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"

	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/update"
)

// version is set at build time via -ldflags "-X github.com/BU-Neuromics/datapin/cmd.version=vX.Y.Z"
var version = "dev"

// Global flag values shared across commands.
var (
	flagToken    string
	flagOutput   string
	flagQuiet    bool
	flagColor    string
	flagVerbose  int
	flagProgress bool
)

var rootCmd = &cobra.Command{
	Use:          "datapin",
	Short:        "Pin, sync, and publish research data",
	Long:         "datapin — pin, sync, and publish research data. Currently syncs against the Open Science Framework (osf.io); archival publication backends are on the way.",
	SilenceUsage: true,
	Version:      version,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := config.InitViper(); err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if flagOutput != "text" && flagOutput != "json" {
			return fmt.Errorf("--output must be 'text' or 'json', got %q", flagOutput)
		}
		switch flagColor {
		case "auto", "always", "never":
		default:
			return fmt.Errorf("--color must be 'auto', 'always', or 'never', got %q", flagColor)
		}
		if err := reconcileVerbosity(flagQuiet, flagVerbose); err != nil {
			return err
		}
		output.InitColor(flagColor, flagOutput == "json", flagQuiet)
		log.Init(flagVerbose, logQuiet(flagQuiet, flagOutput == "json", flagVerbose))
		return nil
	},
}

// reconcileVerbosity rejects contradictory verbosity flags. Pure/testable.
func reconcileVerbosity(quiet bool, verbosity int) error {
	if quiet && verbosity > 0 {
		return fmt.Errorf("--quiet and --verbose cannot be combined")
	}
	return nil
}

// logQuiet reports whether activity logging should drop to errors-only. JSON
// output silences logs by default (stdout stays pure JSON) unless the user
// explicitly asked for verbosity. Pure/testable.
func logQuiet(quiet, jsonMode bool, verbosity int) bool {
	return quiet || (jsonMode && verbosity == 0)
}

// showProgressBar reports whether a live progress bar should be drawn. Bars are
// opt-in via --progress-bar and only make sense on an interactive stderr with
// human (non-JSON, non-quiet) output. Pure/testable.
func showProgressBar(progressFlag, quiet, jsonMode, stderrTTY bool) bool {
	return progressFlag && !quiet && !jsonMode && stderrTTY
}

// progressBarEnabled applies showProgressBar to the current global flag state.
func progressBarEnabled() bool {
	return showProgressBar(flagProgress, flagQuiet, flagOutput == "json",
		term.IsTerminal(int(os.Stderr.Fd())))
}

// exitCodeError is an error that carries a specific exit code without printing
// an additional message. Returned by commands like datapin status.
type exitCodeError struct {
	code int
	msg  string
}

func (e *exitCodeError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return fmt.Sprintf("exit code %d", e.code)
}

// Execute runs the root command and exits with a non-zero code on error.
// It installs a signal-aware context so that Ctrl-C (SIGINT) or SIGTERM
// cancels in-flight HTTP requests and aborts long transfers cleanly.
func Execute() {
	rootCmd.SilenceErrors = true // we print errors ourselves below

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := rootCmd.ExecuteContext(ctx)
	canceled := ctx.Err() != nil
	stop() // release signal handling before the (best-effort) update check

	// Cached, best-effort "new release available" notice. Skipped after a Ctrl-C
	// and gated internally on TTY / --quiet / --output=json / DATAPIN_NO_UPDATE_CHECK.
	if !canceled {
		update.MaybeNotify(version, flagOutput == "json", flagQuiet)
	}

	if err != nil {
		// exitCodeError: use the specified exit code with no message printed.
		var exitErr *exitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		fmt.Fprintln(os.Stderr, output.Red("Error:"), err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagToken, "token", "", "OSF personal access token (overrides env/config/keychain)")
	rootCmd.PersistentFlags().StringVar(&flagOutput, "output", "text", "Output format: text or json")
	rootCmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "Suppress progress and non-error output")
	rootCmd.PersistentFlags().StringVar(&flagColor, "color", "auto", "Colorize output: auto, always, or never")
	rootCmd.PersistentFlags().CountVarP(&flagVerbose, "verbose", "v", "Increase log verbosity (-v, -vv, -vvv)")
	rootCmd.PersistentFlags().BoolVarP(&flagProgress, "progress-bar", "p", false, "Show live progress bars for transfers (default: log lines)")

	// Bind OSF_TOKEN env var via viper so --token and OSF_TOKEN work the same way.
	viper.SetEnvPrefix("OSF")
	viper.AutomaticEnv()
}
