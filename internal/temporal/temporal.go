// Package temporal is the single home of Pulse's epoch-day and calendar
// arithmetic. A `date` cell holds whole epoch DAYS and a `datetime` cell
// holds whole epoch SECONDS (naive UTC); every conversion between the two,
// and every calendar boundary computed from a day, goes through this
// package rather than open-coding the 86,400 factor at a call site.
//
// It is a stdlib-only leaf: it must never import the public `encoding`
// package (which forwards into it) or any other Pulse package.
package temporal

import "time"

// SecondsPerDay is the conversion factor between the `datetime` (epoch
// seconds) and `date` (epoch days) on-wire representations. Untyped so it
// composes with int64, uint64 and float64 arithmetic alike.
const SecondsPerDay = 86400

// DateTimeToDay truncates epoch seconds (naive UTC) to the epoch day that
// contains the instant. Truncation is toward the PAST, never toward zero
// and never rounding: 1969-12-31T23:59:59Z (sec -1) is day -1, not day 0.
func DateTimeToDay(sec int64) int64 {
	day := sec / SecondsPerDay
	if sec < 0 && sec%SecondsPerDay != 0 {
		day--
	}
	return day
}

// TimeToDay returns the epoch day containing t's instant, with the same
// toward-the-past truncation as DateTimeToDay. t's location is ignored;
// only the instant matters.
func TimeToDay(t time.Time) int64 {
	return DateTimeToDay(t.Unix())
}

// DayToTime returns UTC midnight of epoch day day.
func DayToTime(day int64) time.Time {
	return time.Unix(day*SecondsPerDay, 0).UTC()
}

// MonthStart returns UTC midnight of the first day of the calendar month
// containing epoch day day.
func MonthStart(day int64) time.Time {
	t := DayToTime(day)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// QuarterStart returns UTC midnight of the first day of the calendar
// quarter (Jan / Apr / Jul / Oct) containing epoch day day.
func QuarterStart(day int64) time.Time {
	t := DayToTime(day)
	m := time.Month(((int(t.Month())-1)/3)*3 + 1)
	return time.Date(t.Year(), m, 1, 0, 0, 0, 0, time.UTC)
}

// YearStart returns UTC midnight of January 1 of the calendar year
// containing epoch day day.
func YearStart(day int64) time.Time {
	t := DayToTime(day)
	return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
}
