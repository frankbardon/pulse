package exprenv

import (
	stderrors "errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// calendarSchema: d a `date`, ts a `datetime`, x an f64, cat a
// categorical.
func calendarSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "d", Type: encoding.FieldTypeDate},
		{Name: "ts", Type: encoding.FieldTypeDateTime},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2},
	}}
}

func day(y int, m time.Month, d int) float64 {
	return float64(temporal.TimeToDay(time.Date(y, m, d, 0, 0, 0, 0, time.UTC)))
}

// calendarRun compiles src against the calendar schema (with the guard
// and b's bindings) and runs it over env.
func calendarRun(t *testing.T, src string, b Bound, env map[string]any) (any, error) {
	t.Helper()
	schema := calendarSchema()
	opts := append([]expr.Option{expr.Env(Prototype(schema)), CalendarGuard(schema)}, b.Options()...)
	prog, err := expr.Compile(src, opts...)
	if err != nil {
		return nil, err
	}
	return expr.Run(prog, env)
}

// TestCalendarHelpers_Oracle: each helper over a typed env answers what
// internal/temporal (itself pinned against the stdlib) gives — ISO
// weekday, month index, the four period starts, to_day at UTC — across a
// leap day and a year boundary.
func TestCalendarHelpers_Oracle(t *testing.T) {
	ts := float64(time.Date(2024, 12, 31, 23, 30, 0, 0, time.UTC).Unix())
	env := map[string]any{"d": day(2024, 2, 29), "ts": ts, "x": 1.0, "cat": "a"}
	cases := []struct {
		src  string
		want float64
	}{
		{"weekday(d)", 4},
		{"weekday(d + 4)", 1},
		{"month_index(d)", 2024*12 + 1},
		{"month_index(d + 1) - month_index(d)", 1},
		{`date_trunc("week", d)`, day(2024, 2, 26)},
		{`date_trunc("month", d)`, day(2024, 2, 1)},
		{`date_trunc("quarter", d)`, day(2024, 1, 1)},
		{`date_trunc("year", d)`, day(2024, 1, 1)},
		{"to_day(ts)", day(2024, 12, 31)},
		{"weekday(to_day(ts))", 2},
		{"month_index(to_day(ts + 3600)) - month_index(to_day(ts))", 1},
		{`date_trunc("year", to_day(ts + 3600))`, day(2025, 1, 1)},
		{"weekday(19782)", 4}, // an int literal day (2024-02-29)
		{"log1p(x)", detmath.Log1p(1)},
	}
	for _, c := range cases {
		got, err := calendarRun(t, c.src, Bound{}, env)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if f, ok := got.(float64); !ok || f != c.want {
			t.Errorf("%s = %v (%T), want %v", c.src, got, got, c.want)
		}
	}
}

// TestToDay_Zone: to_day reads the wall-clock day in Bound.Zone — the
// UTC day otherwise — including either side of a DST transition and the
// date line.
func TestToDay_Zone(t *testing.T) {
	ny, err := temporal.LoadZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	tokyo, err := temporal.LoadZone("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	unix := func(s string) float64 {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return float64(tm.Unix())
	}
	cases := []struct {
		name string
		zone *temporal.Zone
		ts   string
		want float64
	}{
		{"utc", nil, "2024-03-10T03:30:00Z", day(2024, 3, 10)},
		{"ny before midnight", ny, "2024-03-10T03:30:00Z", day(2024, 3, 9)},
		// 2024-03-10 07:00Z is 03:00 EDT, after the 02:00 spring-forward.
		{"ny after spring forward", ny, "2024-03-10T07:00:00Z", day(2024, 3, 10)},
		// 2024-11-03 05:30Z is 01:30 EDT (first pass of the repeated hour).
		{"ny fall-back first pass", ny, "2024-11-03T05:30:00Z", day(2024, 11, 3)},
		{"ny fall-back day end", ny, "2024-11-04T04:59:59Z", day(2024, 11, 3)},
		{"ny new year", ny, "2025-01-01T04:59:59Z", day(2024, 12, 31)},
		{"tokyo ahead", tokyo, "2024-12-31T15:00:00Z", day(2025, 1, 1)},
		{"pre-epoch utc", nil, "1969-12-31T23:59:59Z", -1},
	}
	for _, c := range cases {
		got, err := calendarRun(t, "to_day(ts)", Bound{Zone: c.zone}, map[string]any{"ts": unix(c.ts)})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: to_day(%s) = %v, want %v", c.name, c.ts, got, c.want)
		}
	}
}

