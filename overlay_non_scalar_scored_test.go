package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// A series overlay reads ONE number per host row. Scoring an
// aggregation whose row value is not one number — AGG_WELFORD's moment
// struct, AGG_SET_UNION / AGG_SET_INTERSECTION's label list,
// AGG_SET_FREQUENCY's count map, an extension declared NonScalar —
// once returned an EMPTY layer with no error and no warning (E4-S11).
// It is now refused on every host, predict and runtime alike, with the
// array-valued refusal's code and Details shape and reason
// "non_scalar"; an unscored one beside a scalar still runs.
//
// Every parity case runs PREDICT FIRST on a fresh request.

// nsAgg is one non-scalar built-in and the field it folds.
type nsAgg struct {
	typ   types.AggregationType
	field string
}

func nsBuiltins() []nsAgg {
	return []nsAgg{
		{types.AGG_WELFORD, "score"},
		{types.AGG_SET_UNION, "tags"},
		{types.AGG_SET_INTERSECTION, "tags"},
		{types.AGG_SET_FREQUENCY, "tags"},
	}
}

// nsFixture: the multi-grouper gate cohort twice (the second slot reads
// only regions north and south through its filter) and an instance
// over it, plus the cohort's bytes for PredictBytes.
func nsFixture(t *testing.T, opts pulse.Options) (*pulse.Pulse, []byte) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	opts.FS = fsys
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	mgWriteCohort(t, p, "ns.pulse", mgRows, 0)
	data, err := afero.ReadFile(fsys, "ns.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return p, data
}

// nsAggs builds aggregations from a layout: "n" = the non-scalar a
// labelled n, "s" = AGG_SUM score labelled s.
func nsAggs(a nsAgg, layout string) []*types.Aggregation {
	var out []*types.Aggregation
	for _, c := range layout {
		switch c {
		case 'n':
			out = append(out, &types.Aggregation{Type: a.typ, Field: a.field, Label: "n"})
		case 's':
			out = append(out, &types.Aggregation{Type: types.AGG_SUM, Field: "score", Label: "s"})
		}
	}
	return out
}

func nsGroups() []*types.Group {
	return []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
}

func nsJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// nsTwoSampleParams are the per-side stat params OVERLAY_T_VS_REF /
// OVERLAY_Z_VS_REF need over a scalar slot.
func nsTwoSampleParams(kind types.OverlayKind, m map[string]any) map[string]any {
	if kind == types.OverlayKindTVsRef || kind == types.OverlayKindZVsRef {
		for _, k := range []string{"variance_target", "variance_ref", "sample_size_target", "sample_size_ref"} {
			m[k] = 4.0
		}
	}
	return m
}

func nsCompose(a nsAgg, kind types.OverlayKind, layout string, field any) *pulse.ComposedRequest {
	slot := func(label string, regions ...string) *types.Request {
		r := &types.Request{Label: label, Cohort: &types.Cohort{Filename: "ns.pulse"}, Groups: nsGroups(), Aggregations: nsAggs(a, layout)}
		if len(regions) > 0 {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: regions}}
		}
		return r
	}
	params := map[string]any{}
	if field != nil {
		params["field"] = field
	}
	spec := types.ComposeOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"},
		Params: nsTwoSampleParams(kind, params)}
	if len(spec.Params) == 0 {
		spec.Params = nil
	}
	return &pulse.ComposedRequest{Requests: []*types.Request{slot("a"), slot("b", "north", "south", "west")},
		Overlays: []types.ComposeOverlaySpec{spec}}
}

