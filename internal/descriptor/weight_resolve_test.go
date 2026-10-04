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
	def := &types.WeightSpec{Field: "wi", Kind: types.WeightKindFrequency}
	req := &types.Request{
		Weight: &types.WeightSpec{Field: "w"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x"},                                                                               // request
			{Type: types.AGG_SUM, Field: "x", Weight: types.NullSlotWeight()},                                               // opted out
			{Type: types.AGG_SUM, Field: "x", Weight: types.SlotWeightOf(types.WeightSpec{Field: "wi", Kind: "frequency"})}, // slot
			{Type: types.AGG_MIN, Field: "x", Weight: types.NullSlotWeight()},                                               // not aware, opted out
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
		{Slot: "aggregations[3]", Operator: "AGG_MIN", Status: "opted_out", Source: "slot"},
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
// attributes, overlays, groupers, crosstab axes, windows — under its
// JSON path.
func TestResolveWeights_SlotFamilies(t *testing.T) {
	null := types.NullSlotWeight()
	req := &types.Request{
		Weight:      &types.WeightSpec{Field: "w"},
		PostTests:   []*types.Test{{Type: types.TEST_T, Field: "x", Weight: null}},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "x", Weight: null}},
		Attributes:  []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "x", Weight: null}},
		Overlays:    []types.OverlaySpec{{Kind: types.OverlayKindDeltaVsMargin}},
		Groups:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		Windows:     []*types.Window{{Type: types.WIN_LAG, Field: "x"}},
		Crosstab: &types.CrosstabSpec{
			Rows:               []*types.Group{{Type: types.GROUP_QUANTILE, Field: "x", Interval: 4, Weight: null}},
			Columns:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
			MarginAggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Weight: null}},
		},
	}
	// The request weight on a window is an explicit weight; the default
	// is not, so this pass reaches every slot.
	def := req.Weight
	req.Weight = nil
	req.Crosstab.Cell.Weight = types.SlotWeightField("w")
	got, err := ResolveWeights(req, weightSchema(), def, nil)
	if err != nil {
		t.Fatal(err)
	}
	var slots []string
	for _, g := range got {
		slots = append(slots, g.Slot)
	}
	want := []string{"crosstab.cell", "crosstab.margin_aggregations[0]", "post_tests[0]", "regressions[0]", "attributes[0]", "overlays[0]",
		"groups[0]", "crosstab.rows[0]", "crosstab.columns[0]", "windows[0]"}
	if !reflect.DeepEqual(slots, want) {
		t.Fatalf("slots = %v, want %v", slots, want)
	}
	if got[1].Status != "opted_out" || got[5].Operator != "OVERLAY_DELTA_VS_MARGIN" || got[7].Status != "opted_out" || got[9].Status != "skipped_not_weight_aware" {
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

// TestResolveWeights_Classes: the operator's weight class decides what
// a resolved weight does — applied on a weight-aware aggregator;
// skipped under the instance default but refused (PROCESSING_CONFIG)
// under an explicit slot or request weight on a not-weightable or
// still-pending one; PULSE_WEIGHT_UNSUPPORTED under ANY weight on the
// inferential AGG_CI_*; skipped on an operator the table does not
// govern. `weight: null` opts every class out.
func TestResolveWeights_Classes(t *testing.T) {
	def := &types.WeightSpec{Field: "w"}
	one := func(a *types.Aggregation, reqW *types.WeightSpec) *types.Request {
		return &types.Request{Weight: reqW, Aggregations: []*types.Aggregation{a}}
	}
	for _, op := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM, types.AGG_AVERAGE, types.AGG_VARIANCE, types.AGG_STDDEV, types.AGG_WELFORD,
		types.AGG_MEDIAN, types.AGG_PERCENTILE, types.AGG_MODE, types.AGG_MODE_COUNT, types.AGG_SKEWNESS, types.AGG_KURTOSIS,
		types.AGG_FREQUENCY, types.AGG_RATIO, types.AGG_SET_FREQUENCY, types.AGG_SET_CARDINALITY_SUM, types.AGG_SET_CARDINALITY_AVG} {
		got, err := ResolveWeights(one(&types.Aggregation{Type: op, Field: "x"}, def), weightSchema(), nil, nil)
		if err != nil || got[0].Status != descriptor.WeightStatusApplied {
			t.Fatalf("%s: %+v %v, want applied", op, got, err)
		}
	}
	for _, op := range []types.AggregationType{types.AGG_MIN, types.AGG_RANGE} {
		got, err := ResolveWeights(one(&types.Aggregation{Type: op, Field: "x"}, nil), weightSchema(), def, nil)
		if err != nil || got[0].Status != descriptor.WeightStatusSkippedNotWeightAware {
			t.Fatalf("%s under default: %+v %v, want skipped", op, got, err)
		}
		for name, req := range map[string]*types.Request{
			"request": one(&types.Aggregation{Type: op, Field: "x"}, def),
			"slot":    one(&types.Aggregation{Type: op, Field: "x", Weight: types.SlotWeightField("w")}, nil),
		} {
			_, err := ResolveWeights(req, weightSchema(), nil, nil)
			ce := codeOf(t, err)
			if ce.Code != errors.PROCESSING_CONFIG || ce.Details["operator"] != string(op) || ce.Details["slot"] != "aggregations[0]" {
				t.Fatalf("%s %s: %s %v", op, name, ce.Code, ce.Details)
			}
		}
		if _, err := ResolveWeights(one(&types.Aggregation{Type: op, Field: "x", Weight: types.NullSlotWeight()}, def), weightSchema(), nil, nil); err != nil {
			t.Fatalf("%s opted out: %v", op, err)
		}
	}
	for _, op := range []types.AggregationType{types.AGG_CI_LOWER, types.AGG_CI_UPPER} {
		_, err := ResolveWeights(one(&types.Aggregation{Type: op, Field: "x"}, nil), weightSchema(), def, nil)
		if ce := codeOf(t, err); ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED || ce.Details["operator"] != string(op) {
			t.Fatalf("%s under default: %s %v", op, ce.Code, ce.Details)
		}
		if _, err := ResolveWeights(one(&types.Aggregation{Type: op, Field: "x", Weight: types.NullSlotWeight()}, def), weightSchema(), def, nil); err != nil {
			t.Fatalf("%s opted out: %v", op, err)
		}
	}
	got, err := ResolveWeights(&types.Request{Weight: def, Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "x * 2"}}}, weightSchema(), nil, nil)
	if err != nil || got[0].Status != descriptor.WeightStatusSkippedNotWeightAware {
		t.Fatalf("ungoverned operator: %+v %v", got, err)
	}
}

