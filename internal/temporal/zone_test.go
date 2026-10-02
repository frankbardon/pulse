package temporal

import (
	stderrors "errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
)

// TestLoadZone_Accept pins the accepted half of the validity rule:
// exactly "UTC", or an Area/Location name resolved from embedded tzdata.
func TestLoadZone_Accept(t *testing.T) {
	for _, name := range []string{
		"UTC",
		"Etc/UTC",
		"Etc/GMT-5",
		"Etc/GMT+8",
		"Europe/Berlin",
		"America/New_York",
		"America/Argentina/Buenos_Aires",
		"America/Port-au-Prince",
		"Australia/Lord_Howe",
		"Asia/Kolkata",
	} {
		t.Run(name, func(t *testing.T) {
			z, err := LoadZone(name)
			if err != nil {
				t.Fatalf("LoadZone(%q) error = %v", name, err)
			}
			if z.Name() != name {
				t.Errorf("Name() = %q, want %q", z.Name(), name)
			}
			if z.Location() == nil {
				t.Errorf("Location() = nil")
			}
		})
	}
}

// TestLoadZone_UTCIsSentinel asserts "UTC" short-circuits to the shared
// sentinel rather than building a table.
func TestLoadZone_UTCIsSentinel(t *testing.T) {
	z, err := LoadZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	if z != UTC {
		t.Fatalf("LoadZone(\"UTC\") did not return the UTC sentinel")
	}
	if UTC.Location() != time.UTC {
		t.Errorf("UTC.Location() = %v, want time.UTC", UTC.Location())
	}
	for _, s := range []int64{-1 << 40, -1, 0, 1, 1 << 40} {
		if got := UTC.Offset(s); got != 0 {
			t.Errorf("UTC.Offset(%d) = %d, want 0", s, got)
		}
	}
}

// TestLoadZone_Reject pins the refused half: every listed example is a
// PULSE_TIMEZONE_UNKNOWN CodedError carrying the name under `tz`.
func TestLoadZone_Reject(t *testing.T) {
	for _, name := range []string{
		"",
		"Local",
		"EST",
		"MST",
		"GMT",
		"EST5EDT",
		"utc",
		"+05:00",
		"-0800",
		"UTC+5",
		"Europe/Nowhere",
		"europe/berlin",
		"Europe/",
		"/Europe/Berlin",
		"Europe/../Etc/UTC",
		"posix/Europe/Berlin",
		"Europe/Berlin ",
	} {
		t.Run(name, func(t *testing.T) {
			z, err := LoadZone(name)
			if err == nil {
				t.Fatalf("LoadZone(%q) = %v, want error", name, z.Name())
			}
			if z != nil {
				t.Errorf("LoadZone(%q) returned non-nil zone with error", name)
			}
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("error %T is not *errors.CodedError", err)
			}
			if ce.Code != perr.PULSE_TIMEZONE_UNKNOWN {
				t.Errorf("Code = %s, want PULSE_TIMEZONE_UNKNOWN", ce.Code)
			}
			if got := ce.Details[perr.DetailTimeZone]; got != name {
				t.Errorf("Details[tz] = %v, want %q", got, name)
			}
		})
	}
}

// propertyZones are the zones the Offset property runs across: DST in
// both hemispheres, a 30-minute and a 45-minute offset, a half-hour DST
// shift (Lord Howe), a fixed Etc zone and Etc/UTC.
var propertyZones = []string{
	"America/New_York",
	"Europe/Berlin",
	"Australia/Sydney",
	"Australia/Lord_Howe",
	"Asia/Kolkata",
	"Asia/Kathmandu",
	"Etc/GMT-5",
	"Etc/UTC",
}

func wantOffset(loc *time.Location, sec int64) int32 {
	_, off := time.Unix(sec, 0).In(loc).Zone()
	return int32(off)
}

