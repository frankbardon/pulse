package processing

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// makeDecimalRecordF64 mirrors makeDecimalRecord but also stamps the f64
// approximation into the values map (which is what feature operators
// read via NumericValue). This matches the runtime path
// ReadRecordWithWide takes when streaming a real .pulse file with a
// decimal field.
func makeDecimalRecordF64(t *testing.T, schema *encoding.Schema, field, dec string, scale uint8) *Record {
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

func TestFeature_LogOnDecimal(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 4, Description: "Amount in USD with 4 decimal places of precision."},
	}}
	records := []*Record{
		makeDecimalRecordF64(t, schema, "amount", "0.0000", 4),
		// e - 1 = ~1.71828; log1p(1.71828) ≈ 1.0
		makeDecimalRecordF64(t, schema, "amount", "1.7183", 4),
	}

	p := NewProcessor(schema)
	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_LOG, Field: "amount", Label: "log_amount"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MAX, Field: "log_amount", Label: "max_log"},
		},
	}
	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resp.Data[0]["max_log"].(float64)
	if !ok {
		t.Fatalf("max_log type %T", resp.Data[0]["max_log"])
	}
	if math.Abs(got-1.0) > 1e-3 {
		t.Errorf("max_log = %f, want ~1.0", got)
	}
}

func TestFeature_SqrtOnDecimal(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2, Description: "Amount cohort decimal field with two-place scale."},
	}}
	records := []*Record{
		makeDecimalRecordF64(t, schema, "amount", "16.00", 2),
		makeDecimalRecordF64(t, schema, "amount", "25.00", 2),
		makeDecimalRecordF64(t, schema, "amount", "100.00", 2),
	}
	p := NewProcessor(schema)
	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_SQRT, Field: "amount", Label: "sqrt_amount"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "sqrt_amount", Label: "total"},
		},
	}
	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resp.Data[0]["total"].(float64)
	if !ok {
		t.Fatalf("total type %T", resp.Data[0]["total"])
	}
	want := 4.0 + 5.0 + 10.0
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("sqrt sum = %f, want %f", got, want)
	}
}

func TestFeature_BucketizeOnDecimal(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2, Description: "Amount field for cohort bucketization tests."},
	}}
	records := []*Record{
		makeDecimalRecordF64(t, schema, "amount", "5.00", 2),   // bucket 0
		makeDecimalRecordF64(t, schema, "amount", "15.00", 2),  // bucket 1
		makeDecimalRecordF64(t, schema, "amount", "25.00", 2),  // bucket 2
		makeDecimalRecordF64(t, schema, "amount", "150.00", 2), // bucket 3
	}
	p := NewProcessor(schema)
	params, _ := json.Marshal(map[string]any{"boundaries": []float64{10, 20, 100}})
	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_BUCKETIZE, Field: "amount", Label: "bucket", Params: params},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "bucket"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "amount"},
		},
	}
	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 4 {
		t.Errorf("expected 4 buckets, got %d: %v", len(resp.Data), resp.Data)
	}
}
