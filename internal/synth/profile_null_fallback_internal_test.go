package synth

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestSpecFromProfile_UnsummarisableFallbackMatchesTheRowShape is the
// drift guard behind E1-S6's fix. The fallback's expectation is DERIVED
// from sentinelFor — the same function that types the expression
// environment, and the same source constantRowValue's three classes come
// from — rather than restated as a second table of type names, because
// this bug is exactly what two tables that must agree eventually do.
//
// The value is drawn from a real sampler built by buildSampler, so the
// assertion is about what actually enters the row map, not about what the
// params slot happens to hold.
func TestSpecFromProfile_UnsummarisableFallbackMatchesTheRowShape(t *testing.T) {
	raw, err := os.ReadFile("testdata/profile_all_null_columns.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var prof Profile
	if err := json.Unmarshal(raw, &prof); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	spec, _ := SpecFromProfile(&prof, 10)
	rng := newRng(1)

	seen := 0
	for _, fs := range spec.Fields {
		if fs.Distribution != DistConstant {
			continue // the profiler summarised this one; not our arm
		}
		seen++
		smp, err := buildSampler(fs)
		if err != nil {
			t.Errorf("field %q (%s): buildSampler rejected the fallback constant: %v",
				fs.Name, fs.Type, err)
			continue
		}
		got, _ := smp.next(rng)
		want := sentinelFor(fs.Type)
		if reflect.TypeOf(got) != reflect.TypeOf(want) {
			t.Errorf("field %q (%s): fallback constant draws %T, but sentinelFor promises %T — "+
				"the row map and the expression environment disagree, and the writer refuses one of them",
				fs.Name, fs.Type, got, want)
		}
	}
	// The fixture carries one always-null column per affected class; a
	// count that drops means the fixture stopped exercising the arm.
	if want := 6; seen != want {
		t.Errorf("fallback arm reached for %d fields, want %d — fixture no longer covers every class", seen, want)
	}
}
