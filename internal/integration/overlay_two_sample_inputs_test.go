package integration_test

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// A Compose two-sample overlay (OVERLAY_T_VS_REF / _Z_VS_REF over
// grouped slots, OVERLAY_T_CELL / _Z_CELL over crosstab slots) needs
// each side's mean, variance and n. A scalar side once took variance 1
// and n 2 when params carried none, and a vs-ref kind over crosstab
// slots answered an empty layer — both silently. Every route that
// yields no figures is now refused, predict and runtime alike, with
// one message and one Details map (U46 E4-S16):
//
//   - scalar side, no usable variance / sample-size params:
//     PULSE_OVERLAY_PARAM_MISSING, reason params_missing;
//   - vs-ref kind over a crosstab slot:
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//
// Explicit params still run and equal the Welch / z arithmetic over the
// slot means; components routes (AGG_WELFORD rows, moment-carrying
// crosstab cells) ignore params entirely, as before.
//
// Every parity case runs PREDICT FIRST on a fresh request.

// tsSlot is one Compose slot over the multi-grouper cohort: grouped by
// region (or a region × sex crosstab) scoring agg of score; slot b
// keeps only sex F, so its means differ from slot a's.
func tsSlot(label string, crosstab bool, agg types.AggregationType, filtered bool) *types.Request {
	r := &types.Request{Label: label, Cohort: &types.Cohort{Filename: "ns.pulse"}}
	if crosstab {
		r.Crosstab = &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tier"}},
			Cell:    &types.Aggregation{Type: agg, Field: "score"},
		}
	} else {
		r.Groups = nsGroups()
		r.Aggregations = []*types.Aggregation{{Type: agg, Field: "score", Label: "v"}}
	}
	if filtered {
		r.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "sex", Values: []string{"F"}}}
	}
	return r
}

func tsCompose(kind types.OverlayKind, crosstab bool, agg types.AggregationType, params map[string]any) *pulse.ComposedRequest {
	scope := types.OverlayScopeGroup
	if crosstab {
		scope = types.OverlayScopeCell
	}
	var p map[string]any
	if params != nil {
		p = map[string]any{}
		for k, v := range params {
			p[k] = v
		}
	}
	return &pulse.ComposedRequest{
		Requests: []*types.Request{tsSlot("a", crosstab, agg, false), tsSlot("b", crosstab, agg, true)},
		Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: kind, Scope: scope, Reference: "a", Targets: []string{"b"}, Params: p}},
	}
}