// TestComposeOverlay_NonScalarScoredRefused: every Compose series kind
// over every non-scalar built-in. Scoring it (default first, or by
// field) is refused PROCESSING_CONFIG non_scalar identically on both
// sides — except a kind that reads that aggregator's payload
// (OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF on AGG_WELFORD), which runs.
// Scoring the scalar beside it runs and equals the scalar-only slot.
func TestComposeOverlay_NonScalarScoredRefused(t *testing.T) {
	ctx := context.Background()
	p, _ := nsFixture(t, pulse.Options{})
	for _, kind := range seriesfield.ComposeKinds() {
		for _, a := range nsBuiltins() {
			name := fmt.Sprintf("%s/%s", kind, a.typ)
			baseline, err := p.Compose(ctx, nsCompose(a, kind, "s", nil))
			if err != nil {
				t.Fatalf("%s baseline: %v", name, err)
			}
			want := nsJSON(t, baseline.Overlays)
			t.Run(name+"/runs", func(t *testing.T) {
				for _, c := range []struct {
					layout string
					field  any
				}{{"ns", "s"}, {"sn", nil}} {
					env, err := p.PredictCompose(ctx, nsCompose(a, kind, c.layout, c.field))
					if err != nil {
						t.Fatal(err)
					}
					if len(env.Errors) != 0 {
						t.Fatalf("%s field=%v: PredictCompose errors = %+v, want none", c.layout, c.field, env.Errors)
					}
					out, err := p.Compose(ctx, nsCompose(a, kind, c.layout, c.field))
					if err != nil {
						t.Fatalf("%s field=%v: Compose: %v", c.layout, c.field, err)
					}
					if got := nsJSON(t, out.Overlays); got != want {
						t.Errorf("%s field=%v: layers =\n%s\nwant (scalar-only slot)\n%s", c.layout, c.field, got, want)
					}
				}
			})
			exempt := a.typ == types.AGG_WELFORD && (kind == types.OverlayKindTVsRef || kind == types.OverlayKindZVsRef)
			for _, c := range []struct {
				layout string
				field  any
				ai     int
			}{{"ns", nil, 0}, {"sn", "n", 1}, {"n", nil, 0}} {
				t.Run(fmt.Sprintf("%s/scored/%s/field=%v", name, c.layout, c.field), func(t *testing.T) {
					env, err := p.PredictCompose(ctx, nsCompose(a, kind, c.layout, c.field))
					if err != nil {
						t.Fatal(err)
					}
					if exempt {
						if len(env.Errors) != 0 {
							t.Fatalf("PredictCompose errors = %+v, want none (the kind reads the triple)", env.Errors)
						}
						out, err := p.Compose(ctx, nsCompose(a, kind, c.layout, c.field))
						if err != nil {
							t.Fatalf("Compose: %v", err)
						}
						if len(out.Overlays) != 1 || out.Overlays[0].Payload.Series == nil || len(out.Overlays[0].Payload.Series.Entries) == 0 {
							t.Fatalf("overlays = %s, want one non-empty SERIES layer", nsJSON(t, out.Overlays))
						}
						return
					}
					wantDetails := map[string]any{"overlay_index": 0, "kind": string(kind), "slot": "a",
						"host": fmt.Sprintf("requests[0].aggregations[%d]", c.ai), "aggregation": string(a.typ),
						"label": "n", "reason": "non_scalar"}
					if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PROCESSING_CONFIG) ||
						!reflect.DeepEqual(env.Errors[0].Details, wantDetails) {
						t.Fatalf("PredictCompose = %+v, want PROCESSING_CONFIG %v first", env.Errors, wantDetails)
					}
					_, err = p.Compose(ctx, nsCompose(a, kind, c.layout, c.field))
					ce := codeOf(t, err)
					if ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
						t.Fatalf("Compose = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
					}
					if ce.Message != env.Errors[0].Message {
						t.Errorf("message drift: runtime %q, predict %q", ce.Message, env.Errors[0].Message)
					}
				})
			}
		}
	}
}

// nsProcess: a grouped Process request carrying one overlay of kind
// (params.field = field when non-nil).
func nsProcess(a nsAgg, kind types.OverlayKind, layout string, field any) *pulse.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: "ns.pulse"}, Groups: nsGroups(), Aggregations: nsAggs(a, layout)}
	spec := types.OverlaySpec{Kind: kind, Scope: types.OverlayScopeGroup}
	params := map[string]any{}
	if field != nil {
		params["field"] = field
	}
	if params = nsTwoSampleParams(kind, params); len(params) > 0 {
		spec.Params, _ = json.Marshal(params)
	}
	req.Overlays = []types.OverlaySpec{spec}
	return req
}

