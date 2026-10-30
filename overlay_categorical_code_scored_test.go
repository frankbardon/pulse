package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/testhook"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// AGG_MODE over a categorical field writes the modal value's
// DICTIONARY INDEX (over a packed_bool field its 0/1 truth value) — one
// number, but a code. A series overlay scoring it once compared codes
// as magnitudes: meaningless figures plus a spurious
// PULSE_OVERLAY_REF_ZERO wherever the modal code was 0 (E4-S14). It is
// now refused on every host, predict and runtime alike,
// PROCESSING_CONFIG with the scored-value Details shape plus the input
// `field` / `field_type` and reason "categorical_code"; AGG_MODE over a
// numeric field still scores, and an unscored code aggregation beside a
// scalar still runs.
//
// Every parity case runs PREDICT FIRST on a fresh request.

// ccSchema: region (the grouper), color (categorical, red listed
// first so code 0 is a live modal value), flag (packed_bool), x (f64).
func ccSchema() encoding.Schema {
	cat := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			_, _ = d.Add(v)
		}
		return d
	}
	fields := []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: cat("east", "north", "south")},
		{Name: "color", Type: encoding.FieldTypeCategoricalU8, Dictionary: cat("red", "blue", "green")},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "x", Type: encoding.FieldTypeF64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Categorical-code overlay column " + fields[i].Name + "."
		if fields[i].Type.IsBitPacked() {
			off++
		} else {
			off += fields[i].Type.ByteSize()
		}
	}
	return encoding.Schema{Fields: fields}
}

// ccRows: per region a distinct modal color (east blue, north red,
// south green), a distinct modal flag and a distinct modal x.
var ccRows = []pulse.CohortRow{
	{"east", "blue", true, 1.0}, {"east", "blue", true, 1.0}, {"east", "red", false, 2.0},
	{"north", "red", false, 3.0}, {"north", "red", false, 3.0}, {"north", "green", true, 4.0},
	{"south", "green", true, 5.0}, {"south", "green", false, 5.0}, {"south", "blue", true, 6.0},
}

func ccFixture(t *testing.T, opts pulse.Options) (*pulse.Pulse, []byte) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	opts.FS = fsys
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), "cc.pulse", ccSchema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ccRows {
		if err := b.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := afero.ReadFile(fsys, "cc.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return p, data
}

// ccAggs builds aggregations from a layout: "m" = typ over field
// labelled m, "s" = AGG_SUM x labelled s.
func ccAggs(typ types.AggregationType, field, layout string) []*types.Aggregation {
	var out []*types.Aggregation
	for _, c := range layout {
		switch c {
		case 'm':
			out = append(out, &types.Aggregation{Type: typ, Field: field, Label: "m"})
		case 's':
			out = append(out, &types.Aggregation{Type: types.AGG_SUM, Field: "x", Label: "s"})
		}
	}
	return out
}

func ccGroups() []*types.Group {
	return []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
}

// ccCode is one refused (aggregation, input) pair.
type ccCode struct {
	typ       types.AggregationType
	field, ft string
}

func ccCodes() []ccCode {
	return []ccCode{{types.AGG_MODE, "color", "categorical_u8"}, {types.AGG_MODE, "flag", "packed_bool"}}
}

func ccProcess(typ types.AggregationType, field string, kind types.OverlayKind, layout string, scored any) *pulse.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: "cc.pulse"}, Groups: ccGroups(), Aggregations: ccAggs(typ, field, layout)}
	spec := types.OverlaySpec{Kind: kind, Scope: types.OverlayScopeGroup}
	if scored != nil {
		spec.Params, _ = json.Marshal(map[string]any{"field": scored})
	}
	req.Overlays = []types.OverlaySpec{spec}
	return req
}

// ccLayouts are the scored placements: default first, named second,
// alone.
var ccLayouts = []struct {
	layout string
	field  any
	ai     int
}{{"ms", nil, 0}, {"sm", "m", 1}, {"m", nil, 0}}

