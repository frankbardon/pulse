package processing

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/winformula"
	"github.com/frankbardon/pulse/types"
)

// window_formula_output_types_test.go holds WIN_FORMULA's output-row
// type rule (winformula.GroupKeyType / AggregationCellType, the
// declarations predict's walk and the window factory both compile
// against) to the cell every built-in grouper and aggregator actually
// writes on the grouped / aggregated rows, read as a formula reads it
// (window.FormulaCell: a decimal figure as its float64).

// cellConforms reports whether v, a non-null output cell, is a value of
// declared type decl as the formula compile treats it: the same Go
// type (AGG_SET_FREQUENCY's map included — integer counts unweighted,
// Σw floats weighted, by the slot's weight), or a struct of decl's
// exact layout (AGG_WELFORD's triple, declared by its winformula mirror
// — a member read compiles to a field index).
func cellConforms(decl reflect.Type, v any) bool {
	vt := reflect.TypeOf(v)
	switch {
	case decl == nil || vt == nil:
		return false
	case vt == decl:
		return true
	case decl.Kind() == reflect.Struct:
		return vt.Kind() == reflect.Struct && vt.ConvertibleTo(decl)
	}
	return false
}

// TestWinFormulaOutputCellTypes_MatchRuntime: every built-in grouper's
// key cell and every built-in aggregator's cell — over a numeric or set
// field, over a decimal field where the type has a decimal arm, under a
// weight, and AGG_CONSTANT per params.type family — conforms to the
// type the rule declares; an extension type is undeclared (`any`).
func TestWinFormulaOutputCellTypes_MatchRuntime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("groupers", func(t *testing.T) {
		fixtures := allGroupParityFixtures(t)
		for _, op := range types.AllGroupTypes() {
			fix, ok := fixtures[op]
			if !ok {
				t.Fatalf("no group parity fixture for %s", op)
			}
			grp := &types.Group{Type: op, Field: fix.field, Fields: fix.fields, Interval: fix.interval, Params: fix.params}
			resp, err := NewProcessor(fix.schema).processRecords(ctx, &types.Request{
				Groups:       []*types.Group{grp},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: fix.field, Label: "n"}},
			}, fix.records)
			if err != nil {
				t.Fatalf("%s: %v", op, err)
			}
			key, want := vectors.GroupKeyField(op, grp), winformula.GroupKeyType(op)
			if len(resp.Data) == 0 {
				t.Fatalf("%s wrote no row", op)
			}
			for _, row := range resp.Data {
				v, ok := window.FormulaCell(row, key)
				if !ok || !cellConforms(want, v) {
					t.Errorf("%s key cell %T(%v), the rule declares %v", op, row[key], row[key], want)
				}
			}
		}
	})

	check := func(t *testing.T, name string, schema *encoding.Schema, records []*Record, slot *types.Aggregation) {
		t.Helper()
		row, _, err := NewProcessor(schema).aggregateWithComponents([]*types.Aggregation{slot}, records, false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		label := types.AggregationLabelOf(slot)
		want := winformula.AggregationCellType(slot)
		v, ok := window.FormulaCell(row, label)
		if !ok {
			t.Fatalf("%s wrote a null cell (%v); the probe must write a value", name, row[label])
		}
		if !cellConforms(want, v) {
			t.Errorf("%s writes %T, the rule declares %v", name, v, want)
		}
	}

	t.Run("aggregators", func(t *testing.T) {
		fixtures := allAggParityFixtures(t)
		for _, op := range types.AllAggregationTypes() {
			fix, ok := fixtures[op]
			if !ok {
				t.Fatalf("no aggregation parity fixture for %s", op)
			}
			check(t, string(op), fix.schema, fix.records,
				&types.Aggregation{Type: op, Field: fix.field, Fields: fix.fields, Label: "primary", Params: fix.params})
		}
	})

	t.Run("decimal_field", func(t *testing.T) {
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2},
		}}
		var records []*Record
		for _, d := range []string{"1.25", "2.50", "-0.75", "4.00"} {
			records = append(records, makeDecimalRecord(t, schema, "amount", d, 2))
		}
		n := 0
		for _, op := range types.AllAggregationTypes() {
			if !fieldaccept.HasDecimalArm(string(op)) {
				continue
			}
			n++
			check(t, string(op), schema, records, &types.Aggregation{Type: op, Field: "amount", Label: "d"})
		}
		if n == 0 {
			t.Fatal("no aggregation type has a decimal arm")
		}
	})

	t.Run("weighted", func(t *testing.T) {
		records := wcountRecords(wcountBase(func(i int) float64 { return float64(i%3 + 1) }))
		w := types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: types.WeightKindFrequency})
		for _, slot := range []*types.Aggregation{
			{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "fr", Weight: w},
			{Type: types.AGG_SET_UNION, Field: "tags", Label: "u", Weight: w},
			{Type: types.AGG_WELFORD, Field: "value", Label: "wf", Weight: w},
			{Type: types.AGG_SUM, Field: "value", Label: "s", Weight: w},
			{Type: types.AGG_COUNT, Field: "value", Label: "n", Weight: w},
		} {
			check(t, string(slot.Type), wcountSchema(), records, slot)
		}
	})

	t.Run("constant", func(t *testing.T) {
		fix := allAggParityFixtures(t)[types.AGG_CONSTANT]
		for _, params := range []string{
			`{"type":"f64","value":2.5}`, `{"type":"u64","value":7}`, `{"type":"date","value":"2024-03-01"}`,
			`{"type":"packed_bool","value":true}`, `{"type":"decimal128","value":"12.50"}`,
			`{"type":"categorical_u8","value":"gold"}`, `{"type":"set_u8","value":["a","b"]}`,
			`{"type":"vec_f64","value":[1,2,3]}`,
		} {
			check(t, params, fix.schema, fix.records, &types.Aggregation{Type: types.AGG_CONSTANT, Label: "c", Params: json.RawMessage(params)})
		}
	})

	t.Run("extension_undeclared", func(t *testing.T) {
		if typ := winformula.AggregationCellType(&types.Aggregation{Type: "AGG_ACME_SCORE", Field: "x"}); typ != nil {
			t.Errorf("an extension aggregation declared %v", typ)
		}
		if typ := winformula.GroupKeyType("GROUP_ACME_BAND"); typ != nil {
			t.Errorf("an extension grouper declared %v", typ)
		}
	})
}
