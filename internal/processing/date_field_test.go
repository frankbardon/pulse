package processing

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// datetimeSchema mirrors dateSchema but types the temporal column as
// `datetime` (epoch seconds) instead of `date` (epoch days), so the
// date-family operator suites can run the identical assertions against
// both temporal field types.
func datetimeSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "enrolled", Type: encoding.FieldTypeDateTime},
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
}

// nonTemporalSchema types the operator's Field as a plain categorical
// column — neither `date` nor `datetime` — so the strict operators'
// rejection path stays covered after the widening.
func nonTemporalSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "enrolled", Type: encoding.FieldTypeCategoricalU8},
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
}

// epochSeconds is the datetime analogue of epochDays: the on-wire value
// a `datetime` cell carries for the given UTC instant.
func epochSeconds(year int, month time.Month, day, hour, min, sec int) float64 {
	return float64(time.Date(year, month, day, hour, min, sec, 0, time.UTC).Unix())
}

// TestResolveDateFieldSeconds_Classification pins the shared adapter's
// three outcomes: a `date` column reads as days, a `datetime` column
// reads as seconds, and anything else either errors (strict) or falls
// back to days (non-strict).
func TestResolveDateFieldSeconds_Classification(t *testing.T) {
	tests := []struct {
		name        string
		schema      *encoding.Schema
		strict      bool
		wantSeconds bool
		wantErr     bool
	}{
		{"date strict", dateSchema(), true, false, false},
		{"date lenient", dateSchema(), false, false, false},
		{"datetime strict", datetimeSchema(), true, true, false},
		{"datetime lenient", datetimeSchema(), false, true, false},
		{"non-temporal strict rejects", nonTemporalSchema(), true, false, true},
		{"non-temporal lenient reads as days", nonTemporalSchema(), false, false, false},
		{"nil schema strict", nil, true, false, false},
		{"nil schema lenient", nil, false, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			seconds, err := resolveDateFieldSeconds("OP", "enrolled", tc.schema, tc.strict)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				var ce *errors.CodedError
				if !asCoded(err, &ce) {
					t.Fatalf("error is not coded: %v", err)
				}
				if ce.Code != errors.PROCESSING_CONFIG {
					t.Errorf("code = %s; want PROCESSING_CONFIG", ce.Code)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if seconds != tc.wantSeconds {
				t.Errorf("seconds = %v; want %v", seconds, tc.wantSeconds)
			}
		})
	}
}

// TestResolveDateFieldSeconds_AbsentField covers the probe path: a
// Field naming no schema column resolves without error under either
// posture, so extension probes and Field-less Group slots still build.
func TestResolveDateFieldSeconds_AbsentField(t *testing.T) {
	for _, strict := range []bool{true, false} {
		seconds, err := resolveDateFieldSeconds("OP", "no_such_column", dateSchema(), strict)
		if err != nil {
			t.Fatalf("strict=%v: unexpected error: %v", strict, err)
		}
		if seconds {
			t.Errorf("strict=%v: absent field classified as seconds", strict)
		}
	}
}

// TestEpochDayFromValue pins the per-record conversion: a days-valued
// column passes through untouched, a seconds-valued column truncates.
func TestEpochDayFromValue(t *testing.T) {
	dayValue := epochDays(2024, time.March, 4)
	if got := epochDayFromValue(dayValue, false); got != int64(dayValue) {
		t.Errorf("date value = %d; want %d (pass-through)", got, int64(dayValue))
	}
	for _, clock := range [][3]int{{0, 0, 0}, {12, 30, 0}, {23, 59, 59}} {
		secValue := epochSeconds(2024, time.March, 4, clock[0], clock[1], clock[2])
		if got := epochDayFromValue(secValue, true); got != int64(dayValue) {
			t.Errorf("datetime %v truncates to day %d; want %d", clock, got, int64(dayValue))
		}
	}
}

// TestGrouper_Date_AcceptsDateTime is the acceptance gate for
// GROUP_DATE over a `datetime` column: every calendar component must
// produce the exact same bucket key it produces for the equivalent
// `date` column, because the instant truncates to the same day.
func TestGrouper_Date_AcceptsDateTime(t *testing.T) {
	components := []string{"day", "day_of_week", "week", "month", "quarter", "year"}

	for _, component := range components {
		t.Run(component, func(t *testing.T) {
			dateG := makeDateGrouper(t, component, dateSchema())
			dtG := makeDateGrouper(t, component, datetimeSchema())

			dateRecs := makeRecords(dateSchema(), "enrolled", []float64{
				epochDays(2024, time.March, 4),
				epochDays(2024, time.March, 4),
				epochDays(2024, time.July, 20),
			})
			// Same three calendar days, but with a time of day that
			// must be discarded — including the last second of the day,
			// which must NOT roll into the next one.
			dtRecs := makeRecords(datetimeSchema(), "enrolled", []float64{
				epochSeconds(2024, time.March, 4, 0, 0, 0),
				epochSeconds(2024, time.March, 4, 23, 59, 59),
				epochSeconds(2024, time.July, 20, 12, 34, 56),
			})

			dateGroups, err := dateG.Group(dateRecs, "enrolled")
			if err != nil {
				t.Fatalf("date Group: %v", err)
			}
			dtGroups, err := dtG.Group(dtRecs, "enrolled")
			if err != nil {
				t.Fatalf("datetime Group: %v", err)
			}

			if len(dateGroups) != len(dtGroups) {
				t.Fatalf("bucket count: date=%d datetime=%d (keys %v vs %v)",
					len(dateGroups), len(dtGroups), keysOf(dateGroups), keysOf(dtGroups))
			}
			for key, bucket := range dateGroups {
				dtBucket, ok := dtGroups[key]
				if !ok {
					t.Errorf("datetime grouping missing bucket %q (has %v)", key, keysOf(dtGroups))
					continue
				}
				if len(dtBucket) != len(bucket) {
					t.Errorf("bucket %q: date=%d rows, datetime=%d rows", key, len(bucket), len(dtBucket))
				}
			}
		})
	}
}

