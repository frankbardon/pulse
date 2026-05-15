package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestExtensions_ExprFunction_AvailableInFormula asserts that a
// custom expr function registered via ExtensionRegistry.ExprFunctions
// is callable from an ATTR_FORMULA expression.
func TestExtensions_ExprFunction_AvailableInFormula(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 4}),
		NewRecord(schema, map[string]float64{"score": 9}),
	}
	exts := &ExtensionRegistry{
		ExprFunctions: []ExprFunction{
			{
				Name: "doublev",
				Fn:   func(v float64) float64 { return v * 2 },
			},
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "score", Expression: "doublev(score)", Label: "d"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "d", Label: "dsum"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := resp.Data[0]["dsum"].(float64); got != (4+9)*2.0 {
		t.Errorf("dsum = %v, want %v", got, (4+9)*2.0)
	}
}

// TestExtensions_LookupTable_AvailableInFormula asserts that the
// built-in lookup() function resolves registered table entries from
// inside ATTR_FORMULA.
func TestExtensions_LookupTable_AvailableInFormula(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 10}),
	}
	exts := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{
			"adjustments": {
				Rows: map[string]float64{"east": 1.5},
			},
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "score", Expression: `score * lookup("adjustments", "east")`, Label: "adj"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "adj", Label: "s"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := resp.Data[0]["s"].(float64); got != 15.0 {
		t.Errorf("adjusted sum = %v, want 15.0", got)
	}
}

// TestExtensions_LookupTable_AvailableInFilterExpression asserts the
// lookup() function is callable from FILTER_EXPRESSION too.
func TestExtensions_LookupTable_AvailableInFilterExpression(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 1}),
		NewRecord(schema, map[string]float64{"score": 5}),
		NewRecord(schema, map[string]float64{"score": 9}),
	}
	exts := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{
			"thresholds": {Rows: map[string]float64{"hi": 5}},
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_EXPRESSION, Expression: `score > lookup("thresholds", "hi")`},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := resp.Data[0]["n"].(float64); got != 1 {
		t.Errorf("filtered count = %v, want 1", got)
	}
}

// TestExtensions_LookupTable_UnknownReturnsCodedError asserts that
// referencing a table that is not registered surfaces
// PULSE_LOOKUP_TABLE_UNKNOWN at evaluation time.
func TestExtensions_LookupTable_UnknownReturnsCodedError(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{NewRecord(schema, map[string]float64{"score": 1})}
	exts := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{
			"present": {Rows: map[string]float64{"k": 1}},
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "score", Expression: `lookup("missing", "k")`, Label: "x"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x", Label: "s"},
		},
	}
	_, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err == nil {
		t.Fatal("expected error for unknown lookup table")
	}
	if !perr.HasCode(err, perr.PULSE_LOOKUP_TABLE_UNKNOWN) {
		t.Errorf("expected error chain to carry PULSE_LOOKUP_TABLE_UNKNOWN; got %v", err)
	}
}

// TestExtensions_LookupTable_MissReturnsCodedError asserts a missing
// key surfaces PULSE_LOOKUP_MISS at evaluation time.
func TestExtensions_LookupTable_MissReturnsCodedError(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{NewRecord(schema, map[string]float64{"score": 1})}
	exts := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{
			"adjustments": {Rows: map[string]float64{"east": 1}},
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "score", Expression: `lookup("adjustments", "west")`, Label: "x"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x", Label: "s"},
		},
	}
	_, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err == nil {
		t.Fatal("expected error for missing lookup key")
	}
	if !perr.HasCode(err, perr.PULSE_LOOKUP_MISS) {
		t.Errorf("expected error chain to carry PULSE_LOOKUP_MISS; got %v", err)
	}
}

// TestExtensions_LookupTable_FuncBackedResolves asserts the function-
// backed lookup path executes correctly: the embedder's Lookup
// callback is invoked with the joined key list.
func TestExtensions_LookupTable_FuncBackedResolves(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{NewRecord(schema, map[string]float64{"score": 2})}
	callCount := 0
	exts := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{
			"computed": {
				Lookup: func(keys ...string) (float64, bool, error) {
					callCount++
					if len(keys) == 1 && keys[0] == "x" {
						return 11, true, nil
					}
					return 0, false, nil
				},
			},
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "score", Expression: `lookup("computed", "x") + score`, Label: "v"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "v", Label: "vsum"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := resp.Data[0]["vsum"].(float64); got != 13 {
		t.Errorf("vsum = %v, want 13", got)
	}
	if callCount == 0 {
		t.Error("Lookup callback never invoked")
	}
}
