// Package docgen renders datapin's cobra tree into a man page.
//
// It deliberately does not use cobra/doc's GenManTree: that pulls in
// go-md2man and blackfriday (dependencies the datapin binary has no other
// use for) and emits one file per command — sixty-odd pages for this tree,
// when what an HPC user reaches for is a single `man datapin`.
package docgen

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Man renders the whole command tree as one roff man page. version and date
// are stamped into the .TH header; date should be ISO-8601 (the caller owns
// the clock so this stays pure and testable).
func Man(root *cobra.Command, version, date string) []byte {
	var b bytes.Buffer

	name := root.Name()
	fmt.Fprintf(&b, ".TH %s 1 %q %q %q\n", strings.ToUpper(name), date,
		fmt.Sprintf("%s %s", name, version), "User Commands")

	section(&b, "NAME")
	fmt.Fprintf(&b, "%s \\- %s\n", esc(name), esc(root.Short))

	section(&b, "SYNOPSIS")
	fmt.Fprintf(&b, ".B %s\n", esc(name))
	b.WriteString("[\\fIglobal options\\fR] \\fIcommand\\fR [\\fIarguments\\fR]\n")

	section(&b, "DESCRIPTION")
	writeText(&b, firstNonEmpty(root.Long, root.Short))

	section(&b, "OPTIONS")
	b.WriteString("These apply to every command.\n")
	writeFlags(&b, root.PersistentFlags())

	section(&b, "COMMANDS")
	for _, cmd := range visibleCommands(root) {
		writeCommand(&b, cmd)
	}

	section(&b, "ENVIRONMENT")
	writeVarList(&b, [][2]string{
		{"DATAPIN_TOKEN_<NAME>", "API token for the archive or workspace remote named <NAME> (uppercased). Highest priority in the per-remote token ladder, ahead of the OS keychain and the token file."},
		{"OSF_TOKEN", "Personal access token for the legacy OSF surface. A separate ladder from the per-remote tokens above."},
		{"DATAPIN_SFTP_PASSWORD", "Password for an sftp workspace remote, used only after the SSH agent and key files are tried."},
		{"AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY", "Fallback credentials for an s3 workspace remote when its per-remote token is unset."},
		{"DATAPIN_FUJI_URL, DATAPIN_FUJI_USER, DATAPIN_FUJI_PASS", "F-UJI server and credentials for datapin check \\-\\-fair."},
		{"DATAPIN_NO_UPDATE_CHECK", "Set to any value to suppress the daily new-release notice."},
		{"NO_COLOR", "Disable colorized output, as does \\-\\-color=never."},
		{"GITHUB_TOKEN, GH_TOKEN", "Token for datapin site publish (the gh-pages push and the Pages toggle)."},
	})

	section(&b, "FILES")
	writeVarList(&b, [][2]string{
		{".datapin/datapin.toml", "The project manifest: datasets, their pins, and the documentation site. Committed to version control; found by walking up from the working directory."},
		{"~/.config/datapin/config.toml", "Named remotes and their probed capabilities. Never contains tokens, so it is safe to commit or share."},
		{"~/.config/datapin/tokens/<name>", "Per-remote token file (mode 0600), used when no keychain is available."},
		{"~/.config/datapin/token", "Legacy OSF token file."},
		{"~/.config/datapin/update_check.json", "Cache for the once-a-day release check."},
	})

	section(&b, "EXIT STATUS")
	writeVarList(&b, [][2]string{
		{"0", "Success. For datapin check, no errors (warnings are allowed); for datapin status, everything in sync."},
		{"1", "An error, or a deliberate non-zero report: metadata errors from check, anything not in sync from status, an AHEAD_OF_MANIFEST entry from sync, or remaining metadata TODOs from migrate."},
	})

	section(&b, "SEE ALSO")
	b.WriteString("Task guides and troubleshooting: \\fBhttps://github.com/BU\\-Neuromics/datapin/tree/main/docs\\fR\n")
	b.WriteString(".PP\n")
	b.WriteString("Source, issues, and the command reference: \\fBhttps://github.com/BU\\-Neuromics/datapin\\fR\n")

	return b.Bytes()
}

// visibleCommands flattens the tree depth-first, skipping hidden commands
// and cobra's own generated help/completion scaffolding.
func visibleCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		for _, c := range parent.Commands() {
			if c.Hidden || c.Name() == "help" {
				continue
			}
			out = append(out, c)
			walk(c)
		}
	}
	walk(root)
	return out
}

