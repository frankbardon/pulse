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

// TestValidateOverlays_ProportionWeightedHostNSource: the weighted-host
// n_source rule on the proportion pairwise kind (weighting-inferential
// E3-S2) — an unweighted count (raw rows, slab, distinct keys) is
// PROCESSING_CONFIG on a weighted cell under both kinds, a weight-sum
// source under probability only, with the runtime's message
// (processing TestPairwisePropZ_WeightedHost); unweighted hosts and an
// omitted source predict clean.
func TestValidateOverlays_ProportionWeightedHostNSource(t *testing.T) {
	for name, basis := range map[string]weighting.Basis{"unweighted": weighting.Unweighted,
		"frequency": weighting.Frequency, "probability": weighting.Probability} {
		for _, mode := range append([]string{""}, pwAllNSources...) {
			params := ""
			if mode != "" {
				params = `{"n_source":"` + mode + `"}`
			}
			req := pwWelfordRequest(types.OverlayKindPairwisePropZ, params)
			req.Crosstab.Cell = &types.Aggregation{Type: types.AGG_COUNT, Field: "score"}
			switch basis {
			case weighting.Frequency:
				req.Crosstab.Cell.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
			case weighting.Probability:
				req.Crosstab.Cell.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
			}
			env := descriptor.NewEnvelope(nil)
			ValidateOverlays(env, req, nil, nil)
			cfg := pwPartFindError(env, string(errors.PROCESSING_CONFIG))
			reason := weighting.NSourceRefusal(mode, basis)
			if reason == "" {
				if cfg != nil {
					t.Fatalf("%s n_source %q: refused %v", name, mode, cfg.Message)
				}
				continue
			}
			if cfg == nil || cfg.Message != "overlay OVERLAY_PAIRWISE_PROP_Z n_source "+mode+": "+reason {
				t.Fatalf("%s n_source %s: codes %v, want PROCESSING_CONFIG", name, mode, pwPartErrorCodes(env))
			}
		}
	}
}

// TestValidateOverlays_FisherProbabilityHost: OVERLAY_FISHER_EXACT_CELL
// is frequency-only — a host cell weighted under kind probability by its
// own slot weight (or keeping the request weight while the overlay opts
// out) is PULSE_WEIGHT_UNSUPPORTED naming the kind, with the runtime's
// message (processing TestFisherCell_WeightedHost); a frequency host is
// clean; a probability REQUEST weight reaching the overlay slot is the
// resolver's refusal alone, never a second host entry.
func TestValidateOverlays_FisherProbabilityHost(t *testing.T) {
	prob := types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
	mk := func() *types.Request {
		return &types.Request{
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "a"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b"}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			Overlays: []types.OverlaySpec{{Kind: types.OverlayKindFisherExactCell, Scope: types.OverlayScopeCell}},
		}
	}
	hostRefusals := func(req *types.Request) []*descriptor.EnvelopeEntry {
		env := descriptor.NewEnvelope(nil)
		ValidateOverlays(env, req, nil, nil)
		var out []*descriptor.EnvelopeEntry
		for _, e := range env.Errors {
			if e.Code == string(errors.PULSE_WEIGHT_UNSUPPORTED) {
				out = append(out, e)
			}
		}
		return out
	}
	slotProb := mk()
	slotProb.Crosstab.Cell.Weight = types.SlotWeightOf(prob)
	optedOut := mk()
	optedOut.Weight = &prob
	optedOut.Overlays[0].Weight = types.NullSlotWeight()
	msg, _ := weighting.FrequencyOnlyHostRefusal("overlays[0]", string(types.OverlayKindFisherExactCell))
	for name, req := range map[string]*types.Request{"slot probability": slotProb, "overlay opted out": optedOut} {
		got := hostRefusals(req)
		if len(got) != 1 || got[0].Message != msg || got[0].Details["kind"] != "probability" {
			t.Fatalf("%s: %+v, want the host refusal", name, got)
		}
	}
	slotFreq := mk()
	slotFreq.Crosstab.Cell.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	reqProb := mk()
	reqProb.Weight = &prob
	for name, req := range map[string]*types.Request{"slot frequency": slotFreq, "request probability": reqProb, "unweighted": mk()} {
		if got := hostRefusals(req); len(got) != 0 {
			t.Fatalf("%s: host refusal %+v", name, got)
		}
	}
}

// TestValidateCompose_PanelWeightedSlotNSource: OVERLAY_PROP_Z_PANEL's
// unweighted-count modes on a panel with a weighted slot are
// PROCESSING_CONFIG with the runtime's message (processing
// TestPropZPanel_WeightedSlots); the payload-margin modes are clean.
func TestValidateCompose_PanelWeightedSlotNSource(t *testing.T) {
	for _, mode := range []string{types.PanelNSourceCellNUnweighted, types.PanelNSourceRowMarginDistinctWithin,
		types.PanelNSourceRowMarginValue, types.PanelNSourceRowMarginValueWithin} {
		req := composePanelRequest(map[string]any{"n_source": mode})
		req.Requests[1].Crosstab.Cell.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
		cfg := pwPartFindError(ValidateCompose(req), string(errors.PROCESSING_CONFIG))
		reason := weighting.NSourceRefusal(mode, weighting.Frequency)
		if reason == "" {
			if cfg != nil {
				t.Fatalf("n_source %s refused: %s", mode, cfg.Message)
			}
			continue
		}
		if cfg == nil || cfg.Message != "overlay OVERLAY_PROP_Z_PANEL n_source "+mode+": "+reason {
			t.Fatalf("n_source %s: %+v", mode, cfg)
		}
		// Unweighted slots: clean of the rule.
		if pwPartFindError(ValidateCompose(composePanelRequest(map[string]any{"n_source": mode})), string(errors.PROCESSING_CONFIG)) != nil {
			t.Fatalf("n_source %s refused on unweighted slots", mode)
		}
	}
}
