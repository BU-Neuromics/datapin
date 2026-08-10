package cmd

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
)

// isInteractive reports whether stdin is a real terminal, i.e. whether a prompt
// can actually be answered. Uses term.IsTerminal rather than a char-device check
// so /dev/null and pipes (e.g. in CI) correctly read as non-interactive.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// printPushPlan writes the rich, per-file push description used by the
// confirmation gate: a header naming the project title and visibility (with a
// loud warning when the target is public), one line per file, and a summary.
func printPushPlan(w io.Writer, node *client.Node, projectID string, plans []entryPlan, states []manifest.FileState) {
	title := projectID
	visibility := "VISIBILITY UNKNOWN"
	public := false
	if node != nil {
		if node.Attributes.Title != "" {
			title = node.Attributes.Title
		}
		public = node.Attributes.Public
		if public {
			visibility = "PUBLIC"
		} else {
			visibility = "PRIVATE"
		}
	}

	visStyled := output.Dim(visibility)
	if public {
		visStyled = output.RedBold(visibility)
	}
	fmt.Fprintf(w, "%s %q (%s, %s)\n", output.Bold("Pushing "+fmt.Sprint(len(plans))+" file(s) to"), title, projectID, visStyled)
	if public {
		fmt.Fprintln(w, output.RedBold("  ⚠  WARNING: this project is PUBLIC — pushed files are visible to everyone."))
	}
	for i := range plans {
		fmt.Fprintln(w, pushItemLine(plans[i], states[i]))
	}
	fmt.Fprintf(w, "Summary: %s\n", output.Bold(summarizePush(states)))
}

// pushLabelStyle colors a push action label in the plan.
func pushLabelStyle(state manifest.FileState) func(string) string {
	switch state {
	case manifest.StateNotPushed:
		return output.Green // new
	case manifest.StateAheadOfManifest:
		return output.Cyan // update
	case manifest.StateRemoteNewer, manifest.StateBehind:
		return output.Yellow // rollback
	case manifest.StateDivergent:
		return output.RedBold
	case manifest.StateMissing:
		return output.Red
	default:
		return output.Dim // pin / unchanged
	}
}

// pushItemLine renders one file's line in the push plan.
func pushItemLine(p entryPlan, state manifest.FileState) string {
	label := pushActionLabel(state)
	target := p.proj + ":" + p.entry.Remote
	var size int64 = -1
	if fi, err := os.Stat(p.localAbs); err == nil {
		size = fi.Size()
	}
	sizeStr := "unknown size"
	if size >= 0 {
		sizeStr = output.FormatSize(size)
	}
	latest := latestRemoteVersionInfo(p.remoteVersions)

	var detail string
	switch state {
	case manifest.StateNotPushed:
		detail = fmt.Sprintf("%s, md5 %s", sizeStr, shortMD5(p.localMD5))
	case manifest.StateAheadOfManifest:
		detail = fmt.Sprintf("v%d→v%d, %s, md5 %s→%s", p.entry.Version, p.entry.Version+1, sizeStr, shortMD5(p.entry.MD5), shortMD5(p.localMD5))
	case manifest.StatePinOnly, manifest.StateInSync:
		detail = fmt.Sprintf("identical to remote v%d", latest.Version)
	case manifest.StateRemoteNewer, manifest.StateBehind:
		detail = fmt.Sprintf("would roll back over remote v%d", latest.Version)
	case manifest.StateDivergent:
		detail = fmt.Sprintf("local md5 %s vs remote v%d md5 %s", shortMD5(p.localMD5), latest.Version, shortMD5(latest.MD5))
	case manifest.StateMissing:
		detail = "missing locally"
	}
	labelCell := pushLabelStyle(state)(fmt.Sprintf("%-9s", label))
	return fmt.Sprintf("  %s %s → %s  %s", labelCell, p.entry.Local, target, output.Dim("("+detail+")"))
}
