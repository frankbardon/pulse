package processing

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func mkSchema(names ...string) *encoding.Schema {
	fields := make([]encoding.Field, len(names))
	off := 0
	for i, n := range names {
		fields[i] = encoding.Field{Name: n, Type: encoding.FieldTypeF64, ByteOffset: off, CsvColumnIdx: i}
		off += 8
	}
	return &encoding.Schema{Fields: fields}
}

func sortedFields(s FieldSet) []string {
	out := make([]string, 0, len(s))
	for name := range s {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestNeededFields_AggregationOnly(t *testing.T) {
	schema := mkSchema("a", "b", "c")
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "b"},
		},
	}
	got := NeededFields(req, schema, nil)
	if got.IsWide() {
		t.Fatalf("expected narrow set, got wide")
	}
	want := []string{"b"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_GroupAndFilter(t *testing.T) {
	schema := mkSchema("a", "b", "c", "d")
	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "a", Values: []string{"1"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "b"},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "c"},
		},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"a", "b", "c"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_WindowReferencesPartitionAndOrder(t *testing.T) {
	schema := mkSchema("a", "b", "ts", "cat", "v")
	req := &types.Request{
		Windows: []*types.Window{{
			Type:        types.WIN_MOVING_AVG,
			Field:       "v",
			PartitionBy: []string{"cat"},
			OrderBy:     []types.OrderKey{{Field: "ts"}},
		}},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"cat", "ts", "v"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_AttributeFormulaExtractsIdents(t *testing.T) {
	schema := mkSchema("a", "b", "c", "d")
	req := &types.Request{
		Attributes: []*types.Attribute{{
			Type:       types.ATTR_FORMULA,
			Field:      "a",
			Expression: "a + b * c",
		}},
	}
	got := NeededFields(req, schema, nil)
	if got.IsWide() {
		t.Fatalf("expected narrow set, got wide")
	}
	want := []string{"a", "b", "c"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_FilterExpressionExtractsIdents(t *testing.T) {
	schema := mkSchema("price", "qty", "name", "unused")
	req := &types.Request{
		Filterers: []*types.Filterer{{
			Type:       types.FILTER_EXPRESSION,
			Expression: "price * qty > 100 && name == \"x\"",
		}},
	}
	got := NeededFields(req, schema, nil)
	if got.IsWide() {
		t.Fatalf("expected narrow set, got wide")
	}
	want := []string{"name", "price", "qty"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_MalformedExpressionWidens(t *testing.T) {
	schema := mkSchema("a", "b")
	req := &types.Request{
		Attributes: []*types.Attribute{{
			Type:       types.ATTR_FORMULA,
			Field:      "a",
			Expression: "this is ( not parseable",
		}},
	}
	got := NeededFields(req, schema, nil)
	if !got.IsWide() {
		t.Fatalf("expected wide set on malformed expression, got %v", sortedFields(got))
	}
}

func TestNeededFields_TestFieldsCaptured(t *testing.T) {
	schema := mkSchema("score", "group", "subject", "unused")
	req := &types.Request{
		Tests: []*types.Test{{
			Type:         types.TEST_ANOVA_RM,
			Field:        "score",
			SplitBy:      "group",
			SubjectField: "subject",
		}},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"group", "score", "subject"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_RegressionTargetAndPredictors(t *testing.T) {
	schema := mkSchema("y", "x1", "x2", "x3", "unused")
	req := &types.Request{
		Regressions: []*types.RegressionSpec{{
			Type:       types.REG_OLS,
			Target:     "y",
			Predictors: []string{"x1", "x2"},
		}},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"x1", "x2", "y"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_FeatureStratifyFromParams(t *testing.T) {
	schema := mkSchema("a", "label", "unused")
	req := &types.Request{
		Features: []*types.Feature{{
			Type:   types.FEAT_TRAIN_TEST_SPLIT,
			Params: json.RawMessage(`{"ratios":[0.8,0.2],"seed":1,"stratify":"label"}`),
		}},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"label"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_FeatureTargetEncodeFromParams(t *testing.T) {
	schema := mkSchema("category", "outcome", "unused")
	req := &types.Request{
		Features: []*types.Feature{{
			Type:   types.FEAT_TARGET_ENCODE,
			Field:  "category",
			Params: json.RawMessage(`{"target":"outcome","smoothing":0.5}`),
		}},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"category", "outcome"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_UnknownExtensionWidens(t *testing.T) {
	schema := mkSchema("a", "b")
	ext := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			types.AggregationType("AGG_CUSTOM_X"): nil,
		},
		// No FieldInputs registered for AGG_CUSTOM_X.
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AggregationType("AGG_CUSTOM_X"), Field: "a", Params: json.RawMessage(`{}`)},
		},
	}
	got := NeededFields(req, schema, ext)
	if !got.IsWide() {
		t.Fatalf("expected wide set when extension lacks FieldInputs, got %v", sortedFields(got))
	}
}

func TestNeededFields_ExtensionWithFieldInputs(t *testing.T) {
	schema := mkSchema("a", "b", "c", "d")
	ext := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			types.AggregationType("AGG_CUSTOM_X"): nil,
		},
		FieldInputs: map[string]FieldInputsFunc{
			StreamabilityKey("aggregator", "AGG_CUSTOM_X"): func(json.RawMessage) []string {
				return []string{"b", "c"}
			},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AggregationType("AGG_CUSTOM_X"), Field: "a"},
		},
	}
	got := NeededFields(req, schema, ext)
	if got.IsWide() {
		t.Fatalf("expected narrow set when FieldInputs registered, got wide")
	}
	want := []string{"a", "b", "c"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_UnknownNamesDropped(t *testing.T) {
	schema := mkSchema("a", "b")
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "not_in_schema"},
			{Type: types.AGG_SUM, Field: "b"},
		},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"b"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_CrosstabAxesAndCell(t *testing.T) {
	schema := mkSchema("region", "segment", "value", "unused", "noise")
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		},
	}
	got := NeededFields(req, schema, nil)
	if got.IsWide() {
		t.Fatalf("expected narrow set, got wide")
	}
	want := []string{"region", "segment", "value"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_CrosstabNestedAxes(t *testing.T) {
	schema := mkSchema("r1", "r2", "r3", "c1", "v", "unused")
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "r1"},
				{Type: types.GROUP_CATEGORY, Field: "r2"},
				{Type: types.GROUP_CATEGORY, Field: "r3"},
			},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "c1"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "v"},
		},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"c1", "r1", "r2", "r3", "v"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_CrosstabCellParamsWeightedMean(t *testing.T) {
	schema := mkSchema("region", "segment", "value", "weight", "unused")
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell: &types.Aggregation{
				Type:   types.AGG_WEIGHTED_MEAN,
				Field:  "value",
				Params: json.RawMessage(`{"weight_field":"weight"}`),
			},
		},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"region", "segment", "value", "weight"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_CrosstabCellParamsRatio(t *testing.T) {
	schema := mkSchema("region", "segment", "num", "den", "unused")
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell: &types.Aggregation{
				Type:   types.AGG_RATIO,
				Field:  "num",
				Params: json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`),
			},
		},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"den", "num", "region", "segment"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_LabelBindingField(t *testing.T) {
	schema := mkSchema("region", "value", "unused")
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value"},
		},
		Labels: []*types.LabelBinding{
			{Field: "region", Table: "regions", Mode: types.LabelModeReplace},
		},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"region", "value"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_TopLevelAggregationWeightedMean(t *testing.T) {
	schema := mkSchema("v", "w", "unused")
	req := &types.Request{
		Aggregations: []*types.Aggregation{{
			Type:   types.AGG_WEIGHTED_MEAN,
			Field:  "v",
			Params: json.RawMessage(`{"weight_field":"w"}`),
		}},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"v", "w"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

func TestNeededFields_CrosstabFilterExpressionBailsOut(t *testing.T) {
	schema := mkSchema("region", "segment", "value", "unused")
	req := &types.Request{
		Filterers: []*types.Filterer{{
			Type:       types.FILTER_EXPRESSION,
			Expression: "not parseable (",
		}},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		},
	}
	got := NeededFields(req, schema, nil)
	if !got.IsWide() {
		t.Fatalf("expected wide set on unparseable filter expression, got %v", sortedFields(got))
	}
}

func TestNeededFields_NilInputsWiden(t *testing.T) {
	if !NeededFields(nil, mkSchema("a"), nil).IsWide() {
		t.Errorf("nil request must widen")
	}
	if !NeededFields(&types.Request{}, nil, nil).IsWide() {
		t.Errorf("nil schema must widen")
	}
}

func TestFieldSet_HasAndWiden(t *testing.T) {
	s := NewFieldSet(2)
	s.Add("a")
	if !s.Has("a") {
		t.Error("expected Has(a) = true")
	}
	if s.Has("b") {
		t.Error("expected Has(b) = false before widen")
	}
	s.Widen()
	if !s.Has("anything") {
		t.Error("expected Has = true after Widen")
	}
	if !s.IsWide() {
		t.Error("expected IsWide = true after Widen")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNeededFields_AggregationParamsDistinctBy pins the FIRST of the two
// projection holes E2-S7 closes: params.distinct_by names a schema field
// that lives nowhere on the Aggregation struct, so a projection that does
// not read it never decodes the key half of the (key, value) pair and
// AGG_DISTINCT_SUM folds every record as "missing" — a confident, silent 0.
//
// Deliberately a TOP-LEVEL aggregation with no crosstab at all: this arm
// must stay red when only the distinct_by lookup is reverted and green
// when only the MarginAggregations walk is reverted, so the two halves of
// the fix are independently mutation-proved.
func TestNeededFields_AggregationParamsDistinctBy(t *testing.T) {
	schema := mkSchema("weight", "respondent", "unused")
	req := &types.Request{
		Aggregations: []*types.Aggregation{{
			Type:   types.AGG_DISTINCT_SUM,
			Field:  "weight",
			Params: json.RawMessage(`{"distinct_by":"respondent"}`),
		}},
	}
	got := NeededFields(req, schema, nil)
	if got.IsWide() {
		t.Fatalf("expected narrow set, got wide")
	}
	want := []string{"respondent", "weight"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v (params.distinct_by must be projected)", g, want)
	}
}

// TestNeededFields_CrosstabCellParamsDistinctBy is the same hole reached
// through Crosstab.Cell, which already calls addAggParamFields — so this
// arm too only reddens when the distinct_by lookup itself is missing.
func TestNeededFields_CrosstabCellParamsDistinctBy(t *testing.T) {
	schema := mkSchema("region", "segment", "weight", "respondent", "unused")
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell: &types.Aggregation{
				Type:   types.AGG_DISTINCT_SUM,
				Field:  "weight",
				Params: json.RawMessage(`{"distinct_by":"respondent"}`),
			},
		},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"region", "respondent", "segment", "weight"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

// TestNeededFields_CrosstabMarginAggregations pins the SECOND hole: the
// req.Crosstab block walked Rows, Columns and Cell and stopped, so an
// auxiliary margin aggregation contributed NOTHING to the projection —
// not even its own Field.
//
// The cell deliberately names `respondent`, so `respondent` is projected
// via the cell whether or not distinct_by is read. `weight` is reachable
// ONLY through the MarginAggregations walk, which is what makes this arm
// red for the walk alone and green for the distinct_by lookup alone.
func TestNeededFields_CrosstabMarginAggregations(t *testing.T) {
	schema := mkSchema("brand", "audience", "respondent", "weight", "unused")
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "audience"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "respondent"},
			MarginAggregations: []*types.Aggregation{{
				Type:   types.AGG_DISTINCT_SUM,
				Field:  "weight",
				Params: json.RawMessage(`{"distinct_by":"respondent"}`),
				Label:  "weighted_base",
			}},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
	got := NeededFields(req, schema, nil)
	if got.IsWide() {
		t.Fatalf("expected narrow set, got wide")
	}
	want := []string{"audience", "brand", "respondent", "weight"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v (margin_aggregations must contribute Field and params)", g, want)
	}
}

// TestNeededFields_CrosstabMarginAggregationsNilEntryTolerated mirrors the
// nil-skip every other slot loop performs. Validation refuses a nil entry
// later; the projection must not panic on the way there.
func TestNeededFields_CrosstabMarginAggregationsNilEntryTolerated(t *testing.T) {
	schema := mkSchema("brand", "audience", "respondent", "weight")
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:               []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
			Columns:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "audience"}},
			Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "respondent"},
			MarginAggregations: []*types.Aggregation{nil},
		},
	}
	got := NeededFields(req, schema, nil)
	want := []string{"audience", "brand", "respondent"}
	if g := sortedFields(got); !equalStrings(g, want) {
		t.Errorf("fields = %v, want %v", g, want)
	}
}

// TestNeededFields_CrosstabMarginAggregationUnknownExtensionWidens closes
// the same defect class one level up: an extension aggregator with no
// FieldInputs hook is opaque, and reading it as a margin auxiliary must
// widen exactly as it does in Aggregations and Crosstab.Cell. Without
// this the fused path would run on a projection that provably cannot
// cover what the operator reads.
func TestNeededFields_CrosstabMarginAggregationUnknownExtensionWidens(t *testing.T) {
	schema := mkSchema("brand", "audience", "respondent", "weight")
	ext := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			types.AggregationType("AGG_CUSTOM_AUX"): nil,
		},
		// No FieldInputs registered for AGG_CUSTOM_AUX.
	}
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "audience"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "respondent"},
			MarginAggregations: []*types.Aggregation{
				{Type: types.AggregationType("AGG_CUSTOM_AUX"), Field: "weight"},
			},
		},
	}
	if !NeededFields(req, schema, ext).IsWide() {
		t.Errorf("an opaque extension aggregator in margin_aggregations must widen the projection")
	}
}
