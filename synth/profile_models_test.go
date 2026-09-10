package synth_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// modelFlagFixture writes a cohort with two categoricals, two numerics
// and a scattering of nulls — enough shape that every capture flag on
// `profile create` has something to say about it, which is what makes
// the flag-independence assertions below meaningful rather than vacuous.
func modelFlagFixture(t *testing.T, rowCount int, seed int64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	regions := []string{"east", "west", "north"}
	tiers := []string{"gold", "silver"}
	var buf strings.Builder
	buf.WriteString("region,tier,spend,visits\n")
	for i := 0; i < rowCount; i++ {
		region := regions[i%len(regions)]
		tier := tiers[(i/3)%len(tiers)]
		spend := 40.0 + 12.0*float64(i%len(regions)) + rng.NormFloat64()*3
		visits := 2.0 + rng.NormFloat64()
		// Every fifth row drops its spend, so listwise deletion and the
		// null-rate capture both have something to bite on.
		spendCell := fmt.Sprintf("%.6f", spend)
		if i%5 == 0 {
			spendCell = ""
		}
		fmt.Fprintf(&buf, "%s,%s,%s,%.6f\n", region, tier, spendCell, visits)
	}
	return importCSVFixture(t, buf.String(), rowCount)
}

// profileOptionMatrix is every capture-flag combination whose behaviour
// this story promises not to touch.
func profileOptionMatrix() []struct {
	name string
	opts synth.ProfileOptions
} {
	return []struct {
		name string
		opts synth.ProfileOptions
	}{
		{"defaults", synth.ProfileOptions{}},
		{"include-stats", synth.ProfileOptions{IncludeStats: true}},
		{"include-correlations", synth.ProfileOptions{IncludeCorrelations: true}},
		{"correlation-top-k", synth.ProfileOptions{IncludeCorrelations: true, CorrelationTopK: 1}},
		{"conditional", synth.ProfileOptions{IncludeConditional: true}},
		{"conditional+top-k", synth.ProfileOptions{IncludeConditional: true, CorrelationTopK: 2}},
		{"fit-shape", synth.ProfileOptions{FitShape: true}},
		{"everything", synth.ProfileOptions{
			IncludeStats:        true,
			IncludeCorrelations: true,
			CorrelationTopK:     4,
			IncludeConditional:  true,
			FitShape:            true,
		}},
	}
}

// The byte-identity gate that used to live here moved to
// TestProfile_ModelsSection_IsPurelyAdditive in
// profile_models_document_test.go. E1-S2 could assert the absolute form
// — the flag moved no byte at all — only because it serialised nothing;
// now that `models` reaches the document the promise is the additive
// one, and it is asserted section by section over the same
// profileOptionMatrix rather than as an opaque string compare.

// TestProfile_FitModels_LeavesExistingCaptureUntouched asserts the
// per-section equality the byte comparison above implies but does not
// name: --conditional's captured pairs, --include-correlations' top-K
// list and --fit-shape's mixture fits must be identical objects whether
// or not models were also fitted.
//
// It is a separate assertion because the two capture paths share a
// SEED: the residual reservoir draws from its own RNG stream precisely
// so --conditional's categorical reservoir cannot shift underneath it,
// and that is the property most likely to regress silently.
func TestProfile_FitModels_LeavesExistingCaptureUntouched(t *testing.T) {
	data := modelFlagFixture(t, 900, 11)
	opts := synth.ProfileOptions{
		IncludeStats:        true,
		IncludeCorrelations: true,
		CorrelationTopK:     4,
		IncludeConditional:  true,
		FitShape:            true,
		Seed:                42,
	}
	base, err := synth.ProfileBytes(data, opts)
	if err != nil {
		t.Fatalf("baseline profile: %v", err)
	}
	fitOpts := opts
	fitOpts.FitModels = true
	fitted, err := synth.ProfileBytes(data, fitOpts)
	if err != nil {
		t.Fatalf("fit-models profile: %v", err)
	}

	if base.Conditional == nil || fitted.Conditional == nil {
		t.Fatal("fixture must populate the conditional section on both runs")
	}
	sections := []struct {
		name    string
		a, b    any
		nonZero bool
	}{
		{"pairwise", base.Pairwise, fitted.Pairwise, true},
		{"conditional", base.Conditional, fitted.Conditional, true},
		{"fields", base.Fields, fitted.Fields, true},
		{"warnings", base.Warnings, fitted.Warnings, false},
	}
	for _, s := range sections {
		aj, err := json.Marshal(s.a)
		if err != nil {
			t.Fatalf("marshal %s: %v", s.name, err)
		}
		bj, err := json.Marshal(s.b)
		if err != nil {
			t.Fatalf("marshal %s: %v", s.name, err)
		}
		if string(aj) != string(bj) {
			t.Errorf("%s section moved under --fit-models\n base: %s\nfitted: %s", s.name, aj, bj)
		}
		if s.nonZero && (string(aj) == "null" || string(aj) == "[]") {
			t.Errorf("%s section is empty; the fixture cannot prove anything about it", s.name)
		}
	}
}

// TestProfile_FitModels_DeterministicAcrossRuns pins the capture to its
// seed: two runs over the same cohort with the same options must agree
// on every coefficient and every retained residual. The residual
// reservoir is randomised, so without this the section would be free to
// drift run to run and nothing downstream could be golden-tested.
func TestProfile_FitModels_DeterministicAcrossRuns(t *testing.T) {
	data := modelFlagFixture(t, 400, 3)
	opts := synth.ProfileOptions{FitModels: true, Seed: 9}

	first, err := synth.ProfileBytes(data, opts)
	if err != nil {
		t.Fatalf("first profile: %v", err)
	}
	second, err := synth.ProfileBytes(data, opts)
	if err != nil {
		t.Fatalf("second profile: %v", err)
	}
	a, err := json.Marshal(first.FittedModels())
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	b, err := json.Marshal(second.FittedModels())
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("model capture is not deterministic\nfirst:  %s\nsecond: %s", a, b)
	}
	if len(first.FittedModels()) == 0 {
		t.Fatal("fixture produced no models")
	}
	// Every fitted model must carry the two categoricals' non-reference
	// levels and nothing from the numeric side — candidate predictors at
	// this stage are categoricals and set options only.
	for _, m := range first.FittedModels() {
		if len(m.Predictors) == 0 {
			t.Errorf("model %q retained with no predictors", m.Field)
		}
		for _, p := range m.Predictors {
			if p.Kind != synth.ModelPredictorCategoricalLevel && p.Kind != synth.ModelPredictorSetOption {
				t.Errorf("model %q carries a %s predictor %q", m.Field, p.Kind, p.Column)
			}
			if p.Field == m.Field {
				t.Errorf("model %q regressed on itself", m.Field)
			}
		}
	}
}
