package main

// CLI-LEAF DOCUMENTATION COVERAGE.
//
// # Why this file exists
//
// Three places in the repo — two rows in `.claude/reference/update-demand.md`,
// one in `docs/src/internals/update-demand.md` and one in
// `.claude/agents/pulse-docs-skills.md` — cited `TestSkillsCoverAllCliLeaves`
// as the enforcer for "a new CLI leaf must be documented". No such test
// existed. CLAUDE.md's gate list is validated ONE WAY ONLY (every real
// `TestSkillsCover*` must be listed in CLAUDE.md; a listed-but-nonexistent
// gate passes silently), so the claim survived unchallenged through several
// efforts that added leaves.
//
// A doc claiming enforcement that does not exist is worse than no claim: it
// tells the next author the gate will catch them, so they stop checking. This
// file makes the claim true.
//
// # What it enforces, exactly
//
// Every ACTIONABLE command in the binary's command tree — a command with a
// non-nil Action, which is the definition of "a thing a user can run", and
// which deliberately includes `pulse convert` even though it also has a
// `predict` subcommand — must have its full invocation path named verbatim
// somewhere under `skills/` or `docs/src/`.
//
// It does NOT check that the mention is any good. It cannot: prose quality is
// not machine-checkable, and a gate that pretended otherwise would be the same
// kind of overclaim this file was written to remove. What it does catch is the
// case that actually happens — a leaf lands and NOTHING anywhere mentions it.
// The command index in `docs/src/cli/flags.md` is the natural home for a leaf
// with no dedicated page.
//
// # Why the command tree and not a hand-written list
//
// A hand-written list of leaves would drift exactly the way the phantom gate
// drifted. `buildApp()` is what the binary actually runs, so it cannot.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	cli "github.com/urfave/cli/v3"
)

// cliLeaves returns every runnable command path in the binary's command tree,
// sorted, in `pulse <group> <leaf>` form.
func cliLeaves() []string {
	var out []string
	var walk func(prefix string, cmds []*cli.Command)
	walk = func(prefix string, cmds []*cli.Command) {
		for _, c := range cmds {
			path := prefix + " " + c.Name
			if c.Action != nil {
				out = append(out, path)
			}
			walk(path, c.Commands)
		}
	}
	walk("pulse", buildApp().Commands)
	sort.Strings(out)
	return out
}

// docCorpus concatenates every markdown file under the given repo-relative
// roots. Tests run from the package directory, so the roots are reached via
// `../..`.
func docCorpus(t *testing.T, roots ...string) string {
	t.Helper()
	var b strings.Builder
	for _, root := range roots {
		dir := filepath.Join("..", "..", root)
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".md") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			b.Write(data)
			b.WriteByte('\n')
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	if b.Len() == 0 {
		t.Fatalf("documentation corpus is empty for roots %v", roots)
	}
	return b.String()
}

// TestSkillsCoverAllCliLeaves verifies that every runnable CLI leaf is named
// in the documentation surface (`skills/` plus `docs/src/`).
//
// This is a non-skippable CI gate.
func TestSkillsCoverAllCliLeaves(t *testing.T) {
	leaves := cliLeaves()
	if len(leaves) == 0 {
		t.Fatal("found no runnable CLI leaves; the command tree walk is broken")
	}

	corpus := docCorpus(t, "skills", "docs/src")

	for _, leaf := range leaves {
		if !strings.Contains(corpus, leaf) {
			t.Errorf("CLI leaf %q is named in no file under skills/ or docs/src/; "+
				"add it to the command index in docs/src/cli/flags.md at minimum", leaf)
		}
	}
}
