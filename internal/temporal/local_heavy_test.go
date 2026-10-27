//go:build heavy

package temporal

import (
	"math/rand/v2"
	"testing"
)

// TestLocalMidnightUTC_Invariants checks, for sampled days in every
// property and midnight-transition zone, the brute-force answer and both
// round-trip invariants. Days around every transition 1970–2040 are
// included, plus days on both sides of the table window.
func TestLocalMidnightUTC_Invariants(t *testing.T) {
	t.Parallel()

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
