package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestExprCalendar_BuildRefusesBareDatetime: the engine's own build —
// not only the service's entry walk — runs the calendar guard, so a
// bare datetime as a day helper's day fails the formula and the filter
// build with PULSE_FORMULA_COMPILE_ERROR, and to_day() over it builds.
func TestExprCalendar_BuildRefusesBareDatetime(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "d", Type: encoding.FieldTypeDate},
		{Name: "ts", Type: encoding.FieldTypeDateTime},
	}}
	for _, c := range []struct {
		src string
		bad bool
	}{
		{"weekday(ts)", true},
		{`date_trunc("year", ts)`, true},
		{`date_trunc("decade", d)`, true},
		{"weekday(to_day(ts))", false},
		{"month_index(d)", false},
	} {
		_, err := newExprProgram(c.src, "formula", schema, nil, nil, (*ExtensionRegistry)(nil).ExprOptions())
		if got := topCode(err); c.bad != (got == perr.PULSE_FORMULA_COMPILE_ERROR) || (!c.bad && err != nil) {
			t.Errorf("%s: %v (bad=%v)", c.src, err, c.bad)
		}
	}
}

// TestExprCalendar_SlotZone: a formula slot carrying `tz` (the zone
// transport's write) reads to_day in it; none reads UTC.
func TestExprCalendar_SlotZone(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "ts", Type: encoding.FieldTypeDateTime}}}
	// 2024-03-10T03:30:00Z: the 10th in UTC, the 9th in New York.
	rec := NewRecord(schema, map[string]float64{"ts": 1710041400})
	for _, c := range []struct {
		tz   string
		want float64
	}{{"", 19792}, {"UTC", 19792}, {"America/New_York", 19791}} {
		proc := NewProcessorWithExtensions(schema, &ExtensionRegistry{})
		resp, err := proc.Process(context.Background(), &types.Request{
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "to_day(ts)", TimeZone: c.tz}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}},
		}, NewSliceIterator([]*Record{rec}))
		if err != nil {
			t.Fatal(err)
		}
		if got := resp.Data[0]["s"]; got != c.want {
			t.Errorf("tz %q: to_day = %v, want %v", c.tz, got, c.want)
		}
	}
	if _, err := exprZone("Mars/Olympus"); topCode(err) != perr.PULSE_TIMEZONE_UNKNOWN {
		t.Errorf("unknown slot zone: %v", err)
	}
}
