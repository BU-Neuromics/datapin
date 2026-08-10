package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The agent skill (skills/datapin/SKILL.md) is a shipped artifact: it is installed
// into coding agents via skills.sh, and its frontmatter `description` is what
// decides whether the skill loads for a given task at all. Nothing in CI used to
// compare it against the CLI, and it drifted silently — the entire `datapin wiki`
// command group shipped in v1.9.0 and went undocumented for two releases,
// including in the description, so agents asked to work on an OSF wiki were
// never offered the skill in the first place.
//
// These tests close that loop by walking the real cobra command tree, so a new
// command or flag cannot land without either documenting it or recording a
// deliberate omission below.

// skillPath locates the skill relative to this package.
func skillPath() string { return filepath.Join("..", "skills", "datapin", "SKILL.md") }

func readSkill(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(skillPath())
	if err != nil {
		t.Fatalf("reading the agent skill: %v", err)
	}
	return string(data)
}

// skillFrontmatter returns the YAML frontmatter block, which carries the
// `description` that gates whether an agent loads the skill.
func skillFrontmatter(t *testing.T) string {
	t.Helper()
	body := readSkill(t)
	if !strings.HasPrefix(body, "---\n") {
		t.Fatal("the skill must open with a YAML frontmatter block")
	}
	rest := body[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		t.Fatal("unterminated frontmatter in the skill")
	}
	return rest[:end]
}

// walkCommands calls fn for every user-facing command in the tree, skipping the
// root itself, hidden commands, and cobra's generated `help`/`completion`.
func walkCommands(root *cobra.Command, fn func(*cobra.Command)) {
	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		fn(c)
		walkCommands(c, fn)
	}
}

// undocumentedCommands are commands deliberately absent from the skill, with the
// reason. Keep this list short and justified: an entry here is a decision that
// agents should not reach for the command, not a TODO.
var undocumentedCommands = map[string]string{}

// undocumentedFlags are flags deliberately absent from the skill, keyed by
// "<command path> <flag>".
var undocumentedFlags = map[string]string{}

// Every command must be named in the skill body, or explicitly excused.
func TestSkill_DocumentsEveryCommand(t *testing.T) {
	skill := readSkill(t)

	walkCommands(rootCmd, func(c *cobra.Command) {
		path := c.CommandPath() // e.g. "datapin wiki add"
		if reason, excused := undocumentedCommands[path]; excused {
			t.Logf("%s is deliberately undocumented: %s", path, reason)
			return
		}
		if !strings.Contains(skill, path) {
			t.Errorf("%q is not mentioned in %s — document it, or add it to undocumentedCommands with a reason",
				path, skillPath())
		}
	})
}

// Every flag a command accepts must appear in the skill, or be excused. This is
// the check that catches the subtler drift: a flag that exists but is
// undocumented is invisible to an agent, and one whose meaning changed (as
// --force did when it stopped meaning "authorize a rollback" on sync) is worse
// than invisible.
func TestSkill_DocumentsEveryFlag(t *testing.T) {
	skill := readSkill(t)

	check := func(path string, f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		key := path + " --" + f.Name
		if reason, excused := undocumentedFlags[key]; excused {
			t.Logf("%s is deliberately undocumented: %s", key, reason)
			return
		}
		if !strings.Contains(skill, "--"+f.Name) {
			t.Errorf("%s accepts --%s, which is not mentioned in %s — document it, or add it to undocumentedFlags with a reason",
				path, f.Name, skillPath())
		}
	}

	// Global (root persistent) flags.
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { check(rootCmd.CommandPath(), f) })
	// Per-command flags, excluding those inherited from the root.
	walkCommands(rootCmd, func(c *cobra.Command) {
		c.NonInheritedFlags().VisitAll(func(f *pflag.Flag) { check(c.CommandPath(), f) })
	})
}

// The frontmatter `description` is the skill's trigger: an agent decides from it
// alone whether to load the skill. A command group missing here is worse than a
// missing body section, because the skill is never consulted at all.
func TestSkill_DescriptionMentionsEveryCommandGroup(t *testing.T) {
	fm := skillFrontmatter(t)
	if !strings.Contains(fm, "description:") {
		t.Fatal("the skill frontmatter must carry a description")
	}

	// Deliberate omissions from the trigger text, with reasons.
	excused := map[string]string{
		"onboard": "TTY-only interactive wizard; the skill steers agents to init+add+sync instead",
	}

	for _, c := range rootCmd.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		if reason, ok := excused[c.Name()]; ok {
			t.Logf("%q is deliberately absent from the description: %s", c.Name(), reason)
			continue
		}
		if !strings.Contains(fm, c.Name()) {
			t.Errorf("top-level command %q is not named in the skill's frontmatter description, "+
				"so the skill will not be triggered for tasks about it", c.Name())
		}
	}
}

// The skill must declare a version so consumers can tell editions apart.
func TestSkill_HasVersion(t *testing.T) {
	if fm := skillFrontmatter(t); !strings.Contains(fm, "version:") {
		t.Error("the skill frontmatter must carry metadata.version")
	}
}