// TestZoneOffset_MatchesStdlib is the property: Offset equals the stdlib
// answer on randomized instants 1850–2200 (both fallback sides of the
// 1900–2100 table window), on every transition instant and its
// neighbours, and on the window edges themselves.
func TestZoneOffset_MatchesStdlib(t *testing.T) {
	lo := time.Date(1850, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	hi := time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	rng := rand.New(rand.NewPCG(1, 2))
	for _, name := range propertyZones {
		t.Run(name, func(t *testing.T) {
			z, err := LoadZone(name)
			if err != nil {
				t.Fatal(err)
			}
			loc := z.Location()
			check := func(s int64) {
				t.Helper()
				if got, want := z.Offset(s), wantOffset(loc, s); got != want {
					t.Fatalf("Offset(%d = %s) = %d, want %d", s, time.Unix(s, 0).UTC(), got, want)
				}
			}
			// Random, then clustered (cache-hit-heavy) walks.
			for range 20000 {
				check(lo + rng.Int64N(hi-lo))
			}
			for s := int64(1_600_000_000); s < 1_700_000_000; s += 3607 {
				check(s)
			}
			for s := int64(1_700_000_000); s > 1_600_000_000; s -= 7919 {
				check(s)
			}
			// Every transition and its neighbours, alternating far
			// jumps so the cache is stale on each probe.
			for i, st := range z.starts {
				for _, d := range []int64{-1, 0, 1} {
					check(st + d)
					check(z.starts[(i*7)%len(z.starts)])
				}
			}
			// Window edges.
			for _, e := range []int64{windowStart, windowEnd} {
				for _, d := range []int64{-SecondsPerDay, -1, 0, 1, SecondsPerDay} {
					check(e + d)
				}
			}
		})
	}
}

// TestZoneOffset_TableShape asserts the transition table is sorted,
// starts at the window start and is parallel, so the binary search and
// the cache bounds check have something well-formed to work over.
func TestZoneOffset_TableShape(t *testing.T) {
	z, err := LoadZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if len(z.starts) != len(z.offs) || len(z.starts) < 300 {
		t.Fatalf("table len starts=%d offs=%d", len(z.starts), len(z.offs))
	}
	if z.starts[0] != windowStart {
		t.Errorf("starts[0] = %d, want windowStart %d", z.starts[0], windowStart)
	}
	for i := 1; i < len(z.starts); i++ {
		if z.starts[i] <= z.starts[i-1] {
			t.Fatalf("starts not strictly increasing at %d", i)
		}
		if z.offs[i] == z.offs[i-1] {
			t.Fatalf("adjacent spans %d,%d share offset %d (not merged)", i-1, i, z.offs[i])
		}
		if z.starts[i] >= windowEnd {
			t.Fatalf("start %d past window end", z.starts[i])
		}
	}
	fixed, err := LoadZone("Etc/GMT-5")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed.starts) != 1 || fixed.offs[0] != 5*3600 {
		t.Errorf("Etc/GMT-5 table = %v / %v, want one span at +18000", fixed.starts, fixed.offs)
	}
}

// TestZoneOffset_Concurrent hammers one shared *Zone from many
// goroutines with disjoint instant streams; run under -race the shared
// last-span cache must be clean, and every answer must still be right.
func TestZoneOffset_Concurrent(t *testing.T) {
	z, err := LoadZone("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	loc := z.Location()
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for g := range 16 {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, seed+1))
			for range 5000 {
				s := windowStart + rng.Int64N(windowEnd-windowStart)
				if got, want := z.Offset(s), wantOffset(loc, s); got != want {
					errs <- time.Unix(s, 0).UTC().String()
					return
				}
			}
		}(uint64(g))
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent Offset mismatch at %s", e)
	}
}

func TestZoneOffset_AllocFree(t *testing.T) {
	z, err := LoadZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	s := int64(1_650_000_000)
	far := int64(100_000_000)
	pre := windowStart - 10*SecondsPerDay
	allocs := testing.AllocsPerRun(1000, func() {
		_ = z.Offset(s)
		_ = z.Offset(far)
		_ = z.Offset(pre)
		_ = UTC.Offset(s)
	})
	if allocs != 0 {
		t.Errorf("Offset allocs/op = %v, want 0", allocs)
	}
}

var sinkOffset int32

func BenchmarkZoneOffset(b *testing.B) {
	z, err := LoadZone("America/New_York")
	if err != nil {
		b.Fatal(err)
	}
	b.Run("clustered", func(b *testing.B) {
		b.ReportAllocs()
		base := int64(1_650_000_000)
		for i := 0; b.Loop(); i++ {
			sinkOffset = z.Offset(base + int64(i%86400))
		}
	})
	b.Run("random", func(b *testing.B) {
		rng := rand.New(rand.NewPCG(3, 4))
		inst := make([]int64, 4096)
		for i := range inst {
			inst[i] = windowStart + rng.Int64N(windowEnd-windowStart)
		}
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			sinkOffset = z.Offset(inst[i&4095])
		}
	})
	b.Run("fallback", func(b *testing.B) {
		b.ReportAllocs()
		pre := windowStart - 1000*SecondsPerDay
		for i := 0; b.Loop(); i++ {
			sinkOffset = z.Offset(pre + int64(i%86400))
		}
	})
	b.Run("utc", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			sinkOffset = UTC.Offset(int64(i))
		}
	})
}

// TestFirstChange pins the bisect the table walk falls back on when the
// stdlib's ZoneBounds reports a degenerate (non-advancing) end.
func TestFirstChange(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 2041-03-10T07:00:00Z is the spring-forward instant (02:00 EST).
	tr := time.Date(2041, 3, 10, 7, 0, 0, 0, time.UTC).Unix()
	if got := firstChange(loc, tr-1800, tr+1800); got != tr {
		t.Errorf("firstChange across transition = %d, want %d", got, tr)
	}
	if got := firstChange(loc, tr-1, tr); got != tr {
		t.Errorf("firstChange at transition edge = %d, want %d", got, tr)
	}
	if got := firstChange(loc, tr+10, tr+3610); got != tr+3610 {
		t.Errorf("firstChange with no change = %d, want hi", got)
	}
}

// TestZoneOffset_LeapYearEnd pins the stdlib ZoneBounds defect the walk
// works around: in the rule-extended range the final UTC day of a leap
// year reports a non-advancing end. The table must carry on past it.
func TestZoneOffset_LeapYearEnd(t *testing.T) {
	z, err := LoadZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	last := z.starts[len(z.starts)-1]
	if floor := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix(); last < floor {
		t.Fatalf("table ends at %s, want a 2099 transition", time.Unix(last, 0).UTC())
	}
}
