package descriptor

import (
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func weightSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "w", Type: encoding.FieldTypeF32},
		{Name: "wi", Type: encoding.FieldTypeU16},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8},
		{Name: "d", Type: encoding.FieldTypeDate},
		{Name: "dec", Type: encoding.FieldTypeDecimal128},
	}}
}

// withWeightAware marks ops weight-aware for the test's duration — the
// table is filled operator by operator by later stories, so the
// "applied" branch is driven here directly.
func withWeightAware(t *testing.T, ops ...string) {
	t.Helper()
	saved := weightAwareOperators
	weightAwareOperators = map[string]bool{}
	for _, op := range ops {
		weightAwareOperators[op] = true
	}
	t.Cleanup(func() { weightAwareOperators = saved })
}

func codeOf(t *testing.T, err error) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want coded error, got %v", err)
	}
	return ce
}

// TestResolveWeights_Precedence walks slot → request → options → none
// and the slot `null` opt-out, per slot, in reporting order.
func TestResolveWeights_Precedence(t *testing.T) {
	withWeightAware(t, "AGG_SUM", "AGG_COUNT")
	def := &types.WeightSpec{Field: "wi", Kind: types.WeightKindFrequency}
	req := &types.Request{
		Weight: &types.WeightSpec{Field: "w"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x"},                                                                               // request
			{Type: types.AGG_SUM, Field: "x", Weight: types.NullSlotWeight()},                                               // opted out
			{Type: types.AGG_SUM, Field: "x", Weight: types.SlotWeightOf(types.WeightSpec{Field: "wi", Kind: "frequency"})}, // slot
			{Type: types.AGG_MIN, Field: "x"},                                                                               // not aware
		},
		Tests: []*types.Test{{Type: types.TEST_T, Field: "x", Weight: types.NullSlotWeight()}},
	}
	got, err := ResolveWeights(req, weightSchema(), def, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []descriptor.ResolvedWeight{
		{Slot: "aggregations[0]", Operator: "AGG_SUM", Field: "w", Kind: "probability", Status: "applied", Source: "request"},
		{Slot: "aggregations[1]", Operator: "AGG_SUM", Status: "opted_out", Source: "slot"},
		{Slot: "aggregations[2]", Operator: "AGG_SUM", Field: "wi", Kind: "frequency", Status: "applied", Source: "slot"},
		{Slot: "aggregations[3]", Operator: "AGG_MIN", Field: "w", Kind: "probability", Status: "skipped_not_weight_aware", Source: "request"},
		{Slot: "tests[0]", Operator: "TEST_T", Status: "opted_out", Source: "slot"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}

	// Without the request weight the default applies; without either,
	// nothing does.
	req.Weight = nil
	got, err = ResolveWeights(req, weightSchema(), def, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g := got[0]; g.Source != "options" || g.Field != "wi" || g.Kind != "frequency" || g.Status != "applied" {
		t.Fatalf("default: %+v", g)
	}
	got, err = ResolveWeights(req, weightSchema(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g := got[0]; g.Source != "none" || g.Status != "none" || g.Field != "" {
		t.Fatalf("none: %+v", g)
	}
}

// TestResolveWeights_SlotFamilies: every weight-bearing slot is walked
// — crosstab cell and margin aggregations, post-tests, regressions,
// attributes, overlays — under its JSON path.
func TestResolveWeights_SlotFamilies(t *testing.T) {
	req := &types.Request{
		Weight:      &types.WeightSpec{Field: "w"},
		PostTests:   []*types.Test{{Type: types.TEST_T, Field: "x"}},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "x"}},
		Attributes:  []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "x"}},
		Overlays:    []types.OverlaySpec{{Kind: types.OverlayKindDeltaVsMargin}},
		Crosstab: &types.CrosstabSpec{
			Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
			MarginAggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Weight: types.NullSlotWeight()}},
		},
	}
	got, err := ResolveWeights(req, weightSchema(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var slots []string
	for _, g := range got {
		slots = append(slots, g.Slot)
	}
	want := []string{"crosstab.cell", "crosstab.margin_aggregations[0]", "post_tests[0]", "regressions[0]", "attributes[0]", "overlays[0]"}
	if !reflect.DeepEqual(slots, want) {
		t.Fatalf("slots = %v, want %v", slots, want)
	}
	if got[1].Status != "opted_out" || got[5].Operator != "OVERLAY_DELTA_VS_MARGIN" {
		t.Fatalf("got %+v", got)
	}
}

// TestResolveWeights_NothingNamedIsNil: no weight anywhere (request,
// slot, instance) resolves to nil, so predict omits `weights` and an
// unweighted request's output is unchanged.
func TestResolveWeights_NothingNamedIsNil(t *testing.T) {
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}
	got, err := ResolveWeights(req, weightSchema(), nil, nil)
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want nil, nil", got, err)
	}
}

