package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// window_formula_types_test.go: a processor driven without the service
// (no field-reference walk ahead of it) still compiles a WIN_FORMULA
// over the record rows with its non-number record columns typed —
// applyWindows hands the window factory the schema and the
// rows-are-records mode on every record-row arm — and over the grouped
// output rows with each built-in group key and aggregation label typed
// as its cell (winformula.OutputColumns).

func formulaTypesSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, l := range []string{"north", "south"} {
		if _, err := dict.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Numeric value carried per record"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Description: "Sales region of the record"},
	}}
}

func formulaTypesRecords(s *encoding.Schema) []*Record {
	return []*Record{
		NewRecord(s, map[string]float64{"x": 1, "region": 0}),
		NewRecord(s, map[string]float64{"x": 2, "region": 1}),
		NewRecord(s, map[string]float64{"x": 3, "region": 0}),
	}
}

func formulaTypesWindow(src string) []*types.Window {
	b, _ := json.Marshal(map[string]string{"expression": src})
	return []*types.Window{{Type: types.WIN_FORMULA, Label: "f", Params: b}}
}

// wantCompileError fails unless err is PULSE_FORMULA_COMPILE_ERROR (the
// typed compile's refusal), not the row's PROCESSING_RUNTIME.
func wantCompileError(t *testing.T, err error) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_FORMULA_COMPILE_ERROR {
		t.Fatalf("err = %v, want PULSE_FORMULA_COMPILE_ERROR", err)
	}
}

// TestWinFormula_RecordRowArmsTypeColumns: arithmetic on a categorical
// label is refused at compile on the windows-only run, the windows-first
// pass and Materialize's record rows, and over the grouped output rows
// (a group key there); a label comparison still runs.
func TestWinFormula_RecordRowArmsTypeColumns(t *testing.T) {
	t.Parallel()
	s := formulaTypesSchema(t)
	ctx := context.Background()

	_, err := NewProcessor(s).Process(ctx, &types.Request{Windows: formulaTypesWindow("region * 2")}, NewSliceIterator(formulaTypesRecords(s)))
	wantCompileError(t, err)

	_, err = NewProcessor(s).Process(ctx, &types.Request{
		Windows:      formulaTypesWindow("region * 2"),
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "sum_f"}},
	}, NewSliceIterator(formulaTypesRecords(s)))
	wantCompileError(t, err)

	err = NewProcessor(s).DeriveRecords(ctx, &types.Request{Windows: formulaTypesWindow("region * 2")},
		NewSliceIterator(formulaTypesRecords(s)), []DerivedColumn{{Name: "f", Window: true}}, func(int64, []any) error { return nil })
	wantCompileError(t, err)

	// Grouped output rows: the same column is a group key there, typed
	// as its label string (winformula.OutputColumns), so the arithmetic
	// is refused at compile too.
	_, err = NewProcessor(s).Process(ctx, &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		Windows:      formulaTypesWindow("region * 2"),
	}, NewSliceIterator(formulaTypesRecords(s)))
	wantCompileError(t, err)

	resp, err := NewProcessor(s).Process(ctx, &types.Request{Windows: formulaTypesWindow(`region == "north" ? x : 0`)}, NewSliceIterator(formulaTypesRecords(s)))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range resp.Data {
		want := 0.0
		if row["region"] == "north" {
			want = row["x"].(float64)
		}
		if row["f"] != want {
			t.Errorf("row %v: f = %v, want %v", row, row["f"], want)
		}
	}
}
