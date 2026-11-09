package integration_test

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// U66 #444: OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF over grouped slots read
// each group's row value as that side's MEAN. Any scored scalar passed
// — an AGG_SUM, an AGG_VARIANCE, an AGG_MEDIAN, an AGG_MODE code —
// and was tested as if it were one. The measure the slot scores
// (params.field, else aggregations[0]) is now judged by one allowlist,
// predict and runtime alike:
//
//   - AGG_AVERAGE / AGG_WEIGHTED_MEAN: the mean (variance and n from
//     params, as before);
//   - AGG_WELFORD: its triple, as before;
//   - AGG_VARIANCE / AGG_STDDEV, or an extension whose ComponentSchema
//     declares mean + variance: (mean, variance, n) read off the slot's
//     per-group components, as the crosstab cell kinds read them;
//   - anything else: PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, Details
//     measure_type, reason measure_not_mean.
//
// Every parity case runs PREDICT FIRST on a fresh request.

var vsRefKinds = []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef}

// vsRefParity asserts predict-compose raises exactly one error of code
// with Details want, and Compose the same code, Details and message.
func vsRefParity(t *testing.T, p *pulse.Pulse, mk func() *pulse.ComposedRequest, code perr.Code, want map[string]any) {
	t.Helper()
	ctx := context.Background()
	env, err := p.PredictCompose(ctx, mk())
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 1 || env.Errors[0].Code != string(code) || !reflect.DeepEqual(env.Errors[0].Details, want) {
		t.Fatalf("PredictCompose = %+v, want one %s %v", env.Errors, code, want)
	}
	_, err = p.Compose(ctx, mk())
	ce := codeOf(t, err)
	if ce.Code != code || !reflect.DeepEqual(ce.Details, want) {
		t.Fatalf("Compose = %s %v, want %s %v", ce.Code, ce.Details, code, want)
	}
	if ce.Message != env.Errors[0].Message {
		t.Errorf("message drift: runtime %q, predict %q", ce.Message, env.Errors[0].Message)
	}
}

func TestComposeVsRef_NonMeanMeasureRefused(t *testing.T) {
	t.Parallel()

	p, _ := nsFixture(t, pulse.Options{})
	params := map[string]any{"variance": 2.0, "sample_size": 5.0}
	measure := func(kind types.OverlayKind, slot string, j int, agg types.AggregationType) map[string]any {
		return map[string]any{"overlay_index": 0, "kind": string(kind), "slot": slot,
			"host": fmt.Sprintf("requests[%d].aggregations[0]", j), "label": "v",
			"measure_type": string(agg), "reason": "measure_not_mean"}
	}
	for _, kind := range vsRefKinds {
		for _, agg := range []types.AggregationType{types.AGG_SUM, types.AGG_COUNT, types.AGG_MEDIAN, types.AGG_MIN, types.AGG_MAX, types.AGG_MODE} {
			t.Run(fmt.Sprintf("%s/%s/both", kind, agg), func(t *testing.T) {
				vsRefParity(t, p, func() *pulse.ComposedRequest { return tsCompose(kind, false, agg, params) },
					perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, measure(kind, "a", 0, agg))
			})
		}
		// A mean reference does not excuse a non-mean target.
		t.Run(string(kind)+"/target_only", func(t *testing.T) {
			vsRefParity(t, p, func() *pulse.ComposedRequest {
				req := tsCompose(kind, false, types.AGG_AVERAGE, params)
				req.Requests[1].Aggregations[0].Type = types.AGG_SUM
				return req
			}, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, measure(kind, "b", 1, types.AGG_SUM))
		})
		// A mean beside the moments: no pairing of a params leg with a
		// components leg.
		t.Run(string(kind)+"/mean_vs_moments", func(t *testing.T) {
			vsRefParity(t, p, func() *pulse.ComposedRequest {
				req := tsCompose(kind, false, types.AGG_AVERAGE, params)
				req.Requests[1].Aggregations[0].Type = types.AGG_WELFORD
				return req
			}, perr.PULSE_OVERLAY_SCHEMA_DIVERGENT, map[string]any{"overlay_index": 0, "kind": string(kind),
				"reference": "a", "target": "b", "reference_read": "mean", "target_read": "moments", "reason": "mixed_measure_read"})
		})
	}
}

// vsRefLayer runs one Compose two-sample layer (predict first, no
// error) and returns its per-row statistic.
func vsRefLayer(t *testing.T, p *pulse.Pulse, req func() *pulse.ComposedRequest) map[string]float64 {
	t.Helper()
	ctx := context.Background()
	env, err := p.PredictCompose(ctx, req())
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose = %v %+v, want no errors", err, env.Errors)
	}
	out, err := p.Compose(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, e := range out.Overlays[0].Payload.Series.Entries {
		if e.Summary.Statistic != nil {
			got[fmt.Sprint(e.Key)] = *e.Summary.Statistic
		}
	}
	if len(got) == 0 {
		t.Fatalf("empty layer: %s", nsJSON(t, out.Overlays))
	}
	return got
}

