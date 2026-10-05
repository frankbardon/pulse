// Package temporal is the single home of Pulse's epoch-day and calendar
// arithmetic. A `date` cell holds whole epoch DAYS and a `datetime` cell
// holds whole epoch SECONDS (naive UTC); every conversion between the two,
// and every calendar boundary computed from a day, goes through this
// package rather than open-coding the 86,400 factor at a call site.
//
// It is a leaf over the stdlib plus the public `errors` package (for the
// coded PULSE_TIMEZONE_UNKNOWN refusal): it must never import the public
// `encoding` package (which forwards into it) or any other Pulse package.
// It is also the only package that embeds the tz database and resolves
// IANA zones (zone.go).
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

// DayToUnix returns the epoch seconds of UTC midnight of epoch day day.
func DayToUnix(day int64) int64 {
	return day * SecondsPerDay
}

// DayToUnixFloat is DayToUnix over float64 for callers (SPSS) that carry
// day counts as doubles. The multiply is exact for every day count a
// `date` cell can hold.
func DayToUnixFloat(day float64) float64 {
	return day * SecondsPerDay
}

// IsWholeDay reports whether sec (epoch seconds) falls exactly on a UTC
// midnight boundary, for either sign.
func IsWholeDay(sec int64) bool {
	return sec%SecondsPerDay == 0
}

// SecondsPerHour is the conversion factor between epoch seconds and
// epoch HOURS — the unit GROUP_DATE's `hour` component buckets on.
const SecondsPerHour = 3600

// HourToTime returns the UTC time.Time at the start of epoch hour hour.
// Fed a LOCAL epoch hour (LocalHour), its fields are the wall-clock
// date and hour a clock in that zone shows.
func HourToTime(hour int64) time.Time {
	return time.Unix(hour*SecondsPerHour, 0).UTC()
}

// HourToDay returns the epoch day containing epoch hour hour, truncating
// toward the past as DateTimeToDay does.
func HourToDay(hour int64) int64 {
	return DateTimeToDay(hour * SecondsPerHour)
}

// WeekStartDay returns the epoch day on which the week containing epoch
// day day begins, for weeks that begin on weekday start. With start ==
// time.Monday it is the Monday of the day's ISO week.
func WeekStartDay(day int64, start time.Weekday) int64 {
	// Epoch day 0 (1970-01-01) was a Thursday; the double modulo keeps
	// the weekday non-negative for days before the epoch.
	wd := ((day % 7) + 7 + int64(time.Thursday)) % 7
	return day - (wd-int64(start)+7)%7
}

// weekdayNames is the lowercase English spelling ParseWeekday accepts.
var weekdayNames = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday,
	"friday": time.Friday, "saturday": time.Saturday,
}

// ParseWeekday maps a lowercase English day name ("monday" … "sunday")
// to its time.Weekday. Any other spelling (case included) is refused.
func ParseWeekday(name string) (time.Weekday, bool) {
	wd, ok := weekdayNames[name]
	return wd, ok
}
