package processing

import (
	"context"
	"math"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/numrange"
	"github.com/frankbardon/pulse/types"
)

func f64p(v float64) *float64 { return &v }
func strp(v string) *string   { return &v }

// rangeLabelRegistry registers a numeric table "bands" (low [-inf, 10),
// mid [10, 20), a gap, high [30, +inf)), a numeric table "exact" split
// at 2^53 + 4 and at 0.1, and a date table "fy" (FY24 2023-07-01 ..
// 2024-06-30 inclusive, FY25 2024-07-01 .. 2025-06-30).
func rangeLabelRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{RangeTables: map[string]RangeTable{
		"bands": {NumericRanges: []numrange.Spec{
			{Label: "low", End: f64p(10)},
			{Label: "mid", Start: f64p(10), End: f64p(20)},
			{Label: "high", Start: f64p(30)},
		}},
		"exact": {NumericRanges: []numrange.Spec{
			{Label: "tiny", End: f64p(0.1)},
			{Label: "below", Start: f64p(0.1), End: f64p(9007199254740996)},
			{Label: "above", Start: f64p(9007199254740996)},
		}},
		"fy": {Ranges: []DateRangeSpec{
			{Label: "FY24", Start: strp("2023-07-01"), End: strp("2024-06-30")},
			{Label: "FY25", Start: strp("2024-07-01"), End: strp("2025-06-30")},
		}},
	}}
}

// epochDay is s's epoch day.
func epochDay(t *testing.T, s string) float64 {
	t.Helper()
	d, err := encoding.ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return float64(d)
}

// TestRangeLabel_Matching: numeric tables match half-open [start, end)
// (exactly for an integer past 2^53 and a decimal), date tables
// inclusive epoch days; null in is null out; no match is null or the
// unmatched label.
func TestRangeLabel_Matching(t *testing.T) {
	t.Parallel()

	call := newRangeLabeler(rangeLabelRegistry()).call
	dec01, scale, err := encoding.ParseDecimal128("0.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []any
		want any
	}{
		{"open lower", []any{"bands", -1e9}, "low"},
		{"just under end", []any{"bands", 9.999}, "low"},
		{"start inclusive", []any{"bands", 10.0}, "mid"},
		{"end exclusive (gap)", []any{"bands", 20.0}, nil},
		{"gap with unmatched", []any{"bands", 25.0, "other"}, "other"},
		{"open upper", []any{"bands", 1e300}, "high"},
		{"int literal", []any{"bands", 10}, "mid"},
		{"NaN no match", []any{"bands", math.NaN()}, nil},
		{"NaN unmatched", []any{"bands", math.NaN(), "other"}, "other"},
		{"null in", []any{"bands", nil}, nil},
		{"null in, unmatched given", []any{"bands", nil, "other"}, nil},
		{"null unmatched", []any{"bands", 25.0, nil}, nil},
		// 2^53 + 3 rounds to 2^53 + 4 as a float64: only an exact match
		// keeps it below the bound.
		{"u64 past 2^53 exact", []any{"exact", exprenv.U64(9007199254740995)}, "below"},
		{"int64 past 2^53 exact", []any{"exact", int64(9007199254740995)}, "below"},
		{"u64 at bound", []any{"exact", exprenv.U64(9007199254740996)}, "above"},
		// decimal 0.1 is below the float64 0.1 bound (0.1000…0555).
		{"decimal exact", []any{"exact", exprenv.Decimal{Value: dec01, Scale: scale}}, "tiny"},
		{"date start inclusive", []any{"fy", epochDay(t, "2023-07-01")}, "FY24"},
		{"date end inclusive", []any{"fy", epochDay(t, "2024-06-30")}, "FY24"},
		{"date next start", []any{"fy", epochDay(t, "2024-07-01")}, "FY25"},
		{"date fractional floors", []any{"fy", epochDay(t, "2024-06-30") + 0.75}, "FY24"},
		{"date before", []any{"fy", epochDay(t, "2020-01-01"), "none"}, "none"},
		{"date null", []any{"fy", nil}, nil},
	} {
		got, err := call(c.args...)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: range_label%v = %#v, want %#v", c.name, c.args, got, c.want)
		}
	}
}