// TestComposeVsRef_MomentMeasuresMatchWelford: an AGG_VARIANCE or
// AGG_STDDEV slot is read through its components — the population
// variance converted to the sample variance — so its layer equals the
// AGG_WELFORD slot's on the same rows (the two accumulators' m2 may
// round apart in the last bit), unweighted and weighted. Before, the
// variance itself was tested as the mean.
func TestComposeVsRef_MomentMeasuresMatchWelford(t *testing.T) {
	t.Parallel()

	p, _ := nsFixture(t, pulse.Options{})
	weighted := func(base func() *pulse.ComposedRequest) func() *pulse.ComposedRequest {
		return func() *pulse.ComposedRequest {
			req := base()
			for _, r := range req.Requests {
				r.Weight = &types.WeightSpec{Field: "y", Kind: types.WeightKindFrequency}
			}
			return req
		}
	}
	for _, kind := range vsRefKinds {
		for _, w := range []bool{false, true} {
			mk := func(agg types.AggregationType) func() *pulse.ComposedRequest {
				base := func() *pulse.ComposedRequest { return tsCompose(kind, false, agg, nil) }
				if w {
					return weighted(base)
				}
				return base
			}
			want := vsRefLayer(t, p, mk(types.AGG_WELFORD))
			for _, agg := range []types.AggregationType{types.AGG_VARIANCE, types.AGG_STDDEV} {
				t.Run(fmt.Sprintf("%s/%s/weighted=%v", kind, agg, w), func(t *testing.T) {
					got := vsRefLayer(t, p, mk(agg))
					if len(got) != len(want) {
						t.Fatalf("entries %v, want %v", got, want)
					}
					for k, wv := range want {
						if gv, ok := got[k]; !ok || math.Abs(gv-wv) > 1e-12*math.Max(1, math.Abs(wv)) {
							t.Errorf("%s: p = %v, want the AGG_WELFORD slot's %v", k, gv, wv)
						}
					}
				})
			}
		}
	}
}

// TestComposeVsRef_MomentMeasureComponentsDisabled: a moment-carrying
// slot built without components has no moments to read (its value is
// the variance, never a mean): PULSE_OVERLAY_COMPONENTS_REQUIRED on
// both sides, params or not.
func TestComposeVsRef_MomentMeasureComponentsDisabled(t *testing.T) {
	t.Parallel()

	p, _ := nsFixture(t, pulse.Options{DisableComponents: true})
	for _, kind := range vsRefKinds {
		for _, agg := range []types.AggregationType{types.AGG_VARIANCE, types.AGG_STDDEV} {
			t.Run(fmt.Sprintf("%s/%s", kind, agg), func(t *testing.T) {
				vsRefParity(t, p, func() *pulse.ComposedRequest {
					return tsCompose(kind, false, agg, map[string]any{"variance": 2.0, "sample_size": 5.0})
				}, perr.PULSE_OVERLAY_COMPONENTS_REQUIRED, map[string]any{"overlay_index": 0, "kind": string(kind), "slot": "a",
					"host": "requests[0].aggregations[0]", "aggregation": string(agg), "label": "v", "reason": "components_disabled"})
			})
		}
	}
}

// TestComposeVsRef_ExtensionMeasure: an extension aggregator is read
// through its components when its ComponentSchema declares mean and
// variance (each group's p equals the two-sample test over the
// components' mean / variance and the floor n), and refused otherwise.
func TestComposeVsRef_ExtensionMeasure(t *testing.T) {
	t.Parallel()

	ext := pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
		{
			Name: "AGG_ACME_BRAND_SCORE", Factory: brandScoreFactory, Streamable: true,
			Accepts: []encoding.FieldType{encoding.FieldTypeF64}, ComponentSchema: brandScoreSchema(), ComponentsFunc: brandScoreEmit,
		},
		{
			Name: "AGG_ACME_BRAND_MOMENTS", Factory: brandScoreFactory, Streamable: true,
			Accepts: []encoding.FieldType{encoding.FieldTypeF64}, ComponentSchema: welfordExtSchema(), ComponentsFunc: welfordExtEmit,
		},
	}}
	p, _ := nsFixture(t, pulse.Options{Extensions: ext})
	ctx := context.Background()
	for _, kind := range vsRefKinds {
		t.Run(string(kind)+"/no_moments_refused", func(t *testing.T) {
			vsRefParity(t, p, func() *pulse.ComposedRequest {
				return tsCompose(kind, false, "AGG_ACME_BRAND_SCORE", map[string]any{"variance": 2.0, "sample_size": 5.0})
			}, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, map[string]any{"overlay_index": 0, "kind": string(kind), "slot": "a",
				"host": "requests[0].aggregations[0]", "label": "v", "measure_type": "AGG_ACME_BRAND_SCORE", "reason": "measure_not_mean"})
		})
		t.Run(string(kind)+"/moments_read", func(t *testing.T) {
			mk := func() *pulse.ComposedRequest { return tsCompose(kind, false, "AGG_ACME_BRAND_MOMENTS", nil) }
			got := vsRefLayer(t, p, mk)
			out, err := p.Compose(ctx, mk())
			if err != nil {
				t.Fatal(err)
			}
			legs := func(r *types.Response) map[string][2]float64 {
				m := map[string][2]float64{}
				g := r.Components.Aggregations[0].Groups
				for i, row := range r.Data {
					m[fmt.Sprint(types.AxisKey{"region=" + fmt.Sprint(row["region"])})] = [2]float64{g[i].Operator["mean"].(float64), float64(g[i].N)}
				}
				return m
			}
			ref, tgt := legs(out.Responses[0]), legs(out.Responses[1])
			for k, tv := range tgt {
				rv := ref[k]
				num := 1/tv[1] + 1/rv[1] // the emitted variance is 1 on every group
				stat := (tv[0] - rv[0]) / math.Sqrt(num)
				want := math.Erfc(math.Abs(stat) / math.Sqrt2)
				if kind == types.OverlayKindTVsRef {
					den := 1/(tv[1]*tv[1]*(tv[1]-1)) + 1/(rv[1]*rv[1]*(rv[1]-1))
					want = statdist.StudentTTwoSidedP(stat, (num*num)/den)
				}
				if gv, ok := got[k]; !ok || math.Abs(gv-want) > 1e-12*math.Max(1, want) {
					t.Errorf("%s: p = %v, want %v from the components", k, gv, want)
				}
			}
		})
	}
}
