package temporal

import (
	"math"
	"testing"
	"time"
)

func utc(y int, m time.Month, d, hh, mm, ss int) time.Time {
	return time.Date(y, m, d, hh, mm, ss, 0, time.UTC)
}

func TestSecondsPerDay(t *testing.T) {
	if SecondsPerDay != 24*60*60 {
		t.Fatalf("SecondsPerDay = %d, want 86400", SecondsPerDay)
	}
}

func TestDateTimeToDay(t *testing.T) {
	cases := []struct {
		name string
		sec  int64
		want int64
	}{
		{"epoch", 0, 0},
		{"epoch plus one second", 1, 0},
		{"last second of day 0", SecondsPerDay - 1, 0},
		{"first second of day 1", SecondsPerDay, 1},
		{"1969-12-31T23:59:59Z", -1, -1},
		{"1969-12-31T00:00:00Z exact boundary", -SecondsPerDay, -1},
		{"1969-12-30T23:59:59Z", -SecondsPerDay - 1, -2},
		{"2024-03-04T23:59:59Z", utc(2024, 3, 4, 23, 59, 59).Unix(), 19786},
		{"2024-03-05T00:00:00Z", utc(2024, 3, 5, 0, 0, 0).Unix(), 19787},
		{"1900-01-01T12:00:00Z", utc(1900, 1, 1, 12, 0, 0).Unix(), -25567},
		{"max int64", math.MaxInt64, math.MaxInt64 / SecondsPerDay},
		{"min int64", math.MinInt64, math.MinInt64/SecondsPerDay - 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DateTimeToDay(tc.sec); got != tc.want {
				t.Fatalf("DateTimeToDay(%d) = %d, want %d", tc.sec, got, tc.want)
			}
		})
	}
}

func TestTimeToDay(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want int64
	}{
		{"epoch", utc(1970, 1, 1, 0, 0, 0), 0},
		{"1969-12-31T23:59:59Z", utc(1969, 12, 31, 23, 59, 59), -1},
		{"1969-12-31T12:00:00Z", utc(1969, 12, 31, 12, 0, 0), -1},
		{"2024-02-29 noon", utc(2024, 2, 29, 12, 0, 0), 19782},
		{"non-UTC location same instant", utc(2024, 2, 29, 23, 30, 0).In(time.FixedZone("x", 3600)), 19782},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TimeToDay(tc.in); got != tc.want {
				t.Fatalf("TimeToDay(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestDayToTime(t *testing.T) {
	cases := []struct {
		day  int64
		want time.Time
	}{
		{0, utc(1970, 1, 1, 0, 0, 0)},
		{-1, utc(1969, 12, 31, 0, 0, 0)},
		{19782, utc(2024, 2, 29, 0, 0, 0)},
		{-25567, utc(1900, 1, 1, 0, 0, 0)},
		{2932896, utc(9999, 12, 31, 0, 0, 0)},
	}
	for _, tc := range cases {
		got := DayToTime(tc.day)
		if !got.Equal(tc.want) || got.Location() != time.UTC {
			t.Fatalf("DayToTime(%d) = %v (%v), want %v UTC", tc.day, got, got.Location(), tc.want)
		}
	}
}

func TestDayToTimeTimeToDayRoundTrip(t *testing.T) {
	for _, day := range []int64{-1_000_000, -25567, -366, -2, -1, 0, 1, 59, 365, 19782, 2932896, 1_000_000} {
		if got := TimeToDay(DayToTime(day)); got != day {
			t.Fatalf("round trip day %d -> %d", day, got)
		}
		// Every second inside the day maps back to the same day.
		base := DayToTime(day)
		for _, off := range []time.Duration{time.Second, 12 * time.Hour, 24*time.Hour - time.Second} {
			if got := TimeToDay(base.Add(off)); got != day {
				t.Fatalf("day %d + %v -> %d", day, off, got)
			}
		}
	}
}

func TestCalendarStarts(t *testing.T) {
	d := func(y int, m time.Month, dd int) int64 { return TimeToDay(utc(y, m, dd, 0, 0, 0)) }
	cases := []struct {
		name                 string
		day                  int64
		month, quarter, year time.Time
	}{
		{"epoch", 0, utc(1970, 1, 1, 0, 0, 0), utc(1970, 1, 1, 0, 0, 0), utc(1970, 1, 1, 0, 0, 0)},
		{"1969-12-31", -1, utc(1969, 12, 1, 0, 0, 0), utc(1969, 10, 1, 0, 0, 0), utc(1969, 1, 1, 0, 0, 0)},
		{"leap day 2024-02-29", d(2024, 2, 29), utc(2024, 2, 1, 0, 0, 0), utc(2024, 1, 1, 0, 0, 0), utc(2024, 1, 1, 0, 0, 0)},
		{"2024-03-01 after leap", d(2024, 3, 1), utc(2024, 3, 1, 0, 0, 0), utc(2024, 1, 1, 0, 0, 0), utc(2024, 1, 1, 0, 0, 0)},
		{"2023-03-31 Q1 end", d(2023, 3, 31), utc(2023, 3, 1, 0, 0, 0), utc(2023, 1, 1, 0, 0, 0), utc(2023, 1, 1, 0, 0, 0)},
		{"2023-04-01 Q2 start", d(2023, 4, 1), utc(2023, 4, 1, 0, 0, 0), utc(2023, 4, 1, 0, 0, 0), utc(2023, 1, 1, 0, 0, 0)},
		{"2023-06-30 Q2 end", d(2023, 6, 30), utc(2023, 6, 1, 0, 0, 0), utc(2023, 4, 1, 0, 0, 0), utc(2023, 1, 1, 0, 0, 0)},
		{"2023-08-15 Q3", d(2023, 8, 15), utc(2023, 8, 1, 0, 0, 0), utc(2023, 7, 1, 0, 0, 0), utc(2023, 1, 1, 0, 0, 0)},
		{"2023-12-31 year end", d(2023, 12, 31), utc(2023, 12, 1, 0, 0, 0), utc(2023, 10, 1, 0, 0, 0), utc(2023, 1, 1, 0, 0, 0)},
		{"2024-01-01 year start", d(2024, 1, 1), utc(2024, 1, 1, 0, 0, 0), utc(2024, 1, 1, 0, 0, 0), utc(2024, 1, 1, 0, 0, 0)},
		{"1900-02-28 non-leap century", d(1900, 2, 28), utc(1900, 2, 1, 0, 0, 0), utc(1900, 1, 1, 0, 0, 0), utc(1900, 1, 1, 0, 0, 0)},
		{"2000-02-29 leap century", d(2000, 2, 29), utc(2000, 2, 1, 0, 0, 0), utc(2000, 1, 1, 0, 0, 0), utc(2000, 1, 1, 0, 0, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := func(fn string, got, want time.Time) {
				if !got.Equal(want) || got.Location() != time.UTC {
					t.Errorf("%s(%d) = %v (%v), want %v UTC", fn, tc.day, got, got.Location(), want)
				}
			}
			check("MonthStart", MonthStart(tc.day), tc.month)
			check("QuarterStart", QuarterStart(tc.day), tc.quarter)
			check("YearStart", YearStart(tc.day), tc.year)
		})
	}
}