// TestProcessOverlay_NonScalarScoredRefused: the Process-host twin,
// over the series kinds that need no reference beyond the host.
func TestProcessOverlay_NonScalarScoredRefused(t *testing.T) {
	ctx := context.Background()
	p, data := nsFixture(t, pulse.Options{})
	for _, kind := range []types.OverlayKind{types.OverlayKindDeltaVsPrior, types.OverlayKindIndexVsTotal, types.OverlayKindShareOfTotal} {
		for _, a := range nsBuiltins() {
			name := fmt.Sprintf("%s/%s", kind, a.typ)
			baseline, err := p.Process(ctx, nsProcess(a, kind, "s", nil))
			if err != nil {
				t.Fatalf("%s baseline: %v", name, err)
			}
			want := nsJSON(t, baseline.Overlays)
			t.Run(name+"/runs", func(t *testing.T) {
				if pe := predictCodedError(t, p, data, nsProcess(a, kind, "ns", "s")); pe != nil {
					t.Fatalf("Predict = %s %v, want clean", pe.Code, pe.Details)
				}
				out, err := p.Process(ctx, nsProcess(a, kind, "ns", "s"))
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if got := nsJSON(t, out.Overlays); got != want {
					t.Errorf("overlays =\n%s\nwant (scalar-only request)\n%s", got, want)
				}
			})
			for _, c := range []struct {
				layout string
				field  any
				ai     int
			}{{"ns", nil, 0}, {"sn", "n", 1}, {"n", nil, 0}} {
				t.Run(fmt.Sprintf("%s/scored/%s/field=%v", name, c.layout, c.field), func(t *testing.T) {
					wantDetails := map[string]any{"aggregation": string(a.typ), "label": "n",
						"slot": fmt.Sprintf("aggregations[%d]", c.ai), "reason": "non_scalar"}
					pe := predictCodedError(t, p, data, nsProcess(a, kind, c.layout, c.field))
					if pe == nil || pe.Code != string(perr.PROCESSING_CONFIG) || !reflect.DeepEqual(pe.Details, wantDetails) {
						t.Fatalf("Predict = %+v, want PROCESSING_CONFIG %v", pe, wantDetails)
					}
					_, err := p.Process(ctx, nsProcess(a, kind, c.layout, c.field))
					ce := codeOf(t, err)
					if ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
						t.Fatalf("Process = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
					}
					if ce.Message != pe.Message {
						t.Errorf("message drift: runtime %q, predict %q", ce.Message, pe.Message)
					}
				})
			}
		}
	}

	// OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF read AGG_WELFORD's triple:
	// scoring it is not a scored-value refusal on either side.
	w := nsAgg{types.AGG_WELFORD, "score"}
	for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
		if pe := predictCodedError(t, p, data, nsProcess(w, kind, "n", nil)); pe != nil && pe.Details["reason"] != nil {
			t.Fatalf("%s over AGG_WELFORD: Predict = %s %v, want no scored-value refusal", kind, pe.Code, pe.Details)
		}
		if _, err := p.Process(ctx, nsProcess(w, kind, "n", nil)); err != nil {
			if ce := codeOf(t, err); ce.Details["reason"] != nil {
				t.Fatalf("%s over AGG_WELFORD: Process = %s %v, want no scored-value refusal", kind, ce.Code, ce.Details)
			}
		}
	}
}

// nsChain: a one-stage chain whose overlay compares the stage with
// itself.
func nsChain(a nsAgg, kind types.OverlayKind, layout string, field any) *pulse.ChainRequest {
	zero := 0
	spec := &types.ChainOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Ref: types.StageRef{Index: &zero}}
	if field != nil {
		spec.Params = map[string]any{"field": field}
	}
	return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "ns.pulse"}, Stages: []*types.ChainStage{
		{Name: "profile", Request: &types.Request{Groups: nsGroups(), Aggregations: nsAggs(a, layout)}},
	}, Overlays: []*types.ChainOverlaySpec{spec}}
}

