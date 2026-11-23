package model

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestCheckFitDesign_FrequencyWeightAdmitted (regression-depth-2
// E1-S3, #640): a frequency-weighted bound fit is admitted — its design
// is the rep() expansion's — while a probability-weighted one keeps the
// requires unweighted refusal, now worded as permanent.
func TestCheckFitDesign_FrequencyWeightAdmitted(t *testing.T) {
	t.Parallel()

	p := FitParams{Fit: "m"}
	base := Signature{Name: "m", Type: types.REG_GLM, Influence: FitDesign{Family: "poisson", Vcov: true, Dispersion: true}}
	freq := base
	freq.Influence.Weighted = true
	if err := CheckFitDesign(types.ATTR_REG_INFLUENCE, p, freq); err != nil {
		t.Fatalf("frequency-weighted fit refused: %v", err)
	}
	prob := freq
	prob.Influence.ProbabilityWeight = true
	for _, at := range []types.AttributeType{types.ATTR_REG_LEVERAGE, types.ATTR_REG_INFLUENCE} {
		err := CheckFitDesign(at, p, prob)
		if err == nil || err.Details["requires"] != "unweighted" {
			t.Fatalf("%s: probability-weighted fit: %v, want requires unweighted", at, err)
		}
		if !strings.Contains(err.Message, "permanently") || !strings.Contains(err.Message, "kind: frequency") {
			t.Errorf("%s: message %q does not say the refusal is permanent and name the frequency kind", at, err.Message)
		}
	}
}

// TestFitDesign_WeightKind: the static and result faces read the
// weight kind alike — a frequency weight (regression, request or
// instance default) is weighted but not probability, any other weight
// is probability, weight: null is unweighted.
func TestFitDesign_WeightKind(t *testing.T) {
	t.Parallel()

	freq := types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	prob := types.WeightSpec{Field: "w"}
	reg := func(w types.SlotWeight) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}, Vcov: true, Weight: w}
	}
	for _, tc := range []struct {
		name           string
		req            *types.Request
		reg            *types.RegressionSpec
		weighted, prob bool
	}{
		{"none", &types.Request{}, reg(types.SlotWeight{}), false, false},
		{"slot frequency", &types.Request{}, reg(types.SlotWeightOf(freq)), true, false},
		{"slot probability", &types.Request{}, reg(types.SlotWeightOf(prob)), true, true},
		{"request frequency", &types.Request{Weight: &freq}, reg(types.SlotWeight{}), true, false},
		{"request probability", &types.Request{Weight: &prob}, reg(types.SlotWeight{}), true, true},
		{"null over request", &types.Request{Weight: &prob}, reg(types.NullSlotWeight()), false, false},
	} {
		d := designFromSpec(tc.req, tc.reg)
		if d.Weighted != tc.weighted || d.ProbabilityWeight != tc.prob {
			t.Errorf("%s: spec face weighted %v probability %v, want %v %v", tc.name, d.Weighted, d.ProbabilityWeight, tc.weighted, tc.prob)
		}
	}
	if d := designFromResult(&types.RegressionResult{SumWeights: 7}); !d.Weighted || d.ProbabilityWeight {
		t.Errorf("frequency result face = %+v", d)
	}
	if d := designFromResult(&types.RegressionResult{SumWeights: 7, NEff: 5.5}); !d.Weighted || !d.ProbabilityWeight {
		t.Errorf("probability result face = %+v", d)
	}
	fits := []types.FitSpec{{Name: "m", Request: &types.Request{Regressions: []*types.RegressionSpec{reg(types.SlotWeight{})}}}}
	for _, tc := range []struct {
		w              *types.WeightSpec
		weighted, prob bool
	}{{nil, false, false}, {&freq, true, false}, {&prob, true, true}} {
		d := CatalogFromFits(fits).WithDefaultWeight(fits, tc.w)["m"].Influence
		if d.Weighted != tc.weighted || d.ProbabilityWeight != tc.prob {
			t.Errorf("default %+v: weighted %v probability %v, want %v %v", tc.w, d.Weighted, d.ProbabilityWeight, tc.weighted, tc.prob)
		}
	}
}