// TestResolveWeights_Refusals: shape and type refusals carry
// PROCESSING_CONFIG with {slot, field, type|kind} details.
func TestResolveWeights_Refusals(t *testing.T) {
	agg := func(w types.SlotWeight) []*types.Aggregation {
		return []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Weight: w}}
	}
	cases := []struct {
		name    string
		req     *types.Request
		def     *types.WeightSpec
		details map[string]any
	}{
		{"request categorical", &types.Request{Weight: &types.WeightSpec{Field: "cat"}, Aggregations: agg(types.SlotWeight{})}, nil,
			map[string]any{"slot": "weight", "field": "cat", "type": "categorical_u8"}},
		{"slot date", &types.Request{Aggregations: agg(types.SlotWeightField("d"))}, nil,
			map[string]any{"slot": "aggregations[0].weight", "field": "d", "type": "date"}},
		{"slot decimal", &types.Request{Aggregations: agg(types.SlotWeightField("dec"))}, nil,
			map[string]any{"slot": "aggregations[0].weight", "field": "dec", "type": "decimal128"}},
		{"request bad kind", &types.Request{Weight: &types.WeightSpec{Field: "w", Kind: "replicate"}, Aggregations: agg(types.SlotWeight{})}, nil,
			map[string]any{"slot": "weight", "field": "w", "kind": "replicate"}},
		{"slot empty field", &types.Request{Aggregations: agg(types.SlotWeightField(""))}, nil,
			map[string]any{"slot": "aggregations[0].weight"}},
		{"default bad kind", &types.Request{Aggregations: agg(types.SlotWeight{})}, &types.WeightSpec{Field: "w", Kind: "x"},
			map[string]any{"slot": "Options.DefaultWeight", "field": "w", "kind": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveWeights(tc.req, weightSchema(), tc.def, nil)
			ce := codeOf(t, err)
			if ce.Code != errors.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, tc.details) {
				t.Fatalf("got %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, tc.details)
			}
		})
	}

	// A nil schema judges no field type (the schema-less validator mode).
	if _, err := ResolveWeights(&types.Request{Weight: &types.WeightSpec{Field: "cat"}, Aggregations: agg(types.SlotWeight{})}, nil, nil, nil); err != nil {
		t.Fatalf("nil schema refused: %v", err)
	}
	// Every allowed weight type is accepted.
	for _, f := range []string{"x", "w", "wi"} {
		if _, err := ResolveWeights(&types.Request{Weight: &types.WeightSpec{Field: f}, Aggregations: agg(types.SlotWeight{})}, weightSchema(), nil, nil); err != nil {
			t.Fatalf("%s refused: %v", f, err)
		}
	}
}

