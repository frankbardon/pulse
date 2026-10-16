package temporal

import "time"

// Calendar readings of an epoch DAY — the formula helpers weekday,
// month_index and date_trunc (internal/exprenv) answer through these, so
// the expression layer never open-codes a weekday modulo or a month
// count. Every function takes a `date` cell's epoch day (a `datetime`
// instant reaches them only through LocalDay, the formula's to_day): a
// calendar date names a day, not an instant, so no zone applies here.

// ISOWeekday returns the ISO 8601 weekday of epoch day day: 1 = Monday
// through 7 = Sunday. Epoch day 0 (1970-01-01) is a Thursday (4); the
// double modulo keeps the answer right for days before the epoch.
func ISOWeekday(day int64) int64 {
	return (((day+3)%7)+7)%7 + 1
}

// MonthIndex returns year*12 + (month-1) for the calendar month holding
// epoch day day — a running month count, so the difference of two
// indexes is the number of calendar months between their days
// (2024-01-31 and 2024-02-01 are one month apart, 2023-12 and 2024-01
// are one apart across the year boundary).
func MonthIndex(day int64) int64 {
	t := DayToTime(day)
	return int64(t.Year())*12 + int64(t.Month()) - 1
}

// The period units TruncDay accepts, in the order a refusal lists them.
const (
	TruncWeek    = "week"
	TruncMonth   = "month"
	TruncQuarter = "quarter"
	TruncYear    = "year"
)

// TruncUnits returns the period units TruncDay accepts, in order.
func TruncUnits() []string {
	return []string{TruncWeek, TruncMonth, TruncQuarter, TruncYear}
}

// TruncDay returns the epoch day on which the period unit holding epoch
// day day begins: the Monday of its ISO week ("week"), the first of its
// month ("month"), of its calendar quarter ("quarter", Jan / Apr / Jul /
// Oct) or of its year ("year"). ok is false for any other unit
// (case-sensitive).
func TruncDay(unit string, day int64) (start int64, ok bool) {
	switch unit {
	case TruncWeek:
		return WeekStartDay(day, time.Monday), true
	case TruncMonth:
		return TimeToDay(MonthStart(day)), true
	case TruncQuarter:
		return TimeToDay(QuarterStart(day)), true
	case TruncYear:
		return TimeToDay(YearStart(day)), true
	}
	return 0, false
}

// IsTruncUnit reports whether TruncDay accepts unit.
func IsTruncUnit(unit string) bool {
	_, ok := TruncDay(unit, 0)
	return ok
}