func TestComposeOverlay_TwoSampleInputsRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	seriesKinds := []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef}
	cellKinds := []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindZCell}
	targetOnly := map[string]any{"variance_target": 2.0, "sample_size_target": 5.0}
	refOnly := map[string]any{"variance_ref": 2.0, "sample_size_ref": 5.0}
	type refusal struct {
		name     string
		kind     types.OverlayKind
		crosstab bool
		agg      types.AggregationType
		params   map[string]any
		off      bool
		want     map[string]any
		code     perr.Code
	}
	missing := func(kind types.OverlayKind, host string, agg types.AggregationType, label, side string, params ...string) map[string]any {
		return map[string]any{"overlay_index": 0, "kind": string(kind), "slot": map[string]string{"reference": "a", "target": "b"}[side],
			"host": host, "aggregation": string(agg), "label": label, "side": side, "params": params, "reason": "params_missing"}
	}
	var cases []refusal
	for _, kind := range seriesKinds {
		for _, agg := range []types.AggregationType{types.AGG_AVERAGE, types.AGG_VARIANCE, types.AGG_SUM} {
			cases = append(cases,
				refusal{"no params", kind, false, agg, nil, false,
					missing(kind, "requests[0].aggregations[0]", agg, "v", "reference", "variance_ref", "sample_size_ref"), perr.PULSE_OVERLAY_PARAM_MISSING},
				refusal{"target side only", kind, false, agg, targetOnly, false,
					missing(kind, "requests[0].aggregations[0]", agg, "v", "reference", "variance_ref", "sample_size_ref"), perr.PULSE_OVERLAY_PARAM_MISSING},
				refusal{"reference side only", kind, false, agg, refOnly, true,
					missing(kind, "requests[1].aggregations[0]", agg, "v", "target", "variance_target", "sample_size_target"), perr.PULSE_OVERLAY_PARAM_MISSING},
				refusal{"non-positive variance", kind, false, agg, map[string]any{"variance": 0.0, "sample_size": 5.0}, false,
					missing(kind, "requests[0].aggregations[0]", agg, "v", "reference", "variance_ref"), perr.PULSE_OVERLAY_PARAM_MISSING},
			)
		}
		for _, agg := range []types.AggregationType{types.AGG_AVERAGE, types.AGG_WELFORD} {
			cases = append(cases, refusal{"over crosstab slots", kind, true, agg, map[string]any{"variance": 2.0, "sample_size": 5.0}, false,
				map[string]any{"overlay_index": 0, "kind": string(kind), "slot": "a", "shape": "matrix", "shapes": []string{"series"}},
				perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE})
		}
	}
	for _, kind := range cellKinds {
		cell := "requests[0].crosstab.cell"
		cases = append(cases,
			refusal{"scalar cell, no params", kind, true, types.AGG_AVERAGE, nil, false,
				missing(kind, cell, types.AGG_AVERAGE, "AGG_AVERAGE_score", "reference", "variance_ref", "sample_size_ref"), perr.PULSE_OVERLAY_PARAM_MISSING},
			refusal{"scalar cell, reference side only", kind, true, types.AGG_AVERAGE, refOnly, false,
				missing(kind, "requests[1].crosstab.cell", types.AGG_AVERAGE, "AGG_AVERAGE_score", "target", "variance_target", "sample_size_target"), perr.PULSE_OVERLAY_PARAM_MISSING},
			refusal{"welford cell, components off, no params", kind, true, types.AGG_WELFORD, nil, true,
				missing(kind, cell, types.AGG_WELFORD, "AGG_WELFORD_score", "reference", "variance_ref", "sample_size_ref"), perr.PULSE_OVERLAY_PARAM_MISSING},
			refusal{"variance cell, components off, no params", kind, true, types.AGG_VARIANCE, nil, true,
				missing(kind, cell, types.AGG_VARIANCE, "AGG_VARIANCE_score", "reference", "variance_ref", "sample_size_ref"), perr.PULSE_OVERLAY_PARAM_MISSING},
		)
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%s/%s", c.kind, c.agg, c.name), func(t *testing.T) {
			p, _ := nsFixture(t, pulse.Options{DisableComponents: c.off})
			env, err := p.PredictCompose(ctx, tsCompose(c.kind, c.crosstab, c.agg, c.params))
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(c.code) || !reflect.DeepEqual(env.Errors[0].Details, c.want) {
				t.Fatalf("PredictCompose = %+v, want one %s %v", env.Errors, c.code, c.want)
			}
			_, err = p.Compose(ctx, tsCompose(c.kind, c.crosstab, c.agg, c.params))
			ce := codeOf(t, err)
			if ce.Code != c.code || !reflect.DeepEqual(ce.Details, c.want) {
				t.Fatalf("Compose = %s %v, want %s %v", ce.Code, ce.Details, c.code, c.want)
			}
			if ce.Message != env.Errors[0].Message {
				t.Errorf("message drift: runtime %q, predict %q", ce.Message, env.Errors[0].Message)
			}
		})
	}
}

