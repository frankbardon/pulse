package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func makeGrouper(t *testing.T, groupType types.GroupType, field string, interval float64, schema *encoding.Schema) Grouper {
	t.Helper()
	factory, ok := grouperRegistry[groupType]
	if !ok {
		t.Fatalf("no grouper registered for %s", groupType)
	}
	g, err := factory(&types.Group{Type: groupType, Field: field, Interval: interval}, schema)
	if err != nil {
		t.Fatalf("create grouper: %v", err)
	}
	return g
}

// --- Category Grouper ---

func TestGrouper_Category_NumericField(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "age", 0, schema)

	records := makeRecords(schema, "age", []float64{25, 30, 25, 30, 25})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 2 {
		t.Fatalf("group count = %d, want 2", len(groups))
	}
	if len(groups["25"]) != 3 {
		t.Errorf("group 25 count = %d, want 3", len(groups["25"]))
	}
	if len(groups["30"]) != 2 {
		t.Errorf("group 30 count = %d, want 2", len(groups["30"]))
	}
}

func TestGrouper_Category_CategoricalField(t *testing.T) {
	schema := categoricalSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "brand", 0, schema)

	records := make([]*Record, 5)
	records[0] = NewRecord(schema, map[string]float64{"brand": 0, "score": 10})
	records[1] = NewRecord(schema, map[string]float64{"brand": 1, "score": 20})
	records[2] = NewRecord(schema, map[string]float64{"brand": 0, "score": 30})
	records[3] = NewRecord(schema, map[string]float64{"brand": 2, "score": 40})
	records[4] = NewRecord(schema, map[string]float64{"brand": 1, "score": 50})

	groups, err := g.Group(records, "brand")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Keys should be resolved strings, not IDs
	if len(groups) != 3 {
		t.Fatalf("group count = %d, want 3", len(groups))
	}
	if len(groups["Apple"]) != 2 {
		t.Errorf("Apple group count = %d, want 2", len(groups["Apple"]))
	}
	if len(groups["Samsung"]) != 2 {
		t.Errorf("Samsung group count = %d, want 2", len(groups["Samsung"]))
	}
	if len(groups["Google"]) != 1 {
		t.Errorf("Google group count = %d, want 1", len(groups["Google"]))
	}
}

func TestGrouper_Category_Empty(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "age", 0, schema)

	groups, err := g.Group([]*Record{}, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("group count = %d, want 0", len(groups))
	}
}

func TestGrouper_Category_NullHandling(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "age", 0, schema)

	records := make([]*Record, 3)
	records[0] = NewRecord(schema, map[string]float64{"age": 25})
	records[1] = NewRecordWithNulls(schema, map[string]float64{"age": 0}, map[string]bool{"age": true})
	records[2] = NewRecord(schema, map[string]float64{"age": 25})

	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Null records should go to a null group or be skipped
	if len(groups["25"]) != 2 {
		t.Errorf("group 25 count = %d, want 2", len(groups["25"]))
	}
}

// --- Rounded Grouper ---

func TestGrouper_Rounded_Basic(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "age", 10, schema)

	records := makeRecords(schema, "age", []float64{23, 27, 35, 12, 48})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 23,27 => 20; 35 => 30; 12 => 10; 48 => 40
	if len(groups) != 4 {
		t.Fatalf("group count = %d, want 4, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["20"]) != 2 {
		t.Errorf("group 20 count = %d, want 2", len(groups["20"]))
	}
	if len(groups["30"]) != 1 {
		t.Errorf("group 30 count = %d, want 1", len(groups["30"]))
	}
	if len(groups["10"]) != 1 {
		t.Errorf("group 10 count = %d, want 1", len(groups["10"]))
	}
	if len(groups["40"]) != 1 {
		t.Errorf("group 40 count = %d, want 1", len(groups["40"]))
	}
}

func TestGrouper_Rounded_IntervalOf5(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "score", 5, schema)

	records := makeRecords(schema, "score", []float64{3, 7, 12, 14, 18})
	groups, err := g.Group(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3 => 0; 7 => 5; 12,14 => 10; 18 => 15
	if len(groups) != 4 {
		t.Fatalf("group count = %d, want 4, groups: %v", len(groups), groupKeys(groups))
	}
}

func TestGrouper_Rounded_Empty(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "score", 10, schema)

	groups, err := g.Group([]*Record{}, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("group count = %d, want 0", len(groups))
	}
}

func groupKeys(m map[string][]*Record) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