// TestCalendarHelpers_NullAndNonFinite: a nil argument answers nil for
// every helper (log1p included); a non-finite day or instant answers NaN.
func TestCalendarHelpers_NullAndNonFinite(t *testing.T) {
	for _, call := range [][]any{
		{FuncWeekday, nil}, {FuncMonthIndex, nil}, {FuncToDay, nil}, {FuncLog1p, nil},
		{FuncDateTrunc, "month", nil}, {FuncDateTrunc, nil, 1.0},
	} {
		got, err := Impl(call[0].(string))(call[1:]...)
		if err != nil || got != nil {
			t.Errorf("%s%v = %v, %v; want nil", call[0], call[1:], got, err)
		}
	}
	for _, call := range [][]any{
		{FuncWeekday, math.NaN()}, {FuncMonthIndex, math.Inf(1)}, {FuncToDay, math.Inf(-1)},
		{FuncDateTrunc, "week", math.NaN()}, {FuncWeekday, 1e12},
	} {
		got, err := Impl(call[0].(string))(call[1:]...)
		if f, ok := got.(float64); err != nil || !ok || !math.IsNaN(f) {
			t.Errorf("%s%v = %v, %v; want NaN", call[0], call[1:], got, err)
		}
	}
	// A null field inside an expression: the untyped twin env.
	opts := append([]expr.Option{expr.Env(exprtypes.Map{"d": exprtypes.Any})}, Options()...)
	prog, err := expr.Compile(`weekday(d) ?? -1`, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := expr.Run(prog, map[string]any{"d": nil}); err != nil || got != -1 {
		t.Errorf("weekday(null) ?? -1 = %v, %v; want -1", got, err)
	}
}

// TestDateTrunc_RuntimeUnit: a unit computed at run time outside the set
// is the coded PROCESSING_CONFIG refusal {function, unit, valid}.
func TestDateTrunc_RuntimeUnit(t *testing.T) {
	got, err := calendarRun(t, `date_trunc(cat, d)`, Bound{}, map[string]any{"cat": "fortnight", "d": 0.0})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_CONFIG || !stderrors.Is(ce.Cause, ErrUnknownTruncUnit) {
		t.Fatalf("date_trunc(\"fortnight\") = %v, %v; want PROCESSING_CONFIG ErrUnknownTruncUnit", got, err)
	}
	if ce.Details["unit"] != "fortnight" || ce.Details["function"] != FuncDateTrunc {
		t.Errorf("details = %v", ce.Details)
	}
	got, err = calendarRun(t, `date_trunc(cat, d)`, Bound{}, map[string]any{"cat": "quarter", "d": day(2024, 5, 5)})
	if err != nil || got != day(2024, 4, 1) {
		t.Errorf("date_trunc(cat=quarter) = %v, %v", got, err)
	}
}

// TestCalendarGuard: a bare datetime field as a day helper's day, or a
// literal unknown unit, fails to compile with a reason naming the fix;
// to_day(), a date field, an expression over the datetime and a `let`
// over to_day compile.
func TestCalendarGuard(t *testing.T) {
	refused := []struct{ src, reason string }{
		{"weekday(ts)", `weekday() reads epoch days but "ts" is a datetime field (epoch seconds): wrap it in to_day(), as in weekday(to_day(ts))`},
		{"month_index(ts) - month_index(d)", `month_index() reads epoch days but "ts" is a datetime field`},
		{`date_trunc("month", ts)`, `as in date_trunc("month", to_day(ts))`},
		{`x > 0 ? weekday(ts) : 0`, `wrap it in to_day()`},
		{`date_trunc("weeks", d)`, `date_trunc(): unit "weeks" is not one of "week", "month", "quarter", "year"`},
		{`date_trunc("Month", d)`, `unit "Month"`},
		{`let k = 1; weekday(ts) + k`, `wrap it in to_day()`},
	}
	schema := calendarSchema()
	for _, c := range refused {
		opts := append([]expr.Option{expr.Env(Prototype(schema)), CalendarGuard(schema)}, Options()...)
		_, err := expr.Compile(c.src, opts...)
		if err == nil {
			t.Errorf("%s compiled; want refused", c.src)
			continue
		}
		if r := CompileReason(err); !strings.Contains(r, c.reason) {
			t.Errorf("%s: reason %q lacks %q", c.src, r, c.reason)
		}
		if ce := CompileError(SiteFormula, c.src, err); ce.Code != perr.PULSE_FORMULA_COMPILE_ERROR {
			t.Errorf("%s: code %s", c.src, ce.Code)
		}
	}
	for _, src := range []string{
		"weekday(to_day(ts))", "weekday(d)", `date_trunc("quarter", to_day(ts))`,
		"weekday(ts / 86400)", "let t = to_day(ts); weekday(t)", "to_day(ts) - d",
		`date_trunc(cat, d)`,
	} {
		opts := append([]expr.Option{expr.Env(Prototype(schema)), CalendarGuard(schema)}, Options()...)
		if _, err := expr.Compile(src, opts...); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	// Wrong argument types fail the typed declaration, guard or not.
	for _, src := range []string{`weekday(cat)`, `month_index(amt)`, `date_trunc(1, d)`, `to_day("x")`, `weekday(d, d)`} {
		opts := append([]expr.Option{expr.Env(Prototype(schema))}, Options()...)
		if _, err := expr.Compile(src, opts...); err == nil {
			t.Errorf("%s compiled; want a type refusal", src)
		}
	}
	// Without the schema, only the unit is judged.
	opts := append([]expr.Option{expr.Env(Prototype(schema)), CalendarGuard(nil)}, Options()...)
	if _, err := expr.Compile("weekday(ts)", opts...); err != nil {
		t.Errorf("nil-schema guard refused weekday(ts): %v", err)
	}
}
