package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// pwWeightedRequest is pwWelfordRequest with the cell the weighted kind
// actually reads: AGG_WEIGHTED_MEAN under a FREQUENCY slot weight, so
// n_basis "weights" stands (on a probability host it is refused, U12
// review WS-06 — TestValidateOverlays_WeightedTwoMeansZNBasisOnProbabilityHost).
// cellType overrides it when non-nil.
func pwWeightedRequest(kind types.OverlayKind, params string, cellType *types.AggregationType) *types.Request {
	req := pwWelfordRequest(kind, params)
	req.Crosstab.Cell = &types.Aggregation{
		Type:   types.AGG_WEIGHTED_MEAN,
		Field:  "score",
		Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}),
	}
	if cellType != nil {
		req.Crosstab.Cell = &types.Aggregation{Type: *cellType, Field: "score"}
	}
	return req
}

func pwWeightedValidate(kind types.OverlayKind, params string, cellType *types.AggregationType) *descriptor.Envelope {
	env := descriptor.NewEnvelope(nil)
	ValidateOverlays(env, pwWeightedRequest(kind, params, cellType), nil, nil)
	return env
}

// TestValidateOverlays_WeightedTwoMeansZParams pins the predict arm of
// OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z's param contract: n_basis is
// REQUIRED (weights | kish, no default) and n_source / p_source are
// refused. Every refusal carries PULSE_OVERLAY_PARAM_MISSING and names
// the offending param in details, matching the runtime twin in
// processing.applyPairwiseWeightedTwoMeansZ (parity:
// processing.TestPairwiseWeightedTwoMeansZ_PredictRuntimeParity).
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
			env := pwWeightedValidate(kind, tc.params, nil)
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

// TestValidateOverlays_WeightedTwoMeansZNBasisOnProbabilityHost (U12
// review WS-06): n_basis "weights" on a probability-weighted cell — the
// weight_field sugar, or a slot weight of kind probability — reads Σw as
// n and is PROCESSING_CONFIG naming param n_basis, with
// weighting.NBasisRefusal's reason; "kish" on the same host is clean.
func TestValidateOverlays_WeightedTwoMeansZNBasisOnProbabilityHost(t *testing.T) {
	kind := types.OverlayKindPairwiseWeightedTwoMeansZ
	for name, cell := range map[string]*types.Aggregation{
		"sugar":       {Type: types.AGG_WEIGHTED_MEAN, Field: "score", Params: json.RawMessage(`{"weight_field":"w"}`)},
		"slot weight": {Type: types.AGG_WEIGHTED_MEAN, Field: "score", Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})},
	} {
		for _, nb := range []string{types.PairwiseNBasisWeights, types.PairwiseNBasisKish} {
			req := pwWelfordRequest(kind, `{"n_basis":"`+nb+`"}`)
			req.Crosstab.Cell = cell
			env := descriptor.NewEnvelope(nil)
			ValidateOverlays(env, req, nil, nil)
			got := pwPartFindError(env, string(errors.PROCESSING_CONFIG))
			if nb == types.PairwiseNBasisKish {
				if len(env.Errors) != 0 {
					t.Fatalf("%s kish: expected clean, got %v", name, pwPartErrorCodes(env))
				}
				continue
			}
			if got == nil || got.Details["param"] != "n_basis" || len(env.Errors) != 1 ||
				!strings.Contains(got.Message, weighting.NBasisRefusal(nb, weighting.Probability)) {
				t.Fatalf("%s weights: errors %v (%+v)", name, pwPartErrorCodes(env), got)
			}
		}
	}
}

