package temporal

import "time"

// Local-day primitives: the zone-aware counterparts of DateTimeToDay and
// DayToUnix. A `datetime` cell is an instant (epoch seconds); its LOCAL
// day in zone z is the calendar day a wall clock in z shows at that
// instant, counted as epoch days of that wall-clock date. Every function
// here takes the UTC sentinel's fast path, so UTC callers pay nothing for
// zone awareness. A nil *Zone is not accepted; pass UTC.

// LocalDay returns the local epoch day of instant sec (epoch seconds) in
// zone z: floor((sec + z.Offset(sec)) / SecondsPerDay), truncating toward
// the PAST exactly as DateTimeToDay does. LocalDay(sec, UTC) ==
// DateTimeToDay(sec). Allocation-free.
func LocalDay(sec int64, z *Zone) int64 {
	if z.utc {
		return DateTimeToDay(sec)
	}
	return DateTimeToDay(sec + int64(z.Offset(sec)))
}

// LocalMidnightUTC returns the UTC instant (epoch seconds) at which local
// epoch day day begins in zone z: the FIRST instant s whose wall clock in
// z reads day 00:00:00 or later. Under UTC it is day * SecondsPerDay.
//
// Where a DST transition skips local midnight (America/Santiago,
// America/Havana and, historically, Asia/Beirut spring forward from 00:00
// to 01:00), there is no instant reading 00:00, and the result is the
// first instant that exists on that local day — the transition instant,
// whose wall clock reads 01:00. Where a transition repeats the hour
// before midnight (a fall-back from 24:00 to 23:00), the day begins at
// the single instant reading 00:00, after the repeated hour.
//
// Invariants: LocalMidnightUTC(LocalDay(s, z), z) <= s for every s, and
// LocalDay(LocalMidnightUTC(d, z), z) == d for every local day d that has
// at least one instant. A day that a zone skips ENTIRELY (Pacific/Apia,
// 2011-12-30) has no instant; the result is then the first instant of the
// next day that exists, so LocalDay of it is greater than d.
func LocalMidnightUTC(day int64, z *Zone) int64 {
	wall := day * SecondsPerDay
	if z.utc {
		return wall
	}
	// Within one offset span [a, b) the wall clock s + off increases with
	// s, so the first instant of the span reading wall or later is
	// max(a, wall - off). Spans are visited in time order, so the first
	// span holding such an instant holds the answer. Every tz offset is
	// well under a day, so the answer lies in [wall-2d, wall+2d) and the
	// final span (which reaches hi) always holds it.
	lo, hi := wall-2*SecondsPerDay, wall+2*SecondsPerDay
	a := lo
	for {
		b := z.nextChange(a, hi)
		c := max(a, wall-int64(z.Offset(a)))
		if c < b || b >= hi {
			return c
		}
		a = b
	}
}

// nextChange returns the first instant in (sec, hi] at which z's offset
// differs from its offset at sec, or hi when it is constant across that
// interval. Inside the precomputed window it reads the table; outside it
// probes the stdlib an hour at a time and bisects the hour that changes
// (tz transitions are always more than an hour apart).
func (z *Zone) nextChange(sec, hi int64) int64 {
	base := z.Offset(sec)
	s := sec
	for s < hi {
		if s >= windowStart && s < windowEnd {
			if i := z.span(s); i+1 < len(z.starts) {
				return min(z.starts[i+1], hi)
			}
			// The last span runs to windowEnd; resume probing from the
			// final in-window second so a change AT windowEnd is seen.
			s = windowEnd - 1
		}
		n := min(s+3600, hi)
		if s < windowStart {
			n = min(n, windowStart)
		}
		if offsetAt(z.loc, n) != base {
			return firstChange(z.loc, s, n)
		}
		s = n
	}
	return hi
}

// span returns the index of the table span holding in-window instant
// sec: the largest i with starts[i] <= sec (starts[0] == windowStart).
func (z *Zone) span(sec int64) int {
	starts := z.starts
	lo, hi := 0, len(starts)
	for hi-lo > 1 {
		mid := int(uint(lo+hi) >> 1)
		if starts[mid] <= sec {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// Parts is the wall-clock decomposition of an instant in a zone.
type Parts struct {
	Year    int
	Month   time.Month
	Day     int // day of month, 1..31
	Hour    int // 0..23
	Minute  int // 0..59
	Second  int // 0..59
	Weekday time.Weekday
	YearDay int // day of year, 1..366
	ISOYear int // ISO 8601 week-numbering year
	ISOWeek int // ISO 8601 week, 1..53
}

// LocalParts returns the wall-clock fields of instant sec (epoch seconds)
// in zone z. Every field equals what time.Unix(sec, 0).In(z.Location())
// reports. Allocation-free.
func LocalParts(sec int64, z *Zone) Parts {
	local := sec
	if !z.utc {
		local += int64(z.Offset(sec))
	}
	t := time.Unix(local, 0).UTC()
	var p Parts
	p.Year, p.Month, p.Day = t.Date()
	p.Hour, p.Minute, p.Second = t.Clock()
	p.Weekday = t.Weekday()
	p.YearDay = t.YearDay()
	p.ISOYear, p.ISOWeek = t.ISOWeek()
	return p
}

// LocalHour returns the local epoch HOUR of instant sec in zone z:
// floor((sec + z.Offset(sec)) / SecondsPerHour), the wall-clock hour a
// clock in z shows, counted as hours since 1970-01-01T00 on that clock.
// A fall-back transition repeats a wall-clock hour, so the two instants
// an hour apart that read it share one local hour; a spring-forward
// transition skips one, which no instant reads. LocalHour(sec, UTC) is
// the UTC hour. Allocation-free.
func LocalHour(sec int64, z *Zone) int64 {
	if !z.utc {
		sec += int64(z.Offset(sec))
	}
	h := sec / SecondsPerHour
	if sec < 0 && sec%SecondsPerHour != 0 {
		h--
	}
	return h
}
