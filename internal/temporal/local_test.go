package temporal

import (
	"math/rand/v2"
	"testing"
	"time"
)

// localZones are the zones the acceptance criteria name: two northern
// hour-offset DST zones, a half-hour zone without DST, a southern
// hemisphere DST zone and a 45-minute-offset southern DST zone.
var localZones = []string{
	"Europe/Berlin", "America/New_York", "Asia/Kolkata",
	"Australia/Sydney", "Pacific/Chatham",
	// Beyond the named five: a half-hour DST shift.
	"Australia/Lord_Howe",
}

// midnightZones transition AT local midnight (spring forward 00:00 →
// 01:00, fall back 24:00 → 23:00).
var midnightZones = []string{"America/Santiago", "America/Havana", "Asia/Beirut"}

func mustZone(tb testing.TB, name string) *Zone {
	tb.Helper()
	z, err := LoadZone(name)
	if err != nil {
		tb.Fatal(err)
	}
	return z
}

// stdDay is the local epoch day of s in loc, computed from time.In alone.
func stdDay(loc *time.Location, s int64) int64 {
	y, m, d := time.Unix(s, 0).In(loc).Date()
	// A UTC midnight, so the division is exact for either sign.
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}

func stdParts(loc *time.Location, s int64) Parts {
	t := time.Unix(s, 0).In(loc)
	var p Parts
	p.Year, p.Month, p.Day = t.Date()
	p.Hour, p.Minute, p.Second = t.Clock()
	p.Weekday = t.Weekday()
	p.YearDay = t.YearDay()
	p.ISOYear, p.ISOWeek = t.ISOWeek()
	return p
}

