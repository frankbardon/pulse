package processing

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// vsRefSlot is a grouped Compose slot scoring one aggregation of type
// agg (label v), two region rows, with per-group components when comps.
func vsRefSlot(agg types.AggregationType, comps bool) (*types.Request, *types.Response) {
	req := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: agg, Field: "x", Label: "v"}}}
	resp := &types.Response{}
	var groups []types.AggregationGroupComponents
	for i, region := range []string{"east", "north"} {
		n := 5 + i
		mean, m2 := 10.0+float64(i), 8.0+float64(3*i)
		resp.Data = append(resp.Data, map[string]any{"region": region, "v": m2 / float64(n)})
		groups = append(groups, types.AggregationGroupComponents{GroupKey: types.AxisKey{region}, N: n,
			Operator: map[string]any{"mean": mean, "m2": m2, "variance": m2 / float64(n)}})
	}
	if comps {
		resp.Components = &types.ResponseComponents{Aggregations: []types.AggregationComponents{{Label: "v", Groups: groups}}}
	}
	return req, resp
}

// TestComposeVsRefMeasure_DispatcherMatchesPredict: the Compose
// dispatcher (ApplyComposeOverlaysWithRequests, no service) raises the
// measure rule predict-compose's gate 2e raises
// (descriptor.ComposeOverlayTwoSampleInputsRefusal) — same code,
// message and Details — on the same requests (U66 #444).
func TestComposeVsRefMeasure_DispatcherMatchesPredict(t *testing.T) {
	t.Parallel()

	params := map[string]any{"variance": 2.0, "sample_size": 5.0}
	for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
		for _, c := range []struct {
			name     string
			ref, tgt types.AggregationType
			code     pulseerrors.Code
		}{
			{"sum", types.AGG_SUM, types.AGG_SUM, pulseerrors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
			{"median target", types.AGG_AVERAGE, types.AGG_MEDIAN, pulseerrors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
			{"mean vs variance", types.AGG_AVERAGE, types.AGG_VARIANCE, pulseerrors.PULSE_OVERLAY_SCHEMA_DIVERGENT},
		} {
			t.Run(string(kind)+"/"+c.name, func(t *testing.T) {
				refReq, refResp := vsRefSlot(c.ref, true)
				tgtReq, tgtResp := vsRefSlot(c.tgt, true)
				reqs := []*types.Request{refReq, tgtReq}
				labels := []string{"a", "b"}
				specs := []types.ComposeOverlaySpec{{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"}, Params: params}}
				perr := descriptor.ComposeOverlayTwoSampleInputsRefusal(specs, reqs, labels, nil, func(*types.Request) bool { return false })
				_, _, rerr := ApplyComposeOverlaysWithRequests(specs, []*types.Response{refResp, tgtResp}, labels, reqs, nil)
				var pce, rce *pulseerrors.CodedError
				if !stderrors.As(perr, &pce) || !stderrors.As(rerr, &rce) {
					t.Fatalf("predict %v, runtime %v: want both coded", perr, rerr)
				}
				if pce.Code != c.code || rce.Code != c.code || pce.Message != rce.Message || !reflect.DeepEqual(pce.Details, rce.Details) {
					t.Fatalf("predict %s %q %v\nruntime %s %q %v", pce.Code, pce.Message, pce.Details, rce.Code, rce.Message, rce.Details)
				}
			})
		}
	}
}

// TestComposeVsRefMeasure_VarianceRowsReadComponents: an AGG_VARIANCE
// slot's rows are read as the group's components leg — the population
// variance converted to the sample variance m2 / (n - 1) — so the layer
// equals the one over rows carrying the AGG_WELFORD-shaped leg map.
// A slot that came back without components is
// PULSE_OVERLAY_COMPONENTS_REQUIRED (its value is the variance).
func TestComposeVsRefMeasure_VarianceRowsReadComponents(t *testing.T) {
	t.Parallel()

	for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
		t.Run(string(kind), func(t *testing.T) {
			specs := []types.ComposeOverlaySpec{{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"}}}
			labels := []string{"a", "b"}
			refReq, refResp := vsRefSlot(types.AGG_VARIANCE, true)
			tgtReq, tgtResp := vsRefSlot(types.AGG_STDDEV, true)
			tgtResp.Components.Aggregations[0].Groups[1].Operator["mean"] = 13.5
			got, _, err := ApplyComposeOverlaysWithRequests(specs, []*types.Response{refResp, tgtResp}, labels, []*types.Request{refReq, tgtReq}, nil)
			if err != nil {
				t.Fatal(err)
			}
			legs := func(resp *types.Response) *types.Response {
				out := &types.Response{}
				for i, g := range resp.Components.Aggregations[0].Groups {
					m2 := g.Operator["m2"].(float64)
					out.Data = append(out.Data, map[string]any{"region": resp.Data[i]["region"],
						"v": map[string]any{"mean": g.Operator["mean"], "m2": m2, "variance": m2 / float64(g.N-1), "n": g.N}})
				}
				return out
			}
			want, _, err := ApplyComposeOverlays(specs, []*types.Response{legs(refResp), legs(tgtResp)}, labels)
			if err != nil {
				t.Fatal(err)
			}
			g, _ := json.Marshal(got)
			w, _ := json.Marshal(want)
			if string(g) != string(w) || !strings.Contains(string(g), `"count":2`) {
				t.Errorf("variance slots\n%s\nwant (sample-variance legs)\n%s", g, w)
			}
			if refResp.Data[0]["v"] != 8.0/5 {
				t.Errorf("slot response mutated: %v", refResp.Data[0])
			}

			_, offResp := vsRefSlot(types.AGG_VARIANCE, false)
			_, _, err = ApplyComposeOverlaysWithRequests(specs, []*types.Response{offResp, tgtResp}, labels, []*types.Request{refReq, tgtReq}, nil)
			var ce *pulseerrors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != pulseerrors.PULSE_OVERLAY_COMPONENTS_REQUIRED || ce.Details["slot"] != "a" {
				t.Fatalf("components absent: %v, want PULSE_OVERLAY_COMPONENTS_REQUIRED on slot a", err)
			}
		})
	}
}
