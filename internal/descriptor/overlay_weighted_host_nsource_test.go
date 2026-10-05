package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Predict arm of the weighted-host n_source rule (weighting-inferential
// E3-S1, weighting.NSourceRefusal). A host is weighted when its crosstab
// cell resolves an APPLIED weight — slot, request or instance default —
// exactly the cells that carry the weighted floor keys at runtime. There
// a raw-row-count n_source (or the weight sum under kind probability) is
// PROCESSING_CONFIG with the runtime's message (processing
// TestPairwiseWelchT_WeightedHostNSource pins that side; the root
// TestWeight_PairwiseNSourceMatchesPredict pins the two together). Every
// other selector keeps the Welford kind's inertness refusal.
func TestValidateOverlays_WeightedHostNSource(t *testing.T) {
	freq := &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	prob := &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
	type host struct {
		basis weighting.Basis
		build func(req *types.Request) *PredictOptions
	}
	hosts := map[string]host{
		"unweighted": {weighting.Unweighted, func(*types.Request) *PredictOptions { return nil }},
		"slot frequency": {weighting.Frequency, func(r *types.Request) *PredictOptions {
			r.Crosstab.Cell.Weight = types.SlotWeightOf(*freq)
			return nil
		}},
		"slot probability": {weighting.Probability, func(r *types.Request) *PredictOptions {
			r.Crosstab.Cell.Weight = types.SlotWeightOf(*prob)
			return nil
		}},
		"request frequency": {weighting.Frequency, func(r *types.Request) *PredictOptions {
			r.Weight = freq
			return nil
		}},
		"default probability": {weighting.Probability, func(*types.Request) *PredictOptions {
			return &PredictOptions{DefaultWeight: prob}
		}},
		"default opted out": {weighting.Unweighted, func(r *types.Request) *PredictOptions {
			r.Crosstab.Cell.Weight = types.NullSlotWeight()
			return &PredictOptions{DefaultWeight: prob}
		}},
	}
	for name, h := range hosts {
		for _, mode := range pwAllNSources {
			req := pwWelfordRequest(types.OverlayKindPairwiseWelchT, `{"n_source":"`+mode+`"}`)
			opts := h.build(req)
			env := descriptor.NewEnvelope(nil)
			ValidateOverlays(env, req, nil, opts)
			reason := weighting.NSourceRefusal(mode, h.basis)
			cfg := pwPartFindError(env, string(errors.PROCESSING_CONFIG))
			inert := pwPartFindError(env, string(errors.PULSE_OVERLAY_PARAM_MISSING))
			if reason == "" {
				if cfg != nil || inert == nil {
					t.Fatalf("%s n_source %s: codes %v, want the inertness refusal only", name, mode, pwPartErrorCodes(env))
				}
				continue
			}
			if cfg == nil || inert != nil {
				t.Fatalf("%s n_source %s: codes %v, want PROCESSING_CONFIG only", name, mode, pwPartErrorCodes(env))
			}
			if want := "overlay OVERLAY_PAIRWISE_WELCH_T n_source " + mode + ": " + reason; cfg.Message != want {
				t.Fatalf("%s: message %q, want %q", name, cfg.Message, want)
			}
			if cfg.Details["n_source"] != mode || cfg.Details["kind"] != string(types.OverlayKindPairwiseWelchT) {
				t.Fatalf("%s: details %v", name, cfg.Details)
			}
		}
	}
}
