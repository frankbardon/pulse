package imports

import (
	"testing"
	"time"
)

func TestParseTTL(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"3600s", 3600 * time.Second, false},
		{"1h30m", 90 * time.Minute, false},
		{"500ms", 500 * time.Millisecond, false},
		{"pin", -time.Second, false},
		{"none", -time.Second, false},
		{"-1", -time.Second, false},
		{"PIN", -time.Second, false},
		// negative durations rejected (use the named pin form)
		{"-5h", 0, true},
		{"-3d", 0, true},
		// malformed
		{"abc", 0, true},
		{"7days", 0, true},
		{"1d2h", 0, true}, // hybrid day+go form not supported
	}
	for _, tc := range cases {
		got, err := ParseTTL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseTTL(%q) err=nil, want non-nil (got %v)", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTTL(%q) err=%v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseTTL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFormatTTL(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{-1 * time.Second, "pin"},
		{7 * 24 * time.Hour, "7d"},
		{24 * time.Hour, "1d"},
		{90 * time.Minute, "1h30m0s"},
		{3 * time.Hour, "3h0m0s"},
	}
	for _, tc := range cases {
		if got := FormatTTL(tc.in); got != tc.want {
			t.Errorf("FormatTTL(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
