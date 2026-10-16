package processing

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func makeDecimalRecord(t *testing.T, schema *encoding.Schema, field, dec string, scale uint8) *Record {
	t.Helper()
	d, parsedScale, err := encoding.ParseDecimal128(dec)
	if err != nil {
		t.Fatalf("parse %q: %v", dec, err)
	}
	if parsedScale != scale {
		d, err = d.Rescale(parsedScale, scale)
		if err != nil {
			t.Fatalf("rescale %q: %v", dec, err)
		}
	}
	r := NewRecord(schema, map[string]float64{field: d.Float64(scale)})
	r.SetWide(field, d)
	return r
}

func TestAggregateDecimalField_Sum(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 6},
	}}
	records := []*Record{
		makeDecimalRecord(t, schema, "amount", "1.250000", 6),
		makeDecimalRecord(t, schema, "amount", "2.500000", 6),
		makeDecimalRecord(t, schema, "amount", "-0.250000", 6),
	}
	out, err := AggregateDecimalField(types.AGG_SUM, records, "amount", 6)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Value.String(out.Scale); got != "3.500000" {
		t.Errorf("sum = %s, want 3.500000", got)
	}
}

func TestAggregateDecimalField_SumOverflow(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 0},
	}}
	// Two values that each fit in 38 digits but their sum overflows.
	maxVal := "99999999999999999999999999999999999999"
	records := []*Record{
		makeDecimalRecord(t, schema, "amount", maxVal, 0),
		makeDecimalRecord(t, schema, "amount", "1", 0),
	}
	_, err := AggregateDecimalField(types.AGG_SUM, records, "amount", 0)
	if err == nil {
		t.Fatal("expected overflow")
	}
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_DECIMAL_OVERFLOW {
		t.Fatalf("expected PULSE_DECIMAL_OVERFLOW, got %v", err)
	}
}

func TestAggregateDecimalField_Average(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2},
	}}
	records := []*Record{
		makeDecimalRecord(t, schema, "amount", "1.00", 2),
		makeDecimalRecord(t, schema, "amount", "2.00", 2),
		makeDecimalRecord(t, schema, "amount", "3.00", 2),
	}
	out, err := AggregateDecimalField(types.AGG_AVERAGE, records, "amount", 2)
	if err != nil {
		t.Fatal(err)
	}
	// Result scale is max(2, MIN_SCALE=4) = 4.
	if got := out.Value.String(out.Scale); got != "2.0000" {
		t.Errorf("avg = %s, want 2.0000", got)
	}
}

func TestAggregateDecimalField_AverageFallback(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 0},
	}}
	maxVal := "99999999999999999999999999999999999999"
	records := []*Record{
		makeDecimalRecord(t, schema, "amount", maxVal, 0),
		makeDecimalRecord(t, schema, "amount", maxVal, 0),
	}
	out, err := AggregateDecimalField(types.AGG_AVERAGE, records, "amount", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !out.FellBack {
		t.Errorf("expected f64 fallback, got decimal: %v", out)
	}
}

func TestAggregateDecimalField_MinMax(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 4},
	}}
	records := []*Record{
		makeDecimalRecord(t, schema, "amount", "1.5000", 4),
		makeDecimalRecord(t, schema, "amount", "-2.0000", 4),
		makeDecimalRecord(t, schema, "amount", "3.7500", 4),
	}
	mn, err := AggregateDecimalField(types.AGG_MIN, records, "amount", 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := mn.Value.String(mn.Scale); got != "-2.0000" {
		t.Errorf("min = %s, want -2.0000", got)
	}
	mx, err := AggregateDecimalField(types.AGG_MAX, records, "amount", 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := mx.Value.String(mx.Scale); got != "3.7500" {
		t.Errorf("max = %s, want 3.7500", got)
	}
}

func TestAggregateDecimalField_AgainstReference(t *testing.T) {
	// Sum of 1..10000 in decimal128(20, 6).
	// Reference sum = (10000 * 10001) / 2 = 50_005_000.
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "v", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 6},
	}}
	records := make([]*Record, 0, 10000)
	for i := 1; i <= 10000; i++ {
		var m big.Int
		m.SetInt64(int64(i) * 1_000_000)
		d, _ := encoding.NewDecimal128FromBigInt(&m)
		r := NewRecord(schema, map[string]float64{"v": d.Float64(6)})
		r.SetWide("v", d)
		records = append(records, r)
	}
	out, err := AggregateDecimalField(types.AGG_SUM, records, "v", 6)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Value.String(out.Scale); got != "50005000.000000" {
		t.Errorf("sum = %s, want 50005000.000000", got)
	}
}

func TestAggregateDecimalField_VarianceExact(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "v", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2},
	}}
	// Population variance of {1, 2, 3, 4, 5}:
	// mean = 3, deviations 4, 1, 0, 1, 4 -> variance = 10/5 = 2.
	records := []*Record{
		makeDecimalRecord(t, schema, "v", "1.00", 2),
		makeDecimalRecord(t, schema, "v", "2.00", 2),
		makeDecimalRecord(t, schema, "v", "3.00", 2),
		makeDecimalRecord(t, schema, "v", "4.00", 2),
		makeDecimalRecord(t, schema, "v", "5.00", 2),
	}
	out, err := AggregateDecimalField(types.AGG_VARIANCE, records, "v", 2)
	if err != nil {
		t.Fatal(err)
	}
	if out.FellBack {
		t.Errorf("expected decimal variance, got f64 fallback %v", out.Float)
	}
	got := out.Value.String(out.Scale)
	if got != "2.00000000" {
		t.Errorf("variance = %s, want 2.00000000", got)
	}
}