// TestProcessOverlay_CategoricalCodeScoredRefused: the Process host.
func TestProcessOverlay_CategoricalCodeScoredRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, data := ccFixture(t, pulse.Options{})
	for _, kind := range []types.OverlayKind{types.OverlayKindDeltaVsPrior, types.OverlayKindIndexVsTotal, types.OverlayKindShareOfTotal} {
		baseline, err := p.Process(ctx, ccProcess(types.AGG_SUM, "x", kind, "s", nil))
		if err != nil {
			t.Fatalf("%s baseline: %v", kind, err)
		}
		want := nsJSON(t, baseline.Overlays)
		for _, c := range ccCodes() {
			name := fmt.Sprintf("%s/%s(%s)", kind, c.typ, c.ft)
			t.Run(name+"/unscored_runs", func(t *testing.T) {
				if pe := predictCodedError(t, p, data, ccProcess(c.typ, c.field, kind, "ms", "s")); pe != nil {
					t.Fatalf("Predict = %s %v, want clean", pe.Code, pe.Details)
				}
				out, err := p.Process(ctx, ccProcess(c.typ, c.field, kind, "ms", "s"))
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if got := nsJSON(t, out.Overlays); got != want {
					t.Errorf("overlays =\n%s\nwant (scalar-only request)\n%s", got, want)
				}
			})
			for _, l := range ccLayouts {
				t.Run(fmt.Sprintf("%s/scored/%s/field=%v", name, l.layout, l.field), func(t *testing.T) {
					wantDetails := map[string]any{"aggregation": string(c.typ), "label": "m", "slot": fmt.Sprintf("aggregations[%d]", l.ai),
						"reason": "categorical_code", "field": c.field, "field_type": c.ft}
					pe := predictCodedError(t, p, data, ccProcess(c.typ, c.field, kind, l.layout, l.field))
					if pe == nil || pe.Code != string(perr.PROCESSING_CONFIG) || !reflect.DeepEqual(pe.Details, wantDetails) {
						t.Fatalf("Predict = %+v, want PROCESSING_CONFIG %v", pe, wantDetails)
					}
					_, err := p.Process(ctx, ccProcess(c.typ, c.field, kind, l.layout, l.field))
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
		// A magnitude still scores: AGG_MODE over the numeric x, and the
		// smart default over a categorical field (AGG_MODE_COUNT, a
		// count), predict first.
		for _, c := range []struct {
			name string
			typ  types.AggregationType
			fld  string
		}{{"mode_numeric", types.AGG_MODE, "x"}, {"default_categorical", "", "color"}} {
			t.Run(string(kind)+"/"+c.name+"_scores", func(t *testing.T) {
				if pe := predictCodedError(t, p, data, ccProcess(c.typ, c.fld, kind, "m", nil)); pe != nil {
					t.Fatalf("Predict = %s %v, want clean", pe.Code, pe.Details)
				}
				out, err := p.Process(ctx, ccProcess(c.typ, c.fld, kind, "m", nil))
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if len(out.Overlays) != 1 || out.Overlays[0].Payload.Series == nil || len(out.Overlays[0].Payload.Series.Entries) == 0 {
					t.Fatalf("overlays = %s, want one non-empty SERIES layer", nsJSON(t, out.Overlays))
				}
			})
		}
	}
}

func ccCompose(typ types.AggregationType, field string, kind types.OverlayKind, layout string, scored any) *pulse.ComposedRequest {
	slot := func(label string, regions ...string) *types.Request {
		r := &types.Request{Label: label, Cohort: &types.Cohort{Filename: "cc.pulse"}, Groups: ccGroups(), Aggregations: ccAggs(typ, field, layout)}
		if len(regions) > 0 {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "color", Values: regions}}
		}
		return r
	}
	params := map[string]any{}
	if scored != nil {
		params["field"] = scored
	}
	spec := types.ComposeOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"},
		Params: nsTwoSampleParams(kind, params)}
	if len(spec.Params) == 0 {
		spec.Params = nil
	}
	return &pulse.ComposedRequest{Requests: []*types.Request{slot("a"), slot("b", "red", "blue", "green")},
		Overlays: []types.ComposeOverlaySpec{spec}}
}

