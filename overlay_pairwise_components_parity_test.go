package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPairwise_ComponentsRequiredMatchesRuntime (E4-S3): every
// OVERLAY_PAIRWISE_* kind reads its sample-size legs off the crosstab
// components, so with components disabled — Options.DisableComponents,
// or the request's own disable_components: true — the runtime refuses
// PULSE_OVERLAY_COMPONENTS_REQUIRED, and predict now reports the same
// code, message and details (index included) as the only error, on the
// fused and buffered arms and in a Compose slot. Predict used to accept
// it. A request's disable_components: false re-opens an
// Options-disabled instance, so it runs on both sides.
func TestPairwise_ComponentsRequiredMatchesRuntime(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	on, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	off, err := pulse.New(pulse.Options{FS: fs, DisableComponents: true})
	if err != nil {
		t.Fatal(err)
	}
	offBuffered, err := pulse.New(pulse.Options{FS: fs, DisableComponents: true, DisableCrosstabFusion: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	yes, no := true, false
	cols := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}}
	host := func(kind types.OverlayKind, disable *bool) *types.Request {
		spec := types.OverlaySpec{Name: "pw", Kind: kind, Scope: types.OverlayScopeColumn}
		cell := types.AGG_COUNT
		var w *types.WeightSpec
		switch kind {
		case types.OverlayKindPairwiseWelchT, types.OverlayKindPairwiseTwoMeansZ:
			cell = types.AGG_WELFORD
		case types.OverlayKindPairwiseWeightedTwoMeansZ:
			cell = types.AGG_WEIGHTED_MEAN
			w = &types.WeightSpec{Field: "y", Kind: types.WeightKindFrequency}
			spec.Params = json.RawMessage(`{"n_basis":"weights"}`)
		}
		r := markovCrosstab(cohort, cols, cell, w, spec)
		r.DisableComponents = disable
		return r
	}
	for _, kind := range []types.OverlayKind{
		types.OverlayKindPairwisePropZ, types.OverlayKindPairwiseProbitT,
		types.OverlayKindPairwiseWelchT, types.OverlayKindPairwiseTwoMeansZ,
		types.OverlayKindPairwiseWeightedTwoMeansZ,
	} {
		t.Run(string(kind), func(t *testing.T) {
			// Control: components on, the host runs clean on both sides.
			if env := predictEnvelope(t, on, fs, cohort, host(kind, nil)); len(env.Errors) != 0 {
				t.Fatalf("control predict %v", envCodes(env))
			}
			if _, err := on.Process(ctx, host(kind, nil)); err != nil {
				t.Fatalf("control process: %v", err)
			}
			// The request opts back in on an Options-disabled instance.
			if env := predictEnvelope(t, off, fs, cohort, host(kind, &no)); len(env.Errors) != 0 {
				t.Fatalf("re-opened predict %v", envCodes(env))
			}
			if _, err := off.Process(ctx, host(kind, &no)); err != nil {
				t.Fatalf("re-opened process: %v", err)
			}
			want := errors.PULSE_OVERLAY_COMPONENTS_REQUIRED
			for _, c := range []struct {
				name     string
				inst     *pulse.Pulse
				buffered *pulse.Pulse
				disable  *bool
			}{
				{"options", off, offBuffered, nil},
				{"request", on, offBuffered, &yes},
			} {
				env := predictEnvelope(t, c.inst, fs, cohort, host(kind, c.disable))
				if len(env.Errors) != 1 {
					t.Fatalf("%s: predict %v, want one refusal", c.name, envCodes(env))
				}
				pe := env.Errors[0]
				if pe.Details["index"] != 0 {
					t.Fatalf("%s: predict details %v", c.name, pe.Details)
				}
				_, err := c.inst.Process(ctx, host(kind, c.disable))
				sameRefusal(t, c.name+" fused", pe, err, want)
				_, err = c.buffered.Process(ctx, host(kind, c.disable))
				sameRefusal(t, c.name+" buffered", pe, err, want)
				_, err = c.inst.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{host(kind, c.disable)}})
				sameRefusal(t, c.name+" compose slot", pe, unlocated(t, err), want)
			}
		})
	}
}
