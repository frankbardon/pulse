package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestExtensionNameRegex_Matrix pins MAT in the naming policy: a
// MAT_<NS>_<NAME> name parses with category MAT, and the matrix category
// expects that prefix.
func TestExtensionNameRegex_Matrix(t *testing.T) {
	g := extensionNameRegex.FindStringSubmatch("MAT_ACME_X_Y")
	if g == nil || g[1] != "MAT" || g[2] != "ACME" || g[3] != "X_Y" {
		t.Fatalf("MAT_ACME_X_Y parsed %v, want [MAT ACME X_Y]", g)
	}
	if got := categoryMatrix.expectedPrefix(); got != "MAT" {
		t.Fatalf("categoryMatrix.expectedPrefix() = %q, want MAT", got)
	}
	if err := validateExtensionName("MAT_ACME_X_Y", categoryMatrix); err != nil {
		t.Fatalf("MAT_ACME_X_Y refused: %v", err)
	}
	for _, bad := range []string{"MAT_ACME", "MAT_ACME_", "MATX_ACME_Y", "MAT_A_Y"} {
		if extensionNameRegex.MatchString(bad) {
			t.Errorf("extensionNameRegex accepts %q", bad)
		}
	}
}

// TestBuiltinMatrixNames_CoverRegistry pins the collision set to the
// live registry, so a new built-in MAT_* is reserved automatically.
func TestBuiltinMatrixNames_CoverRegistry(t *testing.T) {
	names := builtinMatrixNames()
	if len(names) != len(types.AllMatrixTypes()) {
		t.Fatalf("builtinMatrixNames has %d entries, registry %d", len(names), len(types.AllMatrixTypes()))
	}
	for _, mt := range types.AllMatrixTypes() {
		if _, ok := names[string(mt)]; !ok {
			t.Errorf("builtinMatrixNames misses %s", mt)
		}
	}
}

// TestLooksLikeExtensionName_Matrix audits the feature-universe
// predicate after MAT joined the regex: an embedder MAT_* name is an
// extension feature name, a reserved namespace is not, and SYNTH stays
// excluded.
func TestLooksLikeExtensionName_Matrix(t *testing.T) {
	for name, want := range map[string]bool{
		"MAT_ACME_SCATTER":  true,
		"MAT_PULSE_SCATTER": false,
		"MAT_SCATTER":       false,
		"SYNTH_ACME_DIST":   false,
		"AGG_ACME_SCORE":    true,
	} {
		if got := looksLikeExtensionName(name); got != want {
			t.Errorf("looksLikeExtensionName(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestMergeExtensions_MatrixOps pins that MatrixOps concatenate across
// merged Extensions values, and that a MatrixOps-only value counts as a
// registration.
func TestMergeExtensions_MatrixOps(t *testing.T) {
	a := Extensions{MatrixOps: []MatrixOpRegistration{{Name: "MAT_ACME_A"}}}
	b := Extensions{MatrixOps: []MatrixOpRegistration{{Name: "MAT_ACME_B"}}}
	got := mergeExtensions([]Extensions{a, b}).MatrixOps
	if len(got) != 2 || got[0].Name != "MAT_ACME_A" || got[1].Name != "MAT_ACME_B" {
		t.Fatalf("merged MatrixOps = %v, want [MAT_ACME_A MAT_ACME_B]", got)
	}
	if !hasAnyRegistrations(a) {
		t.Fatal("hasAnyRegistrations ignores MatrixOps")
	}
	if hasAnyRegistrations(Extensions{}) {
		t.Fatal("hasAnyRegistrations reports the zero Extensions")
	}
}
