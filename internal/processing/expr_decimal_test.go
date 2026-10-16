package processing

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Decimal formulas (U45 #314, option C): a decimal128 field binds as a
// {value, scale} decimal and its operators are exact; ATTR_FORMULA
// downcasts a decimal result to its float64 column, FILTER_EXPRESSION
// reads the exact verdict, and every env path — the per-input subset,
// a record whose AllValues map is already built, the deferred
// first-record compile, $env, the null-row twin — binds the same value.

func exprDecimalSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 1, Nullable: true},
		{Name: "b", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 1},
		{Name: "price", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2, Nullable: true},
		{Name: "zero", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
}

// exprDecimalRecords: row 0 is a=0.1 b=0.2 price=3.60; row 1 has a and
// price null.
func exprDecimalRecords(s *encoding.Schema) []*Record {
	return []*Record{
		NewRecordWithWide(s, map[string]float64{"x": 0.3}, nil, map[string]any{
			"a": encoding.NewDecimal128FromInt(1), "b": encoding.NewDecimal128FromInt(2),
			"price": encoding.NewDecimal128FromInt(360), "zero": encoding.NewDecimal128FromInt(0),
		}),
		NewRecordWithWide(s, map[string]float64{"x": 0.3}, map[string]bool{"a": true, "price": true}, map[string]any{
			"b": encoding.NewDecimal128FromInt(2), "zero": encoding.NewDecimal128FromInt(0),
		}),
	}
}

func exprDecimalFormula(t *testing.T, s *encoding.Schema, src string) *formulaAttribute {
	t.Helper()
	comp, err := newFormulaAttribute(&types.Attribute{Type: types.ATTR_FORMULA, Expression: src, Label: "v"}, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := BindAttribute(comp, nil, nil); err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return comp.(*formulaAttribute)
}

func TestExprDecimal_FilterAndFormulaExact(t *testing.T) {
	s := exprDecimalSchema()
	// Each env path: a fresh record (the per-input subset env), a record
	// whose AllValues cache is built, and $env (the whole map).
	paths := map[string]func(src string) (string, []*Record){
		"subset": func(src string) (string, []*Record) { return src, exprDecimalRecords(s) },
		"cached": func(src string) (string, []*Record) {
			recs := exprDecimalRecords(s)
			for _, r := range recs {
				r.AllValues()
			}
			return src, recs
		},
		"env": func(src string) (string, []*Record) { return src + ` && $env.b != nil`, exprDecimalRecords(s) },
	}
	for _, tc := range []struct {
		src        string
		full, null bool
	}{
		{`a + b == 0.3`, true, false},              // float64 says false
		{`a + b == x`, true, false},                // a float joins as its shortest spelling, 0.3
		{`price * 1.1 == 3.96`, true, false},       // float64: 3.9600000000000004
		{`price / 3 == 1.2`, true, false},          //
		{`(price ?? 0) * 2 == 0`, false, true},     // the twin lifts the default
		{`price == nil`, false, true},              //
		{`(a ?? 0.1) + b == 0.3`, true, true},      //
		{`float(a + b) == 0.3`, true, false},       // the exact sum, then float64
		{`float(price) * 1.1 > 3.96`, true, false}, // float() opts out of exactness
	} {
		for name, path := range paths {
			src, recs := path(tc.src)
			fns, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: src}}, s, nil)
			if err != nil {
				t.Fatalf("%s %q: %v", name, src, err)
			}
			for i, want := range []bool{tc.full, tc.null} {
				got, err := fns[0](recs[i])
				if err != nil || got != want {
					t.Errorf("%s %q row %d: got %v, %v; want %v", name, src, i, got, err, want)
				}
			}
		}
	}

	for _, tc := range []struct {
		src  string
		want float64
	}{
		{`a + b`, 0.3},
		{`price`, 3.6},
		{`price * 1.1`, 3.96},
		{`price / 7`, 0.5143}, // scale max(2, 0, 4), banker's rounding
		{`price > 3.5`, 1},
		{`float(price) * 2`, 7.2},
	} {
		for name, path := range paths {
			if name == "env" {
				continue
			}
			_, recs := path(tc.src)
			got, err := exprDecimalFormula(t, s, tc.src).Row(recs[0], "")
			if err != nil || got != tc.want {
				t.Errorf("%s formula %q = %v, %v; want %v", name, tc.src, got, err, tc.want)
			}
		}
	}
}

