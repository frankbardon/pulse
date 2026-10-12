package pulse_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Compose OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF over a grouped AGG_WELFORD
// slot read each row's moments from that slot's per-group Components
// (E4-S11). With components disabled — engine default or the slot's
// disable_components — the layer came back EMPTY with no error. It is
// now PULSE_OVERLAY_COMPONENTS_REQUIRED on predict and runtime alike,
// one message and one Details map (E4-S13). A `return` that excludes
// components does not remove them before the fold (the Compose veto
// keeps every sub-part of a slot an overlay names), so it still runs.
//
// Every parity case runs PREDICT FIRST on a fresh request.

// wcCompose is nsCompose over AGG_WELFORD with each slot's
// disable_components / return set.
func wcCompose(kind types.OverlayKind, layout string, field any, refDisable, tgtDisable *bool, ret *types.Return) *pulse.ComposedRequest {
	req := nsCompose(nsAgg{types.AGG_WELFORD, "score"}, kind, layout, field)
	req.Requests[0].DisableComponents = refDisable
	req.Requests[1].DisableComponents = tgtDisable
	req.Requests[0].Return = ret
	req.Requests[1].Return = ret
	return req
}

func TestComposeOverlay_WelfordComponentsDisabledRefused(t *testing.T) {
	ctx := context.Background()
	on, off := boolp(false), boolp(true)
	for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
		for _, engineOff := range []bool{false, true} {
			p, _ := nsFixture(t, pulse.Options{DisableComponents: engineOff})
			hidden := off
			if engineOff {
				hidden = nil // inherit the engine opt-out
			}
			refused := []struct {
				name     string
				ref, tgt *bool
				layout   string
				field    any
				j, ai    int
				slot     string
			}{
				{"reference hidden", hidden, on, "n", nil, 0, 0, "a"},
				{"target hidden", on, hidden, "n", nil, 1, 0, "b"},
				{"both hidden", hidden, hidden, "n", nil, 0, 0, "a"},
				{"scored by field", hidden, hidden, "sn", "n", 0, 1, "a"},
			}
			for _, c := range refused {
				t.Run(fmt.Sprintf("%s/engineOff=%v/%s", kind, engineOff, c.name), func(t *testing.T) {
					env, err := p.PredictCompose(ctx, wcCompose(kind, c.layout, c.field, c.ref, c.tgt, nil))
					if err != nil {
						t.Fatal(err)
					}
					want := map[string]any{"overlay_index": 0, "kind": string(kind), "slot": c.slot,
						"host": fmt.Sprintf("requests[%d].aggregations[%d]", c.j, c.ai), "aggregation": string(types.AGG_WELFORD),
						"label": "n", "reason": "components_disabled"}
					if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_COMPONENTS_REQUIRED) ||
						!reflect.DeepEqual(env.Errors[0].Details, want) {
						t.Fatalf("PredictCompose = %+v, want one PULSE_OVERLAY_COMPONENTS_REQUIRED %v", env.Errors, want)
					}
					_, err = p.Compose(ctx, wcCompose(kind, c.layout, c.field, c.ref, c.tgt, nil))
					ce := codeOf(t, err)
					if ce.Code != perr.PULSE_OVERLAY_COMPONENTS_REQUIRED || !reflect.DeepEqual(ce.Details, want) {
						t.Fatalf("Compose = %s %v, want PULSE_OVERLAY_COMPONENTS_REQUIRED %v", ce.Code, ce.Details, want)
					}
					if ce.Message != env.Errors[0].Message {
						t.Errorf("message drift: runtime %q, predict %q", ce.Message, env.Errors[0].Message)
					}
				})
			}

			// Runs: components on (explicit false re-opens an engine-off
			// gate), a `return` excluding components, and a scalar scored
			// beside the hidden AGG_WELFORD (it reads the params triple).
			base, _ := nsFixture(t, pulse.Options{})
			baseline, cerr := base.Compose(ctx, wcCompose(kind, "n", nil, nil, nil, nil))
			if cerr != nil {
				t.Fatal(cerr)
			}
			want := nsJSON(t, baseline.Overlays)
			if baseline.Overlays[0].Payload.Series == nil || len(baseline.Overlays[0].Payload.Series.Entries) == 0 {
				t.Fatalf("baseline layer empty: %s", want)
			}
			runs := []struct {
				name     string
				ref, tgt *bool
				layout   string
				field    any
				ret      *types.Return
				same     bool
			}{
				{"components on", on, on, "n", nil, nil, true},
				{"return excludes components", on, on, "n", nil, &types.Return{Exclude: []string{"components"}}, true},
				{"scalar scored beside", hidden, hidden, "ns", "s", nil, false},
			}
			for _, c := range runs {
				t.Run(fmt.Sprintf("%s/engineOff=%v/%s", kind, engineOff, c.name), func(t *testing.T) {
					env, err := p.PredictCompose(ctx, wcCompose(kind, c.layout, c.field, c.ref, c.tgt, c.ret))
					if err != nil {
						t.Fatal(err)
					}
					if len(env.Errors) != 0 {
						t.Fatalf("PredictCompose errors = %+v, want none", env.Errors)
					}
					out, err := p.Compose(ctx, wcCompose(kind, c.layout, c.field, c.ref, c.tgt, c.ret))
					if err != nil {
						t.Fatalf("Compose: %v", err)
					}
					if len(out.Overlays) != 1 || out.Overlays[0].Payload.Series == nil || len(out.Overlays[0].Payload.Series.Entries) == 0 {
						t.Fatalf("overlays = %s, want one non-empty SERIES layer", nsJSON(t, out.Overlays))
					}
					if got := nsJSON(t, out.Overlays); c.same && got != want {
						t.Errorf("layers =\n%s\nwant (components-on run)\n%s", got, want)
					}
				})
			}
		}
	}
}
