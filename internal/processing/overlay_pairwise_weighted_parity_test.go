package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"

	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// TestPairwiseWeightedTwoMeansZ_PredictRuntimeParity runs one request
// table through BOTH arms of OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z's
// contract — descriptor.ValidateOverlays (predict) and a real buffered
// crosstab (runtime) — and requires them to agree: refused by one iff
// refused by the other, and the runtime's code is among predict's. The
// weighted kind's refusals are deliberately two-armed (unlike the
// Welford kinds' predict-only selector refusal), so drift on either side
// is a contract break.
func TestPairwiseWeightedTwoMeansZ_PredictRuntimeParity(t *testing.T) {
	cases := []struct {
		name   string
		params string
		cell   types.AggregationType
	}{
		{"weights_ok", `{"n_basis":"weights"}`, types.AGG_WEIGHTED_MEAN},
		{"kish_ok", `{"n_basis":"kish"}`, types.AGG_WEIGHTED_MEAN},
		{"missing_n_basis", `{}`, types.AGG_WEIGHTED_MEAN},
		{"empty_n_basis", `{"n_basis":""}`, types.AGG_WEIGHTED_MEAN},
		{"unknown_n_basis", `{"n_basis":"effective"}`, types.AGG_WEIGHTED_MEAN},
		{"n_source", `{"n_basis":"kish","n_source":"cell_weight_sum"}`, types.AGG_WEIGHTED_MEAN},
		{"p_source", `{"n_basis":"weights","p_source":"cell_value"}`, types.AGG_WEIGHTED_MEAN},
		{"both_selectors", `{"n_basis":"weights","n_source":"row_margin_n","p_source":"cell_value"}`, types.AGG_WEIGHTED_MEAN},
		{"welford_cell", `{"n_basis":"weights"}`, types.AGG_WELFORD},
		{"average_cell", `{"n_basis":"kish"}`, types.AGG_AVERAGE},
	}
	refusals := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := wtzSchema(t)
			req := crosstabWeightedOverlayBaseRequest()
			if tc.cell != types.AGG_WEIGHTED_MEAN {
				req.Crosstab.Cell = &types.Aggregation{Type: tc.cell, Field: "value", Label: "cell"}
			}
			req.Overlays = []types.OverlaySpec{{
				Name:   "wtz",
				Kind:   types.OverlayKindPairwiseWeightedTwoMeansZ,
				Scope:  types.OverlayScopeRow,
				Params: json.RawMessage(tc.params),
			}}

			env := descriptor.NewEnvelope(nil)
			descx.ValidateOverlays(env, req, schema, nil)
			predictCodes := map[string]bool{}
			for _, e := range env.Errors {
				predictCodes[e.Code] = true
			}

			_, err := runBufferedCrosstabWithComponents(t, schema, req, wtzRecords(schema), false)

			if (len(env.Errors) != 0) != (err != nil) {
				t.Fatalf("predict refused=%v (%v), runtime refused=%v (%v)",
					len(env.Errors) != 0, env.Errors, err != nil, err)
			}
			if err == nil {
				return
			}
			refusals++
			matched := false
			for code := range predictCodes {
				if pairwiseErrHasCode(err, errors.Code(code)) {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("runtime error %v carries no code predict raised (%v)", err, predictCodes)
			}
		})
	}
	if refusals == 0 {
		t.Fatal("no case refused: the parity table is vacuous")
	}
}

// TestPairwiseOverlay_NBasisInertAtRuntimeOnOtherKinds pins the runtime
// half of the n_basis policy on every pairwise kind except the weighted
// one: predict refuses the param (descriptor.
// TestValidateOverlays_NBasisRefusedOnOtherPairwiseKinds), but the
// runtime does NOT — the param is inert, so the overlay output is
// byte-identical with and without it and a pulse.Process that skips
// predict keeps succeeding. Same predict-only split as the Welford
// kinds' n_source / p_source refusal.
func TestPairwiseOverlay_NBasisInertAtRuntimeOnOtherKinds(t *testing.T) {
	for _, kind := range types.AllOverlayKinds() {
		if !types.IsPairwiseOverlayKind(kind) || types.PairwiseKindUsesWeightedMoments(kind) {
			continue
		}
		t.Run(string(kind), func(t *testing.T) {
			run := func(params string) string {
				schema := wtzSchema(t)
				req := crosstabWeightedOverlayBaseRequest()
				if types.PairwiseKindUsesWelford(kind) {
					req.Crosstab.Cell = &types.Aggregation{Type: types.AGG_WELFORD, Field: "value", Label: "cell"}
				}
				req.Overlays = []types.OverlaySpec{{
					Name: "pw", Kind: kind, Scope: types.OverlayScopeRow,
					Params: json.RawMessage(params),
				}}
				resp, err := runBufferedCrosstabWithComponents(t, schema, req, wtzRecords(schema), false)
				if err != nil {
					t.Fatalf("params %s: runtime refused: %v", params, err)
				}
				if len(resp.Overlays) != 1 {
					t.Fatalf("params %s: want 1 overlay layer, got %d", params, len(resp.Overlays))
				}
				return jsonOf(t, resp.Overlays)
			}
			if without, with := run(`{}`), run(`{"n_basis":"kish"}`); without != with {
				t.Fatalf("n_basis changed the output:\nwithout %s\nwith    %s", without, with)
			}
		})
	}
}

// TestPairwiseWeightedMomentKeysMatchCapabilities pins the four keys the
// weighted overlay reads (weightedMomentKeys) against the DECLARED
// ComponentSchema: every one must be an AGG_WEIGHTED_MEAN key, and no
// other built-in aggregator may declare all four. The second half is the
// premise of predict's cell-host refusal (descriptor.
// validateOverlayPairwise refuses any other built-in cell type); if a
// second built-in ever emitted the moments, that refusal would reject a
// host the runtime accepts.
func TestPairwiseWeightedMomentKeysMatchCapabilities(t *testing.T) {
	declared := func(agg types.AggregationType) map[string]bool {
		m := descx.BuildManifest()
		schema, ok := m.ComponentsSchemas.Aggregators[string(agg)]
		if !ok {
			return nil
		}
		out := map[string]bool{}
		for _, k := range schema.Keys {
			out[k.Name] = true
		}
		return out
	}
	hasAll := func(keys map[string]bool) bool {
		for _, k := range weightedMomentKeys {
			if !keys[k] {
				return false
			}
		}
		return true
	}
	if !hasAll(declared(types.AGG_WEIGHTED_MEAN)) {
		t.Fatalf("AGG_WEIGHTED_MEAN ComponentSchema lacks one of %v — update weightedMomentKeys and "+
			"descriptor/capabilities_aggregators.go together", weightedMomentKeys)
	}
	for _, agg := range types.AllAggregationTypes() {
		if agg == types.AGG_WEIGHTED_MEAN {
			continue
		}
		if hasAll(declared(agg)) {
			t.Errorf("%s declares every weighted-moment key %v; predict's cell-host refusal for "+
				"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z would now reject a host the runtime accepts", agg, weightedMomentKeys)
		}
	}
}
