package pulse_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// A grouped slot (SERIES) against an ungrouped one (SCALAR) is a SHAPE
// mismatch, not a key-set divergence: the ungrouped slot's single Data
// row is not a coordinate grid. Predict reads the shape off the
// authored Request; the runtime must judge it the same way rather than
// off the materialised Data, which is non-empty on both sides.
//
// Every parity case runs PREDICT FIRST on a fresh request: the runtime
// resolves smart defaults in place on the caller's aggregation
// pointers, which would hide a predict gap.

// TestComposeSeriesOverlay_GroupedVsUngroupedShapeParity: predict-
// compose and the runtime refuse a grouped slot paired with an
// ungrouped one under the same code with deep-equal Details, whichever
// side is grouped. A MATRIX-requiring kind refuses the series
// reference first (SLOT_NOT_CROSSTAB) on both sides, as before any
// per-target shape check.
func TestComposeSeriesOverlay_GroupedVsUngroupedShapeParity(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	type tc struct {
		name          string
		kind          types.OverlayKind
		ungroupedSlot int // 0 = reference F, 1 = target M; -1 = none
		crosstabRef   bool
		code          perr.Code
		want          map[string]any
	}
	shape := func(kind types.OverlayKind, refShape, tShape string) map[string]any {
		return map[string]any{
			"index":           0,
			"kind":            string(kind),
			"reference":       "F",
			"target_label":    "M",
			"reference_shape": refShape,
			"target_shape":    tShape,
		}
	}
	var cases []tc
	for _, kind := range []types.OverlayKind{types.OverlayKindIndexVsRef, types.OverlayKindDeltaVsRef, types.OverlayKindTVsRef} {
		cases = append(cases,
			tc{"grouped ref/" + string(kind), kind, 1, false, perr.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT, shape(kind, "series", "scalar")},
			tc{"ungrouped ref/" + string(kind), kind, 0, false, perr.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT, shape(kind, "scalar", "series")},
		)
	}
	// A crosstab reference against a grouped target: MATRIX vs SERIES is
	// a shape refusal on both sides too — the runtime key-set gate
	// skips a mixed-shape pair rather than diffing matrix and series
	// keys.
	cases = append(cases, tc{name: "crosstab ref", kind: types.OverlayKindIndexVsRef, ungroupedSlot: -1, crosstabRef: true,
		code: perr.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT, want: shape(types.OverlayKindIndexVsRef, "matrix", "series")})
	cases = append(cases, tc{"matrix kind", types.OverlayKindPropZCell, 1, false, perr.PULSE_OVERLAY_SLOT_NOT_CROSSTAB, map[string]any{
		"index":          0,
		"kind":           string(types.OverlayKindPropZCell),
		"required_shape": "MATRIX",
		"target_label":   "reference",
		"observed_shape": "series",
	}})
	cases = append(cases, tc{"matrix kind ungrouped ref", types.OverlayKindPropZCell, 0, false, perr.PULSE_OVERLAY_SLOT_NOT_CROSSTAB, map[string]any{
		"index":          0,
		"kind":           string(types.OverlayKindPropZCell),
		"required_shape": "MATRIX",
		"target_label":   "reference",
		"observed_shape": "scalar",
	}})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			build := func() *types.ComposedRequest {
				req := csCompose(f, csOverlay(c.kind, ""), csRegion, csZ)
				if c.ungroupedSlot >= 0 {
					req.Requests[c.ungroupedSlot].Groups = nil
				}
				if c.crosstabRef {
					ref := req.Requests[0]
					ref.Crosstab = &types.CrosstabSpec{Rows: []*types.Group{mgRegion()}, Columns: []*types.Group{mgSex()}, Cell: csZ()}
					ref.Groups, ref.Aggregations = nil, nil
				}
				return req
			}
			env := csPredict(t, f.p, build())
			if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				t.Fatalf("predict-compose: want %s first, got %+v", c.code, env.Errors)
			}
			if !reflect.DeepEqual(env.Errors[0].Details, c.want) {
				t.Errorf("predict details %#v, want %#v", env.Errors[0].Details, c.want)
			}
			_, err := f.p.Compose(context.Background(), build())
			ce := requireCodedError(t, err, c.code)
			if !reflect.DeepEqual(ce.Details, c.want) {
				t.Errorf("runtime details %#v, want %#v", ce.Details, c.want)
			}
		})
	}
}

