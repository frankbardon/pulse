package docgen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// validationCovered is every catalog category whose built-in operators
// must all carry a validation record. Overlays are covered by their
// Inferential flag instead (validationRequired).
var validationCovered = []string{"test", "regression", "matrix"}

// validationRequired returns every built-in operator the registry must
// cover: each test, regression and matrix operator, and each
// inferential overlay kind.
func validationRequired() map[string]bool {
	req := map[string]bool{}
	for _, s := range descx.PurposeSurfaces() {
		if slices.Contains(validationCovered, s.Category) {
			for _, n := range s.Names {
				req[n] = true
			}
		}
	}
	for _, c := range descx.OverlayCapabilities() {
		if c.Inferential {
			req[string(c.Kind)] = true
		}
	}
	return req
}

// TestValidationRegistryCoversInferential: no inferential built-in is
// silently missing from "How Pulse is validated". An operator without
// an external reference still needs an entry, declaring the gap.
func TestValidationRegistryCoversInferential(t *testing.T) {
	t.Parallel()
	for name := range validationRequired() {
		v, ok := validationRegistry[name]
		if !ok {
			t.Errorf("%s: no validation record (add one to validationRegistry, declaring a Gap if it has no external reference)", name)
			continue
		}
		if len(v.Refs) == 0 && v.Gap == "" {
			t.Errorf("%s: a record with no reference must declare its Gap", name)
		}
	}
}

// TestValidationRegistryNamesBuiltins: every key is a built-in operator,
// so a rename or removal cannot leave a stale claim behind.
func TestValidationRegistryNamesBuiltins(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	for _, s := range descx.PurposeSurfaces() {
		for _, n := range s.Names {
			known[n] = true
		}
	}
	for name := range validationRegistry {
		if !known[name] {
			t.Errorf("%s: validation record for an operator that is not a built-in", name)
		}
	}
}

// TestValidationReferencesExist: every cited test file exists, so no
// claim is published without the harness behind it.
func TestValidationReferencesExist(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	for name, v := range validationRegistry {
		for _, ref := range v.Refs {
			if ref.Against == "" || ref.Tol == "" || len(ref.Tests) == 0 {
				t.Errorf("%s: reference %+v needs Against, Tol and at least one test file", name, ref)
			}
			for _, f := range ref.Tests {
				if !strings.HasSuffix(f, "_test.go") {
					t.Errorf("%s: %s is not a test file", name, f)
					continue
				}
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil {
					t.Errorf("%s: cited test file %s: %v", name, f, err)
				}
			}
		}
	}
}

// TestValidationDropsWeightedWhenHidden: an instance without weighting
// renders no weighted-reference claim.
func TestValidationDropsWeightedWhenHidden(t *testing.T) {
	t.Parallel()
	var enabled, hidden []string
	for _, n := range descx.FeatureNames() {
		if n == descx.FeatureWeighting {
			hidden = append(hidden, n)
			continue
		}
		enabled = append(enabled, n)
	}
	g := newGen(descx.NewInstanceSnapshot(nil, descx.FeatureSet{Enabled: enabled, Hidden: hidden}), Options{})
	v, ok := g.validationFor("TEST_ANOVA_F")
	if !ok {
		t.Fatal("TEST_ANOVA_F: no record")
	}
	for _, ref := range v.Refs {
		if ref.Weighted {
			t.Errorf("weighting hidden but TEST_ANOVA_F renders the weighted reference")
		}
	}
	if len(v.Refs) == 0 {
		t.Error("TEST_ANOVA_F: the unweighted R reference was dropped too")
	}
}
