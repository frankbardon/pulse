package pulse

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestParseLookupTableFile_Axes: a wrapped file's `keys` (inline values
// and range-table axes) and `complete` reach the LookupTable unchanged.
func TestParseLookupTableFile_Axes(t *testing.T) {
	t.Parallel()

	body := `{
	  "description": "rate grid",
	  "keys": [
	    {"name": "region", "values": ["east", "west"]},
	    {"name": "score", "range_table": "score_bands"}
	  ],
	  "complete": true,
	  "rows": {"east|low": 1.1, "west|high": 0.9}
	}`
	tbl, err := ParseLookupTableFile("grid", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []LookupAxis{
		{Name: "region", Values: []string{"east", "west"}},
		{Name: "score", RangeTable: "score_bands"},
	}
	if !reflect.DeepEqual(tbl.Keys, want) {
		t.Fatalf("Keys = %+v, want %+v", tbl.Keys, want)
	}
	if !tbl.Complete {
		t.Fatal("Complete = false, want true")
	}

	// No keys, no complete: the sparse, undeclared table of E1-S1.
	tbl, err = ParseLookupTableFile("t", []byte(`{"rows": {"a": 1}, "complete": false}`))
	if err != nil {
		t.Fatal(err)
	}
	if tbl.Keys != nil || tbl.Complete {
		t.Fatalf("got Keys %v Complete %v, want none", tbl.Keys, tbl.Complete)
	}
}

// TestParseLookupTableFile_AxisRefusals: every per-file axis fault is
// refused with its own PULSE_LOOKUP_* code at parse time, the table name
// in details.table and the reason (axis-shape faults) or offending key.
func TestParseLookupTableFile_AxisRefusals(t *testing.T) {
	t.Parallel()

	const two = `"keys": [{"name": "r", "values": ["e", "w"]}, {"name": "y", "values": ["1", "2"]}]`
	cases := []struct {
		name, body string
		code       errors.Code
		reason     string
		key        string
	}{
		{"keys not array", `{"keys": {"name": "r"}, "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "keys_not_array", ""},
		{"keys empty", `{"keys": [], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "keys_empty", ""},
		{"axis not object", `{"keys": ["r"], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "axis_not_object", ""},
		{"axis unknown key", `{"keys": [{"name": "r", "value": ["a"]}], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "unknown_key", "value"},
		{"axis duplicate key", `{"keys": [{"name": "r", "name": "s", "values": ["a"]}], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "duplicate_key", "name"},
		{"axis name missing", `{"keys": [{"values": ["a"]}], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "name_empty", ""},
		{"axis name not string", `{"keys": [{"name": 3, "values": ["a"]}], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "name_not_string", ""},
		{"axis name duplicate", `{"keys": [{"name": "r", "values": ["a"]}, {"name": "r", "values": ["b"]}], "rows": {"a|b": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "duplicate_name", ""},
		{"values and range_table", `{"keys": [{"name": "r", "values": ["a"], "range_table": "b"}], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "values_and_range_table", ""},
		{"neither", `{"keys": [{"name": "r"}], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "no_values", ""},
		{"values empty", `{"keys": [{"name": "r", "values": []}], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "values_empty", ""},
		{"values not strings", `{"keys": [{"name": "r", "values": [1, 2]}], "rows": {"1": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "values_not_strings", ""},
		{"values null", `{"keys": [{"name": "r", "values": null}], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "values_not_strings", ""},
		{"value empty", `{"keys": [{"name": "r", "values": ["a", ""]}], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "value_empty", ""},
		{"range_table empty", `{"keys": [{"name": "r", "range_table": ""}], "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "range_table_empty", ""},
		{"separator in value", `{"keys": [{"name": "r", "values": ["a|b", "c"]}], "rows": {"c": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "separator_in_value", ""},
		{"complete not bool", `{"complete": "yes", "rows": {"a": 1}}`, errors.PULSE_LOOKUP_FILE_INVALID, "complete_not_bool", ""},
		{"complete without keys", `{"complete": true, "rows": {"a": 1}}`, errors.PULSE_LOOKUP_AXIS_INVALID, "complete_without_keys", ""},
		{"arity short", `{` + two + `, "rows": {"e|1": 1, "w": 2}}`, errors.PULSE_LOOKUP_KEY_ARITY, "", "w"},
		{"arity long", `{` + two + `, "rows": {"e|1|x": 1}}`, errors.PULSE_LOOKUP_KEY_ARITY, "", "e|1|x"},
		{"off axis first", `{` + two + `, "rows": {"e|1": 1, "n|2": 2}}`, errors.PULSE_LOOKUP_KEY_OFF_AXIS, "", "n|2"},
		{"off axis second", `{` + two + `, "rows": {"e|3": 1}}`, errors.PULSE_LOOKUP_KEY_OFF_AXIS, "", "e|3"},
		{"collision duplicate value", `{"keys": [{"name": "r", "values": ["e", "w", "e"]}], "rows": {"e": 1}}`, errors.PULSE_LOOKUP_KEY_COLLISION, "", "e"},
		{"incomplete", `{` + two + `, "complete": true, "rows": {"e|1": 1, "e|2": 2, "w|2": 4}}`, errors.PULSE_LOOKUP_TABLE_INCOMPLETE, "", "w|1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseLookupTableFile("grid", []byte(tc.body))
			ce := wantLookupCode(t, err, tc.code)
			if ce.Details["table"] != "grid" {
				t.Errorf("details.table = %v, want grid", ce.Details["table"])
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("details.reason = %v, want %s (%v)", ce.Details["reason"], tc.reason, err)
			}
			if tc.key != "" && ce.Details["key"] != tc.key {
				t.Errorf("details.key = %v, want %s (%v)", ce.Details["key"], tc.key, err)
			}
		})
	}
}

// TestParseLookupTableFile_AxesSparseAndComplete: without `complete` a
// declared table may leave cells out (they miss at run time); with
// `complete: true` the full cross product loads. A range-table axis
// defers its membership and completeness checks to pulse.New.
func TestParseLookupTableFile_AxesSparseAndComplete(t *testing.T) {
	t.Parallel()

	const two = `"keys": [{"name": "r", "values": ["e", "w"]}, {"name": "y", "values": ["1", "2"]}]`
	for _, body := range []string{
		`{` + two + `, "rows": {"e|1": 1}}`,
		`{` + two + `, "complete": true, "rows": {"e|1": 1, "e|2": 2, "w|1": 3, "w|2": 4}}`,
		// Range-table axis: "anything" is not judged until New.
		`{"keys": [{"name": "r", "values": ["e"]}, {"name": "b", "range_table": "bands"}], "complete": true, "rows": {"e|anything": 1}}`,
	} {
		if _, err := ParseLookupTableFile("grid", []byte(body)); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
	}
}

func axesRangeTables() map[string]RangeTable {
	lo, mid, hi := 0.0, 600.0, 700.0
	return map[string]RangeTable{
		"bands": {NumericRanges: []NumericRangeSpec{
			{Label: "low", Start: &lo, End: &mid},
			{Label: "high", Start: &mid, End: &hi},
		}},
	}
}

// TestValidateExtensions_LookupAxesAgainstRangeTables: at New a
// range-table axis resolves to the table's labels — membership,
// completeness, an unknown table and a `|` in a label are each refused;
// programmatic tables get the same checks as file-loaded ones.
func TestValidateExtensions_LookupAxesAgainstRangeTables(t *testing.T) {
	t.Parallel()

	axes := []LookupAxis{{Name: "r", Values: []string{"e"}}, {Name: "b", RangeTable: "bands"}}
	pipeLo, pipeHi := 0.0, 1.0
	cases := []struct {
		name   string
		tbl    LookupTable
		ranges map[string]RangeTable
		code   errors.Code
		reason string
		key    string
	}{
		{"ok sparse", LookupTable{Keys: axes, Rows: map[string]float64{"e|low": 1}}, axesRangeTables(), "", "", ""},
		{"ok complete", LookupTable{Keys: axes, Complete: true, Rows: map[string]float64{"e|low": 1, "e|high": 2}}, axesRangeTables(), "", "", ""},
		{"off axis label", LookupTable{Keys: axes, Rows: map[string]float64{"e|mid": 1}}, axesRangeTables(), errors.PULSE_LOOKUP_KEY_OFF_AXIS, "", "e|mid"},
		{"incomplete", LookupTable{Keys: axes, Complete: true, Rows: map[string]float64{"e|low": 1}}, axesRangeTables(), errors.PULSE_LOOKUP_TABLE_INCOMPLETE, "", "e|high"},
		{"unknown range table", LookupTable{Keys: axes, Rows: map[string]float64{"e|low": 1}}, nil, errors.PULSE_LOOKUP_AXIS_INVALID, "range_table_unknown", ""},
		{"separator in range label", LookupTable{Keys: axes, Rows: map[string]float64{"e|x": 1}},
			map[string]RangeTable{"bands": {NumericRanges: []NumericRangeSpec{{Label: "a|b", Start: &pipeLo, End: &pipeHi}}}},
			errors.PULSE_LOOKUP_AXIS_INVALID, "separator_in_range_label", ""},
		{"programmatic arity", LookupTable{Keys: axes, Rows: map[string]float64{"e": 1}}, axesRangeTables(), errors.PULSE_LOOKUP_KEY_ARITY, "", "e"},
		{"programmatic separator", LookupTable{Keys: []LookupAxis{{Name: "r", Values: []string{"a|b"}}}, Rows: map[string]float64{"a": 1}}, nil, errors.PULSE_LOOKUP_AXIS_INVALID, "separator_in_value", ""},
		{"programmatic both", LookupTable{Keys: []LookupAxis{{Name: "r", Values: []string{"a"}, RangeTable: "bands"}}, Rows: map[string]float64{"a": 1}}, axesRangeTables(), errors.PULSE_LOOKUP_AXIS_INVALID, "values_and_range_table", ""},
		{"programmatic complete without keys", LookupTable{Complete: true, Rows: map[string]float64{"a": 1}}, nil, errors.PULSE_LOOKUP_AXIS_INVALID, "complete_without_keys", ""},
		{"lookup func with keys", LookupTable{Keys: []LookupAxis{{Name: "r", Values: []string{"a"}}}, Lookup: func(...string) (float64, bool, error) { return 0, false, nil }}, nil, errors.PULSE_LOOKUP_AXIS_INVALID, "lookup_func_with_keys", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateExtensions(Extensions{
				LookupTables: map[string]LookupTable{"grid": tc.tbl},
				RangeTables:  tc.ranges,
			})
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			ce := wantLookupCode(t, err, tc.code)
			if ce.Details["table"] != "grid" {
				t.Errorf("details.table = %v, want grid", ce.Details["table"])
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("details.reason = %v, want %s (%v)", ce.Details["reason"], tc.reason, err)
			}
			if tc.key != "" && ce.Details["key"] != tc.key {
				t.Errorf("details.key = %v, want %s (%v)", ce.Details["key"], tc.key, err)
			}
		})
	}
}

// TestNew_LookupAxisReferencesDirectoryRangeTable: an axis may name a
// range table loaded from PULSE_RANGE_TABLES_DIR — the cross-check runs
// after every loader — and the manifest lists the axes and `complete`.
// An off-axis key against that directory table fails New.
func TestNew_LookupAxisReferencesDirectoryRangeTable(t *testing.T) {
	t.Parallel()

	rangeDir, lookupDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(rangeDir, "bands.json"),
		[]byte(`[{"label": "low", "start": 0, "end": 600}, {"label": "high", "start": 600, "end": 900}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLookupFile(t, lookupDir, "grid.json", `{
	  "keys": [{"name": "region", "values": ["e", "w"]}, {"name": "score", "range_table": "bands"}],
	  "complete": true,
	  "rows": {"e|low": 1, "e|high": 2, "w|low": 3, "w|high": 4}
	}`)
	p, err := New(Options{DataDir: t.TempDir(), RangeTablesDir: rangeDir, LookupTablesDir: lookupDir})
	if err != nil {
		t.Fatal(err)
	}
	m := p.Manifest(context.Background())
	if len(m.Extensions.LookupTables) != 1 {
		t.Fatalf("manifest lookup tables = %+v", m.Extensions.LookupTables)
	}
	got := m.Extensions.LookupTables[0]
	if !got.Complete || len(got.Keys) != 2 ||
		got.Keys[0].Name != "region" || !reflect.DeepEqual(got.Keys[0].Values, []string{"e", "w"}) || got.Keys[0].RangeTable != "" ||
		got.Keys[1].Name != "score" || got.Keys[1].RangeTable != "bands" || got.Keys[1].Values != nil {
		t.Fatalf("manifest meta = %+v", got)
	}

	writeLookupFile(t, lookupDir, "grid.json", `{
	  "keys": [{"name": "region", "values": ["e", "w"]}, {"name": "score", "range_table": "bands"}],
	  "rows": {"e|medium": 1}
	}`)
	_, err = New(Options{DataDir: t.TempDir(), RangeTablesDir: rangeDir, LookupTablesDir: lookupDir})
	ce := wantLookupCode(t, err, errors.PULSE_LOOKUP_KEY_OFF_AXIS)
	if ce.Details["table"] != "grid" || ce.Details["axis"] != "score" || ce.Details["value"] != "medium" {
		t.Fatalf("details = %v", ce.Details)
	}
}
