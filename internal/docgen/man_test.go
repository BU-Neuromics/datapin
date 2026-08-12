package docgen_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/docgen"
)

// testTree mirrors the shape of datapin's real cobra tree: a root with
// persistent flags, a leaf command with its own flags, a command group with
// a subcommand, and a hidden command that must not be documented.
func testTree() *cobra.Command {
	root := &cobra.Command{
		Use:   "datapin",
		Short: "Pin, sync, and publish research data",
		Long:  "datapin publishes research data to a FAIR archive with a DOI.",
	}
	root.PersistentFlags().String("output", "text", "Output format: text or json")
	root.PersistentFlags().CountP("verbose", "v", "Increase log verbosity")

	publish := &cobra.Command{
		Use:     "publish [<slug>]",
		Short:   "Publish a dataset — mints a DOI",
		Long:    "Promote a dataset's files to a published record.\nPublishing is PERMANENT.",
		Example: "  datapin publish counts --dry-run",
	}
	publish.Flags().Bool("dry-run", false, "Show the plan without uploading")

	wiki := &cobra.Command{Use: "wiki", Short: "Manage OSF wiki pages"}
	wiki.AddCommand(&cobra.Command{Use: "ls <project>", Short: "List wiki pages"})

	hidden := &cobra.Command{Use: "secret", Short: "Internal", Hidden: true}

	root.AddCommand(publish, wiki, hidden)
	return root
}

func TestMan_Structure(t *testing.T) {
	got := string(docgen.Man(testTree(), "1.2.3", "2026-08-12"))

	// A man page is judged by roff a viewer can render: the header macro
	// must come first and carry the version and date.
	if !strings.HasPrefix(got, `.TH DATAPIN 1 "2026-08-12" "datapin 1.2.3"`) {
		t.Errorf("missing or malformed .TH header:\n%s", firstLines(got, 3))
	}
	for _, want := range []string{
		".SH NAME",
		".SH SYNOPSIS",
		".SH DESCRIPTION",
		".SH OPTIONS",
		".SH COMMANDS",
		".SH ENVIRONMENT",
		".SH FILES",
		".SH SEE ALSO",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing section %s", want)
		}
	}
	if !strings.Contains(got, `datapin \- Pin, sync, and publish research data`) {
		t.Error("NAME section must be `name \\- short description`")
	}
}

func TestMan_DocumentsEveryVisibleCommandAndFlag(t *testing.T) {
	raw := string(docgen.Man(testTree(), "1.2.3", "2026-08-12"))
	got := plain(raw) // what a reader sees, not the roff that produces it

	// Every visible command, including nested ones, by full path.
	for _, want := range []string{"datapin publish", "datapin wiki", "datapin wiki ls"} {
		if !strings.Contains(got, want) {
			t.Errorf("command %q is not documented", want)
		}
	}
	// Global and per-command flags alike.
	for _, want := range []string{"--output", "--verbose", "-v", "--dry-run"} {
		if !strings.Contains(got, want) {
			t.Errorf("flag %q is not documented", want)
		}
	}
	// A command's long description and example carry the real guidance.
	if !strings.Contains(got, "Publishing is PERMANENT") {
		t.Error("a command's long description must reach the page")
	}
	if !strings.Contains(got, "datapin publish counts --dry-run") {
		t.Error("a command's example must reach the page")
	}
	// Hidden commands are not user-facing surface.
	if strings.Contains(got, "secret") {
		t.Error("hidden commands must not be documented")
	}
}

// plain undoes the roff escaping and font macros so assertions can be
// written the way the text reads on screen.
func plain(s string) string {
	for _, r := range []struct{ from, to string }{
		{`\fB`, ""}, {`\fI`, ""}, {`\fR`, ""},
		{`\&`, ""}, {`\-`, "-"}, {`\e`, `\`},
	} {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	return s
}

// Several real Long descriptions embed an indented command list. Filled
// prose would run those lines together into one paragraph, so an indented
// block must be emitted literally.
func TestMan_PreservesIndentedBlocks(t *testing.T) {
	root := &cobra.Command{
		Use:   "datapin",
		Short: "Pin, sync, and publish research data",
		Long: "Group files into a dataset, then publish it.\n\n" +
			"  datapin onboard        guided setup\n" +
			"  datapin publish        mint a DOI\n\n" +
			"Trailing prose here.",
	}
	got := string(docgen.Man(root, "1.0.0", "2026-08-12"))

	block := ".nf\n\\&  datapin onboard        guided setup\n\\&  datapin publish        mint a DOI\n.fi\n"
	if !strings.Contains(got, block) {
		t.Errorf("indented block was not emitted literally.\ngot:\n%s", got)
	}
	// The prose around it is still filled prose, not literal.
	if !strings.Contains(got, "Group files into a dataset, then publish it.\n") {
		t.Error("leading prose paragraph missing")
	}
	if !strings.Contains(got, "Trailing prose here.\n") {
		t.Error("trailing prose paragraph missing")
	}
}

func TestMan_EscapesRoffSpecials(t *testing.T) {
	root := &cobra.Command{
		Use:   "datapin",
		Short: "handles a-dash, a\\backslash and a line",
		Long:  ".this line starts with a dot\n'and this with a quote",
	}
	got := string(docgen.Man(root, "1.0.0", "2026-08-12"))

	// A literal backslash would otherwise start an escape sequence.
	if strings.Contains(got, `a\backslash`) {
		t.Error("literal backslashes must be escaped as \\e")
	}
	// A hyphen must be \- so it is not rendered as a soft hyphen.
	if !strings.Contains(got, `a\-dash`) {
		t.Error("hyphens in text must be escaped as \\-")
	}
	// roff treats a leading . or ' as a macro; those lines need \& first.
	for _, bad := range []string{"\n.this line", "\n'and this"} {
		if strings.Contains(got, bad) {
			t.Errorf("a line beginning with %q must be protected with \\&", bad[1:2])
		}
	}
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}