// TestComposeSeriesOverlay_UngroupedPairStillRuns: two ungrouped slots
// share the SCALAR shape — predict admits them and the runtime folds
// them without a refusal, as before.
func TestComposeSeriesOverlay_UngroupedPairStillRuns(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	build := func() *types.ComposedRequest {
		req := csCompose(f, csOverlay(types.OverlayKindIndexVsRef, ""), csRegion, csZ)
		for _, r := range req.Requests {
			r.Groups = nil
		}
		return req
	}
	if env := csPredict(t, f.p, build()); len(env.Errors) > 0 {
		t.Fatalf("predict-compose: %+v", env.Errors)
	}
	if _, err := f.p.Compose(context.Background(), build()); err != nil {
		t.Fatalf("compose: %v", err)
	}
}

// TestChainSeriesOverlay_GroupedVsUngroupedShapeParity: a grouped
// stage against an ungrouped one is PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT
// on both sides — predict's envelope error, the runtime's layer warning
// over an empty payload — with the same shapes and stage indices, in
// either direction, and with deep-equal Details (the runtime adds
// overlay_index only). Before, the runtime saw two non-empty Data slices,
// folded them as SERIES and answered PULSE_OVERLAY_REF_ZERO.
func TestChainSeriesOverlay_GroupedVsUngroupedShapeParity(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	for _, kind := range []types.OverlayKind{types.OverlayKindIndexVsStage, types.OverlayKindDeltaVsStage} {
		for _, c := range []struct {
			name              string
			refIdx, targetIdx int
			refShape, tShape  string
		}{
			// Stage 1 (ungrouped) is the target; stage 0 (grouped) the ref.
			{"ungrouped target", 0, 1, "series", "scalar"},
			// The same stages with the roles swapped.
			{"ungrouped ref", 1, 0, "scalar", "series"},
		} {
			t.Run(string(kind)+"/"+c.name, func(t *testing.T) {
				build := func() *types.ChainRequest {
					req := chChain(f, kind, "", "zz_n")
					req.Stages[1].Request.Groups = nil
					ref, target := c.refIdx, c.targetIdx
					req.Overlays[0].Ref = types.StageRef{Index: &ref}
					req.Overlays[0].Target = types.StageRef{Index: &target}
					return req
				}
				// Every key predict raises the divergence with — index,
				// stage_index and stage_name (the target stage) included —
				// rides the runtime warning too (E4-S8).
				want := map[string]any{
					"index":        0,
					"kind":         string(kind),
					"ref_index":    c.refIdx,
					"ref_shape":    c.refShape,
					"target_index": c.targetIdx,
					"target_shape": c.tShape,
					"stage_index":  c.targetIdx,
					"stage_name":   []string{"base", "regroup"}[c.targetIdx],
				}
				env := chPredict(t, f.p, build())
				if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT) {
					t.Fatalf("predict-chain: want PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT first, got %+v", env.Errors)
				}
				if !reflect.DeepEqual(env.Errors[0].Details, want) {
					t.Errorf("predict details = %#v, want %#v", env.Errors[0].Details, want)
				}
				resp, err := f.p.ProcessChain(context.Background(), build())
				if err != nil {
					t.Fatalf("process chain: %v", err)
				}
				l := resp.Overlays[0]
				if len(l.Warnings) != 1 || l.Warnings[0].Code != string(perr.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT) {
					t.Fatalf("runtime: want one PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT warning, got %+v", l.Warnings)
				}
				// The runtime adds only overlay_index (its layer router).
				got := map[string]any{}
				for k, v := range l.Warnings[0].Details {
					got[k] = v
				}
				if got["overlay_index"] != 0 {
					t.Errorf("runtime details[overlay_index] = %#v, want 0", got["overlay_index"])
				}
				delete(got, "overlay_index")
				if !reflect.DeepEqual(got, want) {
					t.Errorf("runtime details = %#v, want %#v", got, want)
				}
				if string(l.Payload.Shape) != c.tShape || l.Payload.Series != nil {
					t.Errorf("runtime payload: want an empty %s payload, got %+v", c.tShape, l.Payload)
				}
			})
		}
	}
}