// TestResolveWeights_WeightedMeanSugar: AGG_WEIGHTED_MEAN's
// params.weight_field is the slot's own weight (kind probability); it
// agrees with or conflicts against an explicit slot weight, and a slot
// nothing resolves a weight for is refused.
func TestResolveWeights_WeightedMeanSugar(t *testing.T) {
	wm := func(params string, w types.SlotWeight) *types.Request {
		a := &types.Aggregation{Type: types.AGG_WEIGHTED_MEAN, Field: "x", Weight: w}
		if params != "" {
			a.Params = []byte(params)
		}
		return &types.Request{Aggregations: []*types.Aggregation{a}}
	}
	got, err := ResolveWeights(wm(`{"weight_field":"wi"}`, types.SlotWeight{}), weightSchema(), &types.WeightSpec{Field: "w"}, nil)
	want := []descriptor.ResolvedWeight{{Slot: "aggregations[0]", Operator: "AGG_WEIGHTED_MEAN", Field: "wi", Kind: "probability", Status: "applied", Source: "slot"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("sugar: %+v %v", got, err)
	}
	got, err = ResolveWeights(wm(`{"weight_field":"wi"}`, types.SlotWeightOf(types.WeightSpec{Field: "wi", Kind: "frequency"})), weightSchema(), nil, nil)
	if err != nil || got[0].Kind != "frequency" {
		t.Fatalf("agreeing slot weight: %+v %v", got, err)
	}
	// Without the sugar the slot inherits.
	r := wm("", types.SlotWeight{})
	r.Weight = &types.WeightSpec{Field: "w"}
	if got, err = ResolveWeights(r, weightSchema(), nil, nil); err != nil || got[0].Source != "request" {
		t.Fatalf("inherit: %+v %v", got, err)
	}
	for name, req := range map[string]*types.Request{
		"conflict field":    wm(`{"weight_field":"wi"}`, types.SlotWeightField("w")),
		"conflict null":     wm(`{"weight_field":"wi"}`, types.NullSlotWeight()),
		"nothing":           wm("", types.SlotWeight{}),
		"opted out":         wm("", types.NullSlotWeight()),
		"sugar categorical": wm(`{"weight_field":"cat"}`, types.SlotWeight{}),
	} {
		_, err := ResolveWeights(req, weightSchema(), nil, nil)
		if ce := codeOf(t, err); ce.Code != errors.PROCESSING_CONFIG || ce.Details["slot"] == nil {
			t.Fatalf("%s: %s %v", name, ce.Code, ce.Details)
		}
	}
}

// TestResolveWeights_DecimalRefused: a weight-aware slot over a
// decimal128 value field (its Field, or AGG_RATIO's numerator /
// denominator) is PULSE_WEIGHT_UNSUPPORTED under ANY weight in force —
// slot, request and the instance default alike — because the decimal
// path has no weighted form; `weight: null` opts the slot out, and a
// not-weightable operator under the default is skipped as on any type.
func TestResolveWeights_DecimalRefused(t *testing.T) {
	def := &types.WeightSpec{Field: "w"}
	ratio := func(num, den string) *types.Aggregation {
		return &types.Aggregation{Type: types.AGG_RATIO, Field: "x",
			Params: []byte(`{"numerator_field":"` + num + `","denominator_field":"` + den + `"}`)}
	}
	refused := []struct {
		name       string
		req        *types.Request
		def        *types.WeightSpec
		operator   string
		valueField string
	}{
		{"sum default", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "dec"}}}, def, "AGG_SUM", "dec"},
		{"count request", &types.Request{Weight: def, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "dec"}}}, nil, "AGG_COUNT", "dec"},
		{"average slot", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "dec", Weight: types.SlotWeightField("w")}}}, nil, "AGG_AVERAGE", "dec"},
		{"ratio numerator", &types.Request{Weight: def, Aggregations: []*types.Aggregation{ratio("dec", "x")}}, nil, "AGG_RATIO", "dec"},
		{"ratio denominator", &types.Request{Aggregations: []*types.Aggregation{ratio("x", "dec")}}, def, "AGG_RATIO", "dec"},
		{"crosstab cell", &types.Request{Crosstab: &types.CrosstabSpec{Cell: &types.Aggregation{Type: types.AGG_SUM, Field: "dec"}}}, def, "AGG_SUM", "dec"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveWeights(tc.req, weightSchema(), tc.def, nil)
			ce := codeOf(t, err)
			if ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED || ce.Details["operator"] != tc.operator ||
				ce.Details["value_field"] != tc.valueField || ce.Details["type"] != "decimal128" || ce.Details["field"] != "w" {
				t.Fatalf("got %s %v", ce.Code, ce.Details)
			}
		})
	}
	accepted := map[string]*types.Request{
		// Opted out: the decimal path runs unweighted, by request.
		"null opt-out": {Weight: def, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "dec", Weight: types.NullSlotWeight()}}},
		// AGG_RATIO ignores its Field: a decimal there is not weighted.
		"ratio ignored field": {Weight: def, Aggregations: []*types.Aggregation{{Type: types.AGG_RATIO, Field: "dec",
			Params: []byte(`{"numerator_field":"x","denominator_field":"x"}`)}}},
		// No weight anywhere: the unweighted decimal path is untouched.
		"unweighted": {Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "dec"}}},
	}
	for name, req := range accepted {
		if _, err := ResolveWeights(req, weightSchema(), nil, nil); err != nil {
			t.Fatalf("%s refused: %v", name, err)
		}
	}
	got, err := ResolveWeights(&types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MIN, Field: "dec"}}}, weightSchema(), def, nil)
	if err != nil || got[0].Status != descriptor.WeightStatusSkippedNotWeightAware {
		t.Fatalf("not-weightable decimal slot under the default: %+v %v", got, err)
	}
	// A nil schema (schema-less validation) judges no field type.
	if _, err := ResolveWeights(refused[0].req, nil, def, nil); err != nil {
		t.Fatalf("nil schema refused: %v", err)
	}
}