// writeCommand renders one command as an .SS subsection titled with its
// full path, so `datapin wiki ls` reads as it is typed.
func writeCommand(b *bytes.Buffer, cmd *cobra.Command) {
	fmt.Fprintf(b, ".SS %s\n", esc(cmd.CommandPath()+useArgs(cmd)))
	if cmd.Short != "" {
		writeText(b, cmd.Short)
	}
	if long := cmd.Long; long != "" && long != cmd.Short {
		b.WriteString(".PP\n")
		writeText(b, long)
	}
	if ex := strings.TrimRight(cmd.Example, "\n"); ex != "" {
		b.WriteString(".PP\n")
		writeLiteral(b, ex)
	}
	writeFlags(b, cmd.NonInheritedFlags())
}

// useArgs recovers the argument spec from a Use string ("publish [<slug>]"
// → " [<slug>]"), which cobra keeps nowhere else.
func useArgs(cmd *cobra.Command) string {
	_, rest, found := strings.Cut(cmd.Use, " ")
	if !found || strings.TrimSpace(rest) == "" {
		return ""
	}
	return " " + strings.TrimSpace(rest)
}

// writeFlags renders a flag set as a roff definition list. Nothing is
// emitted for an empty set, so commands without flags stay clean.
func writeFlags(b *bytes.Buffer, flags *pflag.FlagSet) {
	var lines []string
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		names := "\\fB\\-\\-" + esc(f.Name) + "\\fR"
		if f.Shorthand != "" {
			names = "\\fB\\-" + esc(f.Shorthand) + "\\fR, " + names
		}
		if f.Value.Type() != "bool" && f.Value.Type() != "count" {
			names += " \\fI" + esc(f.Value.Type()) + "\\fR"
		}
		usage := esc(f.Usage)
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "[]" {
			usage += fmt.Sprintf(" (default %s)", esc(f.DefValue))
		}
		lines = append(lines, fmt.Sprintf(".TP\n%s\n%s\n", names, usage))
	})
	for _, l := range lines {
		b.WriteString(l)
	}
}

// writeVarList renders term/description pairs as a definition list. Terms
// are pre-escaped by the caller so they can carry deliberate roff.
func writeVarList(b *bytes.Buffer, pairs [][2]string) {
	for _, p := range pairs {
		fmt.Fprintf(b, ".TP\n\\fB%s\\fR\n%s\n", p[0], p[1])
	}
}

// writeText emits prose, mapping blank lines to paragraph breaks so a
// multi-paragraph Long description does not collapse into one block. An
// indented paragraph is a literal block — several Long descriptions embed a
// command list that way, and filled prose would run those lines together.
func writeText(b *bytes.Buffer, s string) {
	paras := strings.Split(strings.TrimSpace(s), "\n\n")
	for i, p := range paras {
		if i > 0 {
			b.WriteString(".PP\n")
		}
		if isIndentedBlock(p) {
			writeLiteral(b, strings.Trim(p, "\n"))
			continue
		}
		fmt.Fprintf(b, "%s\n", esc(strings.TrimSpace(p)))
	}
}

// isIndentedBlock reports whether every non-blank line of a paragraph is
// indented — the shape of an embedded example or command list.
func isIndentedBlock(p string) bool {
	lines := strings.Split(strings.Trim(p, "\n"), "\n")
	indented := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if !strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "\t") {
			return false
		}
		indented++
	}
	return indented > 0
}

// writeLiteral emits text with line breaks and spacing preserved (.nf/.fi),
// for examples and command lines.
func writeLiteral(b *bytes.Buffer, s string) {
	b.WriteString(".nf\n")
	fmt.Fprintf(b, "%s\n", esc(s))
	b.WriteString(".fi\n")
}

// esc makes arbitrary text safe as roff body content: backslashes become
// \e and hyphens become \- (so they render as hyphen-minus rather than as
// soft hyphens). Lines are then protected with a zero-width \& where roff
// would otherwise reinterpret them: a leading dot or apostrophe reads as a
// macro, and leading whitespace is significant to the filler.
func esc(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\e")
	s = strings.ReplaceAll(s, "-", "\\-")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if needsGuard(l) {
			lines[i] = "\\&" + l
		}
	}
	return strings.Join(lines, "\n")
}

// needsGuard reports whether roff would reinterpret a line's first
// character rather than print it.
func needsGuard(line string) bool {
	for _, prefix := range []string{".", "'", " ", "\t"} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func section(b *bytes.Buffer, name string) {
	fmt.Fprintf(b, ".SH %s\n", name)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
