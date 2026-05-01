package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// schemaForWindowTest returns a 2-column schema (ts:f64, x:f64) for
// processor-level window tests.
func schemaForWindowTest(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "ts", Type: encoding.FieldTypeF64, Description: "Timestamp axis for window ordering"},
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Numeric value carried per record"},
		},
	}
}

func recordsForWindowTest(t *testing.T) []*Record {
	t.Helper()
	schema := schemaForWindowTest(t)
	return []*Record{
		NewRecord(schema, map[string]float64{"ts": 1.0, "x": 10.0}),
		NewRecord(schema, map[string]float64{"ts": 2.0, "x": 20.0}),
		NewRecord(schema, map[string]float64{"ts": 3.0, "x": 30.0}),
	}
}

// TestProcessor_NoAggOnlyWindow verifies that windowed requests without
// aggregations or groups produce one row per record with the window column.
func TestProcessor_NoAggOnlyWindow(t *testing.T) {
	schema := schemaForWindowTest(t)
	records := recordsForWindowTest(t)

	p := NewProcessor(schema)
	req := &types.Request{
		Windows: []*types.Window{
			{Type: types.WIN_LAG, Field: "x", Label: "lag", OrderBy: []types.OrderKey{{Field: "ts"}}},
		},
	}

	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if p.LastPath() != PathBuffered {
		t.Errorf("LastPath = %v, want PathBuffered (windows force buffered)", p.LastPath())
	}
	if len(resp.Data) != 3 {
		t.Fatalf("Data rows = %d, want 3", len(resp.Data))
	}
	// rows are ordered by ts asc after the sort: lag for ts=1 is nil; ts=2 → 10; ts=3 → 20.
	for _, r := range resp.Data {
		ts := r["ts"].(float64)
		switch ts {
		case 1.0:
			if r["lag"] != nil {
				t.Errorf("ts=1 lag = %v, want nil", r["lag"])
			}
		case 2.0:
			if r["lag"] != 10.0 {
				t.Errorf("ts=2 lag = %v, want 10.0", r["lag"])
			}
		case 3.0:
			if r["lag"] != 20.0 {
				t.Errorf("ts=3 lag = %v, want 20.0", r["lag"])
			}
		}
	}
}

// TestProcessor_WindowForcesBuffered verifies canStream returns false when
// req.Windows is non-empty.
func TestProcessor_WindowForcesBuffered(t *testing.T) {
	schema := schemaForWindowTest(t)
	records := recordsForWindowTest(t)

	p := NewProcessor(schema)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x", Label: "sum_x"},
		},
		Windows: []*types.Window{
			{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "x"}}},
		},
	}
	if _, err := p.Process(context.Background(), req, NewSliceIterator(records)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if p.LastPath() != PathBuffered {
		t.Errorf("LastPath = %v, want PathBuffered", p.LastPath())
	}
}

// TestProcessor_WindowOnAggregateRow verifies window applies to the single
// aggregate row when no group is present.
func TestProcessor_WindowOnAggregateRow(t *testing.T) {
	schema := schemaForWindowTest(t)
	records := recordsForWindowTest(t)

	p := NewProcessor(schema)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x", Label: "sum_x"},
		},
		Windows: []*types.Window{
			{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "sum_x"}}},
		},
	}
	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("Data rows = %d, want 1", len(resp.Data))
	}
	if resp.Data[0]["rn"] != int64(1) {
		t.Errorf("rn = %v, want 1", resp.Data[0]["rn"])
	}
}

// TestProcessor_UnknownWindowType surfaces PROCESSING_CONFIG.
func TestProcessor_UnknownWindowType(t *testing.T) {
	schema := schemaForWindowTest(t)
	records := recordsForWindowTest(t)

	p := NewProcessor(schema)
	req := &types.Request{
		Windows: []*types.Window{
			{Type: types.WindowType("WIN_BOGUS"), Field: "x", OrderBy: []types.OrderKey{{Field: "ts"}}},
		},
	}
	_, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err == nil {
		t.Fatal("expected error for unknown window type")
	}
}
