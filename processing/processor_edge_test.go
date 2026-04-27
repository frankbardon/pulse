package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestProcessor_EmptyRequest(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10, 20})
	iter := NewSliceIterator(records)

	req := &types.Request{}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}
	if resp.Metadata.TotalRows != 2 {
		t.Errorf("TotalRows = %d, want 2", resp.Metadata.TotalRows)
	}
}

func TestProcessor_UnknownAggregationType(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: "AGG_UNKNOWN", Field: "score"},
		},
	}

	_, err := proc.Process(context.Background(), req, iter)
	if err == nil {
		t.Fatal("expected error for unknown aggregation type")
	}
}

func TestProcessor_UnknownFilterType(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: "FILTER_UNKNOWN", Field: "score"},
		},
	}

	_, err := proc.Process(context.Background(), req, iter)
	if err == nil {
		t.Fatal("expected error for unknown filter type")
	}
}

func TestProcessor_UnknownGroupType(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Groups: []*types.Group{
			{Type: "GROUP_UNKNOWN", Field: "score"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score"},
		},
	}

	_, err := proc.Process(context.Background(), req, iter)
	if err == nil {
		t.Fatal("expected error for unknown group type")
	}
}

func TestProcessor_UnknownAttributeType(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: "ATTR_UNKNOWN", Field: "score"},
		},
	}

	_, err := proc.Process(context.Background(), req, iter)
	if err == nil {
		t.Fatal("expected error for unknown attribute type")
	}
}

func TestProcessor_AggregationWithoutLabel(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10, 20})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score"}, // no label
		},
	}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should use auto-generated label
	if _, ok := resp.Data[0]["AGG_SUM_score"]; !ok {
		t.Errorf("expected auto-generated label AGG_SUM_score, got keys: %v", resp.Data[0])
	}
}

func TestProcessor_AttributeWithoutLabel(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10, 20})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_NORMALIZED, Field: "score"}, // no label
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "ATTR_NORMALIZED_score", Label: "avg_norm"},
		},
	}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatal("response data is empty")
	}
}

func TestProcessor_GroupWithNoAggregations(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "age", []float64{25, 30, 25})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "age"},
		},
	}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 groups, got %d", len(resp.Data))
	}
}

func TestProcessor_FormulaAttributeError(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "score", Expression: ""},
		},
	}

	_, err := proc.Process(context.Background(), req, iter)
	if err == nil {
		t.Fatal("expected error for empty formula expression")
	}
}

func TestProcessor_FilterAllOut(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10, 20, 30})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "score", Values: []string{"100", "200"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Metadata.FilteredRows != 0 {
		t.Errorf("FilteredRows = %d, want 0", resp.Metadata.FilteredRows)
	}
	n := resp.Data[0]["n"].(float64)
	if n != 0 {
		t.Errorf("count = %f, want 0", n)
	}
}
