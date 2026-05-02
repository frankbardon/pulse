package processing

import (
	"context"
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
