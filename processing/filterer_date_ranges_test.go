package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// dateRangesFilterParamsJSON builds a FILTER_DATE_RANGES Params blob from
// the given inline ranges. Each range is a {label,start,end} map; a nil
// start/end pointer is expressed by omitting the key.
func dateRangesFilterParamsJSON(t *testing.T, ranges []map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"ranges": ranges})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

func makeDateRangesFilter(t *testing.T, params json.RawMessage, field string, schema *encoding.Schema) FilterFunc {
	t.Helper()
	factory, ok := filtererRegistry[types.FILTER_DATE_RANGES]
	if !ok {
		t.Fatalf("no filterer registered for FILTER_DATE_RANGES")
	}
	fn, err := factory().Build(&types.Filterer{
		Type:   types.FILTER_DATE_RANGES,
		Field:  field,
		Params: params,
	}, schema)
	if err != nil {
		t.Fatalf("build filter: %v", err)
	}
	return fn
}

// TestFilter_DateRanges_KeepDrop exercises keep/drop correctness including
// inclusive edges and unbounded (open) ranges via a table.
func TestFilter_DateRanges_KeepDrop(t *testing.T) {
	schema := dateSchema()
	// Two bounded phases plus one open-upper phase.
	params := dateRangesFilterParamsJSON(t, []map[string]any{
		{"label": "launch", "start": "2024-01-01", "end": "2024-03-31"},
		{"label": "growth", "start": "2024-04-01", "end": "2024-09-30"},
		{"label": "steady", "start": "2024-10-01"},
	})
	fn := makeDateRangesFilter(t, params, "enrolled", schema)

	cases := []struct {
		name string
		day  float64
		keep bool
	}{
		{"inside launch", epochDays(2024, 2, 15), true},
		{"launch lower edge inclusive", epochDays(2024, 1, 1), true},
		{"launch upper edge inclusive", epochDays(2024, 3, 31), true},
		{"growth lower edge inclusive", epochDays(2024, 4, 1), true},
		{"inside open-upper steady", epochDays(2025, 6, 1), true},
		{"before every range", epochDays(2023, 12, 31), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := NewRecord(schema, map[string]float64{"enrolled": tc.day})
			keep, err := fn(rec)
			if err != nil {
				t.Fatalf("filter err: %v", err)
			}
			if keep != tc.keep {
				t.Errorf("keep = %v, want %v", keep, tc.keep)
			}
		})
	}
}

// TestFilter_DateRanges_OpenLowerBound verifies an omitted start matches
// everything up to (and including) the end boundary.
func TestFilter_DateRanges_OpenLowerBound(t *testing.T) {
	schema := dateSchema()
	params := dateRangesFilterParamsJSON(t, []map[string]any{
		{"label": "early", "end": "2024-01-01"},
	})
	fn := makeDateRangesFilter(t, params, "enrolled", schema)

	keep, err := fn(NewRecord(schema, map[string]float64{"enrolled": epochDays(1999, 1, 1)}))
	if err != nil {
		t.Fatalf("filter err: %v", err)
	}
	if !keep {
		t.Errorf("open-lower range should keep an ancient date")
	}
	keep, _ = fn(NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 1, 2)}))
	if keep {
		t.Errorf("date past the end bound should be dropped")
	}
}

// TestFilter_DateRanges_NullDropped confirms a null/missing date is dropped.
func TestFilter_DateRanges_NullDropped(t *testing.T) {
	schema := dateSchema()
	params := dateRangesFilterParamsJSON(t, []map[string]any{
		{"label": "launch", "start": "2024-01-01", "end": "2024-03-31"},
	})
	fn := makeDateRangesFilter(t, params, "enrolled", schema)

	rec := NewRecordWithNulls(schema, map[string]float64{"enrolled": epochDays(2024, 2, 1)}, map[string]bool{"enrolled": true})
	keep, err := fn(rec)
	if err != nil {
		t.Fatalf("filter err: %v", err)
	}
	if keep {
		t.Errorf("null date must be dropped")
	}
}

// TestFilter_DateRanges_Validation covers the config/validation error paths.
func TestFilter_DateRanges_Validation(t *testing.T) {
	schema := dateSchema()
	factory := filtererRegistry[types.FILTER_DATE_RANGES]

	cases := []struct {
		name    string
		filter  *types.Filterer
		wantErr errors.Code
	}{
		{
			name:    "missing field",
			filter:  &types.Filterer{Type: types.FILTER_DATE_RANGES, Params: dateRangesFilterParamsJSON(t, []map[string]any{{"label": "a", "start": "2024-01-01"}})},
			wantErr: errors.PROCESSING_CONFIG,
		},
		{
			name:    "non-date field",
			filter:  &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "score", Params: dateRangesFilterParamsJSON(t, []map[string]any{{"label": "a", "start": "2024-01-01"}})},
			wantErr: errors.PROCESSING_CONFIG,
		},
		{
			name:    "no source (neither ranges nor table)",
			filter:  &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "enrolled", Params: dateRangesFilterParamsJSON(t, nil)},
			wantErr: errors.PULSE_RANGE_SOURCE_AMBIGUOUS,
		},
		{
			name: "overlapping ranges",
			filter: &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "enrolled", Params: dateRangesFilterParamsJSON(t, []map[string]any{
				{"label": "a", "start": "2024-01-01", "end": "2024-06-30"},
				{"label": "b", "start": "2024-06-01", "end": "2024-12-31"},
			})},
			wantErr: errors.PULSE_RANGE_OVERLAP,
		},
		{
			name: "duplicate label",
			filter: &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "enrolled", Params: dateRangesFilterParamsJSON(t, []map[string]any{
				{"label": "a", "start": "2024-01-01", "end": "2024-03-31"},
				{"label": "a", "start": "2024-04-01", "end": "2024-06-30"},
			})},
			wantErr: errors.PULSE_RANGE_DUPLICATE_LABEL,
		},
		{
			name: "invalid literal",
			filter: &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "enrolled", Params: dateRangesFilterParamsJSON(t, []map[string]any{
				{"label": "a", "start": "not-a-date"},
			})},
			wantErr: errors.PULSE_RANGE_INVALID,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := factory().Build(tc.filter, schema)
			if err == nil {
				t.Fatalf("expected error %s, got nil", tc.wantErr)
			}
			if code := codeOf(t, err); code != tc.wantErr {
				t.Errorf("error code = %s, want %s", code, tc.wantErr)
			}
		})
	}
}

// TestFilter_DateRanges_Streamable asserts the type is classified row-local
// streamable so facet/process/sample stay single-pass.
func TestFilter_DateRanges_Streamable(t *testing.T) {
	if !types.FILTER_DATE_RANGES.Streamable() {
		t.Errorf("FILTER_DATE_RANGES must be row-local streamable")
	}
}
