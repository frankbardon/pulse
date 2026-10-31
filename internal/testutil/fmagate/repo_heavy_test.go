//go:build heavy

package fmagate

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFMAReport_RepoWide is the repo-wide contraction scan: every
// non-test package minus cmd/ and internal/cli (or -fmareport.pkg) is
// cross-compiled for linux/arm64 with -d=fmahash=vy over the whole
// module (~30 s uncached). It LOGS each contracted position outside the
// allowlist with per-package counts (see -v; an inlined site shows its
// chain) and fails only on a stale or malformed allowlist entry.
// Report-only until E5-S2. Command for one package: see reportScope.
func TestFMAReport_RepoWide(t *testing.T) {
	t.Parallel()

	root, scope := repoScope(t)
	findings := ScanFMA(t, root, scope)
	listed, unlisted, stale := Apply(loadAllowlist(t), findings, scope, func(k string) bool { return k == KindFMA })
	t.Log("\n" + Report("arm64 FMA contractions", listed, unlisted))
	for _, e := range stale {
		t.Errorf("stale %s: the compiler contracts nothing in that function any more; delete the entry", e)
	}
}

// TestFMAGate_ScanAttributesFunc pins ScanFMA on a scratch module: the
// contraction in a method is reported once with its file and method
// name, a barriered twin and an excluded cmd/ package are not.
func TestFMAGate_ScanAttributesFunc(t *testing.T) {
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
	write("p/p.go", "package p\n\ntype T struct{ a float64 }\n\nfunc (t *T) M(b, c float64) float64 {\n\treturn t.a + b*c\n}\n\nfunc Clean(a, b, c float64) float64 { return a + float64(b*c) }\n")
	write("cmd/x/main.go", "package main\n\nvar a, b, c = 1.0, 2.0, 3.0\n\nfunc main() { println(a + b*c) }\n")

	scope, err := ResolveScope(root, "./...")
	if err != nil {
		t.Fatal(err)
	}
	got := ScanFMA(t, root, scope)
	if len(got) != 1 {
		t.Fatalf("findings = %v, want exactly the method's contraction", got)
	}
	if f := got[0]; f.File != "p/p.go" || f.Line != 6 || f.Func != "T.M" || f.Kind != KindFMA {
		t.Errorf("finding = %+v, want p/p.go:6 in T.M", f)
	}
}
