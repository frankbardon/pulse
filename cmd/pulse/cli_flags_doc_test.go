package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"

	pcli "github.com/frankbardon/pulse/internal/cli"

	"github.com/frankbardon/pulse/internal/doccheck"
)

// flagsDocPath is the flag reference page, repo-relative (the allowlist key)
// and as reached from this package's directory.
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
)

// flagToken spells a flag name as a user types it.
func flagToken(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// treeFlagNames returns every flag name the binary accepts, aliases
// included, mapped to the commands that mount it ("pulse" for the root's
// global flags). The tree is the post-setup one (completionTree), so the
// framework's --help / -h and the root's --version / -v count: a user can
// type them.
func treeFlagNames() map[string][]string {
	out := map[string][]string{}
	add := func(path string, flags []cli.Flag) {
		for _, f := range flags {
			for _, n := range f.Names() {
				out[n] = append(out[n], path)
			}
		}
	}
	root := completionTree()
	add(root.Name, root.Flags)
	pcli.WalkCommands(root, func(path []string, _, c *cli.Command) {
		add(strings.Join(path, " "), c.Flags)
	})
	return out
}

// docFlagNames returns every flag name docs/src/cli/flags.md spells out,
// prose, tables and code blocks alike (link destinations excluded).
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

// containsLeaf reports whether doc names the command path leaf as a whole
// token: `pulse api process` inside `pulse api process-chain` does not
// count. A neighbouring letter, digit, '-' or '_' continues the name.
func containsLeaf(doc, leaf string) bool {
	nameByte := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
	}
	for from := 0; ; {
		i := strings.Index(doc[from:], leaf)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(leaf)
		if (start == 0 || !nameByte(doc[start-1])) && (end == len(doc) || !nameByte(doc[end])) {
			return true
		}
		from = start + 1
	}
}

// commandList names up to three mounting commands, then a count.
func commandList(cmds []string) string {
	cmds = append([]string(nil), cmds...)
	sort.Strings(cmds)
	if len(cmds) <= 3 {
		return strings.Join(cmds, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(cmds[:3], ", "), len(cmds)-3)
}

// cliFlagsDocViolations compares the command tree with the flag reference,
// names only, both directions. Every alias is a name like any other: it is
// documented or allowlisted, and a documented one must be mounted.
func cliFlagsDocViolations(leaves []string, tree map[string][]string, doc string) []doccheck.Violation {
	var vs []doccheck.Violation
	for _, leaf := range leaves {
		if !containsLeaf(doc, leaf) {
			vs = append(vs, doccheck.Violation{Path: flagsDocPath, Entry: "undocumented leaf " + leaf,
				Message: "the CLI leaf is not named on the flag reference page; add it to the Command index"})
		}
	}
	documented := docFlagNames(doc)
	names := make([]string, 0, len(tree))
	for n := range tree {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !documented[n] {
			vs = append(vs, doccheck.Violation{Path: flagsDocPath, Entry: "undocumented flag " + flagToken(n),
				Message: fmt.Sprintf("mounted on %s but never named on the flag reference page; add it (a row, or the shared per-command section)", commandList(tree[n]))})
		}
	}
	var docNames []string
	for n := range documented {
		docNames = append(docNames, n)
	}
	sort.Strings(docNames)
	for _, n := range docNames {
		if _, ok := tree[n]; !ok {
			vs = append(vs, doccheck.Violation{Path: flagsDocPath, Entry: "unmounted flag " + flagToken(n),
				Message: "named on the flag reference page but no command in the binary accepts it; remove or rename it"})
		}
	}
	return vs
}

// TestCliHelpMatchesFlagsDoc holds the urfave command tree and
// docs/src/cli/flags.md to the same names, both directions: every runnable
// leaf and every flag the binary accepts (global, persistent and per-leaf,
// aliases included) is named on the page, and every flag the page names is
// mounted somewhere. Names only: descriptions and defaults are not compared.
// The reverse direction for leaves (an index row naming an unmounted
// command) is TestSkillsCoverAllCliLeaves' "mounted" check.
//
// Known mismatches live in the shrink-only allowlist
// internal/doccheck/allowlists/cli-flags.txt.
func TestCliHelpMatchesFlagsDoc(t *testing.T) {
	t.Parallel()

	repo := afero.NewBasePathFs(afero.NewOsFs(), filepath.Join("..", ".."))
	raw, err := afero.ReadFile(repo, flagsDocPath)
	if err != nil {
		t.Fatal(err)
	}
	leaves := cliLeaves()
	tree := treeFlagNames()
	if len(leaves) == 0 || len(tree) < 20 {
		t.Fatalf("walked %d leaves and %d flag names — the command tree walk is broken", len(leaves), len(tree))
	}
	allow, err := doccheck.Load(repo, doccheck.AllowlistFile("cli-flags"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range allow.Reconcile(cliFlagsDocViolations(leaves, tree, string(raw))).Problems(allow.File) {
		t.Error(p)
	}
}

func TestCliFlagsDocViolations(t *testing.T) {
	t.Parallel()
	tree := map[string][]string{
		"input": {"pulse api process"}, "i": {"pulse api process"},
		"json": {"pulse"}, "log-level": {"pulse"}, "help": {"pulse"}, "h": {"pulse"},
	}
	doc := "# Flags\n| `--input` / `-i` | in |\n`--log-level off|debug`\n" +
		"```\npulse api process --json --help\n```\n" +
		"`--gone` and --also-gone; not flags: x--y, a-b, -1, e-mail, [see](#--input-anchor)\n" +
		"a longer leaf is not a shorter one's mention: `pulse api chain-run`, xpulse api chain\n"
	got := map[string]bool{}
	for _, v := range cliFlagsDocViolations([]string{"pulse api process", "pulse api missing", "pulse api chain"}, tree, doc) {
		got[v.Entry] = true
	}
	want := []string{"undocumented leaf pulse api missing", "undocumented leaf pulse api chain", "undocumented flag -h", "unmounted flag --gone", "unmounted flag --also-gone"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing violation %q in %v", w, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("violations = %v, want exactly %v", got, want)
	}
}