// TestComposeOverlay_TwoSampleExplicitParams: explicit params run on
// every scalar route and equal the two-sample arithmetic over the slot
// means (the handler's inputs, read back from the slot responses); the
// per-side and symmetric keys are interchangeable.
func TestComposeOverlay_TwoSampleExplicitParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	perSide := map[string]any{"variance_target": 3.0, "sample_size_target": 6.0, "variance_ref": 2.0, "sample_size_ref": 5.0}
	pValue := func(kind types.OverlayKind, mt, mr float64) float64 {
		vt, nt, vr, nr := 3.0, 6.0, 2.0, 5.0
		num := vt/nt + vr/nr
		stat := (mt - mr) / math.Sqrt(num)
		if kind == types.OverlayKindZVsRef || kind == types.OverlayKindZCell {
			return math.Erfc(math.Abs(stat) / math.Sqrt2)
		}
		den := (vt*vt)/(nt*nt*(nt-1)) + (vr*vr)/(nr*nr*(nr-1))
		return statdist.StudentTTwoSidedP(stat, (num*num)/den)
	}
	near := func(got, want float64) bool { return math.Abs(got-want) <= 1e-12*math.Max(1, math.Abs(want)) }
	for _, off := range []bool{false, true} {
		p, _ := nsFixture(t, pulse.Options{DisableComponents: off})
		for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
			t.Run(fmt.Sprintf("%s/off=%v", kind, off), func(t *testing.T) {
				env, err := p.PredictCompose(ctx, tsCompose(kind, false, types.AGG_AVERAGE, perSide))
				if err != nil || len(env.Errors) != 0 {
					t.Fatalf("PredictCompose = %v %+v, want no errors", err, env.Errors)
				}
				out, err := p.Compose(ctx, tsCompose(kind, false, types.AGG_AVERAGE, perSide))
				if err != nil {
					t.Fatal(err)
				}
				means := func(r *types.Response) map[string]float64 {
					m := map[string]float64{}
					for _, row := range r.Data {
						m["region="+fmt.Sprint(row["region"])] = row["v"].(float64)
					}
					return m
				}
				ref, tgt := means(out.Responses[0]), means(out.Responses[1])
				entries := out.Overlays[0].Payload.Series.Entries
				if len(entries) != len(tgt) || len(entries) == 0 {
					t.Fatalf("entries = %d, want %d", len(entries), len(tgt))
				}
				for _, e := range entries {
					k := fmt.Sprint(e.Key[0])
					if want := pValue(kind, tgt[k], ref[k]); e.Summary.Statistic == nil || !near(*e.Summary.Statistic, want) {
						t.Errorf("%s: p = %v, want %v", k, e.Summary.Statistic, want)
					}
				}
			})
		}
		for _, kind := range []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindZCell} {
			t.Run(fmt.Sprintf("%s/off=%v", kind, off), func(t *testing.T) {
				env, err := p.PredictCompose(ctx, tsCompose(kind, true, types.AGG_AVERAGE, perSide))
				if err != nil || len(env.Errors) != 0 {
					t.Fatalf("PredictCompose = %v %+v, want no errors", err, env.Errors)
				}
				out, err := p.Compose(ctx, tsCompose(kind, true, types.AGG_AVERAGE, perSide))
				if err != nil {
					t.Fatal(err)
				}
				refMx, tgtMx, layer := out.Responses[0].Crosstab.Matrix, out.Responses[1].Crosstab.Matrix, out.Overlays[0].Payload.Matrix
				refCell := map[string]float64{}
				for i, rk := range refMx.RowKeys {
					for j, ck := range refMx.ColumnKeys {
						if refMx.Cells[i][j].Present {
							refCell[fmt.Sprint(rk, ck)] = refMx.Cells[i][j].Value.(float64)
						}
					}
				}
				seen := 0
				for i, rk := range tgtMx.RowKeys {
					for j, ck := range tgtMx.ColumnKeys {
						r, ok := refCell[fmt.Sprint(rk, ck)]
						if !tgtMx.Cells[i][j].Present || !ok {
							continue
						}
						got, _ := layer.Cells[i][j].Value.(float64)
						if want := pValue(kind, tgtMx.Cells[i][j].Value.(float64), r); !near(got, want) {
							t.Errorf("cell %v/%v: p = %v, want %v", rk, ck, got, want)
						}
						seen++
					}
				}
				if seen == 0 {
					t.Fatal("no cell compared")
				}
			})
		}
	}
	// Per-side and symmetric keys are interchangeable.
	p, _ := nsFixture(t, pulse.Options{})
	for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZCell} {
		crosstab := kind == types.OverlayKindZCell
		a, err := p.Compose(ctx, tsCompose(kind, crosstab, types.AGG_AVERAGE, map[string]any{"variance": 2.0, "sample_size": 5.0}))
		if err != nil {
			t.Fatal(err)
		}
		b, err := p.Compose(ctx, tsCompose(kind, crosstab, types.AGG_AVERAGE, map[string]any{"variance_target": 2.0, "variance_ref": 2.0, "sample_size_target": 5.0, "sample_size_ref": 5.0}))
		if err != nil {
			t.Fatal(err)
		}
		if nsJSON(t, a.Overlays) != nsJSON(t, b.Overlays) {
			t.Errorf("%s: symmetric and per-side params differ", kind)
		}
	}
}

