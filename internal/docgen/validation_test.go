package docgen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/weighting"
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
				checkCitedTest(t, root, name, f)
			}
		}
		for _, f := range v.WeightRefused {
			checkCitedTest(t, root, name, f)
		}
	}
}

func checkCitedTest(t *testing.T, root, name, f string) {
	t.Helper()
	if !strings.HasSuffix(f, "_test.go") {
		t.Errorf("%s: %s is not a test file", name, f)
		return
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil {
		t.Errorf("%s: cited test file %s: %v", name, f, err)
	}
}

// TestValidationWeightRefusalIsPermanent: a record claiming a pinned
// weight refusal names an operator the weighting table refuses
// permanently, with a stated reason; once the operator gains a weighted
// form the claim fails here instead of going stale.
func TestValidationWeightRefusalIsPermanent(t *testing.T) {
	t.Parallel()
	for name, v := range validationRegistry {
		if len(v.WeightRefused) == 0 {
			continue
		}
		if v.PostTestOnly {
			if descx.PostTestWeightRefusal() == "" || weighting.ClassOf(name) != weighting.ClassRefuse {
				t.Errorf("%s: post-test WeightRefused without a refused class and the shared post-test reason", name)
			}
		} else if reason, ok := descx.OperatorWeightRefusal(name); !ok || reason != weighting.RefusalReason(name) {
			t.Errorf("%s: WeightRefused, but weighting does not refuse it permanently (class %v, reason %q)",
				name, weighting.ClassOf(name), weighting.RefusalReason(name))
		}
		for _, ref := range v.Refs {
			if ref.Weighted {
				t.Errorf("%s: both a weighted reference and a weight refusal", name)
			}
		}
	}
}

// TestValidationWeightRefusalRenders: the refusal line, with its reason
// and pinning test, renders in the catalog block on an instance that
// offers weighting and is absent on one that does not.
func TestValidationWeightRefusalRenders(t *testing.T) {
	t.Parallel()
	var all, enabled, hidden []string
	for _, n := range descx.FeatureNames() {
		all = append(all, n)
		if n == descx.FeatureWeighting {
			hidden = append(hidden, n)
			continue
		}
		enabled = append(enabled, n)
	}
	on := newGen(descx.NewInstanceSnapshot(nil, descx.FeatureSet{Enabled: all}), Options{})
	off := newGen(descx.NewInstanceSnapshot(nil, descx.FeatureSet{Enabled: enabled, Hidden: hidden}), Options{})
	for _, op := range []string{"TEST_ANOVA_RM", "TEST_SHAPIRO_WILK", "TEST_TREND", "TEST_TUKEY_HSD"} {
		reason := weighting.RefusalReason(op)
		if validationRegistry[op].PostTestOnly {
			reason = descx.PostTestWeightRefusal()
		}
		var b strings.Builder
		on.renderValidationDetail(&b, op)
		got := b.String()
		for _, want := range []string{"`PULSE_WEIGHT_UNSUPPORTED`", reason, "weight_refusal_pin_test.go"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: catalog block lacks %q:\n%s", op, want, got)
			}
		}
		b.Reset()
		off.renderValidationDetail(&b, op)
		if strings.Contains(b.String(), "PULSE_WEIGHT_UNSUPPORTED") {
			t.Errorf("%s: weighting hidden but the weight refusal renders:\n%s", op, b.String())
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