// TestRangeLabel_Refusals: an unknown table (checked before the value,
// so a null cannot hide it) is PULSE_RANGE_TABLE_UNKNOWN; a value the
// table's kind cannot match is PULSE_RANGE_TABLE_KIND_MISMATCH with
// {operator, table, want, got}; a bad arity or a non-string table is
// PROCESSING_RUNTIME.
func TestRangeLabel_Refusals(t *testing.T) {
	t.Parallel()

	call := newRangeLabeler(rangeLabelRegistry()).call
	for _, c := range []struct {
		name string
		args []any
		code perr.Code
		want map[string]any
	}{
		{"unknown", []any{"nope", 1.0}, perr.PULSE_RANGE_TABLE_UNKNOWN, map[string]any{"operator": "range_label", "table": "nope"}},
		{"unknown, null value", []any{"nope", nil}, perr.PULSE_RANGE_TABLE_UNKNOWN, nil},
		{"string vs numeric", []any{"bands", "12"}, perr.PULSE_RANGE_TABLE_KIND_MISMATCH,
			map[string]any{"operator": "range_label", "table": "bands", "want": "numeric", "got": "string"}},
		{"bool vs numeric", []any{"bands", true}, perr.PULSE_RANGE_TABLE_KIND_MISMATCH, nil},
		{"string vs date", []any{"fy", "2024-01-01"}, perr.PULSE_RANGE_TABLE_KIND_MISMATCH,
			map[string]any{"operator": "range_label", "table": "fy", "want": "date", "got": "string"}},
		{"list vs numeric", []any{"bands", []string{"a"}}, perr.PULSE_RANGE_TABLE_KIND_MISMATCH, nil},
		{"one arg", []any{"bands"}, perr.PROCESSING_RUNTIME, nil},
		{"four args", []any{"bands", 1.0, "a", "b"}, perr.PROCESSING_RUNTIME, nil},
		{"table not string", []any{[]string{"x"}, 1.0}, perr.PROCESSING_RUNTIME, nil},
		{"unmatched not string", []any{"bands", 25.0, 3.0}, perr.PROCESSING_RUNTIME, nil},
	} {
		_, err := call(c.args...)
		ce, ok := err.(*perr.CodedError)
		if !ok || ce.Code != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
			continue
		}
		for k, v := range c.want {
			if ce.Details[k] != v {
				t.Errorf("%s: details[%q] = %v, want %v", c.name, k, ce.Details[k], v)
			}
		}
	}
}

// TestRangeLabel_HiddenCapability: with capability:range_tables hidden
// every table answers unknown (LookupRangeTable's gate), as on an
// instance that registered none.
func TestRangeLabel_HiddenCapability(t *testing.T) {
	t.Parallel()

	reg := rangeLabelRegistry().WithHidden(func(n string) bool { return n == featureRangeTables })
	_, err := newRangeLabeler(reg).call("bands", 1.0)
	if !perr.HasCode(err, perr.PULSE_RANGE_TABLE_UNKNOWN) {
		t.Fatalf("hidden capability: %v, want PULSE_RANGE_TABLE_UNKNOWN", err)
	}
	_, err = newRangeLabeler(nil).call("bands", 1.0)
	if !perr.HasCode(err, perr.PULSE_RANGE_TABLE_UNKNOWN) {
		t.Fatalf("nil registry: %v, want PULSE_RANGE_TABLE_UNKNOWN", err)
	}
}

// TestRangeLabel_NoAllocs: a matched call (numeric and date) and an
// unmatched one returning the caller's label allocate nothing once the
// table is compiled — the label is the table's own boxed string.
//
// serial: testing.AllocsPerRun panics in a parallel test.
func TestRangeLabel_NoAllocs(t *testing.T) {

	call := newRangeLabeler(rangeLabelRegistry()).call
	for name, args := range map[string][]any{
		"numeric hit":       {"bands", 12.5},
		"numeric unmatched": {"bands", 25.0, "other"},
		"date hit":          {"fy", epochDay(t, "2024-01-15")},
	} {
		if _, err := call(args...); err != nil { // compile the table
			t.Fatal(err)
		}
		if n := testing.AllocsPerRun(200, func() { _, _ = call(args...) }); n != 0 && !raceEnabled {
			t.Errorf("%s: %v allocs per call, want 0", name, n)
		}
	}
}

