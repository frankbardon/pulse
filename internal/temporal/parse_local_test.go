package temporal

import (
	stderrors "errors"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
)

func mustImportZone(t *testing.T, name string) *Zone {
	t.Helper()
	z, err := LoadImportZone(name)
	if err != nil {
		t.Fatalf("LoadImportZone(%q): %v", name, err)
	}
	return z
}

func utcSec(t *testing.T, s string) int64 {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm.Unix()
}

// TestParseLocal is the policy table: naive / Z / offset literals, and the
// gap + overlap of a northern zone, a southern zone, a zone with no DST and
// a fixed offset, under every policy.
func TestParseLocal(t *testing.T) {
	type want struct {
		at   string // RFC 3339 UTC; "" = refused
		kind LocalKind
	}
	cases := []struct {
		name, zone, lit string
		err, earlier    want
		later           want
	}{
		{"berlin naive summer", "Europe/Berlin", "2026-07-01T12:00:00",
			want{"2026-07-01T10:00:00Z", LocalExact}, want{"2026-07-01T10:00:00Z", LocalExact}, want{"2026-07-01T10:00:00Z", LocalExact}},
		{"berlin naive winter", "Europe/Berlin", "2026-01-15 08:00",
			want{"2026-01-15T07:00:00Z", LocalExact}, want{"2026-01-15T07:00:00Z", LocalExact}, want{"2026-01-15T07:00:00Z", LocalExact}},
		{"berlin Z literal ignores zone", "Europe/Berlin", "2026-03-29T02:30:00Z",
			want{"2026-03-29T02:30:00Z", LocalOffset}, want{"2026-03-29T02:30:00Z", LocalOffset}, want{"2026-03-29T02:30:00Z", LocalOffset}},
		{"berlin offset literal ignores zone", "Europe/Berlin", "2026-10-25T02:30:00+02:00",
			want{"2026-10-25T00:30:00Z", LocalOffset}, want{"2026-10-25T00:30:00Z", LocalOffset}, want{"2026-10-25T00:30:00Z", LocalOffset}},
		{"berlin minute offset literal", "Europe/Berlin", "2026-10-25T02:30-05:00",
			want{"2026-10-25T07:30:00Z", LocalOffset}, want{"2026-10-25T07:30:00Z", LocalOffset}, want{"2026-10-25T07:30:00Z", LocalOffset}},
		{"berlin gap", "Europe/Berlin", "2026-03-29 02:30",
			want{"", LocalNonexistent}, want{"2026-03-29T01:30:00Z", LocalNonexistent}, want{"2026-03-29T00:30:00Z", LocalNonexistent}},
		{"berlin overlap", "Europe/Berlin", "2026-10-25T02:30:00",
			want{"", LocalAmbiguous}, want{"2026-10-25T00:30:00Z", LocalAmbiguous}, want{"2026-10-25T01:30:00Z", LocalAmbiguous}},
		{"berlin gap edge 03:00 exists", "Europe/Berlin", "2026-03-29 03:00",
			want{"2026-03-29T01:00:00Z", LocalExact}, want{"2026-03-29T01:00:00Z", LocalExact}, want{"2026-03-29T01:00:00Z", LocalExact}},
		{"sydney overlap (April)", "Australia/Sydney", "2026-04-05 02:30",
			want{"", LocalAmbiguous}, want{"2026-04-04T15:30:00Z", LocalAmbiguous}, want{"2026-04-04T16:30:00Z", LocalAmbiguous}},
		{"sydney gap (October)", "Australia/Sydney", "2026-10-04 02:30",
			want{"", LocalNonexistent}, want{"2026-10-03T16:30:00Z", LocalNonexistent}, want{"2026-10-03T15:30:00Z", LocalNonexistent}},
		{"kolkata never ambiguous", "Asia/Kolkata", "2026-03-29 02:30",
			want{"2026-03-28T21:00:00Z", LocalExact}, want{"2026-03-28T21:00:00Z", LocalExact}, want{"2026-03-28T21:00:00Z", LocalExact}},
		{"fixed +05:30", "+05:30", "2026-03-29 02:30",
			want{"2026-03-28T21:00:00Z", LocalExact}, want{"2026-03-28T21:00:00Z", LocalExact}, want{"2026-03-28T21:00:00Z", LocalExact}},
		{"fixed -08:00", "-08:00", "2026-01-01T00:00",
			want{"2026-01-01T08:00:00Z", LocalExact}, want{"2026-01-01T08:00:00Z", LocalExact}, want{"2026-01-01T08:00:00Z", LocalExact}},
		{"fixed offset literal wins", "+05:30", "2026-01-01T00:00:00Z",
			want{"2026-01-01T00:00:00Z", LocalOffset}, want{"2026-01-01T00:00:00Z", LocalOffset}, want{"2026-01-01T00:00:00Z", LocalOffset}},
		{"new york overlap", "America/New_York", "2026-11-01 01:30",
			want{"", LocalAmbiguous}, want{"2026-11-01T05:30:00Z", LocalAmbiguous}, want{"2026-11-01T06:30:00Z", LocalAmbiguous}},
	}
	for _, tc := range cases {
		z := mustImportZone(t, tc.zone)
		for _, p := range []struct {
			a Ambiguity
			w want
		}{{AmbiguityError, tc.err}, {AmbiguityEarlier, tc.earlier}, {AmbiguityLater, tc.later}} {
			t.Run(tc.name+"/"+p.a.String(), func(t *testing.T) {
				got, kind, err := ParseLocal(tc.lit, z, p.a)
				if kind != p.w.kind {
					t.Errorf("kind = %d, want %d", kind, p.w.kind)
				}
				if p.w.at == "" {
					wantErr := ErrLocalAmbiguous
					if p.w.kind == LocalNonexistent {
						wantErr = ErrLocalNonexistent
					}
					if !stderrors.Is(err, wantErr) {
						t.Fatalf("err = %v, want %v", err, wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if w := utcSec(t, p.w.at); got != w {
					t.Errorf("got %s, want %s", time.Unix(got, 0).UTC().Format(time.RFC3339), p.w.at)
				}
			})
		}
	}
}

// TestParseLocal_Pre1970FloorsTowardPast: a fractional pre-epoch second
// floors toward the past, in UTC and in a zone.
func TestParseLocal_Pre1970FloorsTowardPast(t *testing.T) {
	for _, tc := range []struct {
		zone string
		want int64
	}{{"UTC", -1}, {"Asia/Kolkata", -1 - 19800}, {"+05:30", -1 - 19800}, {"-08:00", -1 + 8*3600}} {
		got, _, err := ParseLocal("1969-12-31T23:59:59.5", mustImportZone(t, tc.zone), AmbiguityError)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.zone, got, tc.want)
		}
	}
}

// TestParseLocal_UTCMatchesNaiveUTC: under UTC every accepted layout gives
// the instant a naive-UTC parse gives, and a literal matching none is the
// ENCODING_INVALID refusal.
func TestParseLocal_UTCMatchesNaiveUTC(t *testing.T) {
	for _, lit := range []string{
		"2024-03-04T10:11:12Z", "2024-03-04T10:11:12+02:00", "2024-03-04T10:11:12",
		"2024-03-04T10:11Z", "2024-03-04T10:11", "2024-03-04 10:11:12", "2024-03-04 10:11",
		"2024-03-04T10:11:12.75Z", "1901-01-01T00:00:00",
	} {
		var want int64
		var ok bool
		for _, layout := range dateTimeLayouts {
			if tm, err := time.Parse(layout, lit); err == nil {
				want, ok = tm.Unix(), true
				break
			}
		}
		if !ok {
			t.Fatalf("fixture %q parses under no layout", lit)
		}
		for _, z := range []*Zone{UTC, mustImportZone(t, "Etc/UTC"), mustImportZone(t, "+00:00")} {
			got, _, err := ParseLocal(lit, z, AmbiguityError)
			if err != nil || got != want {
				t.Errorf("%s in %s: got %d, %v; want %d", lit, z.Name(), got, err, want)
			}
		}
	}
	_, _, err := ParseLocal("03/04/2024 10:11", UTC, AmbiguityError)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.ENCODING_INVALID {
		t.Fatalf("err = %v, want ENCODING_INVALID", err)
	}
}

// TestResolveLocal_MatchesStdlib brute-forces the inverse of the stdlib
// offset function around real transitions — every 15 wall-clock minutes
// across two days — and holds ResolveLocal to it under every policy:
// the exact instant, both overlap occurrences, and both gap readings.
func TestResolveLocal_MatchesStdlib(t *testing.T) {
	for _, tc := range []struct{ zone, day string }{
		{"Europe/Berlin", "2026-03-29"}, {"Europe/Berlin", "2026-10-25"},
		{"Australia/Sydney", "2026-04-05"}, {"Australia/Sydney", "2026-10-04"},
		{"Australia/Lord_Howe", "2026-04-05"}, {"Australia/Lord_Howe", "2026-10-04"},
		{"America/New_York", "2026-03-08"}, {"America/New_York", "2026-11-01"},
		{"Pacific/Apia", "2011-12-30"}, {"Asia/Kolkata", "2026-03-29"},
		{"Europe/London", "1890-06-01"}, {"America/Santiago", "2026-09-06"},
	} {
		t.Run(tc.zone+"/"+tc.day, func(t *testing.T) {
			z := mustImportZone(t, tc.zone)
			loc := z.Location()
			off := func(s int64) int64 { _, o := time.Unix(s, 0).In(loc).Zone(); return int64(o) }
			d, _ := time.Parse("2006-01-02", tc.day)
			for w := d.Unix() - 86400; w < d.Unix()+86400; w += 900 {
				seen := map[int64]bool{}
				var valid []int64
				for s := w - 2*86400; s <= w+2*86400; s += 900 {
					o := off(s)
					if c := w - o; !seen[c] && off(c) == o {
						seen[c] = true
						valid = append(valid, c)
					}
					seen[w-o] = true
				}
				for i := 1; i < len(valid); i++ {
					for k := i; k > 0 && valid[k] < valid[k-1]; k-- {
						valid[k], valid[k-1] = valid[k-1], valid[k]
					}
				}
				pre, post := w-off(w-2*86400), w-off(w+2*86400)
				for _, a := range []Ambiguity{AmbiguityError, AmbiguityEarlier, AmbiguityLater} {
					got, kind, err := ResolveLocal(w, z, a)
					switch {
					case len(valid) == 1:
						if err != nil || kind != LocalExact || got != valid[0] {
							t.Fatalf("w=%d %s: got %d kind %d err %v, want exact %d", w, a, got, kind, err, valid[0])
						}
					case len(valid) > 1:
						want := map[Ambiguity]int64{AmbiguityEarlier: valid[0], AmbiguityLater: valid[len(valid)-1]}
						if kind != LocalAmbiguous || (a == AmbiguityError) != (err != nil) || a != AmbiguityError && got != want[a] {
							t.Fatalf("w=%d %s: got %d kind %d err %v, want ambiguous %v", w, a, got, kind, err, valid)
						}
					default:
						want := map[Ambiguity]int64{AmbiguityEarlier: pre, AmbiguityLater: post}
						if kind != LocalNonexistent || (a == AmbiguityError) != (err != nil) || a != AmbiguityError && got != want[a] {
							t.Fatalf("w=%d %s: got %d kind %d err %v, want gap pre %d post %d", w, a, got, kind, err, pre, post)
						}
					}
				}
			}
		})
	}
}

func TestLoadImportZone(t *testing.T) {
	for _, name := range []string{"+05:30", "-08:00", "+00:00", "-00:00", "+18:00", "-18:00", "UTC", "Europe/Berlin"} {
		if _, err := LoadImportZone(name); err != nil {
			t.Errorf("LoadImportZone(%q) = %v", name, err)
		}
	}
	if z := mustImportZone(t, "+00:00"); !z.IsUTC() {
		t.Errorf("+00:00 should be UTC-equivalent")
	}
	if z := mustImportZone(t, "+05:30"); z.IsUTC() || z.Offset(0) != 19800 || z.Offset(1<<40) != 19800 || z.Offset(-1<<40) != 19800 {
		t.Errorf("+05:30 offsets wrong")
	}
	if f := mustImportZone(t, "-08:00").Fork(); f.Offset(0) != -28800 {
		t.Errorf("fork of fixed zone lost its offset")
	}
	for _, name := range []string{"", "+5:30", "+05:60", "+19:00", "+18:30", "05:30", "+0530", "EST", "europe/berlin", "Europe/Nowhere", "UTC+5"} {
		_, err := LoadImportZone(name)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TIMEZONE_UNKNOWN {
			t.Errorf("LoadImportZone(%q) err = %v, want PULSE_TIMEZONE_UNKNOWN", name, err)
		}
	}
	// The request path stays IANA-only.
	if _, err := LoadZone("+05:30"); err == nil {
		t.Errorf("LoadZone must keep refusing a fixed offset")
	}
}

func TestParseAmbiguity(t *testing.T) {
	for s, want := range map[string]Ambiguity{"": AmbiguityError, "error": AmbiguityError, "earlier": AmbiguityEarlier, "later": AmbiguityLater} {
		got, ok := ParseAmbiguity(s)
		if !ok || got != want {
			t.Errorf("ParseAmbiguity(%q) = %v, %v", s, got, ok)
		}
		if s != "" && got.String() != s {
			t.Errorf("String() = %q, want %q", got.String(), s)
		}
	}
	for _, s := range []string{"Earlier", "first", "raise"} {
		if _, ok := ParseAmbiguity(s); ok {
			t.Errorf("ParseAmbiguity(%q) accepted", s)
		}
	}
}