// TestValidateOverlays_WeightedTwoMeansZCellHost pins the predict-visible
// shape refusal: a crosstab cell naming a BUILT-IN aggregator other than
// AGG_WEIGHTED_MEAN fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE (the
// runtime code). An empty Type (smart default, resolved later) and an
// extension type defer to runtime, which gates on the emitted keys.
func TestValidateOverlays_WeightedTwoMeansZCellHost(t *testing.T) {
	kind := types.OverlayKindPairwiseWeightedTwoMeansZ
	ty := func(s types.AggregationType) *types.AggregationType { return &s }
	cases := []struct {
		name    string
		cell    *types.AggregationType
		refused bool
	}{
		{"weighted_mean_ok", nil, false},
		{"welford_refused", ty(types.AGG_WELFORD), true},
		{"average_refused", ty(types.AGG_AVERAGE), true},
		{"empty_type_defers", ty(""), false},
		{"extension_type_defers", ty("AGG_ACME_MOMENTS_X"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := pwWeightedValidate(kind, `{"n_basis":"weights"}`, tc.cell)
			got := pwPartFindError(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE))
			if tc.refused != (got != nil) {
				t.Fatalf("refused=%v, want %v (errors %v)", got != nil, tc.refused, pwPartErrorCodes(env))
			}
			if !tc.refused && len(env.Errors) != 0 {
				t.Fatalf("expected clean envelope, got %v", pwPartErrorCodes(env))
			}
		})
	}
}

// TestValidateOverlays_NBasisRefusedOnOtherPairwiseKinds pins the
// predict policy for n_basis on every pairwise kind except the weighted
// one: it is inert there, so — like the Welford kinds' n_source /
// p_source refusal — predict refuses it with PULSE_OVERLAY_PARAM_MISSING
// naming param n_basis. Predict-only: the runtime stays tolerant (pinned
// by processing.TestPairwiseOverlay_NBasisInertAtRuntimeOnOtherKinds).
func TestValidateOverlays_NBasisRefusedOnOtherPairwiseKinds(t *testing.T) {
	var kinds []types.OverlayKind
	for _, k := range types.AllOverlayKinds() {
		if types.IsPairwiseOverlayKind(k) && !types.PairwiseKindUsesWeightedMoments(k) {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		t.Fatal("no non-weighted pairwise kinds found")
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			// Control: the same request without n_basis predicts clean.
			if env := pwWelfordValidate(kind, `{}`); len(env.Errors) != 0 {
				t.Fatalf("control: expected clean envelope, got %v", pwPartErrorCodes(env))
			}
			env := pwWelfordValidate(kind, `{"n_basis":"kish"}`)
			got := pwPartFindError(env, string(errors.PULSE_OVERLAY_PARAM_MISSING))
			if got == nil || got.Details["param"] != "n_basis" {
				t.Fatalf("expected PULSE_OVERLAY_PARAM_MISSING naming n_basis, got %v", pwPartErrorCodes(env))
			}
		})
	}

	// Reported alongside a Welford selector refusal, not instead of it.
	env := pwWelfordValidate(types.OverlayKindPairwiseTwoMeansZ, `{"n_basis":"kish","n_source":"row_margin_n"}`)
	params := map[any]bool{}
	for _, e := range env.Errors {
		params[e.Details["param"]] = true
	}
	if !params["n_basis"] || !params["n_source"] {
		t.Fatalf("expected both n_basis and n_source refusals, got %v", pwPartErrorCodes(env))
	}
}

// TestCompose_WeightedTwoMeansZTreatedLikeWelfordTwin pins the compose
// predict surface (internal/descriptor/compose.go iterates AllOverlayKinds): the
// weighted kind is a known kind and carries the same matrix-requirement
// classification as its Welford twin, so compose neither rejects it as
// unknown nor routes it differently.
func TestCompose_WeightedTwoMeansZTreatedLikeWelfordTwin(t *testing.T) {
	w, twin := types.OverlayKindPairwiseWeightedTwoMeansZ, types.OverlayKindPairwiseTwoMeansZ
	if !composeOverlayKindKnown(w) {
		t.Fatalf("compose does not know %s", w)
	}
	if kindRequiresMatrixCompose(w) != kindRequiresMatrixCompose(twin) {
		t.Fatalf("kindRequiresMatrixCompose(%s) = %v, twin %s = %v",
			w, kindRequiresMatrixCompose(w), twin, kindRequiresMatrixCompose(twin))
	}
}
