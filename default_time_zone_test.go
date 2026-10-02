package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/spf13/afero"
)

func TestNew_DefaultTimeZone_UnknownRefused(t *testing.T) {
	for _, name := range []string{"EST", "Local", "+05:00", "Mars/Olympus_Mons", "europe/berlin"} {
		t.Run(name, func(t *testing.T) {
			p, err := New(Options{FS: afero.NewMemMapFs(), DefaultTimeZone: name})
			if !errors.HasCode(err, errors.PULSE_TIMEZONE_UNKNOWN) {
				t.Fatalf("DefaultTimeZone %q: want PULSE_TIMEZONE_UNKNOWN, got p=%v err=%v", name, p, err)
			}
			if p != nil {
				t.Fatalf("DefaultTimeZone %q: New returned a non-nil *Pulse alongside the error", name)
			}
		})
	}
}

func TestNew_DefaultTimeZone_Accepted(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"", "UTC"},
		{"UTC", "UTC"},
		{"Europe/Berlin", "Europe/Berlin"},
		{"Etc/GMT-5", "Etc/GMT-5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(Options{FS: afero.NewMemMapFs(), DefaultTimeZone: tc.name})
			if err != nil {
				t.Fatalf("DefaultTimeZone %q: New: %v", tc.name, err)
			}
			if p.defaultZone == nil || p.defaultZone.Name() != tc.want {
				t.Fatalf("DefaultTimeZone %q: defaultZone = %v, want %s", tc.name, p.defaultZone, tc.want)
			}
			if tc.want == "UTC" && p.defaultZone != temporal.UTC {
				t.Fatalf("DefaultTimeZone %q: defaultZone is not the UTC sentinel", tc.name)
			}
		})
	}
}

// The default zone is loaded through the instance cache, so a later
// request-time resolution of the same name reuses the table built at New.
func TestPulse_ZoneCacheSharesDefaultZone(t *testing.T) {
	p, err := New(Options{FS: afero.NewMemMapFs(), DefaultTimeZone: "America/Chicago"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	z, err := p.zone("America/Chicago")
	if err != nil {
		t.Fatalf("zone: %v", err)
	}
	if z != p.defaultZone {
		t.Fatalf("zone(America/Chicago) is not the cached default zone pointer")
	}
	if _, err := p.zone("EST"); !errors.HasCode(err, errors.PULSE_TIMEZONE_UNKNOWN) {
		t.Fatalf("zone(EST): want PULSE_TIMEZONE_UNKNOWN, got %v", err)
	}
}

// Each instance owns its cache: a zone loaded by one never appears as
// the same pointer in another.
func TestPulse_ZoneCacheIsPerInstance(t *testing.T) {
	a, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("New a: %v", err)
	}
	b, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("New b: %v", err)
	}
	za, err := a.zone("Asia/Kolkata")
	if err != nil {
		t.Fatalf("a.zone: %v", err)
	}
	zb, err := b.zone("Asia/Kolkata")
	if err != nil {
		t.Fatalf("b.zone: %v", err)
	}
	if za == zb {
		t.Fatalf("two instances share one *Zone; cache is not per instance")
	}
}