// TestComposeOverlay_TwoSampleComponentsRoutesIgnoreParams: the
// components routes need no params and are byte-identical with or
// without them — AGG_WELFORD rows, and crosstab cells whose components
// carry mean and variance (AGG_WELFORD, AGG_VARIANCE, AGG_STDDEV).
func TestComposeOverlay_TwoSampleComponentsRoutesIgnoreParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := nsFixture(t, pulse.Options{})
	params := map[string]any{"variance": 99.0, "sample_size": 3.0}
	type route struct {
		kind     types.OverlayKind
		crosstab bool
		agg      types.AggregationType
	}
	var routes []route
	for _, k := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
		routes = append(routes, route{k, false, types.AGG_WELFORD})
	}
	for _, k := range []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindZCell} {
		for _, a := range []types.AggregationType{types.AGG_WELFORD, types.AGG_VARIANCE, types.AGG_STDDEV} {
			routes = append(routes, route{k, true, a})
		}
	}
	for _, r := range routes {
		t.Run(fmt.Sprintf("%s/%s", r.kind, r.agg), func(t *testing.T) {
			env, err := p.PredictCompose(ctx, tsCompose(r.kind, r.crosstab, r.agg, nil))
			if err != nil || len(env.Errors) != 0 {
				t.Fatalf("PredictCompose = %v %+v, want no errors", err, env.Errors)
			}
			bare, err := p.Compose(ctx, tsCompose(r.kind, r.crosstab, r.agg, nil))
			if err != nil {
				t.Fatal(err)
			}
			withParams, err := p.Compose(ctx, tsCompose(r.kind, r.crosstab, r.agg, params))
			if err != nil {
				t.Fatal(err)
			}
			got, want := nsJSON(t, withParams.Overlays[0].Payload), nsJSON(t, bare.Overlays[0].Payload)
			if got != want {
				t.Errorf("params moved a components route:\n%s\nwant\n%s", got, want)
			}
			if s := bare.Overlays[0].Summary; s == nil || s.Count == nil || *s.Count == 0 {
				t.Errorf("empty layer: %s", nsJSON(t, bare.Overlays))
			}
		})
	}
}

// TestComposeOverlay_CellKindsOverGroupedSlots: OVERLAY_T_CELL and
// OVERLAY_Z_CELL read crosstab cells; over grouped slots both are
// PULSE_OVERLAY_SLOT_NOT_CROSSTAB on predict and runtime (OVERLAY_Z_CELL
// was refused only at runtime, by its handler).
func TestComposeOverlay_CellKindsOverGroupedSlots(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := nsFixture(t, pulse.Options{})
	params := map[string]any{"variance": 2.0, "sample_size": 5.0}
	for _, kind := range []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindZCell} {
		t.Run(string(kind), func(t *testing.T) {
			req := tsCompose(kind, false, types.AGG_AVERAGE, params)
			req.Overlays[0].Scope = types.OverlayScopeCell
			env, err := p.PredictCompose(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_SLOT_NOT_CROSSTAB) {
				t.Fatalf("PredictCompose = %+v, want PULSE_OVERLAY_SLOT_NOT_CROSSTAB first", env.Errors)
			}
			req = tsCompose(kind, false, types.AGG_AVERAGE, params)
			req.Overlays[0].Scope = types.OverlayScopeCell
			_, err = p.Compose(ctx, req)
			if ce := codeOf(t, err); ce.Code != perr.PULSE_OVERLAY_SLOT_NOT_CROSSTAB {
				t.Fatalf("Compose = %s %v, want PULSE_OVERLAY_SLOT_NOT_CROSSTAB", ce.Code, ce.Details)
			}
		})
	}
}
