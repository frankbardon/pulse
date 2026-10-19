package feature

import (
	"testing"
	"time"

	"github.com/frankbardon/pulse/internal/temporal"
)

// TestDecodeDateParts pins FEAT_DATE_FEATURES' epoch-day decode across
// the sign boundary.
func TestDecodeDateParts(t *testing.T) {
	for _, tc := range []struct {
		v               float64
		y, m, d, dow, q float64
	}{
		{0, 1970, 1, 1, 4, 1},
		{-1, 1969, 12, 31, 3, 4},
		{19787, 2024, 3, 5, 2, 1},
	} {
		y, m, d, dow, q := decodeDateParts(tc.v)
		if y != tc.y || m != tc.m || d != tc.d || dow != tc.dow || q != tc.q {
			t.Errorf("decodeDateParts(%v) = %v-%v-%v dow=%v q=%v, want %v-%v-%v dow=%v q=%v",
				tc.v, y, m, d, dow, q, tc.y, tc.m, tc.d, tc.dow, tc.q)
		}
	}
}

// TestDecodeParts_ISODow: dow is ISO 8601 (Monday = 1 .. Sunday = 7) on
// both arms — a `date` read on the calendar and a `datetime` on a zone's
// wall clock. Saturday 23:30 in New York is Sunday 04:30 UTC: dow 6
// there, 7 in UTC.
func TestDecodeParts_ISODow(t *testing.T) {
	for _, tc := range []struct {
		day  float64
		want float64
	}{{19797, 5}, {19798, 6}, {19799, 7}, {19800, 1}} { // 2024-03-15 Fri .. 03-18 Mon
		if _, _, _, dow, _ := decodeDateParts(tc.day); dow != tc.want {
			t.Errorf("decodeDateParts(%v) dow = %v, want %v", tc.day, dow, tc.want)
		}
	}
	ny, err := temporal.LoadZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse(time.RFC3339, "2026-03-15T03:30:00Z")
	if _, _, d, dow, _, h := decodeInstantParts(at.Unix(), ny); dow != 6 || d != 14 || h != 23 {
		t.Errorf("New York: day %v dow %v hour %v, want 14 / 6 / 23", d, dow, h)
	}
	if _, _, d, dow, _, h := decodeInstantParts(at.Unix(), nil); dow != 7 || d != 15 || h != 3 {
		t.Errorf("UTC: day %v dow %v hour %v, want 15 / 7 / 3", d, dow, h)
	}
}
