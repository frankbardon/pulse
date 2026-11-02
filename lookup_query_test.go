package pulse

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// lookupQueryPulse registers three lookup tables: a keyed, complete
// Rows grid (one inline axis, one range-table axis), a free-form Rows
// table and a function-driven Lookup table.
func lookupQueryPulse(t *testing.T, profile []string) *Pulse {
	t.Helper()
	opts := Options{
		FS: afero.NewMemMapFs(),
		Extensions: Extensions{
			RangeTables: map[string]RangeTable{"age_bands": {NumericRanges: []NumericRangeSpec{
				{Label: "young", End: rlF64(30)}, {Label: "old", Start: rlF64(30)},
			}}},
			LookupTables: map[string]LookupTable{
				"rates": {
					Description: "premium rate grid",
					Rows: map[string]float64{
						"north|young": 1.25, "north|old": 2.5,
						"south|young": 3.75, "south|old": 4.5,
					},
					Keys: []LookupAxis{
						{Name: "region", Values: []string{"north", "south"}},
						{Name: "band", RangeTable: "age_bands"},
					},
					Complete: true,
				},
				"adhoc": {Rows: map[string]float64{"x": 9.5}},
				"live": {
					Description: "live rates",
					Lookup:      func(...string) (float64, bool, error) { return 7.25, true, nil },
				},
			},
		},
	}
	if profile != nil {
		opts.FeatureProfile = &FeatureProfile{Features: profile}
	}
	p, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// TestLookupTables_ListsMetadataNotCells pins the discovery surface:
// name order, description, axes, complete, cell_count for a Rows table
// and none for a Lookup table — and never a cell value.
func TestLookupTables_ListsMetadataNotCells(t *testing.T) {
	t.Parallel()

	got := lookupQueryPulse(t, nil).LookupTables()
	if len(got) != 3 {
		t.Fatalf("LookupTables() = %+v; want 3 tables", got)
	}
	if got[0].Name != "adhoc" || got[1].Name != "live" || got[2].Name != "rates" {
		t.Fatalf("order = %q,%q,%q; want adhoc,live,rates", got[0].Name, got[1].Name, got[2].Name)
	}

	adhoc, live, rates := got[0], got[1], got[2]
	if !adhoc.HasRowsData || adhoc.CellCount == nil || *adhoc.CellCount != 1 {
		t.Errorf("adhoc = %+v; want rows data, cell_count 1", adhoc)
	}
	if adhoc.Keys == nil || len(adhoc.Keys) != 0 || adhoc.Complete {
		t.Errorf("adhoc keys = %#v complete = %v; want empty non-nil keys, not complete", adhoc.Keys, adhoc.Complete)
	}
	if live.HasRowsData || live.CellCount != nil || live.Description != "live rates" {
		t.Errorf("live = %+v; want no rows data, no cell_count", live)
	}
	if rates.Description != "premium rate grid" || !rates.HasRowsData || !rates.Complete {
		t.Errorf("rates = %+v", rates)
	}
	if rates.CellCount == nil || *rates.CellCount != 4 {
		t.Errorf("rates cell_count = %v; want 4", rates.CellCount)
	}
	if len(rates.Keys) != 2 ||
		rates.Keys[0].Name != "region" || strings.Join(rates.Keys[0].Values, ",") != "north,south" || rates.Keys[0].RangeTable != "" ||
		rates.Keys[1].Name != "band" || rates.Keys[1].RangeTable != "age_bands" || rates.Keys[1].Values != nil {
		t.Errorf("rates keys = %+v", rates.Keys)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(raw)
	for _, cell := range []string{"1.25", "2.5", "3.75", "4.5", "9.5", "7.25", "north|young", `"rows"`} {
		if strings.Contains(wire, cell) {
			t.Errorf("wire carries cell data %q: %s", cell, wire)
		}
	}
	if !strings.Contains(wire, `"name":"live","description":"live rates","has_rows_data":false,"keys":[],"complete":false}`) {
		t.Errorf("live wire = %s; want cell_count absent, keys [] and complete false", wire)
	}
	if !strings.Contains(wire, `"cell_count":4`) {
		t.Errorf("rates wire lacks cell_count 4: %s", wire)
	}
}

// TestLookupTables_EmptyAndHidden: no table, or capability:lookup_tables
// hidden, lists nothing.
func TestLookupTables_EmptyAndHidden(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := p.LookupTables(); got != nil {
		t.Errorf("empty instance LookupTables() = %+v; want nil", got)
	}
	if got := lookupQueryPulse(t, []string{"capability:range_tables"}).LookupTables(); got != nil {
		t.Errorf("hidden LookupTables() = %+v; want nil", got)
	}
	if got := lookupQueryPulse(t, []string{"capability:range_tables", "capability:lookup_tables"}).LookupTables(); len(got) != 3 {
		t.Errorf("enabled LookupTables() = %+v; want 3", got)
	}
}
