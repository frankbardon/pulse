package temporal

import (
	"testing"
	"time"
)

// TestFormatLocal_Literals pins the rendered literal for the acceptance
// zones, across Berlin's 2026 spring-forward and fall-back.
func TestFormatLocal_Literals(t *testing.T) {
	at := func(s string) int64 {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return tm.Unix()
	}
	cases := []struct {
		zone, utc, want string
	}{
		{"Asia/Kolkata", "2026-03-29T02:30:00Z", "2026-03-29T08:00:00+05:30"},
		{"Europe/Berlin", "2026-03-29T00:59:59Z", "2026-03-29T01:59:59+01:00"},
		{"Europe/Berlin", "2026-03-29T01:00:00Z", "2026-03-29T03:00:00+02:00"},
		{"Europe/Berlin", "2026-03-29T01:30:00Z", "2026-03-29T03:30:00+02:00"},
		// Fall-back: 02:30 local is read twice — the offset tells them apart.
		{"Europe/Berlin", "2026-10-25T00:30:00Z", "2026-10-25T02:30:00+02:00"},
		{"Europe/Berlin", "2026-10-25T01:30:00Z", "2026-10-25T02:30:00+01:00"},
		{"America/New_York", "2026-01-15T03:00:00Z", "2026-01-14T22:00:00-05:00"},
		{"America/St_Johns", "2026-01-15T03:00:00Z", "2026-01-14T23:30:00-03:30"},
		{"Pacific/Chatham", "2026-01-15T00:00:00Z", "2026-01-15T13:45:00+13:45"},
		// Pre-1970 instant keeps its real calendar time.
		{"Europe/Berlin", "1960-06-01T12:00:00Z", "1960-06-01T13:00:00+01:00"},
		// UTC and UTC-equivalent zones render the canonical Z form.
		{"UTC", "2026-03-29T01:30:00Z", "2026-03-29T01:30:00Z"},
		{"Etc/UTC", "2026-03-29T01:30:00Z", "2026-03-29T01:30:00Z"},
		// A seconds-bearing LMT offset (+00:53:28) has no RFC 3339 spelling.
		{"Europe/Berlin", "1890-01-01T00:00:00Z", "1890-01-01T00:00:00Z"},
	}
	for _, tc := range cases {
		got := FormatLocal(at(tc.utc), mustZone(t, tc.zone))
		if got != tc.want {
			t.Errorf("FormatLocal(%s, %s) = %s, want %s", tc.utc, tc.zone, got, tc.want)
		}
	}
	if got := FormatLocal(at("2026-03-29T01:30:00Z"), nil); got != "2026-03-29T01:30:00Z" {
		t.Errorf("nil zone = %s, want the canonical Z form", got)
	}
}

// TestFormatLocal_MatchesStdlibAndRoundTrips sweeps every quarter hour
// of 2026 in zones with DST, half-hour and negative offsets: the literal
// equals time.In's RFC 3339 rendering and re-parses to the same instant.
func TestFormatLocal_MatchesStdlibAndRoundTrips(t *testing.T) {
	for _, name := range []string{"Europe/Berlin", "Asia/Kolkata", "Australia/Sydney", "America/Santiago", "America/St_Johns", "Australia/Lord_Howe"} {
		z := mustZone(t, name)
		from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
		to := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
		for s := from; s < to; s += 900 {
			got := FormatLocal(s, z)
			if want := time.Unix(s, 0).In(z.Location()).Format(time.RFC3339); got != want {
				t.Fatalf("%s @%d: FormatLocal = %s, time.In = %s", name, s, got, want)
			}
			back, err := time.Parse(time.RFC3339, got)
			if err != nil || back.Unix() != s {
				t.Fatalf("%s @%d: %s re-parses to %d (%v)", name, s, got, back.Unix(), err)
			}
		}
	}
}
