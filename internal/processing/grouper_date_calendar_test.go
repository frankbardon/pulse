package processing

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

func newCalendarGrouper(t *testing.T, params, tz string) *dateGrouper {
	t.Helper()
	g, err := newDateGrouper(&types.Group{Type: types.GROUP_DATE, Field: "enrolled", Params: json.RawMessage(params), TimeZone: tz}, datetimeSchema())
	if err != nil {
		t.Fatalf("newDateGrouper(%s): %v", params, err)
	}
	return g.(*dateGrouper)
}

// TestGroupDate_HourKeysAreLocalWallClock: `hour` keys a `datetime` by
// the local wall-clock hour (`YYYY-MM-DDTHH`) — UTC with no zone, Berlin
// with one — and both arms (KeyFor and buffered Group) agree.
func TestGroupDate_HourKeysAreLocalWallClock(t *testing.T) {
	for _, tc := range []struct {
		tz   string
		v    float64
		want string
	}{
		{"", epochSeconds(2026, time.March, 28, 23, 30, 0), "2026-03-28T23"},
		{"Europe/Berlin", epochSeconds(2026, time.March, 28, 23, 30, 0), "2026-03-29T00"},
		// Spring forward: 01:00Z is 03:00 CEST — no T02.
		{"Europe/Berlin", epochSeconds(2026, time.March, 29, 1, 0, 0), "2026-03-29T03"},
		// Fall back: 00:30Z (02:30 CEST) and 01:30Z (02:30 CET) share T02.
		{"Europe/Berlin", epochSeconds(2026, time.October, 25, 0, 30, 0), "2026-10-25T02"},
		{"Europe/Berlin", epochSeconds(2026, time.October, 25, 1, 30, 0), "2026-10-25T02"},
		{"Asia/Kolkata", epochSeconds(2026, time.June, 15, 18, 29, 0), "2026-06-15T23"},
		{"Asia/Kolkata", epochSeconds(2026, time.June, 15, 18, 30, 0), "2026-06-16T00"},
	} {
		g := newCalendarGrouper(t, `{"component":"hour"}`, tc.tz)
		r := NewRecord(datetimeSchema(), map[string]float64{"enrolled": tc.v})
		key, err := g.KeyFor(r)
		if err != nil || key != tc.want {
			t.Fatalf("tz %q: KeyFor = %q, %v; want %q", tc.tz, key, err, tc.want)
		}
		groups, err := g.Group([]*Record{r}, "enrolled")
		if err != nil || len(groups[tc.want]) != 1 {
			t.Fatalf("tz %q: Group = %v, %v; want one row under %q", tc.tz, groups, err, tc.want)
		}
	}
}

// TestGroupDate_HourComponents: hour buckets report period_start ==
// period_end == the hour label, and range_start / range_end at hour
// resolution; the merged fall-back hour is one bucket of both rows.
func TestGroupDate_HourComponents(t *testing.T) {
	g := newCalendarGrouper(t, `{"component":"hour"}`, "Europe/Berlin")
	recs := makeRecords(datetimeSchema(), "enrolled", []float64{
		epochSeconds(2026, time.October, 24, 23, 15, 0), // 01:15 CEST
		epochSeconds(2026, time.October, 25, 0, 30, 0),  // 02:30 CEST
		epochSeconds(2026, time.October, 25, 1, 30, 0),  // 02:30 CET
	})
	if _, err := g.Group(recs, "enrolled"); err != nil {
		t.Fatal(err)
	}
	comp, err := g.Components()
	if err != nil {
		t.Fatal(err)
	}
	if comp["granularity"] != "hour" || comp["range_start"] != "2026-10-25T01" || comp["range_end"] != "2026-10-25T02" || comp["n_buckets"] != 2 {
		t.Fatalf("components = %v", comp)
	}
	b := comp["buckets"].([]map[string]any)[1]
	if b["key"] != "2026-10-25T02" || b["period_start"] != "2026-10-25T02" || b["period_end"] != "2026-10-25T02" || b["count"] != 2 {
		t.Fatalf("merged fall-back bucket = %v", b)
	}
}