// TestExprDecimal_BuiltinsBothRows: the decimal built-ins, a ternary and
// an untyped `??` run on a full row (the typed program) and on a row
// whose price is null (the null-row twin) — the twin lifts the default
// exactly, so (price ?? x) * 3 is 0.9 there, not float64's
// 0.8999999999999999.
func TestExprDecimal_BuiltinsBothRows(t *testing.T) {
	s := exprDecimalSchema()
	for _, tc := range []struct {
		src        string
		full, null float64
	}{
		{`(price ?? x) * 3`, 10.8, 0.9},
		{`abs(price ?? -1.5)`, 3.6, 1.5},
		{`max(price ?? 0, b)`, 3.6, 0.2},
		{`round(price ?? 2.5)`, 4, 2}, // half-even
		{`ceil(price ?? b) + floor(-(price ?? b))`, 0, 0},
		{`(x > 0 ? (price ?? b) : 0) * 3`, 10.8, 0.6},
		{`sum([price ?? 0, b, 0.1])`, 3.9, 0.3},
		{`int(price ?? 7.9)`, 3, 7},
	} {
		recs := exprDecimalRecords(s)
		fa := exprDecimalFormula(t, s, tc.src)
		for i, want := range []float64{tc.full, tc.null} {
			got, err := fa.Row(recs[i], "")
			if err != nil || got != want {
				t.Errorf("%q row %d = %v, %v; want %v", tc.src, i, got, err, want)
			}
		}
	}
}

// TestExprDecimal_DeferredCompile: a formula naming a derived column
// compiles on the first record, which binds the decimal the same way.
func TestExprDecimal_DeferredCompile(t *testing.T) {
	s := exprDecimalSchema()
	fa := exprDecimalFormula(t, s, `price * 1.1 + bonus`)
	r := exprDecimalRecords(s)[0]
	r.injectValue("bonus", 1)
	got, err := fa.Row(r, "")
	if err != nil || got != 4.96 {
		t.Fatalf("deferred formula = %v, %v; want 4.96", got, err)
	}
}

// TestExprDecimal_CodedFailures: a zero divisor and an ill-typed
// operand surface under their own codes.
func TestExprDecimal_CodedFailures(t *testing.T) {
	s := exprDecimalSchema()
	r := exprDecimalRecords(s)[0]
	_, err := exprDecimalFormula(t, s, `price / zero`).Row(r, "")
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_DECIMAL_DIVIDE_BY_ZERO {
		t.Fatalf("price / zero: %v, want PULSE_DECIMAL_DIVIDE_BY_ZERO", err)
	}
	fns, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `price / zero > 1`}}, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fns[0](r); !stderrors.As(err, &ce) || ce.Code != errors.PULSE_DECIMAL_DIVIDE_BY_ZERO {
		t.Fatalf("filter price / zero: %v, want PULSE_DECIMAL_DIVIDE_BY_ZERO", err)
	}
	for _, src := range []string{`price / "x"`, `price % 2`} {
		_, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: src + ` > 1`}}, s, nil)
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_FORMULA_COMPILE_ERROR {
			t.Errorf("%q: %v, want PULSE_FORMULA_COMPILE_ERROR", src, err)
		}
	}
}

// TestExprDecimal_TotalArgument: a decimal total() argument folds as its
// float64 reading.
func TestExprDecimal_TotalArgument(t *testing.T) {
	s := exprDecimalSchema()
	recs := exprDecimalRecords(s)
	req := &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: `b / total(b)`, Label: "v"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
	}
	resp, err := NewProcessor(s).Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["s"]; got != 1.0 {
		t.Fatalf("sum of b / total(b) = %v, want 1", got)
	}
}
