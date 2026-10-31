package temporal

import (
	"testing"
	"time"
)

// TestParseWall: a bare date reads its midnight (dateOnly), a naive
// datetime its reading, and an offset-bearing or malformed literal is
// refused — a bound is a clock reading, never an instant.
func TestParseWall(t *testing.T) {
	t.Parallel()

	at := func(s string) int64 {
		tm, err := time.Parse("2006-01-02T15:04:05", s)
		if err != nil {
			t.Fatal(err)
		}
		return tm.Unix()
	}
	for _, c := range []struct {
		in       string
		wall     int64
		dateOnly bool
		ok       bool
	}{
		{"2026-05-12", at("2026-05-12T00:00:00"), true, true},
		{"2026-05-12T09:30:00", at("2026-05-12T09:30:00"), false, true},
		{"2026-05-12T09:30", at("2026-05-12T09:30:00"), false, true},
		{"2026-05-12 17:00:05", at("2026-05-12T17:00:05"), false, true},
		{"2026-05-12 17:00", at("2026-05-12T17:00:00"), false, true},
		{"2026-05-12T09:30:00Z", 0, false, false},
		{"2026-05-12T09:30:00+02:00", 0, false, false},
		{"May 12", 0, false, false},
	} {
		wall, dateOnly, ok := ParseWall(c.in)
		if ok != c.ok || (ok && (wall != c.wall || dateOnly != c.dateOnly)) {
			t.Errorf("ParseWall(%q) = (%d, %v, %v), want (%d, %v, %v)", c.in, wall, dateOnly, ok, c.wall, c.dateOnly, c.ok)
		}
	}
}

// TestLocalWall: the reading a clock in the zone shows, matching
// time.In, across Sydney's 2026 DST end; nil and UTC are the identity.
func TestLocalWall(t *testing.T) {
	t.Parallel()

	z, err := LoadZone("Australia/Sydney")
	if err != nil {
		t.Fatal(err)
	}
	loc := z.Location()
	start := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC).Unix()
	for s := start; s < start+86400; s += 1800 {
		l := time.Unix(s, 0).In(loc)
		want := time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), 0, time.UTC).Unix()
		if got := LocalWall(s, z); got != want {
			t.Fatalf("LocalWall(%d) = %d, want %d", s, got, want)
		}
		if LocalWall(s, nil) != s || LocalWall(s, UTC) != s {
			t.Fatalf("LocalWall(%d) under nil / UTC is not the identity", s)
		}
	}
}

// TestDayEndWallAndElapsedDays pins the two day helpers.
func TestDayEndWallAndElapsedDays(t *testing.T) {
	t.Parallel()

	if got := DayEndWall(1); got != 2*86400-1 {
		t.Errorf("DayEndWall(1) = %d", got)
	}
	if got := DayEndWall(-1); got != -1 {
		t.Errorf("DayEndWall(-1) = %d", got)
	}
	if got := ElapsedDays(86400, 86400*3+43200); got != 2.5 {
		t.Errorf("ElapsedDays = %v, want 2.5", got)
	}
}
