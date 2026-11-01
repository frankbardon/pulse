package fmagate

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// reportScope narrows the repo-wide reports. Run one package or file
// set (how a story proves its sites are "clean in the report"):
//
//	go test -tags heavy -count=1 -v ./internal/testutil/fmagate \
//	  -run 'TestFMAReport_RepoWide|TestBannedMathReport_RepoWide' \
//	  -fmareport.pkg=./internal/processing/...
//
// Patterns are comma-separated: ./... (default), ./a/b/..., ./a/b or
// ./a/b/c.go. Drop -tags heavy for the banned-call scan alone.
var reportScope = flag.String("fmareport.pkg", "./...",
	"comma-separated module-relative package patterns or .go files the repo-wide numeric reports cover")

// allowlistPath is the reviewed exception list (format in its header).
const allowlistPath = "testdata/numeric-allowlist.txt"

func loadAllowlist(t *testing.T) []Entry {
	t.Helper()
	f, err := os.Open(allowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	entries, err := ParseAllowlist(f)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func repoScope(t *testing.T) (string, Scope) {
	t.Helper()
	root := ModuleRoot(t)
	scope, err := ResolveScope(root, *reportScope)
	if err != nil {
		t.Fatal(err)
	}
	return root, scope
}

// failUnlisted makes a repo-wide scan binding: one t.Errorf per site
// outside the allowlist, naming file:line:col, the enclosing function,
// the finding and how to resolve it (fix, then the allowlist route).
func failUnlisted(t *testing.T, unlisted []Finding, fix string) {
	t.Helper()
	for _, f := range unlisted {
		t.Errorf("unlisted %s\n\tfix: %s\n\tor, if the site cannot or need not be deterministic, add `%s %s %s <reason>` to %s (reviewed; the reason is required)",
			f, fix, f.Kind, f.File, f.Func, "internal/testutil/fmagate/"+allowlistPath)
	}
}

// fixBanned is the remedy failUnlisted names for a banned call.
const fixBanned = "call the linalg/detmath twin (detmath.Exp, detmath.Log, detmath.Pow, ...) — stdlib transcendentals differ in the last ulp across arm64 and amd64 (and amd64 math.Exp follows the runner CPU)"

// TestBannedMathReport_RepoWide is the BINDING banned-transcendental
// source scan over every non-test package minus cmd/ and internal/cli
// (or -fmareport.pkg): every math.<Name> reference outside the
// allowlist fails (failUnlisted), as does a stale, malformed or
// duplicate allowlist entry. The full per-package report is logged
// (see -v).
func TestBannedMathReport_RepoWide(t *testing.T) {
	t.Parallel()

	root, scope := repoScope(t)
	findings, err := BannedCalls(root, scope)
	if err != nil {
		t.Fatal(err)
	}
	listed, unlisted, stale := Apply(loadAllowlist(t), findings, scope, func(k string) bool { return k != KindFMA })
	t.Log("\n" + Report("banned stdlib transcendentals", listed, unlisted))
	failUnlisted(t, unlisted, fixBanned)
	for _, e := range stale {
		t.Errorf("stale %s: no banned call left in that function; delete the entry", e)
	}
}

// TestBannedCalls_SeesAliasesSkipsExact pins the source scan's verdicts
// on a scratch package: aliased imports and method values are found,
// Sqrt / Abs and a non-math selector are not, and each finding names
// its enclosing top-level declaration.
func TestBannedCalls_SeesAliasesSkipsExact(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module scratch\n\ngo 1.26\n")
	write("a/a.go", `package a

import m "math"

type detmath struct{}

func (detmath) Exp(x float64) float64 { return x }

var tab = m.Pow(2, 0.5)

type G[T any] struct{ v T }

func (g *G[T]) M(x float64) float64 {
	f := func() float64 { return m.Exp(x) }
	return f() + m.Sqrt(x) + m.Abs(x)
}

func F(x float64) func(float64) float64 {
	var d detmath
	_ = d.Exp(x)
	return m.Log1p
}
`)
	write("a/a_test.go", "package a\n\nimport \"math\"\n\nvar _ = math.Exp(1)\n")
	write("cmd/c/c.go", "package main\n\nimport \"math\"\n\nfunc main() { _ = math.Exp(1) }\n")

	scope, err := ResolveScope(root, "./...")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scope.Dirs, map[string]bool{"a": true}) {
		t.Errorf("scope dirs = %v, want only a (cmd/ excluded)", scope.Dirs)
	}
	got, err := BannedCalls(root, scope)
	if err != nil {
		t.Fatal(err)
	}
	var summary []string
	for _, f := range got {
		summary = append(summary, f.Kind+"@"+f.Func)
	}
	want := []string{"math.Pow@(package)", "math.Exp@G.M", "math.Log1p@F"}
	if !reflect.DeepEqual(summary, want) {
		t.Errorf("findings = %v, want %v", summary, want)
	}
	if len(got) == 3 && (got[1].File != "a/a.go" || got[1].Line != 14) {
		t.Errorf("Exp finding at %s:%d, want a/a.go:14", got[1].File, got[1].Line)
	}
}

// TestAllowlist_ExcusesAndFlagsStale pins Apply: an entry excuses every
// finding of its kind in its function, an in-scope entry that excuses
// nothing is stale, an out-of-scope or other-kind entry is not checked.
func TestAllowlist_ExcusesAndFlagsStale(t *testing.T) {
	t.Parallel()

	entries, err := ParseAllowlist(strings.NewReader(`# header
fma a/x.go F control-only sizing
math.Exp a/x.go F fixture generator
fma a/x.go Gone   the site was fixed
fma b/y.go H out of scope
math.Log a/x.go F other kind, not checked by the fma pass
`))
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Reason != "control-only sizing" || entries[2].Reason != "the site was fixed" || entries[2].Line != 4 {
		t.Errorf("parsed entries = %+v", entries)
	}
	findings := []Finding{
		{Kind: KindFMA, File: "a/x.go", Line: 3, Func: "F"},
		{Kind: KindFMA, File: "a/x.go", Line: 9, Func: "F"},
		{Kind: KindFMA, File: "a/x.go", Line: 20, Func: "K"},
		{Kind: "math.Exp", File: "a/x.go", Line: 4, Func: "F"},
	}
	scope := Scope{Dirs: map[string]bool{"a": true}}
	listed, unlisted, stale := Apply(entries, findings, scope, func(k string) bool { return k == KindFMA })
	if len(listed) != 3 || len(unlisted) != 1 || unlisted[0].Func != "K" {
		t.Errorf("listed %v unlisted %v", listed, unlisted)
	}
	if len(stale) != 1 || stale[0].Func != "Gone" {
		t.Errorf("stale = %v, want only the Gone entry", stale)
	}
}

// TestAllowlist_RejectsMalformed keeps the file reviewable: every
// entry needs a reason, a known kind and a clean module-relative path.
func TestAllowlist_RejectsMalformed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, line string }{
		{"no reason", "fma a/x.go F"},
		{"unknown kind", "flop a/x.go F why"},
		{"exact math is not banned", "math.Sqrt a/x.go F why"},
		{"absolute path", "fma /a/x.go F why"},
		{"parent path", "fma ../x.go F why"},
		{"not a go file", "fma a/x F why"},
		{"duplicate", "fma a/x.go F why\nfma a/x.go F again"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseAllowlist(strings.NewReader(tc.line)); err == nil {
				t.Errorf("%q parsed; want an error", tc.line)
			}
		})
	}
}