// TestGrouper_Date_DateTimeComponentsBoundaries checks that the
// Components() period boundaries — derived from the tracked day value —
// are day-truncated too, not seconds reinterpreted as days.
func TestGrouper_Date_DateTimeComponentsBoundaries(t *testing.T) {
	g := makeDateGrouper(t, "month", datetimeSchema())
	recs := makeRecords(datetimeSchema(), "enrolled", []float64{
		epochSeconds(2024, time.March, 4, 8, 0, 0),
		epochSeconds(2024, time.March, 29, 17, 45, 0),
	})
	if _, err := g.Group(recs, "enrolled"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	mg, ok := g.(MetaGrouper)
	if !ok {
		t.Fatal("date grouper does not implement MetaGrouper")
	}
	comp, err := mg.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	if got := comp["range_start"]; got != "2024-03-01" {
		t.Errorf("range_start = %v; want 2024-03-01", got)
	}
	if got := comp["range_end"]; got != "2024-03-31" {
		t.Errorf("range_end = %v; want 2024-03-31", got)
	}
}

// TestGrouper_DateRanges_AcceptsDateTime asserts a `datetime` column
// matches the same inline range set — compiled from ISO date literals,
// i.e. in epoch days — as the equivalent `date` column.
func TestGrouper_DateRanges_AcceptsDateTime(t *testing.T) {
	g := makeDateRangesGrouper(t, phaseRangesParams(t, ""), datetimeSchema())

	records := makeRecords(datetimeSchema(), "enrolled", []float64{
		epochSeconds(2024, time.February, 15, 6, 0, 0),  // launch
		epochSeconds(2024, time.March, 31, 23, 59, 59),  // launch — inclusive upper edge, must not spill
		epochSeconds(2024, time.April, 1, 0, 0, 0),      // growth — inclusive lower edge
		epochSeconds(2024, time.December, 1, 13, 30, 0), // steady — open upper
		epochSeconds(2023, time.June, 1, 9, 0, 0),       // unmatched
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}

	want := map[string]int{"launch": 2, "growth": 1, "steady": 1, "unmatched": 1}
	if len(groups) != len(want) {
		t.Fatalf("bucket keys = %v; want %v", keysOf(groups), want)
	}
	for label, n := range want {
		if got := len(groups[label]); got != n {
			t.Errorf("bucket %q = %d rows; want %d", label, got, n)
		}
	}
}

// TestGrouper_DateRanges_RejectsNonTemporal is the "as before"
// regression: widening to `datetime` must not have widened to
// everything.
func TestGrouper_DateRanges_RejectsNonTemporal(t *testing.T) {
	factory, ok := grouperRegistry[types.GROUP_DATE_RANGES]
	if !ok {
		t.Fatal("no grouper registered for GROUP_DATE_RANGES")
	}
	_, err := factory(&types.Group{
		Type:   types.GROUP_DATE_RANGES,
		Field:  "enrolled",
		Params: phaseRangesParams(t, ""),
	}, nonTemporalSchema())
	if err == nil {
		t.Fatal("expected PROCESSING_CONFIG for a non-temporal field, got nil")
	}
	var ce *errors.CodedError
	if !asCoded(err, &ce) {
		t.Fatalf("error is not coded: %v", err)
	}
	if ce.Code != errors.PROCESSING_CONFIG {
		t.Errorf("code = %s; want PROCESSING_CONFIG", ce.Code)
	}
}

// TestFilterer_DateRanges_AcceptsDateTime asserts the day-truncated
// keep/drop decision for a `datetime` column, including both inclusive
// edges and the null drop.
func TestFilterer_DateRanges_AcceptsDateTime(t *testing.T) {
	params, err := json.Marshal(map[string]any{
		"ranges": []map[string]any{
			{"label": "q1", "start": "2024-01-01", "end": "2024-03-31"},
		},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	builder := newDateRangesFilterer()
	fn, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_DATE_RANGES,
		Field:  "enrolled",
		Params: params,
	}, datetimeSchema())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	tests := []struct {
		name string
		sec  float64
		keep bool
	}{
		{"inside", epochSeconds(2024, time.February, 15, 11, 0, 0), true},
		{"first second of the first day", epochSeconds(2024, time.January, 1, 0, 0, 0), true},
		{"last second of the last day", epochSeconds(2024, time.March, 31, 23, 59, 59), true},
		{"first second after the range", epochSeconds(2024, time.April, 1, 0, 0, 0), false},
		{"last second before the range", epochSeconds(2023, time.December, 31, 23, 59, 59), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := NewRecord(datetimeSchema(), map[string]float64{"enrolled": tc.sec})
			keep, err := fn(rec)
			if err != nil {
				t.Fatalf("filter: %v", err)
			}
			if keep != tc.keep {
				t.Errorf("keep = %v; want %v", keep, tc.keep)
			}
		})
	}

	t.Run("null dropped", func(t *testing.T) {
		rec := NewRecordWithNulls(datetimeSchema(),
			map[string]float64{"enrolled": epochSeconds(2024, time.February, 15, 11, 0, 0)},
			map[string]bool{"enrolled": true})
		keep, err := fn(rec)
		if err != nil {
			t.Fatalf("filter: %v", err)
		}
		if keep {
			t.Error("null datetime kept; want dropped")
		}
	})
}

// TestFilterer_DateRanges_RejectsNonTemporal is the "as before"
// regression for the filterer's strict posture.
func TestFilterer_DateRanges_RejectsNonTemporal(t *testing.T) {
	params, err := json.Marshal(map[string]any{
		"ranges": []map[string]any{{"label": "q1", "start": "2024-01-01", "end": "2024-03-31"}},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	builder := newDateRangesFilterer()
	_, err = builder.Build(&types.Filterer{
		Type:   types.FILTER_DATE_RANGES,
		Field:  "enrolled",
		Params: params,
	}, nonTemporalSchema())
	if err == nil {
		t.Fatal("expected PROCESSING_CONFIG for a non-temporal field, got nil")
	}
	var ce *errors.CodedError
	if !asCoded(err, &ce) {
		t.Fatalf("error is not coded: %v", err)
	}
	if ce.Code != errors.PROCESSING_CONFIG {
		t.Errorf("code = %s; want PROCESSING_CONFIG", ce.Code)
	}
}

// TestIsIndexKeyableFieldType_DateTime pins the keyable-type policy
// decision for `datetime`: ALLOW, on the same exact-fixed-width-integer
// rationale that admits `date` and `u64`.
func TestIsIndexKeyableFieldType_DateTime(t *testing.T) {
	if !IsIndexKeyableFieldType(encoding.FieldTypeDateTime) {
		t.Error("datetime must be an allowed point-lookup index key type")
	}
}

// TestResolveLookupKeyBytes_DateTime asserts that a datetime lookup
// literal resolves to the exact on-wire bytes the build path derives
// from a decoded record carrying the same instant — the round-trip
// property the whole sidecar index depends on.
func TestResolveLookupKeyBytes_DateTime(t *testing.T) {
	schema := datetimeSchema()
	field := schema.Field("enrolled")

	got, err := ResolveLookupKeyBytes(field, "2024-03-04T10:11:12Z")
	if err != nil {
		t.Fatalf("ResolveLookupKeyBytes: %v", err)
	}
	if len(got) != 8 {
		t.Fatalf("key width = %d bytes; want 8", len(got))
	}

	rec := NewRecord(schema, map[string]float64{
		"enrolled": epochSeconds(2024, time.March, 4, 10, 11, 12),
	})
	want, ok := KeyFieldOnWireBytes(rec, field)
	if !ok {
		t.Fatal("KeyFieldOnWireBytes returned not-ok for a populated datetime field")
	}
	if string(got) != string(want) {
		t.Errorf("literal key %v != record key %v", got, want)
	}

	// An offset-carrying literal names the same instant and must
	// resolve to the same bytes.
	same, err := ResolveLookupKeyBytes(field, "2024-03-04T12:11:12+02:00")
	if err != nil {
		t.Fatalf("ResolveLookupKeyBytes (offset form): %v", err)
	}
	if string(same) != string(want) {
		t.Errorf("offset literal key %v != record key %v", same, want)
	}

	// A non-datetime literal fails loudly rather than resolving to a
	// silently wrong key.
	if _, err := ResolveLookupKeyBytes(field, "not-a-datetime"); err == nil {
		t.Error("expected an error for an unparseable datetime literal")
	}
}

// keysOf is a small sorted-key helper for readable bucket assertions.
func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// asCoded is a local type-assertion shim so this file does not need the
// stdlib errors package alongside the project's own.
func asCoded(err error, target **errors.CodedError) bool {
	ce, ok := err.(*errors.CodedError)
	if ok {
		*target = ce
	}
	return ok
}
