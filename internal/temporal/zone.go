package temporal

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	// The IANA tz database is embedded so zone resolution never depends
	// on the host having /usr/share/zoneinfo. This is the ONLY import of
	// it in the module (TestNoZoneMathOutsideTemporal).
	_ "time/tzdata"

	perr "github.com/frankbardon/pulse/errors"
)

// The precomputed transition table covers [windowStart, windowEnd):
// 1900-01-01T00:00:00Z to 2100-01-01T00:00:00Z. Instants outside it
// fall back to the stdlib lookup, which is correct but slower.
var (
	windowStart = time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()
	windowEnd   = time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()
)

// Zone is a validated IANA time zone with a precomputed transition table
// for allocation-free offset lookup. A *Zone is immutable apart from its
// lookup cache, which is atomic, so one *Zone is safe to share across
// goroutines. A Zone must not be copied after LoadZone returns it.
type Zone struct {
	name string
	loc  *time.Location
	utc  bool

	// starts[i] is the first instant (epoch seconds) at which offs[i]
	// (seconds east of UTC) is in effect; span i runs to starts[i+1],
	// and the last span to windowEnd. starts[0] == windowStart.
	// Adjacent spans never share an offset.
	starts []int64
	offs   []int32

	// last is the span index of the most recent table hit — the cache
	// that makes clustered lookups O(1).
	last atomic.Int64
}

// UTC is the UTC sentinel: zero offset everywhere, no table, and every
// lookup short-circuits. LoadZone("UTC") returns exactly this pointer.
var UTC = &Zone{name: "UTC", loc: time.UTC, utc: true}

// LoadZone resolves name to a Zone. The validity rule is strict: name is
// exactly "UTC" (the UTC sentinel), or an IANA Area/Location name — it
// contains a '/', every '/'-separated segment opens with an ASCII
// upper-case letter and carries only letters, digits, '_', '-' and '+' —
// that time.LoadLocation resolves (the tz database is embedded, so this
// never depends on the host). "Etc/UTC", "Etc/GMT-5" and "Europe/Berlin"
// are accepted. Empty, "Local", abbreviations and legacy names without a
// '/' ("EST", "MST", "GMT", "EST5EDT") and offset strings ("+05:00") are
// refused with a PULSE_TIMEZONE_UNKNOWN *errors.CodedError whose details
// carry the name under errors.DetailTimeZone.
func LoadZone(name string) (*Zone, error) {
	if name == "UTC" {
		return UTC, nil
	}
	if !wellFormedZoneName(name) {
		return nil, unknownZone(name, "it is not \"UTC\" or an IANA Area/Location name")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, unknownZone(name, "it is not in the tz database")
	}
	z := &Zone{name: name, loc: loc}
	z.buildTable()
	return z, nil
}

// wellFormedZoneName reports whether name has the shape of an IANA
// Area/Location name. The shape check runs BEFORE time.LoadLocation so a
// host whose zoneinfo directory is case-insensitive or carries extra
// trees (posix/, right/) cannot widen the accepted set.
func wellFormedZoneName(name string) bool {
	if !strings.Contains(name, "/") {
		return false
	}
	for seg := range strings.SplitSeq(name, "/") {
		if seg == "" || seg[0] < 'A' || seg[0] > 'Z' {
			return false
		}
		for i := 1; i < len(seg); i++ {
			c := seg[i]
			switch {
			case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
				c == '_', c == '-', c == '+':
			default:
				return false
			}
		}
	}
	return true
}

func unknownZone(name, why string) error {
	return perr.NewCodedErrorWithDetails(
		perr.PULSE_TIMEZONE_UNKNOWN,
		fmt.Sprintf("unknown time zone %q: %s; use \"UTC\" or an IANA Area/Location name such as \"Europe/Berlin\" (fixed offsets: \"Etc/GMT-5\")", name, why),
		map[string]any{perr.DetailTimeZone: name},
	)
}

// buildTable walks Time.ZoneBounds across the window, recording one span
// per distinct offset run.
//
// ZoneBounds is not trusted blindly: in the rule-extended range (past the
// last explicit tzdata transition) the stdlib reports the leap-year
// year-end bound one day early, so for the final UTC day of a leap year
// the returned end is at or before the probe instant. When that happens
// the walk steps an hour at a time and bisects any offset change it
// crosses, so the table stays exact regardless.
func (z *Zone) buildTable() {
	sec := windowStart
	off := offsetAt(z.loc, sec)
	z.starts = append(z.starts, sec)
	z.offs = append(z.offs, off)
	for {
		_, end := time.Unix(sec, 0).In(z.loc).ZoneBounds()
		if end.IsZero() {
			return
		}
		next := end.Unix()
		if next <= sec {
			next = firstChange(z.loc, sec, sec+3600)
		}
		if next >= windowEnd {
			return
		}
		sec = next
		if o := offsetAt(z.loc, sec); o != off {
			off = o
			z.starts = append(z.starts, sec)
			z.offs = append(z.offs, off)
		}
	}
}

// offsetAt is the stdlib ground truth Offset is held equal to.
func offsetAt(loc *time.Location, sec int64) int32 {
	_, off := time.Unix(sec, 0).In(loc).Zone()
	return int32(off)
}

// firstChange returns the first instant in (lo, hi] whose offset differs
// from lo's, or hi when the offset is constant across the interval. It
// assumes at most one change in the interval (an hour, in practice).
func firstChange(loc *time.Location, lo, hi int64) int64 {
	base := offsetAt(loc, lo)
	if offsetAt(loc, hi) == base {
		return hi
	}
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if offsetAt(loc, mid) == base {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi
}

// Name returns the zone name as given to LoadZone.
func (z *Zone) Name() string { return z.name }

// Location returns the underlying *time.Location.
func (z *Zone) Location() *time.Location { return z.loc }

// Offset returns the zone's offset from UTC, in seconds east, in effect
// at instant sec (epoch seconds). It equals
// `_, off := time.Unix(sec, 0).In(loc).Zone()` everywhere, and is
// allocation-free: the UTC sentinel short-circuits, an instant in the
// span of the previous hit returns from the cache, other in-window
// instants binary-search the table, and out-of-window instants defer to
// the stdlib.
func (z *Zone) Offset(sec int64) int32 {
	if z.utc {
		return 0
	}
	if sec < windowStart || sec >= windowEnd {
		return offsetAt(z.loc, sec)
	}
	starts := z.starts
	n := len(starts)
	i := int(z.last.Load())
	if starts[i] <= sec && (i+1 == n || sec < starts[i+1]) {
		return z.offs[i]
	}
	lo := z.span(sec)
	z.last.Store(int64(lo))
	return z.offs[lo]
}