// TestResolveWeights_DefaultJudgedWhereItApplies: an instance default
// naming a column this cohort lacks (or one of the wrong type) is
// refused only where it would be applied — a request it reaches only on
// non-weight-aware slots, or only through opted-out slots, runs.
func TestResolveWeights_DefaultJudgedWhereItApplies(t *testing.T) {
	withWeightAware(t, "AGG_SUM")
	missing := &types.WeightSpec{Field: "nope"}
	minOnly := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MIN, Field: "x"}}}
	if _, err := ResolveWeights(minOnly, weightSchema(), missing, nil); err != nil {
		t.Fatalf("skipped slot refused: %v", err)
	}
	optedOut := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Weight: types.NullSlotWeight()}}}
	if _, err := ResolveWeights(optedOut, weightSchema(), missing, nil); err != nil {
		t.Fatalf("opted-out slot refused: %v", err)
	}
	applies := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}
	ce := codeOf(t, func() error { _, err := ResolveWeights(applies, weightSchema(), missing, nil); return err }())
	if ce.Code != errors.SERVICE_VALIDATION || ce.Details["field"] != "nope" || ce.Details["slot"] != "aggregations[0]" {
		t.Fatalf("missing default: %s %v", ce.Code, ce.Details)
	}
	ce = codeOf(t, func() error {
		_, err := ResolveWeights(applies, weightSchema(), &types.WeightSpec{Field: "cat"}, nil)
		return err
	}())
	if ce.Code != errors.PROCESSING_CONFIG || ce.Details["type"] != "categorical_u8" {
		t.Fatalf("mistyped default: %s %v", ce.Code, ce.Details)
	}
	// A hidden operator is never-registered: not weight-aware, so the
	// default is skipped there.
	inst := scopedExcept("AGG_SUM")
	got, err := ResolveWeights(applies, weightSchema(), missing, inst)
	if err != nil || got[0].Status != "skipped_not_weight_aware" {
		t.Fatalf("hidden operator: %+v %v", got, err)
	}
}

// TestFieldRefs_WeightFields: an explicit weight naming a column the
// schema lacks — or a derived column (an attribute label) — is the
// field-reference refusal; the inherited default is not judged there.
func TestFieldRefs_WeightFields(t *testing.T) {
	schema := weightSchema()
	cases := []struct {
		name string
		req  *types.Request
		slot string
	}{
		{"request", &types.Request{Weight: &types.WeightSpec{Field: "nope"}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}, "weight"},
		{"slot", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Weight: types.SlotWeightField("nope")}}}, "aggregations[0].weight"},
		{"margin", &types.Request{Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
			MarginAggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "base", Weight: types.SlotWeightField("nope")}},
		}}, "crosstab.margin_aggregations[0].weight"},
		{"derived", &types.Request{
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "nope", Expression: "x * 2"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Weight: types.SlotWeightField("nope")}},
		}, "aggregations[0].weight"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs := FieldRefRefusals(tc.req, schema, nil)
			if len(refs) != 1 {
				t.Fatalf("refusals = %v, want one", refs)
			}
			if refs[0].Code != errors.SERVICE_VALIDATION || refs[0].Details["field"] != "nope" || refs[0].Details["slot"] != tc.slot {
				t.Fatalf("got %s %v", refs[0].Code, refs[0].Details)
			}
		})
	}
	ok := &types.Request{Weight: &types.WeightSpec{Field: "w"}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Weight: types.NullSlotWeight()}}}
	if refs := FieldRefRefusals(ok, schema, nil); len(refs) != 0 {
		t.Fatalf("valid weights refused: %v", refs)
	}
}

// TestPredictWeights_Surface: predict echoes ResolveWeights as
// PredictResult.Weights, reports a refusal as an error under the
// runtime's code, and omits `weights` for an unweighted request.
func TestPredictWeights_Surface(t *testing.T) {
	data := buildTestPulseFile(t, weightSchema())
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}

	env := predictFromBytes(data, req, &PredictOptions{})
	if res := env.Data.(*descriptor.PredictResult); res.Weights != nil {
		t.Fatalf("unweighted request: Weights = %+v, want nil", res.Weights)
	}

	env = predictFromBytes(data, req, &PredictOptions{DefaultWeight: &types.WeightSpec{Field: "w"}})
	res := env.Data.(*descriptor.PredictResult)
	if len(env.Errors) != 0 || len(res.Weights) != 1 || res.Weights[0].Source != "options" || res.Weights[0].Field != "w" {
		t.Fatalf("default: errors %+v weights %+v", env.Errors, res.Weights)
	}

	bad := &types.Request{Weight: &types.WeightSpec{Field: "cat"}, Aggregations: req.Aggregations}
	env = predictFromBytes(data, bad, &PredictOptions{})
	if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PROCESSING_CONFIG) || env.Errors[0].Details["type"] != "categorical_u8" {
		t.Fatalf("refusal: %+v", env.Errors)
	}
}