// TestChainOverlay_NonScalarScoredRefused: the whole-chain twin. A
// mergeable non-scalar (the set folds) is refused non_scalar; a
// non-mergeable one (AGG_WELFORD) is the chain gate's
// PULSE_CHAIN_NOT_MERGEABLE on both sides, whichever host it sits in.
func TestChainOverlay_NonScalarScoredRefused(t *testing.T) {
	ctx := context.Background()
	p, _ := nsFixture(t, pulse.Options{})
	for _, kind := range seriesfield.ChainKinds() {
		for _, a := range nsBuiltins() {
			name := fmt.Sprintf("%s/%s", kind, a.typ)
			gate := a.typ == types.AGG_WELFORD
			if !gate {
				baseline, err := p.ProcessChain(ctx, nsChain(a, kind, "s", nil))
				if err != nil {
					t.Fatalf("%s baseline: %v", name, err)
				}
				want := nsJSON(t, baseline.Overlays)
				t.Run(name+"/runs", func(t *testing.T) {
					env, err := p.PredictChain(ctx, nsChain(a, kind, "ns", "s"))
					if err != nil {
						t.Fatal(err)
					}
					if len(env.Errors) != 0 {
						t.Fatalf("PredictChain errors = %+v, want none", env.Errors)
					}
					out, err := p.ProcessChain(ctx, nsChain(a, kind, "ns", "s"))
					if err != nil {
						t.Fatalf("ProcessChain: %v", err)
					}
					if got := nsJSON(t, out.Overlays); got != want {
						t.Errorf("layers =\n%s\nwant (scalar-only stage)\n%s", got, want)
					}
				})
			}
			for _, c := range []struct {
				layout string
				field  any
				ai     int
			}{{"ns", nil, 0}, {"sn", "n", 1}, {"n", nil, 0}} {
				t.Run(fmt.Sprintf("%s/scored/%s/field=%v", name, c.layout, c.field), func(t *testing.T) {
					env, err := p.PredictChain(ctx, nsChain(a, kind, c.layout, c.field))
					if err != nil {
						t.Fatal(err)
					}
					if len(env.Errors) == 0 {
						t.Fatal("PredictChain: no error, want a refusal")
					}
					_, err = p.ProcessChain(ctx, nsChain(a, kind, c.layout, c.field))
					ce := codeOf(t, err)
					if gate {
						if env.Errors[0].Code != string(perr.PULSE_CHAIN_NOT_MERGEABLE) || ce.Code != perr.PULSE_CHAIN_NOT_MERGEABLE {
							t.Fatalf("PredictChain %s / ProcessChain %s, want PULSE_CHAIN_NOT_MERGEABLE on both", env.Errors[0].Code, ce.Code)
						}
						return
					}
					wantDetails := map[string]any{"overlay_index": 0, "kind": string(kind), "role": "target",
						"stage_index": 0, "stage_name": "profile", "aggregation_index": c.ai, "aggregation": string(a.typ),
						"label": "n", "reason": "non_scalar"}
					if env.Errors[0].Code != string(perr.PROCESSING_CONFIG) || !reflect.DeepEqual(env.Errors[0].Details, wantDetails) {
						t.Fatalf("PredictChain = %+v, want PROCESSING_CONFIG %v first", env.Errors, wantDetails)
					}
					if ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
						t.Fatalf("ProcessChain = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
					}
					if ce.Message != env.Errors[0].Message {
						t.Errorf("message drift: runtime %q, predict %q", ce.Message, env.Errors[0].Message)
					}
				})
			}
		}
	}
}

// nsLabels is an extension aggregator whose Rich value is a label list.
type nsLabels struct{ n int }

func (a *nsLabels) Aggregate(rows extend.Rows, _ string) (float64, error) {
	a.n = rows.Len()
	return float64(a.n), nil
}

func (a *nsLabels) Rich() (any, error) { return []string{"x"}, nil }

func nsLabelsRegistration(nonScalar bool) pulse.AggregatorRegistration {
	return pulse.AggregatorRegistration{
		Name:        "AGG_NSTEST_LABELS",
		Description: "Test aggregator whose value is a label list.",
		NonScalar:   nonScalar,
		Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
			return &nsLabels{}, nil
		},
	}
}