// TestComposeOverlay_CategoricalCodeScoredRefused: every Compose series
// kind, the slot's cohort schema deciding.
func TestComposeOverlay_CategoricalCodeScoredRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := ccFixture(t, pulse.Options{})
	for _, kind := range seriesfield.ComposeKinds() {
		baseline, err := p.Compose(ctx, ccCompose(types.AGG_SUM, "x", kind, "s", nil))
		if err != nil {
			t.Fatalf("%s baseline: %v", kind, err)
		}
		want := nsJSON(t, baseline.Overlays)
		for _, c := range ccCodes() {
			name := fmt.Sprintf("%s/%s(%s)", kind, c.typ, c.ft)
			t.Run(name+"/unscored_runs", func(t *testing.T) {
				env, err := p.PredictCompose(ctx, ccCompose(c.typ, c.field, kind, "ms", "s"))
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Errors) != 0 {
					t.Fatalf("PredictCompose errors = %+v, want none", env.Errors)
				}
				out, err := p.Compose(ctx, ccCompose(c.typ, c.field, kind, "ms", "s"))
				if err != nil {
					t.Fatalf("Compose: %v", err)
				}
				if got := nsJSON(t, out.Overlays); got != want {
					t.Errorf("layers =\n%s\nwant (scalar-only slot)\n%s", got, want)
				}
			})
			for _, l := range ccLayouts {
				t.Run(fmt.Sprintf("%s/scored/%s/field=%v", name, l.layout, l.field), func(t *testing.T) {
					env, err := p.PredictCompose(ctx, ccCompose(c.typ, c.field, kind, l.layout, l.field))
					if err != nil {
						t.Fatal(err)
					}
					wantDetails := map[string]any{"overlay_index": 0, "kind": string(kind), "slot": "a",
						"host": fmt.Sprintf("requests[0].aggregations[%d]", l.ai), "aggregation": string(c.typ),
						"label": "m", "reason": "categorical_code", "field": c.field, "field_type": c.ft}
					if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PROCESSING_CONFIG) ||
						!reflect.DeepEqual(env.Errors[0].Details, wantDetails) {
						t.Fatalf("PredictCompose = %+v, want PROCESSING_CONFIG %v first", env.Errors, wantDetails)
					}
					_, err = p.Compose(ctx, ccCompose(c.typ, c.field, kind, l.layout, l.field))
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
		t.Run(string(kind)+"/mode_numeric_scores", func(t *testing.T) {
			env, err := p.PredictCompose(ctx, ccCompose(types.AGG_MODE, "x", kind, "m", nil))
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 0 {
				t.Fatalf("PredictCompose errors = %+v, want none", env.Errors)
			}
			if _, err := p.Compose(ctx, ccCompose(types.AGG_MODE, "x", kind, "m", nil)); err != nil {
				t.Fatalf("Compose: %v", err)
			}
		})
	}
}

// ccChain: stage 0 groups by region; stages beyond it are appended
// verbatim. The overlay targets stage target and refs stage 0.
func ccChain(stage0 []*types.Aggregation, later []*types.Request, kind types.OverlayKind, target int, scored any) *pulse.ChainRequest {
	zero := 0
	spec := &types.ChainOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Ref: types.StageRef{Index: &zero}, Target: types.StageRef{Index: &target}}
	if scored != nil {
		spec.Params = map[string]any{"field": scored}
	}
	stages := []*types.ChainStage{{Name: "s0", Request: &types.Request{Groups: ccGroups(), Aggregations: stage0}}}
	for i, r := range later {
		stages = append(stages, &types.ChainStage{Name: fmt.Sprintf("s%d", i+1), Request: r})
	}
	return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "cc.pulse"}, Stages: stages, Overlays: []*types.ChainOverlaySpec{spec}}
}