// inferentialSlotRequests builds, per refused non-aggregator operator,
// a one-slot request carrying slot weight w — every built-in TEST_*
// (as a test and, for TEST_T, a post-test), every REG_*, the four
// reference-distribution attributes and GROUP_QUANTILE (as a grouper
// and on both crosstab axes). The key is the slot path.
func inferentialSlotRequests(w types.SlotWeight) map[string]*types.Request {
	out := map[string]*types.Request{}
	for _, tt := range types.AllTestTypes() {
		out["tests[0]/"+string(tt)] = &types.Request{Tests: []*types.Test{{Type: tt, Field: "x", Weight: w}}}
	}
	out["post_tests[0]/TEST_T"] = &types.Request{PostTests: []*types.Test{{Type: types.TEST_T, Field: "x", Weight: w}}}
	for _, rt := range types.AllRegressionTypes() {
		out["regressions[0]/"+string(rt)] = &types.Request{Regressions: []*types.RegressionSpec{{Type: rt, Target: "x", Weight: w}}}
	}
	for _, at := range []types.AttributeType{types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_PERCENTILE, types.ATTR_NORMALIZED} {
		out["attributes[0]/"+string(at)] = &types.Request{Attributes: []*types.Attribute{{Type: at, Field: "x", Weight: w}}}
	}
	q := func() *types.Group {
		return &types.Group{Type: types.GROUP_QUANTILE, Field: "x", Interval: 4, Weight: w}
	}
	cat := func() *types.Group { return &types.Group{Type: types.GROUP_CATEGORY, Field: "cat"} }
	out["groups[0]/GROUP_QUANTILE"] = &types.Request{Groups: []*types.Group{q()}}
	out["crosstab.rows[0]/GROUP_QUANTILE"] = &types.Request{Crosstab: &types.CrosstabSpec{
		Rows: []*types.Group{q()}, Columns: []*types.Group{cat()}, Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Weight: types.NullSlotWeight()}}}
	out["crosstab.columns[0]/GROUP_QUANTILE"] = &types.Request{Crosstab: &types.CrosstabSpec{
		Rows: []*types.Group{cat()}, Columns: []*types.Group{q()}, Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Weight: types.NullSlotWeight()}}}
	return out
}

