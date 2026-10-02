package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlays_WeightedTwoMeansZParams pins the predict arm of
// OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z's param contract: n_basis is
// REQUIRED (weights | kish, no default) and n_source / p_source are
// refused. Every refusal carries PULSE_OVERLAY_PARAM_MISSING and names
// the offending param in details, matching the runtime twin in
// processing.applyPairwiseWeightedTwoMeansZ.
func TestValidateOverlays_WeightedTwoMeansZParams(t *testing.T) {
	kind := types.OverlayKindPairwiseWeightedTwoMeansZ
	cases := []struct {
		name   string
		params string
		param  string // "" = expect a clean envelope
	}{
		{"weights_ok", `{"n_basis":"weights"}`, ""},
		{"kish_ok", `{"n_basis":"kish"}`, ""},
		{"kish_with_pair_along_dim_ok", `{"n_basis":"kish","pair_along_dim":0}`, ""},
		{"missing_params", ``, "n_basis"},
		{"empty_n_basis", `{"n_basis":""}`, "n_basis"},
		{"unknown_n_basis", `{"n_basis":"effective"}`, "n_basis"},
		{"n_source_refused", `{"n_basis":"kish","n_source":"cell_weight_sum"}`, "n_source"},
		{"p_source_refused", `{"n_basis":"weights","p_source":"cell_value"}`, "p_source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := pwWelfordValidate(kind, tc.params)
			if tc.param == "" {
				if len(env.Errors) != 0 {
					t.Fatalf("params %s: expected no errors, got %v", tc.params, pwPartErrorCodes(env))
				}
				return
			}
			got := pwPartFindError(env, string(errors.PULSE_OVERLAY_PARAM_MISSING))
			if got == nil {
				t.Fatalf("params %s: expected PULSE_OVERLAY_PARAM_MISSING, got %v", tc.params, pwPartErrorCodes(env))
			}
			if got.Details["param"] != tc.param {
				t.Fatalf("params %s: refusal names param %v, want %s", tc.params, got.Details["param"], tc.param)
			}
		})
	}
}
