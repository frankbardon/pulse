package synth_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// alwaysNullFixture reads the hand-built profile document carrying one
// always-null column per affected row-value class (E1-S6). It is a
// document rather than a Go literal because that is how a real profile
// reaches SpecFromProfile — `profile create` writes JSON and
// `synth from-profile` decodes it — and the fallback's whole job is to
// turn a summary-less field into something the writer can hold.
func alwaysNullFixture(t *testing.T) *synth.Profile {
	t.Helper()
	raw, err := os.ReadFile("testdata/profile_all_null_columns.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var prof synth.Profile
	if err := json.Unmarshal(raw, &prof); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &prof
}

// TestSpecFromProfile_AlwaysNullColumnGeneratesAllNullOnTheWire is the
// E1-S6 regression gate, and it deliberately drives the WHOLE path —
// Profile document -> SpecFromProfile -> SynthBytes -> decoded records —
// rather than asserting on the returned Spec. The spec was always
// well-formed by its own lights: it declared `constant` with a value,
// which is a complete FieldSpec. What disagreed was the ROW SHAPE, and
// only the writer can see that.
//
// A column the profiler could not summarise is, in practice, a column
// that is 100% null: there were no values to summarise. SpecFromProfile's
// fallback emitted `float64(0)` for every such field regardless of type,
// which E1-S2's constantRowValue tightening correctly refuses on a
// categorical_*, so generating from any profile with an always-null
// categorical column failed outright with
//
//	SERVICE_VALIDATION: field "region": constant value for categorical_u8
//	must be a string, got float64
//
// The assertion is the NULL BITMAP, never the value: NullRate is 1.0, so
// nullableSampler nulls every row and the constant is a placeholder that
// is never visible on the wire. It has to be well TYPED, not meaningful.
func TestSpecFromProfile_AlwaysNullColumnGeneratesAllNullOnTheWire(t *testing.T) {
	prof := alwaysNullFixture(t)

	const rows = 200
	spec, _ := synth.SpecFromProfile(prof, rows)

	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("SynthBytes from a profile with always-null columns: %v", err)
	}
	if got := readRecordCount(t, data); got != rows {
		t.Fatalf("record count = %d, want %d", got, rows)
	}

	// One column per affected class. The classes are what matters, not
	// the names: categorical_* needs a string, set_* the empty
	// selection, and the numeric/date/bool/decimal family a numeric
	// zero — three different Go types behind one fallback.
	alwaysNull := []string{"region", "topics", "score", "signup", "consented", "spend"}
	for _, name := range alwaysNull {
		if got := readNullCountForField(t, data, name); got != rows {
			t.Errorf("field %q: %d of %d records flagged null, want all of them — "+
				"a 100%%-null profile column must generate as null, not as the fallback constant",
				name, got, rows)
		}
	}
	// The anchor field the profiler DID summarise must be unaffected:
	// nothing here may turn a healthy column null.
	if got := readNullCountForField(t, data, "respondent"); got != 0 {
		t.Errorf("field %q: %d records flagged null, want 0", "respondent", got)
	}
}
