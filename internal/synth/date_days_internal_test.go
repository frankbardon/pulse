package synth

import "testing"

// TestDaysToISO pins the epoch-day → ISO rendering used by profile
// capture, across the sign boundary.
func TestDaysToISO(t *testing.T) {
	for _, tc := range []struct {
		days int64
		want string
	}{{0, "1970-01-01"}, {-1, "1969-12-31"}, {-25567, "1900-01-01"}, {19787, "2024-03-05"}} {
		if got := daysToISO(tc.days); got != tc.want {
			t.Errorf("daysToISO(%d) = %q, want %q", tc.days, got, tc.want)
		}
	}
}

// TestUniformDateSampler_DayBounds pins the parsed start/end epoch days,
// including pre-epoch bounds where floor and toward-zero division would
// diverge on a non-midnight instant.
func TestUniformDateSampler_DayBounds(t *testing.T) {
	for _, tc := range []struct {
		start, end         string
		wantStart, wantEnd int64
	}{
		{"1970-01-01", "1970-01-02", 0, 1},
		{"1969-12-31", "1970-01-01", -1, 0},
		{"1900-01-01", "2024-03-05", -25567, 19787},
	} {
		s, err := newUniformDateSampler(FieldSpec{Name: "d", Distribution: DistUniformDate,
			Params: map[string]any{"start": tc.start, "end": tc.end}})
		if err != nil {
			t.Fatalf("%s..%s: %v", tc.start, tc.end, err)
		}
		u := s.(*uniformDateSampler)
		if u.startDays != tc.wantStart || u.endDays != tc.wantEnd {
			t.Errorf("%s..%s: got [%d,%d], want [%d,%d]", tc.start, tc.end, u.startDays, u.endDays, tc.wantStart, tc.wantEnd)
		}
	}
}
