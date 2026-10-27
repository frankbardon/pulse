package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// TestNonScalarAggregationTypes_MatchRowValues holds the declared
// non-scalar class (vectors.NonScalarAggregationTypes — the overlay
// scored-value refusal's built-in half) to what every built-in
// aggregator actually writes into a Response.Data row: a non-scalar
// type writes something other than one number, every other type
// writes one number. A new RichAggregator lift fails here until the
// class names it.
func TestNonScalarAggregationTypes_MatchRowValues(t *testing.T) {
	t.Parallel()

	fixtures := allAggParityFixtures(t)
	nonScalar := vectors.NonScalarAggregationTypes()
	for _, op := range types.AllAggregationTypes() {
		fix := fixtures[op]
		t.Run(string(op), func(t *testing.T) {
			req := &types.Request{Aggregations: []*types.Aggregation{
				{Type: op, Field: fix.field, Fields: fix.fields, Label: "primary", Params: fix.params},
			}}
			resp, err := NewProcessor(fix.schema).processRecords(context.Background(), req, fix.records)
			if err != nil {
				t.Fatalf("processRecords: %v", err)
			}
			if len(resp.Data) != 1 {
				t.Fatalf("Data rows = %d, want 1", len(resp.Data))
			}
			v, ok := resp.Data[0]["primary"]
			if !ok {
				t.Fatalf("row %v carries no primary column", resp.Data[0])
			}
			_, scalar := coerceNumericValue(v)
			if declared := slices.Contains(nonScalar, op); declared == scalar {
				t.Errorf("%s writes %T (one number: %v) but NonScalarAggregationTypes declares non-scalar=%v", op, v, scalar, declared)
			}
		})
	}
}

// TestKeySetDivergence_DetailsCarryGroupColumnsOnly: a bare Compose
// slot (no Request in reach, so not narrowed) whose rows carry a
// RichAggregator payload beside the group column and the scored value
// keys on the group column alone — the payload column once rendered as
// "u=\x01unknown" inside the divergence Details.
func TestKeySetDivergence_DetailsCarryGroupColumnsOnly(t *testing.T) {
	t.Parallel()

	row := func(region string, u any, s float64) map[string]any {
		return map[string]any{"region": region, "u": u, "s": s}
	}
	for _, c := range []struct {
		name string
		u    any
	}{
		{"labels", []string{"a", "b"}},
		{"counts", map[string]int{"a": 2}},
		{"triple", WelfordTriple{Mean: 1, Variance: 2, N: 3}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ref := &types.Response{Data: []map[string]any{row("east", c.u, 1), row("north", c.u, 2)}}
			tgt := &types.Response{Data: []map[string]any{row("north", c.u, 3)}}
			spec := types.ComposeOverlaySpec{Name: "o", Kind: types.OverlayKindIndexVsRef, Reference: "a", Targets: []string{"b"}}
			err := checkKeySetAlignment(ref, []*types.Response{tgt}, spec, 0)
			var ce *pulseerrors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != pulseerrors.PULSE_OVERLAY_KEY_SET_DIVERGENT {
				t.Fatalf("err = %v, want PULSE_OVERLAY_KEY_SET_DIVERGENT", err)
			}
			if got, want := ce.Details["missing"], []string{"region=east"}; !reflect.DeepEqual(got, want) {
				t.Errorf("missing = %#v, want %#v", got, want)
			}
		})
	}
}

