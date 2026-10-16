package temporal

import (
	"slices"
	"testing"
	"time"
)

// isoWeekdayOracle is the stdlib's weekday of the day, renumbered ISO
// (Sunday = 7).
func isoWeekdayOracle(day int64) int64 {
	wd := int64(DayToTime(day).Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

// TestCalendarDay_MatchesStdlib: every day from 1900-01-01 to 2100-12-31
// (leap years and both century rules included, days before the epoch
// too) reads the ISO weekday, month index and period starts the stdlib
// calendar gives.
func TestCalendarDay_MatchesStdlib(t *testing.T) {
	first := TimeToDay(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC))
	last := TimeToDay(time.Date(2100, 12, 31, 0, 0, 0, 0, time.UTC))
	for day := first; day <= last; day++ {
		tm := DayToTime(day)
		if got, want := ISOWeekday(day), isoWeekdayOracle(day); got != want {
			t.Fatalf("ISOWeekday(%s) = %d, want %d", tm.Format("2006-01-02"), got, want)
		}
		if got, want := MonthIndex(day), int64(tm.Year())*12+int64(tm.Month())-1; got != want {
			t.Fatalf("MonthIndex(%s) = %d, want %d", tm.Format("2006-01-02"), got, want)
		}
		wantStart := map[string]time.Time{
			TruncWeek:    tm.AddDate(0, 0, -int(isoWeekdayOracle(day)-1)),
			TruncMonth:   time.Date(tm.Year(), tm.Month(), 1, 0, 0, 0, 0, time.UTC),
			TruncQuarter: time.Date(tm.Year(), (tm.Month()-1)/3*3+1, 1, 0, 0, 0, 0, time.UTC),
			TruncYear:    time.Date(tm.Year(), 1, 1, 0, 0, 0, 0, time.UTC),
		}
		for unit, want := range wantStart {
			got, ok := TruncDay(unit, day)
			if !ok || got != TimeToDay(want) {
				t.Fatalf("TruncDay(%q, %s) = %s (ok=%v), want %s", unit, tm.Format("2006-01-02"),
					DayToTime(got).Format("2006-01-02"), ok, want.Format("2006-01-02"))
			}
		}
	}
}

// TestCalendarDay_Pins: hand-checked readings at the edges — the epoch
// Thursday, a leap day, year and quarter boundaries, a week straddling
// New Year.
func TestCalendarDay_Pins(t *testing.T) {
	d := func(y int, m time.Month, dd int) int64 { return TimeToDay(time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)) }
	cases := []struct {
		name string
		got  int64
		want int64
	}{
		{"epoch is Thursday", ISOWeekday(0), 4},
		{"1969-12-31 is Wednesday", ISOWeekday(-1), 3},
		{"2024-02-29 is Thursday", ISOWeekday(d(2024, 2, 29)), 4},
		{"2024-03-03 is Sunday", ISOWeekday(d(2024, 3, 3)), 7},
		{"2024-03-04 is Monday", ISOWeekday(d(2024, 3, 4)), 1},
		{"month index 2024-01", MonthIndex(d(2024, 1, 15)), 2024 * 12},
		{"month index 2023-12-31", MonthIndex(d(2023, 12, 31)), 2023*12 + 11},
		{"month index 1969-12", MonthIndex(-1), 1969*12 + 11},
		{"leap day month start", must(TruncDay(TruncMonth, d(2024, 2, 29))), d(2024, 2, 1)},
		{"week across New Year", must(TruncDay(TruncWeek, d(2025, 1, 1))), d(2024, 12, 30)},
		{"Monday is its own week start", must(TruncDay(TruncWeek, d(2024, 3, 4))), d(2024, 3, 4)},
		{"Sunday belongs to the prior Monday", must(TruncDay(TruncWeek, d(2024, 3, 3))), d(2024, 2, 26)},
		{"Q4 start", must(TruncDay(TruncQuarter, d(2024, 12, 31))), d(2024, 10, 1)},
		{"Q1 start", must(TruncDay(TruncQuarter, d(2024, 3, 31))), d(2024, 1, 1)},
		{"year start", must(TruncDay(TruncYear, d(2024, 12, 31))), d(2024, 1, 1)},
		{"pre-epoch week", must(TruncDay(TruncWeek, -1)), d(1969, 12, 29)},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, c.got, c.want)
		}
	}
}

func must(v int64, ok bool) int64 {
	if !ok {
		return -1 << 62
	}
	return v
}

// TestTruncDay_Units: only the four lowercase units are accepted, in the
// order a refusal lists them.
func TestTruncDay_Units(t *testing.T) {
	if got := TruncUnits(); !slices.Equal(got, []string{"week", "month", "quarter", "year"}) {
		t.Fatalf("TruncUnits = %v", got)
	}
	for _, u := range []string{"", "day", "Week", "MONTH", "weeks", "isoweek"} {
		if IsTruncUnit(u) {
			t.Errorf("IsTruncUnit(%q) = true", u)
		}
		if _, ok := TruncDay(u, 0); ok {
			t.Errorf("TruncDay(%q) ok", u)
		}
	}
	for _, u := range TruncUnits() {
		if !IsTruncUnit(u) {
			t.Errorf("IsTruncUnit(%q) = false", u)
		}
	}
}