// TestGroupDate_WeekStart: monday (default or explicit) keeps the ISO
// `YYYY-Www` label and Monday..Sunday periods; another start keys by the
// local date of the week's first day and periods follow it.
func TestGroupDate_WeekStart(t *testing.T) {
	sat := epochSeconds(2026, time.February, 28, 23, 30, 0) // Sat UTC; Sun 00:30 Berlin
	for _, tc := range []struct {
		params, tz, key, start, end string
	}{
		{`{"component":"week"}`, "", "2026-W09", "2026-02-23", "2026-03-01"},
		{`{"component":"week","week_start":"monday"}`, "", "2026-W09", "2026-02-23", "2026-03-01"},
		{`{"component":"week","week_start":"sunday"}`, "", "2026-02-22", "2026-02-22", "2026-02-28"},
		{`{"component":"week","week_start":"sunday"}`, "Europe/Berlin", "2026-03-01", "2026-03-01", "2026-03-07"},
		{`{"component":"week","week_start":"saturday"}`, "", "2026-02-28", "2026-02-28", "2026-03-06"},
		{`{"component":"week","week_start":"wednesday"}`, "Europe/Berlin", "2026-02-25", "2026-02-25", "2026-03-03"},
	} {
		g := newCalendarGrouper(t, tc.params, tc.tz)
		r := NewRecord(datetimeSchema(), map[string]float64{"enrolled": sat})
		key, err := g.KeyFor(r)
		if err != nil || key != tc.key {
			t.Fatalf("%s tz %q: key = %q, %v; want %q", tc.params, tc.tz, key, err, tc.key)
		}
		if _, err := g.Group([]*Record{r}, "enrolled"); err != nil {
			t.Fatal(err)
		}
		comp, _ := g.Components()
		b := comp["buckets"].([]map[string]any)[0]
		if b["period_start"] != tc.start || b["period_end"] != tc.end {
			t.Fatalf("%s tz %q: period = %v..%v, want %s..%s", tc.params, tc.tz, b["period_start"], b["period_end"], tc.start, tc.end)
		}
	}
}

// TestGroupDate_WeekStartDateColumn: a `date` column keys dated weeks
// the same way (no zone involved).
func TestGroupDate_WeekStartDateColumn(t *testing.T) {
	g, err := newDateGrouper(&types.Group{Type: types.GROUP_DATE, Field: "enrolled", Params: json.RawMessage(`{"component":"week","week_start":"sunday"}`)}, dateSchema())
	if err != nil {
		t.Fatal(err)
	}
	r := NewRecord(dateSchema(), map[string]float64{"enrolled": epochDays(2026, time.March, 4)})
	if key, err := g.(StreamableGrouper).KeyFor(r); err != nil || key != "2026-03-01" {
		t.Fatalf("key = %q, %v; want 2026-03-01", key, err)
	}
}

// TestGroupDate_FactoryRefusals: the factory refuses through dategroup —
// `hour` on a `date` column, `week_start` without `week`, a bad day
// name — with PROCESSING_CONFIG.
func TestGroupDate_FactoryRefusals(t *testing.T) {
	for _, tc := range []struct {
		params string
		date   bool
		msg    string
	}{
		{`{"component":"hour"}`, true, `GROUP_DATE component=hour requires a datetime field; field "enrolled" is of type date`},
		{`{"component":"day","week_start":"sunday"}`, false, `GROUP_DATE week_start only applies to component=week, got "day"`},
		{`{"component":"week","week_start":"Sun"}`, false, `invalid GROUP_DATE week_start "Sun": must be one of monday, tuesday, wednesday, thursday, friday, saturday, sunday`},
	} {
		schema := datetimeSchema()
		if tc.date {
			schema = dateSchema()
		}
		_, err := newDateGrouper(&types.Group{Type: types.GROUP_DATE, Field: "enrolled", Params: json.RawMessage(tc.params)}, schema)
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PROCESSING_CONFIG || ce.Message != tc.msg {
			t.Fatalf("%s: err = %v, want %q", tc.params, err, tc.msg)
		}
	}
}

// TestDateField_EpochHourProbe: a day count (the schema-less probe path)
// reads as hour 00 of that day.
func TestDateField_EpochHourProbe(t *testing.T) {
	day := epochDays(2026, time.March, 4)
	got := dateField{}.epochHour(day)
	if s := temporal.HourToTime(got).Format("2006-01-02T15"); s != "2026-03-04T00" {
		t.Fatalf("epochHour(day) reads %s", s)
	}
}
