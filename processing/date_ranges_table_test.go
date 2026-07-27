package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// fiscalTableRegistry builds an ExtensionRegistry carrying a single
// "fiscal" range table whose spans deliberately mirror phaseRangesParams
// so inline-vs-table equivalence can be asserted byte-for-byte on buckets.
func fiscalTableRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{
		RangeTables: map[string]RangeTable{
			"fiscal": {Ranges: []DateRangeSpec{
				{Label: "launch", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
				{Label: "growth", Start: ptr("2024-04-01"), End: ptr("2024-09-30")},
				{Label: "steady", Start: ptr("2024-10-01")},
			}},
		},
	}
}

func tableParams(t *testing.T, table string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"table": table})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

// TestGrouper_DateRanges_TableSource resolves a named table via the
// ExtensionAware SetExtensions hook and asserts the resulting buckets match
// the inline equivalent exactly.
func TestGrouper_DateRanges_TableSource(t *testing.T) {
	schema := dateSchema()
	exts := fiscalTableRegistry()

	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	g, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: tableParams(t, "fiscal")}, schema)
	if err != nil {
		t.Fatalf("construct table grouper: %v", err)
	}
	ApplyGrouperExtensions(g, exts)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 2, 15), // launch
		epochDays(2024, 3, 31), // launch (inclusive upper edge)
		epochDays(2024, 4, 1),  // growth (inclusive lower edge)
		epochDays(2024, 12, 1), // steady (open upper)
		epochDays(2023, 6, 1),  // unmatched
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("group via table: %v", err)
	}
	want := map[string]int{"launch": 2, "growth": 1, "steady": 1, "unmatched": 1}
	if len(groups) != len(want) {
		t.Fatalf("group count = %d, want %d", len(groups), len(want))
	}
	for label, n := range want {
		if len(groups[label]) != n {
			t.Errorf("bucket %q = %d, want %d", label, len(groups[label]), n)
		}
	}
}

// TestGrouper_DateRanges_InlineVsTableEquivalence proves a table and an
// equivalent inline set produce identical buckets for the same records.
func TestGrouper_DateRanges_InlineVsTableEquivalence(t *testing.T) {
	schema := dateSchema()
	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 2, 15),
		epochDays(2024, 5, 1),
		epochDays(2024, 11, 1),
		epochDays(2023, 1, 1),
	})

	inline := makeDateRangesGrouper(t, phaseRangesParams(t, ""), schema)
	inlineGroups, err := inline.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("inline group: %v", err)
	}

	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	table, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: tableParams(t, "fiscal")}, schema)
	if err != nil {
		t.Fatalf("construct table grouper: %v", err)
	}
	ApplyGrouperExtensions(table, fiscalTableRegistry())
	tableGroups, err := table.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("table group: %v", err)
	}

	if len(inlineGroups) != len(tableGroups) {
		t.Fatalf("bucket set differs: inline=%d table=%d", len(inlineGroups), len(tableGroups))
	}
	for label, recs := range inlineGroups {
		if len(tableGroups[label]) != len(recs) {
			t.Errorf("bucket %q: inline=%d table=%d", label, len(recs), len(tableGroups[label]))
		}
	}
}

// TestGrouper_DateRanges_SourceAmbiguous covers both-present and
// neither-present at construction (no registry needed).
func TestGrouper_DateRanges_SourceAmbiguous(t *testing.T) {
	schema := dateSchema()
	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	cases := map[string]json.RawMessage{
		"both":    json.RawMessage(`{"ranges":[{"label":"a","start":"2024-01-01"}],"table":"fiscal"}`),
		"neither": json.RawMessage(`{}`),
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: params}, schema)
			if got := codeOf(t, err); got != errors.PULSE_RANGE_SOURCE_AMBIGUOUS {
				t.Errorf("code = %v, want PULSE_RANGE_SOURCE_AMBIGUOUS", got)
			}
		})
	}
}

