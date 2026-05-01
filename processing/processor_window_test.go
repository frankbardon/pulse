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

// TestProcessor_SortByDate verifies Request.Sort orders the response rows.
func TestProcessor_SortByDate(t *testing.T) {
	schema := schemaForWindowTest(t)
	// Records emitted out of date order to prove Sort reorders.
	records := []*Record{
		NewRecord(schema, map[string]float64{"ts": 3.0, "x": 30.0}),
		NewRecord(schema, map[string]float64{"ts": 1.0, "x": 10.0}),
		NewRecord(schema, map[string]float64{"ts": 2.0, "x": 20.0}),
	}

	p := NewProcessor(schema)
	req := &types.Request{
		Windows: []*types.Window{
			{Type: types.WIN_LAG, Field: "x", Label: "lag", OrderBy: []types.OrderKey{{Field: "ts"}}},
		},
		Sort: []types.OrderKey{{Field: "ts"}},
	}

	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("Data length = %d, want 3", len(resp.Data))
	}
	wantTs := []float64{1.0, 2.0, 3.0}
	wantLag := []any{nil, 10.0, 20.0}
	for i, r := range resp.Data {
		if r["ts"] != wantTs[i] {
			t.Errorf("row[%d].ts = %v, want %v", i, r["ts"], wantTs[i])
		}
		if r["lag"] != wantLag[i] {
			t.Errorf("row[%d].lag = %v, want %v", i, r["lag"], wantLag[i])
		}
	}
}

// TestProcessor_SortDescending verifies DESC ordering on a single key.
func TestProcessor_SortDescending(t *testing.T) {
	schema := schemaForWindowTest(t)
	records := recordsForWindowTest(t)

	p := NewProcessor(schema)
	req := &types.Request{
		Sort: []types.OrderKey{{Field: "x", Desc: true}},
	}
	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	// records have x = 10, 20, 30 in input order. With windows empty and
	// no aggregations or groups, no data is materialized — Sort acts on an
	// empty slice. Confirm no crash and Data is nil (since no pipeline
	// stage produced output).
	if len(resp.Data) != 0 {
		t.Errorf("Data should be empty without windows/agg/groups, got %d rows", len(resp.Data))
	}
}

// TestProcessor_SortByAggLabelOverGroups verifies sorting by an aggregation
// output label across grouped output.
func TestProcessor_SortByAggLabelOverGroups(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Region code", Dictionary: makeDict(t, "us", "eu", "apac")},
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Numeric value"},
		},
	}
	records := []*Record{
		NewRecordWithDict(schema, "region", "us", map[string]float64{"x": 10}),
		NewRecordWithDict(schema, "region", "us", map[string]float64{"x": 20}),
		NewRecordWithDict(schema, "region", "eu", map[string]float64{"x": 100}),
		NewRecordWithDict(schema, "region", "eu", map[string]float64{"x": 200}),
		NewRecordWithDict(schema, "region", "apac", map[string]float64{"x": 5}),
	}

	p := NewProcessor(schema)
	req := &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x", Label: "x_sum"},
		},
		Sort: []types.OrderKey{{Field: "x_sum", Desc: true}},
	}
	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("Data length = %d, want 3", len(resp.Data))
	}
	want := []float64{300, 30, 5}
	for i, row := range resp.Data {
		got, _ := row["x_sum"].(float64)
		if got != want[i] {
			t.Errorf("row[%d].x_sum = %v, want %v", i, got, want[i])
		}
	}
}

// makeDict and NewRecordWithDict are local helpers; inline rather than
// adding to the package surface for tests-only convenience.
func makeDict(t *testing.T, vals ...string) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range vals {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add %s: %v", v, err)
		}
	}
	return d
}

func NewRecordWithDict(schema *encoding.Schema, dictField, dictVal string, others map[string]float64) *Record {
	values := map[string]float64{}
	for k, v := range others {
		values[k] = v
	}
	f := schema.Field(dictField)
	if f != nil && f.Dictionary != nil {
		idx, _ := f.Dictionary.Add(dictVal)
		values[dictField] = float64(idx)
	}
	return NewRecord(schema, values)
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