// ccChainRefused asserts predict (first) and runtime refuse req with
// wantDetails, messages equal.
func ccChainRefused(t *testing.T, p *pulse.Pulse, req func() *pulse.ChainRequest, wantDetails map[string]any) {
	t.Helper()
	ctx := context.Background()
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
}

// ccChainRuns asserts predict (first) and runtime both run req.
func ccChainRuns(t *testing.T, p *pulse.Pulse, req func() *pulse.ChainRequest) *types.ChainResponse {
	t.Helper()
	ctx := context.Background()
	env, err := p.PredictChain(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("PredictChain errors = %+v, want none", env.Errors)
	}
	out, err := p.ProcessChain(ctx, req())
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	return out
}

// TestChainOverlay_CategoricalCodeScoredRefused: the whole-chain host.
// Stage 0 is judged against the cohort schema; a later stage against
// the schema synthesised from the stage before it, where a group key
// column is categorical (AGG_MODE over it is a code, refused) and an
// aggregation output column is f64 — AGG_MODE over it scores, unless a
// code-emitting aggregation wrote it (still a code, refused: E4-S17).
func TestChainOverlay_CategoricalCodeScoredRefused(t *testing.T) {
	t.Parallel()

	p, _ := ccFixture(t, pulse.Options{})
	for _, kind := range seriesfield.ChainKinds() {
		for _, c := range ccCodes() {
			for _, l := range ccLayouts {
				t.Run(fmt.Sprintf("%s/stage0/%s(%s)/%s/field=%v", kind, c.typ, c.ft, l.layout, l.field), func(t *testing.T) {
					ccChainRefused(t, p, func() *pulse.ChainRequest {
						return ccChain(ccAggs(c.typ, c.field, l.layout), nil, kind, 0, l.field)
					}, map[string]any{"overlay_index": 0, "kind": string(kind), "role": "target", "stage_index": 0, "stage_name": "s0",
						"aggregation_index": l.ai, "aggregation": string(c.typ), "label": "m", "reason": "categorical_code",
						"field": c.field, "field_type": c.ft})
				})
			}
			t.Run(fmt.Sprintf("%s/stage0/%s(%s)/unscored_runs", kind, c.typ, c.ft), func(t *testing.T) {
				ccChainRuns(t, p, func() *pulse.ChainRequest { return ccChain(ccAggs(c.typ, c.field, "ms"), nil, kind, 0, "s") })
			})
		}
		// Stage 1 groups by the stage-0 key column region (categorical
		// in the synthesised schema).
		stage1 := func(aggs ...*types.Aggregation) []*types.Request {
			return []*types.Request{{Groups: ccGroups(), Aggregations: aggs}}
		}
		s0 := func() []*types.Aggregation {
			return []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}, {Type: types.AGG_MODE, Field: "color", Label: "mc"}}
		}
		t.Run(string(kind)+"/stage1_mode_over_group_key_refused", func(t *testing.T) {
			ccChainRefused(t, p, func() *pulse.ChainRequest {
				return ccChain(s0(), stage1(&types.Aggregation{Type: types.AGG_MODE, Field: "region", Label: "s"}), kind, 1, nil)
			}, map[string]any{"overlay_index": 0, "kind": string(kind), "role": "target", "stage_index": 1, "stage_name": "s1",
				"aggregation_index": 0, "aggregation": "AGG_MODE", "label": "s", "reason": "categorical_code",
				"field": "region", "field_type": "categorical_u32"})
		})
		// A forwarded aggregation column is f64 in the synthesised
		// schema, but one a code-emitting aggregation wrote is still a
		// code (E4-S17): refused, field_type the type it is a code of.
		t.Run(string(kind)+"/stage1_mode_over_forwarded_code_refused", func(t *testing.T) {
			ccChainRefused(t, p, func() *pulse.ChainRequest {
				return ccChain(s0(), stage1(&types.Aggregation{Type: types.AGG_MODE, Field: "mc", Label: "s"}), kind, 1, nil)
			}, map[string]any{"overlay_index": 0, "kind": string(kind), "role": "target", "stage_index": 1, "stage_name": "s1",
				"aggregation_index": 0, "aggregation": "AGG_MODE", "label": "s", "reason": "categorical_code",
				"field": "mc", "field_type": "categorical_u8"})
		})
		t.Run(string(kind)+"/stage1_mode_over_forwarded_magnitude_scores", func(t *testing.T) {
			ccChainRuns(t, p, func() *pulse.ChainRequest {
				return ccChain(s0(), stage1(&types.Aggregation{Type: types.AGG_MODE, Field: "s", Label: "s"}), kind, 1, nil)
			})
		})
	}
}