// TestTwoSampleVsRef_ScoredWelfordReadsComponents: OVERLAY_T_VS_REF /
// OVERLAY_Z_VS_REF read AGG_WELFORD's leg from the components source.
// An in-process Compose slot's Data carries the WelfordTriple struct
// (no weighting, so never read); the narrowed slot takes each row's leg
// from its bucket components instead, so the layer equals the one over
// rows carrying that components map — weighted or not. It once came
// back empty. Without components (disabled) the struct stays and the
// layer is empty, as before.
func TestTwoSampleVsRef_ScoredWelfordReadsComponents(t *testing.T) {
	t.Parallel()

	type bucket struct {
		mean, variance, m2 float64
		n                  int
		sumW               *float64
	}
	w := func(x float64) *float64 { return &x }
	regions := []string{"east", "north"}
	slot := func(bs []bucket, components bool) *types.Response {
		resp := &types.Response{}
		var groups []types.AggregationGroupComponents
		for i, b := range bs {
			resp.Data = append(resp.Data, map[string]any{"region": regions[i], "w": WelfordTriple{Mean: b.mean, Variance: b.variance, N: uint64(b.n)}, "s": 1.0})
			groups = append(groups, types.AggregationGroupComponents{GroupKey: types.AxisKey{regions[i]}, N: b.n, SumWeights: b.sumW,
				Operator: map[string]any{"mean": b.mean, "variance": b.variance, "m2": b.m2, "stddev": 0.0}})
		}
		if components {
			resp.Components = &types.ResponseComponents{Aggregations: []types.AggregationComponents{{Label: "w", Groups: groups}, {Label: "s"}}}
		}
		return resp
	}
	decoded := func(bs []bucket) *types.Response {
		resp := &types.Response{}
		for i, b := range bs {
			leg := map[string]any{"mean": b.mean, "variance": b.variance, "m2": b.m2, "n": b.n}
			if b.sumW != nil {
				leg["sum_weights"] = *b.sumW
			}
			resp.Data = append(resp.Data, map[string]any{"region": regions[i], "w": leg})
		}
		return resp
	}
	req := func(label string) *types.Request {
		return &types.Request{Label: label, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_WELFORD, Field: "x", Label: "w"}, {Type: types.AGG_SUM, Field: "x", Label: "s"}}}
	}
	layerJSON := func(t *testing.T, layers []types.OverlayLayer) string {
		out, err := json.Marshal(layers)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	for _, c := range []struct {
		name     string
		ref, tgt []bucket
	}{
		{"unweighted", []bucket{{10, 4, 44, 12, nil}, {20, 9, 81, 10, nil}}, []bucket{{12, 5, 50, 11, nil}, {18, 8, 64, 9, nil}}},
		{"weighted", []bucket{{10, 4, 60, 12, w(16)}, {20, 9, 99, 10, w(12)}}, []bucket{{12, 5, 70, 11, w(15)}, {18, 8, 88, 9, w(12)}}},
	} {
		for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
			t.Run(c.name+"/"+string(kind), func(t *testing.T) {
				specs := []types.ComposeOverlaySpec{{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"}}}
				labels := []string{"a", "b"}
				reqs := []*types.Request{req("a"), req("b")}
				got, _, err := ApplyComposeOverlaysWithRequests(specs, []*types.Response{slot(c.ref, true), slot(c.tgt, true)}, labels, reqs, nil)
				if err != nil {
					t.Fatalf("in-process: %v", err)
				}
				want, _, err := ApplyComposeOverlays(specs, []*types.Response{decoded(c.ref), decoded(c.tgt)}, labels)
				if err != nil {
					t.Fatalf("decoded: %v", err)
				}
				if g, w := layerJSON(t, got), layerJSON(t, want); g != w || !strings.Contains(g, `"count":2`) {
					t.Errorf("in-process layer\n%s\nwant (components-map rows, two entries)\n%s", g, w)
				}
				off, _, err := ApplyComposeOverlaysWithRequests(specs, []*types.Response{slot(c.ref, false), slot(c.tgt, false)}, labels, reqs, nil)
				if err != nil {
					t.Fatalf("components off: %v", err)
				}
				if g := layerJSON(t, off); !strings.Contains(g, `"count":0`) {
					t.Errorf("components off: layer %s, want empty (the struct is never read)", g)
				}
			})
		}
	}
}
