package temporal

import (
	"strings"
	"time"
)

// Wall-clock readings of an instant — the synth rule functions trend,
// season and between (internal/synth/row_time.go) read a `datetime` on
// its LOCAL clock through these, so no zone offset is ever added outside
// this package.

// LocalWall returns the wall-clock reading of instant sec (epoch
// seconds) in zone z, counted as seconds since 1970-01-01T00:00:00 on
// that clock — the inverse direction of ResolveLocal. A nil or
// UTC-equivalent zone returns sec unchanged. Allocation-free.
func LocalWall(sec int64, z *Zone) int64 {
	if z == nil || z.IsUTC() {
		return sec
	}
	return sec + int64(z.Offset(sec))
}

// ElapsedDays returns (to - from) seconds as fractional days. The
// division is the only operation, so the result is the same on every
// architecture.
func ElapsedDays(from, to int64) float64 {
	return float64(to-from) / SecondsPerDay
}

// DayEndWall returns the last second of epoch day day as a wall-clock
// reading: DayToUnix(day+1) - 1.
func DayEndWall(day int64) int64 {
	return (day+1)*SecondsPerDay - 1
}

// ParseWall parses a NAIVE date or datetime literal into a wall-clock
// reading (seconds since 1970-01-01T00:00:00 on some clock). A bare
// YYYY-MM-DD returns dateOnly = true and the reading of that day's
// midnight; every other accepted literal is one of the naive layouts
// ParseLocal accepts (an offset-bearing literal — `Z`, `+02:00` — names
// an instant, not a reading, and is refused). Sub-second digits are
// floored toward the past. ok is false for anything else.
func ParseWall(s string) (wall int64, dateOnly bool, ok bool) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Unix(), true, true
	}
	for _, layout := range dateTimeLayouts {
		if strings.Contains(layout, "Z07:00") {
			continue
		}
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix(), false, true
		}
	}
	return 0, false, false
}