// TestGrouper_DateRanges_UnknownTable asserts an unregistered table name
// surfaces PULSE_RANGE_TABLE_UNKNOWN. The grouper factory succeeds (the
// name is only resolved at SetExtensions); the error surfaces at KeyFor.
func TestGrouper_DateRanges_UnknownTable(t *testing.T) {
	schema := dateSchema()
	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	g, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: tableParams(t, "nope")}, schema)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	ApplyGrouperExtensions(g, fiscalTableRegistry())
	records := makeRecords(schema, "enrolled", []float64{epochDays(2024, 2, 15)})
	_, err = g.Group(records, "enrolled")
	if got := codeOf(t, err); got != errors.PULSE_RANGE_TABLE_UNKNOWN {
		t.Errorf("code = %v, want PULSE_RANGE_TABLE_UNKNOWN", got)
	}
}

// TestGrouper_DateRanges_TableNilRegistry asserts a table grouper whose
// SetExtensions never fired (or fired with a nil registry) fails cleanly
// rather than panicking.
func TestGrouper_DateRanges_TableNilRegistry(t *testing.T) {
	schema := dateSchema()
	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	g, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: tableParams(t, "fiscal")}, schema)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	// SetExtensions with a nil registry ⇒ table unresolvable.
	ApplyGrouperExtensions(g, nil)
	records := makeRecords(schema, "enrolled", []float64{epochDays(2024, 2, 15)})
	if _, err := g.Group(records, "enrolled"); err == nil {
		t.Fatalf("expected error for nil-registry table grouper")
	}
}

// TestFilter_DateRanges_TableSource resolves a named table through the
// filterer's ExtensionAware hook and asserts keep/drop matches the inline
// equivalent.
func TestFilter_DateRanges_TableSource(t *testing.T) {
	schema := dateSchema()
	factory := filtererRegistry[types.FILTER_DATE_RANGES]

	build := func(exts *ExtensionRegistry, params json.RawMessage) FilterFunc {
		builder := factory()
		if aware, ok := builder.(ExtensionAware); ok {
			aware.SetExtensions(exts)
		}
		fn, err := builder.Build(&types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "enrolled", Params: params}, schema)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return fn
	}

	tableFn := build(fiscalTableRegistry(), tableParams(t, "fiscal"))
	inlineFn := build(nil, phaseRangesParams(t, ""))

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 2, 15), // in
		epochDays(2024, 12, 1), // in (steady, open upper)
		epochDays(2023, 1, 1),  // out
	})
	for i, r := range records {
		gotTable, err := tableFn(r)
		if err != nil {
			t.Fatalf("table filter row %d: %v", i, err)
		}
		gotInline, err := inlineFn(r)
		if err != nil {
			t.Fatalf("inline filter row %d: %v", i, err)
		}
		if gotTable != gotInline {
			t.Errorf("row %d: table keep=%v inline keep=%v (must match)", i, gotTable, gotInline)
		}
	}
}

// TestFilter_DateRanges_SourceSelectionErrors covers ambiguous-source and
// unknown-table on the filter (both surface at Build).
func TestFilter_DateRanges_SourceSelectionErrors(t *testing.T) {
	schema := dateSchema()
	factory := filtererRegistry[types.FILTER_DATE_RANGES]

	cases := []struct {
		name   string
		exts   *ExtensionRegistry
		params json.RawMessage
		want   errors.Code
	}{
		{"both", fiscalTableRegistry(), json.RawMessage(`{"ranges":[{"label":"a","start":"2024-01-01"}],"table":"fiscal"}`), errors.PULSE_RANGE_SOURCE_AMBIGUOUS},
		{"neither", fiscalTableRegistry(), json.RawMessage(`{}`), errors.PULSE_RANGE_SOURCE_AMBIGUOUS},
		{"unknown table", fiscalTableRegistry(), tableParams(t, "nope"), errors.PULSE_RANGE_TABLE_UNKNOWN},
		{"table nil registry", nil, tableParams(t, "fiscal"), errors.PULSE_RANGE_TABLE_UNKNOWN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			builder := factory()
			if aware, ok := builder.(ExtensionAware); ok {
				aware.SetExtensions(tc.exts)
			}
			_, err := builder.Build(&types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "enrolled", Params: tc.params}, schema)
			if got := codeOf(t, err); got != tc.want {
				t.Errorf("code = %v, want %v", got, tc.want)
			}
		})
	}
}