// propertySamples returns instants densely sampled ±48h around every
// offset transition 1970–2040 (every 15 minutes and the second before
// each, which hits every local midnight in quarter-hour-offset zones),
// a few seconds either side of each transition, plus seeded random
// instants 1850–2200 (both table-fallback sides included).
func propertySamples(z *Zone, rng *rand.Rand) []int64 {
	from := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	to := time.Date(2041, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	var out []int64
	for _, tr := range z.starts[1:] {
		if tr < from || tr >= to {
			continue
		}
		for _, d := range []int64{-3601, -3600, -2, -1, 0, 1, 2, 3599, 3600} {
			out = append(out, tr+d)
		}
		for k := int64(-192); k <= 192; k++ {
			out = append(out, tr+k*900, tr+k*900-1)
		}
	}
	lo := time.Date(1850, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	hi := time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	for range 20000 {
		out = append(out, lo+rng.Int64N(hi-lo))
	}
	return out
}

// TestLocalDayAndParts_MatchStdlib is the DST property test: LocalDay and
// every LocalParts field equal time.Unix(s, 0).In(loc) on dense samples
// around every transition 1970–2040 plus random instants.
func TestLocalDayAndParts_MatchStdlib(t *testing.T) {
	for i, name := range append(append([]string{}, localZones...), midnightZones...) {
		t.Run(name, func(t *testing.T) {
			z := mustZone(t, name)
			rng := rand.New(rand.NewPCG(uint64(i)+10, 77))
			samples := propertySamples(z, rng)
			bad := 0
			for _, s := range samples {
				if got, want := LocalDay(s, z), stdDay(z.loc, s); got != want {
					t.Errorf("LocalDay(%d) = %d, want %d", s, got, want)
					bad++
				}
				if got, want := LocalParts(s, z), stdParts(z.loc, s); got != want {
					t.Errorf("LocalParts(%d) = %+v, want %+v", s, got, want)
					bad++
				}
				if bad > 10 {
					t.Fatal("too many mismatches")
				}
			}
			if name != "Asia/Kolkata" && len(samples) < 30000 {
				t.Errorf("only %d samples: transitions 1970–2040 not enumerated", len(samples))
			}
		})
	}
}

// TestLocalDay_UTC pins the UTC sentinel to DateTimeToDay, negatives and
// exact midnights included.
func TestLocalDay_UTC(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	samples := []int64{-86401, -86400, -86399, -1, 0, 1, 86399, 86400}
	for range 20000 {
		samples = append(samples, rng.Int64N(1<<40)-(1<<39))
	}
	for _, s := range samples {
		if got, want := LocalDay(s, UTC), DateTimeToDay(s); got != want {
			t.Fatalf("LocalDay(%d, UTC) = %d, want %d", s, got, want)
		}
		if got, want := LocalParts(s, UTC), stdParts(time.UTC, s); got != want {
			t.Fatalf("LocalParts(%d, UTC) = %+v, want %+v", s, got, want)
		}
	}
	for _, d := range []int64{-719162, -1, 0, 1, 19000, 2932896} {
		if got := LocalMidnightUTC(d, UTC); got != d*86400 {
			t.Errorf("LocalMidnightUTC(%d, UTC) = %d, want %d", d, got, d*86400)
		}
	}
}

// TestLocalDay_NegativeOffsetFloor: before 1970 in a west-of-UTC zone the
// shifted instant is negative and the day must floor toward the past.
func TestLocalDay_NegativeOffsetFloor(t *testing.T) {
	ny := mustZone(t, "America/New_York")
	// 1970-01-01T04:59:59Z is 1969-12-31T23:59:59-05:00: local day -1.
	if got := LocalDay(4*3600+3599, ny); got != -1 {
		t.Errorf("LocalDay = %d, want -1", got)
	}
	// 1970-01-01T05:00:00Z is local midnight, day 0.
	if got := LocalDay(5*3600, ny); got != 0 {
		t.Errorf("LocalDay = %d, want 0", got)
	}
}

// refMidnight is a brute-force LocalMidnightUTC from time.In alone: the
// first minute whose local day is >= day, then a bisect inside it.
func refMidnight(loc *time.Location, day int64) int64 {
	s := day*86400 - 30*3600
	for stdDay(loc, s) < day {
		s += 60
	}
	lo, hi := s-60, s
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if stdDay(loc, mid) >= day {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// TestLocalMidnightUTC_Invariants checks, for sampled days in every
// property and midnight-transition zone, the brute-force answer and both
// round-trip invariants. Days around every transition 1970–2040 are
// included, plus days on both sides of the table window.
func TestLocalMidnightUTC_Invariants(t *testing.T) {
	edges := []int64{
		stdDayUTC(1850, 6, 1), stdDayUTC(1899, 12, 31), stdDayUTC(1900, 1, 1),
		stdDayUTC(2099, 12, 31), stdDayUTC(2100, 1, 1), stdDayUTC(2150, 3, 28),
		stdDayUTC(2150, 10, 31),
	}
	for i, name := range append(append([]string{}, localZones...), midnightZones...) {
		t.Run(name, func(t *testing.T) {
			z := mustZone(t, name)
			rng := rand.New(rand.NewPCG(uint64(i)+40, 3))
			days := append([]int64{}, edges...)
			for _, tr := range z.starts[1:] {
				if tr >= 0 && tr < stdDayUTC(2041, 1, 1)*86400 {
					d := DateTimeToDay(tr)
					days = append(days, d-1, d, d+1)
				}
			}
			for range 300 {
				days = append(days, stdDayUTC(1850, 1, 1)+rng.Int64N(350*365))
			}
			for _, d := range days {
				m := LocalMidnightUTC(d, z)
				if want := refMidnight(z.loc, d); m != want {
					t.Fatalf("LocalMidnightUTC(%d) = %d, want %d", d, m, want)
				}
				if got := LocalDay(m, z); got != d {
					t.Fatalf("LocalDay(LocalMidnightUTC(%d)) = %d", d, got)
				}
				if LocalDay(m-1, z) >= d {
					t.Fatalf("LocalMidnightUTC(%d) = %d is not the first instant", d, m)
				}
			}
			for range 5000 {
				s := stdDayUTC(1850, 1, 1)*86400 + rng.Int64N(350*365*86400)
				if m := LocalMidnightUTC(LocalDay(s, z), z); m > s {
					t.Fatalf("LocalMidnightUTC(LocalDay(%d)) = %d > s", s, m)
				}
			}
		})
	}
}

func stdDayUTC(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}

// TestLocalMidnightUTC_DSTAtMidnight pins the documented rules on known
// transitions: a skipped midnight returns the transition instant (wall
// 01:00), a fall-back from 24:00 starts the day after the repeated hour,
// and an entirely skipped day yields the next existing day's start.
func TestLocalMidnightUTC_DSTAtMidnight(t *testing.T) {
	ts := func(s string) int64 {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v.Unix()
	}
	cases := []struct {
		zone     string
		y        int
		m        time.Month
		d        int
		want     string
		wantHour int
		wantDay  int64 // offset of LocalDay(result) from the asked day
	}{
		{"America/Santiago", 2022, 9, 11, "2022-09-11T04:00:00Z", 1, 0},
		{"America/Santiago", 2022, 4, 3, "2022-04-03T04:00:00Z", 0, 0},
		{"America/Havana", 2023, 3, 12, "2023-03-12T05:00:00Z", 1, 0},
		{"Asia/Beirut", 1995, 3, 26, "1995-03-25T22:00:00Z", 1, 0},
		{"Pacific/Apia", 2011, 12, 30, "2011-12-30T10:00:00Z", 0, 1},
		{"Europe/Berlin", 2022, 3, 27, "2022-03-26T23:00:00Z", 0, 0},
	}
	for _, c := range cases {
		z := mustZone(t, c.zone)
		d := stdDayUTC(c.y, c.m, c.d)
		got := LocalMidnightUTC(d, z)
		if want := ts(c.want); got != want {
			t.Errorf("%s %d-%02d-%02d: LocalMidnightUTC = %s, want %s",
				c.zone, c.y, c.m, c.d, time.Unix(got, 0).UTC().Format(time.RFC3339), c.want)
			continue
		}
		p := LocalParts(got, z)
		if p.Hour != c.wantHour || p.Minute != 0 || p.Second != 0 {
			t.Errorf("%s: wall at result = %02d:%02d:%02d, want %02d:00:00", c.zone, p.Hour, p.Minute, p.Second, c.wantHour)
		}
		if LocalDay(got, z)-d != c.wantDay {
			t.Errorf("%s: LocalDay(result) - d = %d, want %d", c.zone, LocalDay(got, z)-d, c.wantDay)
		}
	}
}

// TestNextChange_OutOfWindow covers the stdlib probe on both sides of the
// table window, including a change exactly at an hour-probe boundary.
func TestNextChange_OutOfWindow(t *testing.T) {
	ny := mustZone(t, "America/New_York")
	// 2150-03-08 is the second Sunday of March: DST begins 07:00Z.
	from := time.Date(2150, 3, 7, 0, 0, 0, 0, time.UTC).Unix()
	want := time.Date(2150, 3, 8, 7, 0, 0, 0, time.UTC).Unix()
	if got := ny.nextChange(from, from+10*86400); got != want {
		t.Errorf("nextChange = %s, want %s", time.Unix(got, 0).UTC(), time.Unix(want, 0).UTC())
	}
	// Constant across the interval: hi.
	if got := ny.nextChange(from, from+3*3600+17); got != from+3*3600+17 {
		t.Errorf("nextChange constant = %d", got)
	}
	// Before the window, probing runs up to windowStart and into the table.
	pre := windowStart - 5*86400
	if got, want := ny.nextChange(pre, windowEnd), ny.starts[1]; got != want {
		t.Errorf("nextChange(pre-window) = %d, want %d", got, want)
	}
	// From inside the final span, probing resumes past windowEnd.
	last := ny.starts[len(ny.starts)-1]
	if got := ny.nextChange(last, windowEnd+400*86400); got <= windowEnd || offsetAt(ny.loc, got) == offsetAt(ny.loc, got-1) {
		t.Errorf("nextChange(last span) = %d, want first post-window change", got)
	}
}

// TestNextChange_AtWindowEnd pins the resume-from-the-last-in-window-
// second rule. No real zone changes offset exactly at windowEnd, so the
// test moves windowEnd onto a real New York transition and rebuilds the
// table, which then ends just before it.
func TestNextChange_AtWindowEnd(t *testing.T) {
	ny := mustZone(t, "America/New_York")
	tr := time.Date(2050, 3, 13, 7, 0, 0, 0, time.UTC).Unix()
	if offsetAt(ny.loc, tr) == offsetAt(ny.loc, tr-1) {
		t.Fatal("fixture: no transition at 2050-03-13T07:00Z")
	}
	saved := windowEnd
	windowEnd = tr
	defer func() { windowEnd = saved }()
	z := mustZone(t, "America/New_York")
	if got := z.nextChange(tr-86400, tr+10*3600); got != tr {
		t.Errorf("nextChange = %d, want the transition at windowEnd %d", got, tr)
	}
}

func TestLocal_AllocFree(t *testing.T) {
	z := mustZone(t, "Europe/Berlin")
	s := int64(1_650_000_000)
	var sink int64
	allocs := testing.AllocsPerRun(1000, func() {
		sink += LocalDay(s, z) + LocalDay(s, UTC)
		p := LocalParts(s, z)
		sink += int64(p.ISOWeek)
		sink += LocalMidnightUTC(19000, z)
	})
	if allocs != 0 {
		t.Errorf("allocs/op = %v, want 0", allocs)
	}
	_ = sink
}

var (
	sinkDay   int64
	sinkParts Parts
)

func BenchmarkLocalDay(b *testing.B) {
	z := mustZone(b, "Europe/Berlin")
	base := int64(1_650_000_000)
	b.Run("zone-clustered", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			sinkDay = LocalDay(base+int64(i%86400), z)
		}
	})
	b.Run("zone-random", func(b *testing.B) {
		rng := rand.New(rand.NewPCG(8, 9))
		inst := make([]int64, 4096)
		for i := range inst {
			inst[i] = windowStart + rng.Int64N(windowEnd-windowStart)
		}
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			sinkDay = LocalDay(inst[i&4095], z)
		}
	})
	b.Run("utc", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			sinkDay = LocalDay(base+int64(i%86400), UTC)
		}
	})
	b.Run("DateTimeToDay", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			sinkDay = DateTimeToDay(base + int64(i%86400))
		}
	})
}

func BenchmarkLocalParts(b *testing.B) {
	z := mustZone(b, "Europe/Berlin")
	base := int64(1_650_000_000)
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		sinkParts = LocalParts(base+int64(i%86400), z)
	}
}

func BenchmarkLocalMidnightUTC(b *testing.B) {
	z := mustZone(b, "Europe/Berlin")
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		sinkDay = LocalMidnightUTC(19000+int64(i%365), z)
	}
}