// TestExtensionAggregator_NonScalarScoredRefused: an extension declared
// NonScalar is refused as the scored aggregation exactly like a
// built-in (predict from the snapshot, runtime from the registry);
// undeclared, the same operator is judged scalar on both sides.
func TestExtensionAggregator_NonScalarScoredRefused(t *testing.T) {
	ctx := context.Background()
	a := nsAgg{"AGG_NSTEST_LABELS", "score"}
	kind := types.OverlayKindDeltaVsPrior

	p, data := nsFixture(t, pulse.Options{Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{nsLabelsRegistration(true)}}})
	wantDetails := map[string]any{"aggregation": "AGG_NSTEST_LABELS", "label": "n", "slot": "aggregations[0]", "reason": "non_scalar"}
	pe := predictCodedError(t, p, data, nsProcess(a, kind, "ns", nil))
	if pe == nil || pe.Code != string(perr.PROCESSING_CONFIG) || !reflect.DeepEqual(pe.Details, wantDetails) {
		t.Fatalf("Predict = %+v, want PROCESSING_CONFIG %v", pe, wantDetails)
	}
	_, err := p.Process(ctx, nsProcess(a, kind, "ns", nil))
	if ce := codeOf(t, err); ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
		t.Fatalf("Process = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
	}
	if pe := predictCodedError(t, p, data, nsProcess(a, kind, "ns", "s")); pe != nil {
		t.Fatalf("Predict scoring s = %s %v, want clean", pe.Code, pe.Details)
	}
	if _, err := p.Process(ctx, nsProcess(a, kind, "ns", "s")); err != nil {
		t.Fatalf("Process scoring s: %v", err)
	}
	// The runtime registry's classifier reads the same declaration.
	if !pulse.ServiceForTest(p).Extensions().AggregationKinds().IsNonScalar(a.typ) {
		t.Error("runtime AggregationKinds does not class the declared extension non-scalar")
	}

	// Undeclared: no scored-value refusal on either side.
	q, qdata := nsFixture(t, pulse.Options{Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{nsLabelsRegistration(false)}}})
	if pe := predictCodedError(t, q, qdata, nsProcess(a, kind, "ns", nil)); pe != nil {
		t.Fatalf("undeclared: Predict = %s %v, want clean", pe.Code, pe.Details)
	}
	if _, err := q.Process(ctx, nsProcess(a, kind, "ns", nil)); err != nil {
		t.Fatalf("undeclared: Process: %v", err)
	}
	if pulse.ServiceForTest(q).Extensions().AggregationKinds().IsNonScalar(a.typ) {
		t.Error("undeclared: runtime AggregationKinds classes the extension non-scalar")
	}
}

// TestChainStage_NonScalarCannotFeed: a set fold's label list or count
// map in a stage that feeds another read as null there — a sum over it
// came back 0, silently. It is refused like an array-valued
// aggregation, reason "non_scalar", on predict (first) and runtime.
func TestChainStage_NonScalarCannotFeed(t *testing.T) {
	ctx := context.Background()
	p, _ := nsFixture(t, pulse.Options{})
	for _, a := range nsBuiltins() {
		if a.typ == types.AGG_WELFORD {
			continue // not mergeable: PULSE_CHAIN_NOT_MERGEABLE (TestChainOverlay_NonScalarScoredRefused)
		}
		t.Run(string(a.typ), func(t *testing.T) {
			req := func() *pulse.ChainRequest {
				return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "ns.pulse"}, Stages: []*types.ChainStage{
					{Name: "s0", Request: &types.Request{Groups: nsGroups(), Aggregations: nsAggs(a, "sn")}},
					{Name: "s1", Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}}}},
				}}
			}
			wantDetails := map[string]any{"stage_index": 0, "stage_name": "s0", "aggregation": string(a.typ), "label": "n", "reason": "non_scalar"}
			env, err := p.PredictChain(ctx, req())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PROCESSING_CONFIG) || !reflect.DeepEqual(env.Errors[0].Details, wantDetails) {
				t.Fatalf("PredictChain = %+v, want PROCESSING_CONFIG %v first", env.Errors, wantDetails)
			}
			_, err = p.ProcessChain(ctx, req())
			ce := codeOf(t, err)
			if ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
				t.Fatalf("ProcessChain = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
			}
			if ce.Message != env.Errors[0].Message {
				t.Errorf("message drift: runtime %q, predict %q", ce.Message, env.Errors[0].Message)
			}
		})
	}
}