// TestResolveWeights_InferentialRefused: every TEST_*, REG_*,
// reference-distribution attribute and GROUP_QUANTILE is
// PULSE_WEIGHT_UNSUPPORTED under ANY weight in force — slot, request
// or the instance default — with details {slot, operator, field}; the
// slot's `weight: null` opts it out on all three.
func TestResolveWeights_InferentialRefused(t *testing.T) {
	def := &types.WeightSpec{Field: "w"}
	sources := map[string]func(map[string]*types.Request) (map[string]*types.Request, *types.WeightSpec){
		"default": func(m map[string]*types.Request) (map[string]*types.Request, *types.WeightSpec) { return m, def },
		"request": func(m map[string]*types.Request) (map[string]*types.Request, *types.WeightSpec) {
			for _, r := range m {
				r.Weight = def
			}
			return m, nil
		},
	}
	run := func(t *testing.T, key string, req *types.Request, defW *types.WeightSpec) {
		t.Helper()
		_, err := ResolveWeights(req, weightSchema(), defW, nil)
		ce := codeOf(t, err)
		slot, op := splitKey(key)
		want := map[string]any{"slot": slot, "operator": op, "field": "w"}
		if ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED || !reflect.DeepEqual(ce.Details, want) {
			t.Fatalf("%s: got %s %v, want PULSE_WEIGHT_UNSUPPORTED %v", key, ce.Code, ce.Details, want)
		}
	}
	for name, src := range sources {
		reqs, defW := src(inferentialSlotRequests(types.SlotWeight{}))
		for key, req := range reqs {
			t.Run(name+"/"+key, func(t *testing.T) { run(t, key, req, defW) })
		}
	}
	for key, req := range inferentialSlotRequests(types.SlotWeightField("w")) {
		t.Run("slot/"+key, func(t *testing.T) { run(t, key, req, nil) })
	}
	for key, req := range inferentialSlotRequests(types.NullSlotWeight()) {
		t.Run("null/"+key, func(t *testing.T) {
			req.Weight = def
			got, err := ResolveWeights(req, weightSchema(), def, nil)
			if err != nil {
				t.Fatalf("opted-out slot refused: %v", err)
			}
			slot, _ := splitKey(key)
			for _, g := range got {
				if g.Slot == slot && g.Status != descriptor.WeightStatusOptedOut {
					t.Fatalf("%s: %+v, want opted_out", slot, g)
				}
			}
		})
	}
}

