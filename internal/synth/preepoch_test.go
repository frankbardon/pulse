package synth_test

import (
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
)

// TestSynth_PreEpochDate_RuleLiteralAndProfile drives a negative date
// literal through a rule, then profiles the bytes: the date word is
// signed int32 epoch days, so the rule's -25567 (1900-01-01) must land on
// the wire and the profile must report a pre-1970 range rather than a day
// near 4.29e9.
func TestSynth_PreEpochDate_RuleLiteralAndProfile(t *testing.T) {
	const spec = `{
	  "row_count": 6,
	  "fields": [
	    {"name": "id", "type": "u32", "distribution": "monotonic_from", "params": {"start": 1}},
	    {"name": "d", "type": "date", "distribution": "uniform_date",
	     "params": {"start": "1969-12-01", "end": "1969-12-31"}}
	  ],
	  "rules": [{"when": "id == 1", "set": {"d": -25567}}]
	}`
	s, err := synth.ParseSpec([]byte(spec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	data, _, err := synth.SynthBytes(s, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	days := readF64Field(t, data, "d")
	if days[0] != -25567 {
		t.Fatalf("row 0 d = %v, want the rule literal -25567", days[0])
	}
	for i, d := range days[1:] {
		if d < -31 || d > -1 {
			t.Fatalf("row %d d = %v, want a December-1969 day in [-31, -1]", i+1, d)
		}
	}

	p, err := synth.ProfileBytes(data, synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	for _, f := range p.Fields {
		if f.Name != "d" {
			continue
		}
		if f.Date == nil {
			t.Fatal("profile carries no date block for d")
		}
		if f.Date.Start != "1900-01-01" {
			t.Errorf("profile d start = %q, want 1900-01-01", f.Date.Start)
		}
		if f.Date.End > "1969-12-31" || f.Date.End < "1969-12-01" {
			t.Errorf("profile d end = %q, want a December-1969 day", f.Date.End)
		}
		return
	}
	t.Fatal("profile has no field d")
}
