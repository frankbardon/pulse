package docgen

import (
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/weighting"
)

// The registry gates (coverage, built-in names, cited tests that exist
// and exercise their operator, permanent weight refusals) live beside
// the registry in internal/descriptor/validation_test.go; these tests
// pin how docgen renders it.

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
		if rec, _ := descx.ValidationRecordOf(op); rec.PostTestOnly {
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
	for _, ref := range v.References {
		if ref.Weighted {
			t.Errorf("weighting hidden but TEST_ANOVA_F renders the weighted reference")
		}
	}
	if len(v.References) == 0 {
		t.Error("TEST_ANOVA_F: the unweighted R reference was dropped too")
	}
}