// BenchmarkRangeLabel is one matched numeric call.
func BenchmarkRangeLabel(b *testing.B) {
	call := newRangeLabeler(rangeLabelRegistry()).call
	args := []any{"bands", 12.5}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = call(args...)
	}
}

// TestExprBound_DeclaresRangeLabel: the registry's options declare
// range_label on every registry, nil included — an expression naming it
// compiles and runs with no range table registered (its unknown table is
// the run-time refusal, never an undeclared name).
func TestExprBound_DeclaresRangeLabel(t *testing.T) {
	t.Parallel()

	for name, r := range map[string]*ExtensionRegistry{
		"nil":        nil,
		"empty":      {},
		"registered": rangeLabelRegistry(),
	} {
		opts := append([]expr.Option{expr.Env(map[string]any{"x": 0.0})}, r.ExprOptions()...)
		prog, err := expr.Compile(`range_label("bands", x) == "mid"`, opts...)
		if err != nil {
			t.Errorf("%s: compile: %v", name, err)
			continue
		}
		got, err := expr.Run(prog, map[string]any{"x": 12.0})
		if name == "registered" {
			if err != nil || got != true {
				t.Errorf("%s: %v, %v; want true", name, got, err)
			}
			continue
		}
		if !perr.HasCode(err, perr.PULSE_RANGE_TABLE_UNKNOWN) {
			t.Errorf("%s: %v, want PULSE_RANGE_TABLE_UNKNOWN", name, err)
		}
	}
}

// TestRangeLabel_InFormulaAndFilter: range_label reaches ATTR_FORMULA
// (as lookup()'s banded key) and FILTER_EXPRESSION, where an unmatched
// row's null label compares false — the row drops, no error.
func TestRangeLabel_InFormulaAndFilter(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}}}
	records := func() []*Record {
		return []*Record{
			NewRecord(schema, map[string]float64{"score": 5}),  // low
			NewRecord(schema, map[string]float64{"score": 15}), // mid
			NewRecord(schema, map[string]float64{"score": 25}), // gap: unmatched
			NewRecord(schema, map[string]float64{"score": 35}), // high
		}
	}
	exts := rangeLabelRegistry()
	exts.LookupTables = map[string]LookupTable{"rate": {Rows: map[string]float64{"low": 1, "mid": 2, "high": 3, "other": 100}}}

	proc := NewProcessorWithExtensions(schema, exts)
	resp, err := proc.Process(context.Background(), &types.Request{
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "score",
			Expression: `lookup("rate", range_label("bands", score, "other"))`, Label: "rate"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "rate", Label: "s"}},
	}, NewSliceIterator(records()))
	if err != nil {
		t.Fatalf("formula: %v", err)
	}
	if got := resp.Data[0]["s"].(float64); got != 1+2+100+3 {
		t.Errorf("formula sum = %v, want %v", got, 1+2+100+3)
	}

	proc = NewProcessorWithExtensions(schema, exts)
	resp, err = proc.Process(context.Background(), &types.Request{
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `range_label("bands", score) != "low"`}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
	}, NewSliceIterator(records()))
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	// != on the unmatched row's null is true (null != "low"): 15+25+35.
	if got := resp.Data[0]["s"].(float64); got != 15+25+35 {
		t.Errorf("filter != sum = %v, want %v", got, 15+25+35)
	}

	proc = NewProcessorWithExtensions(schema, exts)
	resp, err = proc.Process(context.Background(), &types.Request{
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `range_label("bands", score) in ["mid", "high"]`}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
	}, NewSliceIterator(records()))
	if err != nil {
		t.Fatalf("filter in: %v", err)
	}
	if got := resp.Data[0]["s"].(float64); got != 15+35 {
		t.Errorf("filter in sum = %v, want %v (the unmatched row drops)", got, 15+35)
	}
}
