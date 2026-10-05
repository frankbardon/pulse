package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// The weighted floor is how a contingency / proportion overlay places a
// probability-weighted host's Σw cells on N* (weighting-inferential
// E3-S3). With components disabled the floor is gone, and before this
// rule the overlay silently read Σw as a sample size: the per-Request
// χ² kinds scaled by 1, the Compose kinds inferred an unweighted slot.
// Now the overlay is PROCESSING_CONFIG naming the host, at runtime and
// in predict alike; a frequency host (where Σw IS N*) and a host with
// components on are unaffected.

func floorCrosstab(cohort string, reqW *types.WeightSpec, cellW types.SlotWeight, disable *bool, overlays ...types.OverlaySpec) *types.Request {
	return &types.Request{
		Cohort:            &types.Cohort{Filename: cohort},
		Weight:            reqW,
		DisableComponents: disable,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 20}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n", Weight: cellW},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: overlays,
	}
}

func boolp(b bool) *bool { return &b }

func TestWeight_ComponentsDisabledFloorRefusalMatchesPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	prob := types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	freq := types.WeightSpec{Field: "t_u8", Kind: types.WeightKindFrequency}
	chisq := []types.OverlaySpec{
		{Name: "m", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
		{Name: "r", Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow},
		{Name: "c", Kind: types.OverlayKindChiSqCol, Scope: types.OverlayScopeColumn},
	}

	// instance builds a Pulse with or without the engine-level opt-out.
	instance := func(engineOff bool) *pulse.Pulse {
		p, err := pulse.New(pulse.Options{FS: fs, DisableComponents: engineOff})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("request host", func(t *testing.T) {
		for _, engineOff := range []bool{false, true} {
			p := instance(engineOff)
			var disable *bool
			if !engineOff {
				disable = boolp(true)
			}
			for _, spec := range chisq {
				for src, req := range map[string]*types.Request{
					"request":   floorCrosstab(cohort, &prob, types.SlotWeight{}, disable, spec),
					"slot only": floorCrosstab(cohort, nil, types.SlotWeightOf(prob), disable, spec),
				} {
					_, rerr := p.Process(ctx, req)
					ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
					if ce.Details["host"] != "crosstab.cell" || ce.Details["slot"] != "overlays[0]" {
						t.Fatalf("%s %s: details %v", spec.Kind, src, ce.Details)
					}
					sameEntry(t, predictEnvelope(t, p, fs, cohort, req), rerr)
				}
				// Frequency: Σw is N*, nothing hidden. Components on:
				// the floor is read.
				for name, req := range map[string]*types.Request{
					"frequency":     floorCrosstab(cohort, &freq, types.SlotWeight{}, disable, spec),
					"components on": floorCrosstab(cohort, &prob, types.SlotWeight{}, boolp(false), spec),
				} {
					if _, err := p.Process(ctx, req); err != nil {
						t.Fatalf("%s %s: %v", spec.Kind, name, err)
					}
					if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
						t.Fatalf("%s %s: predict %+v", spec.Kind, name, env.Errors)
					}
				}
			}
		}
	})

	t.Run("compose host", func(t *testing.T) {
		slot := func(label string, w *types.WeightSpec, disable *bool, filtered bool) *types.Request {
			r := floorCrosstab(cohort, w, types.SlotWeight{}, disable)
			r.Label = label
			if filtered {
				r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x >= 10"}}
			}
			return r
		}
		mk := func(kind types.OverlayKind, scope types.OverlayScope, refDisable, tgtDisable *bool, w *types.WeightSpec) *types.ComposedRequest {
			return &types.ComposedRequest{
				Requests: []*types.Request{slot("total", w, refDisable, false), slot("sub", w, tgtDisable, true)},
				Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: kind, Scope: scope, Reference: "total", Targets: []string{"sub"}}},
			}
		}
		kinds := []struct {
			kind  types.OverlayKind
			scope types.OverlayScope
			// readsRef: the reference slot's floor matters (it is a
			// tested leg); CHISQ_VS_REF reads the reference only as a
			// distribution.
			readsRef bool
		}{
			{types.OverlayKindPropZCell, types.OverlayScopeCell, true},
			{types.OverlayKindPropZPanel, types.OverlayScopeCell, true},
			{types.OverlayKindChiSqVsRef, types.OverlayScopeMatrix, false},
		}
		for _, engineOff := range []bool{false, true} {
			p := instance(engineOff)
			opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs), DisableComponents: engineOff}
			off, on := boolp(true), boolp(false)
			if engineOff {
				off = nil // inherit the engine opt-out
			}
			for _, k := range kinds {
				refused := map[string]*types.ComposedRequest{"target hidden": mk(k.kind, k.scope, on, off, &prob)}
				if k.readsRef {
					refused["reference hidden"] = mk(k.kind, k.scope, off, on, &prob)
				}
				for name, req := range refused {
					_, rerr := p.Compose(ctx, req)
					ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
					env := descx.ValidateComposeWithOptions(req, opts)
					if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
						t.Fatalf("%s %s: validator %+v, runtime %s %q", k.kind, name, env.Errors, ce.Code, ce.Message)
					}
					want, _ := json.Marshal(ce.Details)
					have, _ := json.Marshal(env.Errors[0].Details)
					if string(want) != string(have) {
						t.Fatalf("%s %s: validator details %s, runtime %s", k.kind, name, have, want)
					}
				}
				runs := map[string]*types.ComposedRequest{
					"frequency":     mk(k.kind, k.scope, off, off, &freq),
					"components on": mk(k.kind, k.scope, on, on, &prob),
				}
				if !k.readsRef {
					runs["reference hidden"] = mk(k.kind, k.scope, off, on, &prob)
				}
				for name, req := range runs {
					if _, err := p.Compose(ctx, req); err != nil {
						t.Fatalf("%s %s: %v", k.kind, name, err)
					}
					if env := descx.ValidateComposeWithOptions(req, opts); len(env.Errors) != 0 {
						t.Fatalf("%s %s: validator %+v", k.kind, name, env.Errors)
					}
				}
			}
		}
	})
}