func TestAggregateDecimalField_StddevExact(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "v", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2},
	}}
	// Population stddev of {2, 4, 4, 4, 5, 5, 7, 9} = 2.0 exactly.
	values := []string{"2.00", "4.00", "4.00", "4.00", "5.00", "5.00", "7.00", "9.00"}
	records := make([]*Record, 0, len(values))
	for _, v := range values {
		records = append(records, makeDecimalRecord(t, schema, "v", v, 2))
	}
	out, err := AggregateDecimalField(types.AGG_STDDEV, records, "v", 2)
	if err != nil {
		t.Fatal(err)
	}
	if out.FellBack {
		t.Errorf("expected decimal stddev, got f64 fallback %v", out.Float)
	}
	got := out.Value.String(out.Scale)
	if got != "2.0000" {
		t.Errorf("stddev = %s, want 2.0000", got)
	}
}

func TestProcessor_DecimalSumDispatch(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2},
	}}
	records := []*Record{
		makeDecimalRecord(t, schema, "amount", "10.00", 2),
		makeDecimalRecord(t, schema, "amount", "20.00", 2),
		makeDecimalRecord(t, schema, "amount", "30.00", 2),
	}
	p := NewProcessor(schema)
	resp, err := p.Process(context.Background(), &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "amount", Label: "total"},
		},
	}, NewSliceIterator(records))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("rows = %d, want 1", len(resp.Data))
	}
	res, ok := resp.Data[0]["total"].(decimalAggResult)
	if !ok {
		t.Fatalf("total = %T %v", resp.Data[0]["total"], resp.Data[0]["total"])
	}
	if got := res.Value.String(res.Scale); got != "60.00" {
		t.Errorf("total = %s, want 60.00", got)
	}
}

// TestDecimalAggregation_NoValueIsNull (E4-S5): over a decimal128 column
// with no contributing value, AGG_AVERAGE / MIN / MAX / VARIANCE /
// STDDEV are undefined — NaN in the row (null on the wire), exactly as
// over a float column, never a "0" decimal string — while AGG_SUM,
// AGG_COUNT, AGG_DISTINCT_COUNT and AGG_NULL_COUNT stay the defined
// empty figure — the counts a JSON integer, never a decimal string. Both
// the row path and the crosstab cell path.
func TestDecimalAggregation_NoValueIsNull(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "cat", Type: encoding.FieldTypeU8},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2, Nullable: true},
	}}
	undefined := []types.AggregationType{types.AGG_AVERAGE, types.AGG_MIN, types.AGG_MAX, types.AGG_VARIANCE, types.AGG_STDDEV}
	defined := map[types.AggregationType]string{types.AGG_SUM: `"0.00"`, types.AGG_COUNT: `0`, types.AGG_DISTINCT_COUNT: `0`, types.AGG_NULL_COUNT: `0`}

	var aggs []*types.Aggregation
	for _, op := range undefined {
		aggs = append(aggs, &types.Aggregation{Type: op, Field: "amount", Label: string(op)})
	}
	for op := range defined {
		aggs = append(aggs, &types.Aggregation{Type: op, Field: "amount", Label: string(op)})
	}
	resp, err := NewProcessor(schema).Process(context.Background(), &types.Request{Aggregations: aggs}, NewSliceIterator(nil))
	if err != nil {
		t.Fatal(err)
	}
	row := resp.Data[0]
	wire := map[string]json.RawMessage{}
	b, err := types.MarshalFinite(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	for _, op := range undefined {
		if v, ok := row[string(op)].(float64); !ok || !math.IsNaN(v) {
			t.Errorf("%s over no value = %v (%T), want NaN", op, row[string(op)], row[string(op)])
		}
		if string(wire[string(op)]) != "null" {
			t.Errorf("%s on the wire = %s, want null", op, wire[string(op)])
		}
	}
	for op, want := range defined {
		if string(wire[string(op)]) != want {
			t.Errorf("%s on the wire = %s, want %s", op, wire[string(op)], want)
		}
	}

	// Crosstab: cat 1's rows all carry a null amount.
	d, _, err := encoding.ParseDecimal128("4.50")
	if err != nil {
		t.Fatal(err)
	}
	full := NewRecord(schema, map[string]float64{"cat": 0, "amount": 4.5})
	full.SetWide("amount", d)
	empty := NewRecord(schema, map[string]float64{"cat": 1})
	for _, op := range undefined {
		ct, err := NewProcessor(schema).RunCrosstab(context.Background(), &types.Request{Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:    &types.Aggregation{Type: op, Field: "amount", Label: "c"},
		}}, []*Record{full, empty})
		if err != nil {
			t.Fatalf("%s crosstab: %v", op, err)
		}
		cell := ct.Crosstab.Matrix.Cells[1][1]
		if v, ok := cell.Value.(float64); !cell.Present || !ok || !math.IsNaN(v) {
			t.Errorf("%s cell over no value = %+v, want present NaN", op, cell)
		}
		if _, ok := ct.Crosstab.Matrix.Cells[0][0].Value.(decimalAggResult); !ok {
			t.Errorf("%s cell over a value = %T, want a decimal result", op, ct.Crosstab.Matrix.Cells[0][0].Value)
		}
	}
}