// TestResolveScope_Patterns pins the -fmareport.pkg pattern forms.
func TestResolveScope_Patterns(t *testing.T) {
	t.Parallel()

	root := ModuleRoot(t)
	for _, tc := range []struct {
		pattern string
		has     string
		hasNot  string
	}{
		{"./...", "internal/processing/aggregator.go", "internal/cli/app.go"},
		{"./internal/processing/...", "internal/processing/regression/stats.go", "internal/statdist/normal.go"},
		{"./internal/processing", "internal/processing/aggregator.go", "internal/processing/regression/stats.go"},
		{"./internal/statdist/normal.go,internal/sem/fit.go", "internal/sem/fit.go", "internal/statdist/beta.go"},
	} {
		s, err := ResolveScope(root, tc.pattern)
		if err != nil {
			t.Errorf("%s: %v", tc.pattern, err)
			continue
		}
		if !s.Has(tc.has) || s.Has(tc.hasNot) {
			t.Errorf("%s: Has(%s)=%v Has(%s)=%v", tc.pattern, tc.has, s.Has(tc.has), tc.hasNot, s.Has(tc.hasNot))
		}
	}
	for _, bad := range []string{"./nope/...", "./cmd/pulse", "./internal/statdist/nope.go", "./internal/statdist,./internal/sem/fit.go"} {
		if _, err := ResolveScope(root, bad); err == nil {
			t.Errorf("%s resolved; want an error", bad)
		}
	}
}
