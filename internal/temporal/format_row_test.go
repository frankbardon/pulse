package temporal

import "testing"

// TestFormatDay pins the calendar literal a `date` cell renders as in a
// caller-facing row (Sample, Lookup): ISO 8601 YYYY-MM-DD, pre-epoch
// days included.
func TestFormatDay(t *testing.T) {
	cases := []struct {
		day  int64
		want string
	}{
		{0, "1970-01-01"},
		{-1, "1969-12-31"},
		{20454, "2026-01-01"},
		{-719162, "0001-01-01"},
	}
	for _, c := range cases {
		if got := FormatDay(c.day); got != c.want {
			t.Errorf("FormatDay(%d) = %q, want %q", c.day, got, c.want)
		}
	}
}

// TestFormatUTC pins the RFC 3339 UTC literal a `datetime` cell renders
// as in a caller-facing row: always the `…Z` form, whatever zone a
// request declares.
func TestFormatUTC(t *testing.T) {
	cases := []struct {
		sec  int64
		want string
	}{
		{0, "1970-01-01T00:00:00Z"},
		{-1, "1969-12-31T23:59:59Z"},
		{1767323045, "2026-01-02T03:04:05Z"},
	}
	for _, c := range cases {
		if got := FormatUTC(c.sec); got != c.want {
			t.Errorf("FormatUTC(%d) = %q, want %q", c.sec, got, c.want)
		}
	}
}