func splitKey(key string) (slot, op string) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '/' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

// TestResolveWeights_WindowsAndUnaffected: a WIN_* slot (no slot
// weight of its own) is PROCESSING_CONFIG under a request weight and
// skipped under the instance default; filterers, features, row-local
// attributes and the other groupers are untouched — the request runs
// and each reports skipped (filterers and features carry no slot).
func TestResolveWeights_WindowsAndUnaffected(t *testing.T) {
	def := &types.WeightSpec{Field: "w"}
	win := func(reqW *types.WeightSpec) *types.Request {
		return &types.Request{Weight: reqW, Windows: []*types.Window{{Type: types.WIN_LAG, Field: "x"}}}
	}
	_, err := ResolveWeights(win(def), weightSchema(), nil, nil)
	ce := codeOf(t, err)
	if want := (map[string]any{"slot": "windows[0]", "operator": "WIN_LAG", "field": "w"}); ce.Code != errors.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, want) {
		t.Fatalf("explicit window: %s %v", ce.Code, ce.Details)
	}
	got, err := ResolveWeights(win(nil), weightSchema(), def, nil)
	if err != nil || len(got) != 1 || got[0].Slot != "windows[0]" || got[0].Status != descriptor.WeightStatusSkippedNotWeightAware {
		t.Fatalf("default window: %+v %v", got, err)
	}

	unaffected := &types.Request{
		Weight:     def,
		Filterers:  []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "cat", Values: []string{"a"}}},
		Features:   []*types.Feature{{Type: types.FEAT_LOG, Field: "x"}},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "x * 2"}},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "cat"},
			{Type: types.GROUP_RANGE, Field: "x", Interval: 10},
			{Type: types.GROUP_ROUNDED, Field: "x", Interval: 10, Weight: types.SlotWeightField("w")},
		},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}},
	}
	got, err = ResolveWeights(unaffected, weightSchema(), def, nil)
	if err != nil {
		t.Fatalf("unaffected surfaces refused: %v", err)
	}
	status := map[string]string{}
	for _, g := range got {
		status[g.Slot] = g.Status
	}
	want := map[string]string{
		"aggregations[0]": "applied", "attributes[0]": "skipped_not_weight_aware",
		"groups[0]": "skipped_not_weight_aware", "groups[1]": "skipped_not_weight_aware", "groups[2]": "skipped_not_weight_aware",
	}
	if !reflect.DeepEqual(status, want) {
		t.Fatalf("statuses = %v, want %v", status, want)
	}
}

// TestResolveWeights_InferentialOverlays: the overlay refusal is keyed
// off the manifest Inferential flag, never a hand list — every
// Inferential kind but OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z is
// PULSE_WEIGHT_UNSUPPORTED under a weight in force on its own slot
// (request / default / slot); `weight: null` on the overlay opts out.
// A host weighted only by its own slot weight does not refuse an
// overlay nothing weights (the shipped pairwise n_source modes read
// such a host on purpose). Descriptive kinds and the exemption are
// skipped.
func TestResolveWeights_InferentialOverlays(t *testing.T) {
	def := &types.WeightSpec{Field: "w"}
	cell := func(w types.SlotWeight) *types.CrosstabSpec {
		return &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Weight: w},
		}
	}
	var inferential int
	for _, c := range OverlayCapabilities() {
		k := c.Kind
		refuses := c.Inferential && k != types.OverlayKindPairwiseWeightedTwoMeansZ
		if refuses {
			inferential++
		}
		ov := func(w types.SlotWeight) []types.OverlaySpec { return []types.OverlaySpec{{Kind: k, Weight: w}} }
		cases := map[string]struct {
			req  *types.Request
			defW *types.WeightSpec
		}{
			"request": {&types.Request{Weight: def, Crosstab: cell(types.NullSlotWeight()), Overlays: ov(types.SlotWeight{})}, nil},
			"default": {&types.Request{Crosstab: cell(types.NullSlotWeight()), Overlays: ov(types.SlotWeight{})}, def},
			"slot":    {&types.Request{Crosstab: cell(types.SlotWeight{}), Overlays: ov(types.SlotWeightField("w"))}, nil},
		}
		for name, tc := range cases {
			got, err := ResolveWeights(tc.req, weightSchema(), tc.defW, nil)
			if !refuses {
				if err != nil {
					t.Fatalf("%s %s: descriptive kind refused: %v", k, name, err)
				}
				continue
			}
			ce := codeOf(t, err)
			want := map[string]any{"slot": "overlays[0]", "operator": string(k), "field": "w"}
			if ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED || !reflect.DeepEqual(ce.Details, want) {
				t.Fatalf("%s %s: got %s %v (weights %+v), want %v", k, name, ce.Code, ce.Details, got, want)
			}
		}
		for name, req := range map[string]*types.Request{
			"null":           {Weight: def, Crosstab: cell(types.SlotWeight{}), Overlays: ov(types.NullSlotWeight())},
			"host slot only": {Crosstab: cell(types.SlotWeightField("w")), Overlays: ov(types.SlotWeight{})},
		} {
			if _, err := ResolveWeights(req, weightSchema(), nil, nil); err != nil {
				t.Fatalf("%s %s: overlay refused: %v", k, name, err)
			}
		}
	}
	if inferential == 0 {
		t.Fatal("no Inferential overlay kind found — the flag keying is vacuous")
	}
}

