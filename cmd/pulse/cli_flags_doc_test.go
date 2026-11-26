package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"

	pcli "github.com/frankbardon/pulse/internal/cli"
	"github.com/frankbardon/pulse/internal/doccheck"
	"github.com/frankbardon/pulse/internal/docgen"
)

// flagsDocPath is the hand-written CLI concepts page, repo-relative (the
// allowlist key) and as reached from this package's directory. The
// per-leaf flag lists live on the generated reference
// (docgen.CLIReferencePath).
const flagsDocPath = "docs/src/cli/flags.md"

// docLongFlagRe matches a long flag token ("--input", "--log-level") not
// glued to a preceding word or hyphen; docShortFlagRe a one-letter short
// flag ("-i") standing alone.
var (
	docLongFlagRe  = regexp.MustCompile(`(?:^|[^\w-])--([a-z][a-z0-9]*(?:-[a-z0-9]+)*)`)
	docShortFlagRe = regexp.MustCompile(`(?:^|[^\w-])-([a-zA-Z])(?:[^\w-]|$)`)
	// docLinkDestRe matches an inline link's destination, whose #anchor
	// ("#--strict-import") spells a heading slug, not a flag.
	docLinkDestRe = regexp.MustCompile(`\]\([^)\s]*\)`)
	// refLeafHeading is a leaf section heading on the generated reference.
	refLeafHeading = regexp.MustCompile("^### `(pulse [^`]+)`$")
	// refFlagCellRe is one backticked flag spelling in a table row's
	// first cell ("`--input`, `-i`").
	refFlagCellRe = regexp.MustCompile("`--?([A-Za-z0-9][A-Za-z0-9-]*)`")
)

