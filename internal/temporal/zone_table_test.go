package temporal

import (
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// TestValidZoneName: the cheap validity check accepts exactly what
// LoadZone accepts and refuses with LoadZone's own coded error.
func TestValidZoneName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"UTC", "Europe/Berlin", "America/New_York", "Etc/GMT-5", "Etc/UTC"} {
		if err := ValidZoneName(name); err != nil {
			t.Errorf("ValidZoneName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "Local", "EST", "EST5EDT", "GMT", "+05:00", "europe/berlin", "Mars/Base", "Europe/"} {
		err := ValidZoneName(name)
		want, lerr := LoadZone(name)
		if want != nil || lerr == nil {
			t.Fatalf("LoadZone(%q) accepted; the case list is wrong", name)
		}
		ce, ok := err.(*perr.CodedError)
		if !ok || ce.Code != perr.PULSE_TIMEZONE_UNKNOWN || ce.Details[perr.DetailTimeZone] != name {
			t.Errorf("ValidZoneName(%q) = %v, want PULSE_TIMEZONE_UNKNOWN {tz: %q}", name, err, name)
		}
		if err.Error() != lerr.Error() {
			t.Errorf("ValidZoneName(%q) message %q differs from LoadZone's %q", name, err, lerr)
		}
	}
}

// TestLoadZones_CodeIndexed: the table is indexed by dictionary code,
// a repeated name shares one *Zone, an out-of-range code is nil and a
// bad entry is refused with its dictionary index.
func TestLoadZones_CodeIndexed(t *testing.T) {
	t.Parallel()

	names := []string{"Europe/Berlin", "UTC", "America/New_York", "Europe/Berlin"}
	tab, err := LoadZones(names, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tab.Len() != len(names) {
		t.Fatalf("Len = %d, want %d", tab.Len(), len(names))
	}
	for i, n := range names {
		if z := tab.At(uint32(i)); z == nil || z.Name() != n {
			t.Fatalf("At(%d) = %v, want %s", i, z, n)
		}
	}
	if tab.At(1) != UTC {
		t.Fatal("UTC entry is not the UTC sentinel")
	}
	if tab.At(0) != tab.At(3) {
		t.Fatal("a repeated name built two zones")
	}
	if tab.At(uint32(len(names))) != nil {
		t.Fatal("out-of-range code is not nil")
	}
	if tab.AllUTC() {
		t.Fatal("AllUTC true on a table with non-UTC zones")
	}
	if u, err := LoadZones([]string{"UTC", "Etc/UTC"}, nil); err != nil || !u.AllUTC() {
		t.Fatalf("AllUTC false on a UTC-equivalent table (%v)", err)
	}

	_, err = LoadZones([]string{"Europe/Berlin", "Mars/Base"}, nil)
	ce, ok := err.(*perr.CodedError)
	if !ok || ce.Code != perr.PULSE_TIMEZONE_UNKNOWN || ce.Details[perr.DetailTimeZone] != "Mars/Base" || ce.Details[DetailDictionaryIndex] != 1 {
		t.Fatalf("bad entry = %v, want PULSE_TIMEZONE_UNKNOWN {tz: Mars/Base, dictionary_index: 1}", err)
	}
}

// TestLoadZones_UsesLoader: a long-lived owner's loader (a Cache) is
// consulted once per distinct name.
func TestLoadZones_UsesLoader(t *testing.T) {
	t.Parallel()

	var c Cache
	calls := map[string]int{}
	load := func(n string) (*Zone, error) { calls[n]++; return c.Load(n) }
	tab, err := LoadZones([]string{"Asia/Tokyo", "Asia/Tokyo", "UTC"}, load)
	if err != nil {
		t.Fatal(err)
	}
	if calls["Asia/Tokyo"] != 1 || calls["UTC"] != 1 {
		t.Fatalf("loader calls = %v, want one per distinct name", calls)
	}
	cached, _ := c.Load("Asia/Tokyo")
	if tab.At(0) != cached {
		t.Fatal("table does not hold the loader's zone")
	}
}

// TestZoneTable_Fork: a fork answers identically, keeps the sharing
// between repeated names, and owns its own lookup caches.
func TestZoneTable_Fork(t *testing.T) {
	t.Parallel()

	tab, err := LoadZones([]string{"Europe/Berlin", "UTC", "Europe/Berlin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := tab.Fork()
	if f.Len() != tab.Len() {
		t.Fatalf("fork Len = %d", f.Len())
	}
	if f.At(0) == tab.At(0) {
		t.Fatal("fork shares the parent's non-UTC zone (and its cache)")
	}
	if f.At(0) != f.At(2) {
		t.Fatal("fork split a shared zone into two")
	}
	if f.At(1) != UTC {
		t.Fatal("fork replaced the UTC sentinel")
	}
	const sec = int64(1719835200) // 2024-07-01T12:00:00Z
	if f.At(0).Offset(sec) != tab.At(0).Offset(sec) {
		t.Fatal("fork answers differently")
	}
	var nilTab *ZoneTable
	if nilTab.Fork() != nil || nilTab.At(0) != nil || nilTab.Len() != 0 {
		t.Fatal("nil table is not inert")
	}
}
