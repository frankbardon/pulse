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