// TestComposeOverlayWeightRefusal: a Compose-host Inferential overlay
// (keyed off the flag, sole exemption the weighted-z) is refused when a
// slot it reads has a request or instance default weight applied to an
// aggregation or cell; a slot weighted only by its own slot weight, a
// host opted out with `weight: null`, and a slot the overlay does not
// read leave it alone.
func TestComposeOverlayWeightRefusal(t *testing.T) {
	def := &types.WeightSpec{Field: "w"}
	slot := func(reqW *types.WeightSpec, w types.SlotWeight) *types.Request {
		return &types.Request{Weight: reqW, Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "x", Weight: w}}}
	}
	labels := []string{"a", "b", "c"}
	var checked int
	for _, c := range OverlayCapabilities() {
		k := c.Kind
		refuses := c.Inferential && k != types.OverlayKindPairwiseWeightedTwoMeansZ
		ov := []types.ComposeOverlaySpec{{Kind: k, Reference: "a", Targets: []string{"b"}}}
		run := func(reqs []*types.Request, defW *types.WeightSpec) error {
			return ComposeOverlayWeightRefusal(ov, reqs, labels, defW, nil)
		}
		err := run([]*types.Request{slot(nil, types.SlotWeight{}), slot(def, types.SlotWeight{}), slot(nil, types.SlotWeight{})}, nil)
		if !refuses {
			if err != nil {
				t.Fatalf("%s: non-inferential refused: %v", k, err)
			}
			continue
		}
		checked++
		ce := codeOf(t, err)
		want := map[string]any{"slot": "overlays[0]", "operator": string(k), "field": "w", "host": "requests[1].aggregations[0]"}
		if ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED || !reflect.DeepEqual(ce.Details, want) {
			t.Fatalf("%s request weight: %s %v", k, ce.Code, ce.Details)
		}
		ce = codeOf(t, run([]*types.Request{slot(nil, types.SlotWeight{}), slot(nil, types.SlotWeight{}), slot(nil, types.SlotWeight{})}, def))
		if ce.Details["host"] != "requests[0].aggregations[0]" {
			t.Fatalf("%s default: %v", k, ce.Details)
		}
		for name, reqs := range map[string][]*types.Request{
			"slot weight only": {slot(nil, types.SlotWeightField("w")), slot(nil, types.SlotWeightField("w")), nil},
			"unread slot":      {slot(nil, types.SlotWeight{}), slot(nil, types.SlotWeight{}), slot(def, types.SlotWeight{})},
		} {
			if err := run(reqs, nil); err != nil {
				t.Fatalf("%s %s: refused: %v", k, name, err)
			}
		}
		null := []*types.Request{slot(def, types.NullSlotWeight()), slot(nil, types.NullSlotWeight()), slot(nil, types.SlotWeight{})}
		if err := ComposeOverlayWeightRefusal([]types.ComposeOverlaySpec{{Kind: k, Reference: "a", Targets: []string{"b"}}}, null, labels, def, nil); err != nil {
			t.Fatalf("%s host opted out: %v", k, err)
		}
	}
	if checked == 0 {
		t.Fatal("no Inferential kind checked")
	}
}
