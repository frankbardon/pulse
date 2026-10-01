package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// fakeBoostAttribute is a row-local custom attribute that emits
// field_value * 2 for every record. Implements RowLocalAttribute +
// AttributeComputer.
type fakeBoostAttribute struct{}

func (fakeBoostAttribute) Row(r *Record, field string) (float64, error) {
	v, _ := r.NumericValue(field)
	return v * 2, nil
}

func (fakeBoostAttribute) Compute(records []*Record, field string) ([]float64, error) {
	out := make([]float64, len(records))
	for i, r := range records {
		v, err := fakeBoostAttribute{}.Row(r, field)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func fakeBoostFactory(*types.Attribute, *encoding.Schema) (AttributeComputer, error) {
	return fakeBoostAttribute{}, nil
}

// TestExtensions_AttributeRoundTrip_RowLocal asserts that a custom
// row-local attribute is constructed via the overlay, runs over the
// record set, and emits its computed values onto the result rows
// under its label.
func TestExtensions_AttributeRoundTrip_RowLocal(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 10}),
		NewRecord(schema, map[string]float64{"score": 5}),
		NewRecord(schema, map[string]float64{"score": 3}),
	}
	exts := &ExtensionRegistry{
		Attributes: map[types.AttributeType]AttributeFactory{
			"ATTR_ACME_BOOST": fakeBoostFactory,
		},
		Streamable: map[string]bool{
			StreamabilityKey("attribute", "ATTR_ACME_BOOST"): true,
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: "ATTR_ACME_BOOST", Field: "score", Label: "boosted"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "boosted", Label: "boost_sum"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 result row, got %d", len(resp.Data))
	}
	got := resp.Data[0]["boost_sum"].(float64)
	want := (10 + 5 + 3) * 2.0
	if got != want {
		t.Errorf("boost_sum = %v, want %v", got, want)
	}
}

// TestExtensions_AttributeRoundTrip_Buffered exercises the buffered
// attribute path: a custom AttributeComputer (not RowLocalAttribute)
// runs over the full record set before aggregation.
func TestExtensions_AttributeRoundTrip_Buffered(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 1}),
		NewRecord(schema, map[string]float64{"score": 2}),
		NewRecord(schema, map[string]float64{"score": 3}),
	}
	// bufferedAttribute returns the global mean for each record —
	// classic two-pass shape collapsed onto Compute since the test
	// runs in buffered mode.
	bufFactory := AttributeFactory(func(*types.Attribute, *encoding.Schema) (AttributeComputer, error) {
		return bufferedAttribute{}, nil
	})
	exts := &ExtensionRegistry{
		Attributes: map[types.AttributeType]AttributeFactory{
			"ATTR_ACME_GLOBAL_MEAN": bufFactory,
		},
		// Streamable map left without ATTR_ACME_GLOBAL_MEAN — overlay
		// IsStreamable falls through to built-in type method, which
		// returns false for unknown names.
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: "ATTR_ACME_GLOBAL_MEAN", Field: "score", Label: "mean"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MAX, Field: "mean", Label: "mean_max"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	got := resp.Data[0]["mean_max"].(float64)
	if got != 2.0 {
		t.Errorf("mean_max = %v, want 2.0", got)
	}
}

type bufferedAttribute struct{}

func (bufferedAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return nil, nil
	}
	var sum float64
	for _, r := range records {
		v, _ := r.NumericValue(field)
		sum += v
	}
	mean := sum / float64(len(records))
	out := make([]float64, len(records))
	for i := range out {
		out[i] = mean
	}
	return out, nil
}