// flagToken spells a flag name as a user types it.
func flagToken(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// treeFlags is the command tree's flag names, aliases included: the
// root's global flags and each runnable leaf's own flags. urfave's help
// flag, mounted on every leaf, counts once, with the globals.
type treeFlags struct {
	global []string
	leaves map[string][]string
}

func treeFlagSet() treeFlags {
	root := completionTree()
	names := func(fs []pcli.FlagDoc) []string {
		var out []string
		for _, f := range fs {
			if f.Names[0] == "help" {
				continue
			}
			out = append(out, f.Names...)
		}
		sort.Strings(out)
		return out
	}
	t := treeFlags{leaves: map[string][]string{}}
	for _, f := range pcli.GlobalFlags(root) {
		t.global = append(t.global, f.Names...)
	}
	sort.Strings(t.global)
	for _, l := range pcli.Leaves(root) {
		t.leaves[l.Path] = names(l.Flags)
	}
	return t
}

// all returns every flag name the binary accepts.
func (t treeFlags) all() map[string]bool {
	out := map[string]bool{}
	for _, n := range t.global {
		out[n] = true
	}
	for _, ns := range t.leaves {
		for _, n := range ns {
			out[n] = true
		}
	}
	return out
}

// refFlags parses the generated reference into the same shape: the flag
// names in the `## Global flags` table and in each leaf section's table.
func refFlags(doc string) treeFlags {
	t := treeFlags{leaves: map[string][]string{}}
	var into *[]string
	var leaf string
	for _, line := range strings.Split(doc, "\n") {
		switch {
		case line == "## Global flags":
			into, leaf = &t.global, ""
		case strings.HasPrefix(line, "## "):
			into, leaf = nil, ""
		case refLeafHeading.MatchString(line):
			leaf = refLeafHeading.FindStringSubmatch(line)[1]
			t.leaves[leaf] = []string{}
			into = nil
		case strings.HasPrefix(line, "| `-"):
			cell := strings.SplitN(line, " | ", 2)[0]
			for _, m := range refFlagCellRe.FindAllStringSubmatch(cell, -1) {
				if leaf != "" {
					t.leaves[leaf] = append(t.leaves[leaf], m[1])
				} else if into != nil {
					*into = append(*into, m[1])
				}
			}
		}
	}
	sort.Strings(t.global)
	for k := range t.leaves {
		sort.Strings(t.leaves[k])
	}
	return t
}

// docFlagNames returns every flag name a page spells out, prose, tables
// and code blocks alike (link destinations excluded).
func docFlagNames(doc string) map[string]bool {
	out := map[string]bool{}
	doc = docLinkDestRe.ReplaceAllString(doc, "]")
	for _, m := range docLongFlagRe.FindAllStringSubmatch(doc, -1) {
		out[m[1]] = true
	}
	for _, m := range docShortFlagRe.FindAllStringSubmatch(doc, -1) {
		out[m[1]] = true
	}
	return out
}

// diffNames returns the names in a missing from b, sorted.
func diffNames(a, b []string) []string {
	var out []string
	for _, n := range a {
		if !slices.Contains(b, n) {
			out = append(out, n)
		}
	}
	return slices.Compact(out)
}

// cliFlagsDocViolations compares the command tree with the generated
// reference per leaf, both directions (leaves and each leaf's flags,
// aliases included, plus the global flags), and requires every flag the
// concepts page names to be mounted somewhere.
func cliFlagsDocViolations(tree treeFlags, ref treeFlags, concepts string) []doccheck.Violation {
	var vs []doccheck.Violation
	refPath := docgen.CLIReferencePath
	add := func(path, entry, msg string) {
		vs = append(vs, doccheck.Violation{Path: path, Entry: entry, Message: msg})
	}
	const regen = "; run `make docs` (the page is rendered from the command tree)"
	for _, n := range diffNames(tree.global, ref.global) {
		add(refPath, "undocumented global flag "+flagToken(n), "the root mounts it but the Global flags table omits it"+regen)
	}
	for _, n := range diffNames(ref.global, tree.global) {
		add(refPath, "unmounted global flag "+flagToken(n), "the Global flags table lists it but the root does not mount it"+regen)
	}
	leaves := make([]string, 0, len(tree.leaves))
	for l := range tree.leaves {
		leaves = append(leaves, l)
	}
	sort.Strings(leaves)
	for _, l := range leaves {
		got, ok := ref.leaves[l]
		if !ok {
			add(refPath, "undocumented leaf "+l, "the CLI leaf has no section on the reference"+regen)
			continue
		}
		for _, n := range diffNames(tree.leaves[l], got) {
			add(refPath, "undocumented flag "+flagToken(n)+" on "+l, "the leaf mounts it but its section omits it"+regen)
		}
		for _, n := range diffNames(got, tree.leaves[l]) {
			add(refPath, "unmounted flag "+flagToken(n)+" on "+l, "the leaf's section lists it but the leaf does not mount it"+regen)
		}
	}
	var stale []string
	for l := range ref.leaves {
		if _, ok := tree.leaves[l]; !ok {
			stale = append(stale, l)
		}
	}
	sort.Strings(stale)
	for _, l := range stale {
		add(refPath, "unmounted leaf "+l, "the reference has a section for a command the binary does not mount"+regen)
	}
	mounted := tree.all()
	var named []string
	for n := range docFlagNames(concepts) {
		named = append(named, n)
	}
	sort.Strings(named)
	for _, n := range named {
		if !mounted[n] {
			add(flagsDocPath, "unmounted flag "+flagToken(n),
				"named on the CLI concepts page but no command in the binary accepts it; remove or rename it")
		}
	}
	return vs
}

// TestCliHelpMatchesFlagsDoc holds the urfave command tree and the
// generated reference (docs/src/cli/reference.md) to the same leaves and,
// per leaf, the same flags (aliases included), both directions, plus the
// root's global flags; and every flag the hand-written concepts page
// (docs/src/cli/flags.md) names is mounted somewhere. Names only: the
// reference's text is held byte for byte by TestCLIReferenceCurrent.
//
// Known mismatches live in the shrink-only allowlist
// internal/doccheck/allowlists/cli-flags.txt.
func TestCliHelpMatchesFlagsDoc(t *testing.T) {
	t.Parallel()

	repo := afero.NewBasePathFs(afero.NewOsFs(), filepath.Join("..", ".."))
	ref, err := afero.ReadFile(repo, docgen.CLIReferencePath)
	if err != nil {
		t.Fatal(err)
	}
	concepts, err := afero.ReadFile(repo, flagsDocPath)
	if err != nil {
		t.Fatal(err)
	}
	tree := treeFlagSet()
	parsed := refFlags(string(ref))
	if len(tree.leaves) < 50 || len(parsed.leaves) < 50 || len(tree.global) < 5 {
		t.Fatalf("walked %d leaves, parsed %d sections and %d global flags — the tree walk or the reference parse is broken",
			len(tree.leaves), len(parsed.leaves), len(tree.global))
	}
	allow, err := doccheck.Load(repo, doccheck.AllowlistFile("cli-flags"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range allow.Reconcile(cliFlagsDocViolations(tree, parsed, string(concepts))).Problems(allow.File) {
		t.Error(p)
	}
}

func TestCliFlagsDocViolations(t *testing.T) {
	t.Parallel()
	tree := treeFlags{
		global: []string{"h", "help", "log-level"},
		leaves: map[string][]string{
			"pulse api process": {"i", "input", "json"},
			"pulse api missing": {"json"},
		},
	}
	ref := refFlags("# R\n\n## Global flags\n\n| Flag | Value |\n|---|---|\n| `--log-level` | string |\n| `--help`, `-h` |  |\n| `--gone-global` |  |\n\n" +
		"## Commands\n\n### `pulse api process`\n\n| Flag | Value |\n|---|---|\n| `--input` | string |\n| `--stale` |  |\n\n" +
		"### `pulse api ghost`\n\nNo flags of its own.\n\n## Flags by name\n\n| `--json` | [`api process`](#pulse-api-process) |\n")
	concepts := "`--input` and `-i`; `--never-mounted`; not flags: x--y, a-b, -1, [see](#--input-anchor)\n"
	got := map[string]bool{}
	for _, v := range cliFlagsDocViolations(tree, ref, concepts) {
		got[v.Entry] = true
	}
	want := []string{
		"unmounted global flag --gone-global",
		"undocumented flag -i on pulse api process",
		"undocumented flag --json on pulse api process",
		"unmounted flag --stale on pulse api process",
		"undocumented leaf pulse api missing",
		"unmounted leaf pulse api ghost",
		"unmounted flag --never-mounted",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing violation %q in %v", w, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("violations = %v, want exactly %v", got, want)
	}
}

// TestRefFlagsParsesTheGeneratedPage: the parse sees every leaf section
// and global flag the renderer writes, so the per-leaf check is not
// vacuous.
func TestRefFlagsParsesTheGeneratedPage(t *testing.T) {
	t.Parallel()
	page := docgen.RenderCLIReference(pcli.CLIReference(buildApp()))
	tree, ref := treeFlagSet(), refFlags(string(page))
	if v := cliFlagsDocViolations(tree, ref, ""); len(v) != 0 {
		t.Errorf("a freshly rendered reference disagrees with the tree: %v", v)
	}
	if fmt.Sprint(ref.global) != fmt.Sprint(tree.global) {
		t.Errorf("global flags parsed %v, tree %v", ref.global, tree.global)
	}
}
