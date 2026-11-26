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
// The generated command and flag reference (`docs/src/cli/reference.md`,
// rendered from the tree by `make docs`) gives every leaf a section.
//
// # Why the command tree and not a hand-written list
//
// A hand-written list of leaves would drift exactly the way the phantom gate
// drifted. `buildApp()` is what the binary actually runs, so it cannot.
//
// # The direction the original check could not see
//
// "Every leaf is documented" is one-directional, and the direction it misses
// is the one that breaks users: DELETE a leaf's registration from buildApp()
// and the walk simply yields one fewer leaf, so the gate stays green while
// the documented command answers "command not found". Proven by unmounting
// `pcli.WidenCommand()` and watching the gate pass.
//
// The closing check runs the other way: every leaf section of the
// generated reference (docs/src/cli/reference.md) must be an ACTIONABLE
// command in the tree. The page is parsed, never hardcoded — a hardcoded
// list is the same drifting artefact this file exists to remove — and a
// stale page fails TestCLIReferenceCurrent as well.

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	cli "github.com/urfave/cli/v3"

	pcli "github.com/frankbardon/pulse/internal/cli"

	"github.com/frankbardon/pulse/internal/doccoverage"
	"github.com/frankbardon/pulse/internal/docgen"
)

// cliLeaves returns every runnable command path in the binary's command tree,
// sorted, in `pulse <group> <leaf>` form. The tree is the one the binary
// runs AFTER urfave/cli's setup (completionTree), so the framework-mounted
// `pulse completion <shell>` leaves count too; urfave's `help` commands do
// not (pcli.WalkCommands skips them).
func cliLeaves() []string {
	var out []string
	pcli.WalkCommands(completionTree(), func(path []string, _, c *cli.Command) {
		if c.Action != nil {
			out = append(out, strings.Join(path, " "))
		}
	})
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
			// The generated coverage matrix names every leaf; counting it
			// would make this gate vacuous.
			if filepath.ToSlash(p) == "../../"+doccoverage.PagePath {
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

// referenceLeaves returns every invocation path with a leaf section
// (a "### `pulse …`" heading) under `## Commands` on the generated reference,
// sorted.
func referenceLeaves(t *testing.T) []string {
	t.Helper()
	path := filepath.Join("..", "..", filepath.FromSlash(docgen.CLIReferencePath))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var out []string
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			inSection = strings.TrimSpace(line) == "## Commands"
			continue
		}
		if m := refLeafHeading.FindStringSubmatch(line); inSection && m != nil {
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// TestSkillsCoverAllCliLeaves holds the CLI leaf surface and its
// documentation in a two-way correspondence.
//
// The rule, precisely: the leaf sections of the generated reference
// (docs/src/cli/reference.md, "### `pulse …`" headings under `## Commands`) and the set
// of ACTIONABLE commands in buildApp() (a command with a non-nil Action,
// `pulse convert` included even though it also carries a subcommand) are
// the SAME SET. Group nodes that carry no Action — `pulse api`,
// `pulse shard`, `pulse index` — are leaves of neither set.
//
// Three checks, each with its own job:
//
//   - mounted   — every reference section is a real actionable command.
//     This is the direction that catches a leaf whose registration was
//     deleted (or never landed) while its documentation stayed.
//   - indexed   — every actionable command has a section.
//   - documented — every leaf is named SOMEWHERE under skills/ or docs/src/.
//     Implied by `indexed` while the reference lives under docs/src/, and
//     kept because it states the actual contract the reference docs cite.
//
// It does NOT check that the mention is any good — prose quality is not
// machine-checkable, and a gate that pretended otherwise would be the same
// kind of overclaim this file was written to remove.
//
// This is a non-skippable CI gate.
func TestSkillsCoverAllCliLeaves(t *testing.T) {
	t.Parallel()

	leaves := cliLeaves()
	if len(leaves) == 0 {
		t.Fatal("found no runnable CLI leaves; the command tree walk is broken")
	}
	indexed := referenceLeaves(t)
	if len(indexed) == 0 {
		t.Fatal("parsed no leaf sections from " + docgen.CLIReferencePath +
			"; the page moved or changed shape and this gate is blind until " +
			"the parse is repaired")
	}

	t.Run("mounted", func(t *testing.T) {
		for _, name := range indexed {
			if !slices.Contains(leaves, name) {
				t.Errorf("%s has a section for %q, but that is not a runnable "+
					"command in buildApp() — the leaf is documented and unreachable. "+
					"Mount it, or run `make docs`.", docgen.CLIReferencePath, name)
			}
		}
	})

	t.Run("indexed", func(t *testing.T) {
		for _, leaf := range leaves {
			if !slices.Contains(indexed, leaf) {
				t.Errorf("CLI leaf %q has no section in %s; run `make docs`",
					leaf, docgen.CLIReferencePath)
			}
		}
	})

	t.Run("documented", func(t *testing.T) {
		corpus := docCorpus(t, "internal/skills", "docs/src")
		for _, leaf := range leaves {
			if !strings.Contains(corpus, leaf) {
				t.Errorf("CLI leaf %q is named in no file under skills/ or docs/src/; "+
					"run `make docs` so the generated reference names it", leaf)
			}
		}
	})
}