// ccPick is an extension aggregator returning the first value it
// reads — an input-valued pick.
type ccPick struct{ field string }

func (a *ccPick) Aggregate(rows extend.Rows, _ string) (float64, error) {
	if rows.Len() == 0 {
		return 0, nil
	}
	v, _ := rows.At(0).NumericValue(a.field)
	return v, nil
}

func ccPickRegistration(declared bool) pulse.AggregatorRegistration {
	return pulse.AggregatorRegistration{
		Name:            "AGG_CCTEST_PICK",
		Description:     "Test aggregator returning the first value of its field.",
		EmitsInputValue: declared,
		Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
			return &ccPick{field: spec.Field}, nil
		},
	}
}

// TestExtensionAggregator_CategoricalCodeScoredRefused: an extension
// declared EmitsInputValue is refused as the scored aggregation over a
// categorical field exactly like AGG_MODE (predict from the snapshot,
// runtime from the registry) and scores over a numeric one; the
// manifest carries emits_input_value; undeclared, it scores.
func TestExtensionAggregator_CategoricalCodeScoredRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kind := types.OverlayKindDeltaVsPrior
	const typ = types.AggregationType("AGG_CCTEST_PICK")

	p, data := ccFixture(t, pulse.Options{Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{ccPickRegistration(true)}}})
	wantDetails := map[string]any{"aggregation": string(typ), "label": "m", "slot": "aggregations[0]", "reason": "categorical_code",
		"field": "color", "field_type": "categorical_u8"}
	pe := predictCodedError(t, p, data, ccProcess(typ, "color", kind, "ms", nil))
	if pe == nil || pe.Code != string(perr.PROCESSING_CONFIG) || !reflect.DeepEqual(pe.Details, wantDetails) {
		t.Fatalf("Predict = %+v, want PROCESSING_CONFIG %v", pe, wantDetails)
	}
	_, err := p.Process(ctx, ccProcess(typ, "color", kind, "ms", nil))
	if ce := codeOf(t, err); ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
		t.Fatalf("Process = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
	}
	if pe := predictCodedError(t, p, data, ccProcess(typ, "x", kind, "ms", nil)); pe != nil {
		t.Fatalf("Predict over x = %s %v, want clean", pe.Code, pe.Details)
	}
	if _, err := p.Process(ctx, ccProcess(typ, "x", kind, "ms", nil)); err != nil {
		t.Fatalf("Process over x: %v", err)
	}
	if !testhook.Service(p).Extensions().AggregationKinds().EmitsInputValue(typ) {
		t.Error("runtime AggregationKinds does not class the declared extension input-valued")
	}
	m := p.Manifest(ctx)
	var meta *descriptor.OperatorMeta
	for i := range m.Extensions.Aggregators {
		if m.Extensions.Aggregators[i].Name == string(typ) {
			meta = &m.Extensions.Aggregators[i]
		}
	}
	if meta == nil || !meta.EmitsInputValue {
		t.Errorf("manifest extension entry = %+v, want emits_input_value", meta)
	}

	q, qdata := ccFixture(t, pulse.Options{Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{ccPickRegistration(false)}}})
	if pe := predictCodedError(t, q, qdata, ccProcess(typ, "color", kind, "ms", nil)); pe != nil {
		t.Fatalf("undeclared: Predict = %s %v, want clean", pe.Code, pe.Details)
	}
	if _, err := q.Process(ctx, ccProcess(typ, "color", kind, "ms", nil)); err != nil {
		t.Fatalf("undeclared: Process: %v", err)
	}
}
