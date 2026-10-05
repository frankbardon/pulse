package temporal

import (
	"math/rand/v2"
	"testing"
	"time"
)

// TestLocalHour_MatchesStdlib: the local hour of every sampled instant,
// read back through HourToTime, is the wall-clock date and hour
// time.Unix(s, 0).In(loc) shows — dense around every 1970–2040
// transition (both the repeated fall-back hour and the skipped spring
// hour), the half-hour and 45-minute zones, plus random instants.
func TestLocalHour_MatchesStdlib(t *testing.T) {
	for i, name := range append(append([]string{"UTC"}, localZones...), midnightZones...) {
		t.Run(name, func(t *testing.T) {
			z := UTC
			if name != "UTC" {
				z = mustZone(t, name)
			}
			rng := rand.New(rand.NewPCG(uint64(i)+40, 91))
			var samples []int64
			if z.utc {
				for range 20000 {
					samples = append(samples, rng.Int64N(1<<34)-1<<33)
				}
			} else {
				samples = propertySamples(z, rng)
			}
			bad := 0
			for _, s := range samples {
				h := LocalHour(s, z)
				want := time.Unix(s, 0).In(z.loc).Format("2006-01-02T15")
				if got := HourToTime(h).Format("2006-01-02T15"); got != want {
					t.Errorf("LocalHour(%d) reads %s, want %s", s, got, want)
					bad++
				}
				if got, wantDay := HourToDay(h), stdDay(z.loc, s); got != wantDay {
					t.Errorf("HourToDay(LocalHour(%d)) = %d, want local day %d", s, got, wantDay)
					bad++
				}
				if bad > 10 {
					t.Fatal("too many mismatches")
				}
			}
		})
	}
}

// TestLocalHour_BerlinTransitions pins the two DST behaviours GROUP_DATE
// `hour` documents: no instant reads 02:xx on 2026-03-29 (skipped), and
// both 02:xx hours of 2026-10-25 share one local hour (merged).
func TestLocalHour_BerlinTransitions(t *testing.T) {
	z := mustZone(t, "Europe/Berlin")
	seen := map[string]int{}
	from := time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC).Unix()
	for s := from; s < from+3*SecondsPerDay; s += 900 {
		seen[HourToTime(LocalHour(s, z)).Format("2006-01-02T15")]++
	}
	if n := seen["2026-03-29T02"]; n != 0 {
		t.Errorf("2026-03-29T02 read by %d quarter hours; the hour is skipped", n)
	}
	seen = map[string]int{}
	from = time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC).Unix()
	for s := from; s < from+3*SecondsPerDay; s += 900 {
		seen[HourToTime(LocalHour(s, z)).Format("2006-01-02T15")]++
	}
	if n := seen["2026-10-25T02"]; n != 8 {
		t.Errorf("2026-10-25T02 read by %d quarter hours, want 8 (the repeated hour merges)", n)
	}
}

// TestLocalHour_UTCNegativeFloor: before the epoch the hour truncates
// toward the past, never toward zero.
func TestLocalHour_UTCNegativeFloor(t *testing.T) {
	for _, c := range []struct{ sec, want int64 }{{-1, -1}, {-3600, -1}, {-3601, -2}, {0, 0}, {3599, 0}, {3600, 1}} {
		if got := LocalHour(c.sec, UTC); got != c.want {
			t.Errorf("LocalHour(%d, UTC) = %d, want %d", c.sec, got, c.want)
		}
	}
	if got := HourToDay(-1); got != -1 {
		t.Errorf("HourToDay(-1) = %d, want -1", got)
	}
}

// TestWeekStartDay: for every start weekday and days either side of the
// epoch, the result is the latest day on or before day whose weekday is
// start; Monday is the ISO week's Monday.
func TestWeekStartDay(t *testing.T) {
	for start := time.Sunday; start <= time.Saturday; start++ {
		for day := int64(-800); day <= 800; day++ {
			got := WeekStartDay(day, start)
			if wd := DayToTime(got).Weekday(); wd != start {
				t.Fatalf("WeekStartDay(%d, %s) = %d, a %s", day, start, got, wd)
			}
			if got > day || day-got > 6 {
				t.Fatalf("WeekStartDay(%d, %s) = %d, not the week holding day", day, start, got)
			}
			if start == time.Monday {
				y1, w1 := DayToTime(day).ISOWeek()
				y2, w2 := DayToTime(got).ISOWeek()
				if y1 != y2 || w1 != w2 {
					t.Fatalf("Monday start of day %d leaves its ISO week", day)
				}
			}
		}
	}
}

func TestParseWeekday(t *testing.T) {
	for wd := time.Sunday; wd <= time.Saturday; wd++ {
		name := map[time.Weekday]string{0: "sunday", 1: "monday", 2: "tuesday", 3: "wednesday", 4: "thursday", 5: "friday", 6: "saturday"}[wd]
		if got, ok := ParseWeekday(name); !ok || got != wd {
			t.Errorf("ParseWeekday(%q) = %v, %v", name, got, ok)
		}
	}
	for _, bad := range []string{"", "Monday", "mon", "sun", "funday"} {
		if _, ok := ParseWeekday(bad); ok {
			t.Errorf("ParseWeekday(%q) accepted", bad)
		}
	}
}

func TestLocalHour_AllocFree(t *testing.T) {
	z := mustZone(t, "Europe/Berlin")
	s := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC).Unix()
	if n := testing.AllocsPerRun(100, func() { _ = LocalHour(s, z) }); n != 0 {
		t.Errorf("LocalHour allocates %v per call", n)
	}
}
