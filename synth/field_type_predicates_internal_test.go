package synth

import (
	"testing"
)

// TestFieldTypePredicates_ClassifyEveryDeclarableType is the forcing
// gate on synth's three type-NAME predicates: every field type a Spec
// can declare must carry an EXPLICIT expected answer from all three, and
// the table must cover exactly that set — no more, no fewer. A type
// added to fieldTypeFromName therefore fails this test until someone
// decides what the copula, the boolean arm and the discrete arm should
// do with it, rather than acquiring three default answers silently.
//
// The type list is DERIVED (declarableTypeNames walks the
// encoding.FieldType enum by value), never transcribed, because
// transcription is the exact failure being closed here:
// isNumericFieldType carried `nullable_u4` / `nullable_u8` /
// `nullable_u16` — spellings fieldTypeFromName cannot build, so no spec
// naming them ever reached the writer — and did NOT carry `u4`, the
// spelling encoding.FieldType.String() actually emits. addCorrelation
// gates on it, so every u4 field was dropped from Spec.Correlations in
// silence: 11 of the motivating survey cohort's 14 integer columns, and
// all 16 of its captured numeric pairs. sentinelFor had the same
// stale-alias defect for packed_bool (issue #258). A hand-picked sample
// of type names catches neither, because the name that is missing is by
// definition not in the sample.
//
// Same forcing shape as TestLatentFor_EveryDistributionIsClassified, on
// the type axis instead of the distribution axis.
func TestFieldTypePredicates_ClassifyEveryDeclarableType(t *testing.T) {
	type want struct {
		numeric, boolean, integerQuantized bool
	}
	table := map[string]want{
		// Scalars. Every one is numeric to the copula: the row value is
		// the float64 every scalar sampler emits, so correlator.transform
		// can write Q(Φ(u)) back over it. Whether a given field has
		// MOMENTS is fieldMoments' question, one call later.
		"u4":          {numeric: true, integerQuantized: true},
		"u8":          {numeric: true, integerQuantized: true},
		"u16":         {numeric: true, integerQuantized: true},
		"u32":         {numeric: true, integerQuantized: true},
		"u64":         {numeric: true, integerQuantized: true},
		"f32":         {numeric: true},
		"f64":         {numeric: true},
		"date":        {numeric: true},
		"decimal128":  {numeric: true},
		"packed_bool": {numeric: true, boolean: true},
		// Dictionary-backed. The row value is a string / map[string]bool,
		// not a number anything here could write back.
		"categorical_u8":  {},
		"categorical_u16": {},
		"categorical_u32": {},
		"set_u8":          {},
		"set_u16":         {},
		"set_u32":         {},
		"set_u64":         {},
	}

	declarable := declarableTypeNames(t)
	if len(declarable) < 17 {
		t.Fatalf("expected at least 17 declarable field types, got %d: %v", len(declarable), declarable)
	}
	seen := make(map[string]bool, len(declarable))
	for _, name := range declarable {
		seen[name] = true
		w, ok := table[name]
		if !ok {
			t.Errorf("type %q is declarable but carries no expected classification — "+
				"decide what isNumericFieldType / isBooleanFieldType / "+
				"isIntegerQuantizedFieldType should answer for it", name)
			continue
		}
		if got := isNumericFieldType(name); got != w.numeric {
			t.Errorf("isNumericFieldType(%q) = %v, want %v", name, got, w.numeric)
		}
		if got := isBooleanFieldType(name); got != w.boolean {
			t.Errorf("isBooleanFieldType(%q) = %v, want %v", name, got, w.boolean)
		}
		if got := isIntegerQuantizedFieldType(name); got != w.integerQuantized {
			t.Errorf("isIntegerQuantizedFieldType(%q) = %v, want %v", name, got, w.integerQuantized)
		}
	}
	for name := range table {
		if !seen[name] {
			t.Errorf("table names %q, which fieldTypeFromName cannot build — "+
				"a predicate gating on it can never fire", name)
		}
	}
}

// TestFieldTypePredicates_RefuseUndeclarableNames is the other half, and
// the one that actually failed: a predicate must answer false for every
// name no Spec can declare.
//
// `datetime` is the live case — an encoding.FieldType that exists and
// whose String() is stable, but which fieldTypeFromName cannot build, so
// a predicate admitting it would gate on a column synth can never
// produce. The nullable_* spellings are the historical aliases
// isNumericFieldType and sentinelFor really carried; they are pinned
// here so neither can come back.
func TestFieldTypePredicates_RefuseUndeclarableNames(t *testing.T) {
	names := make([]string, 0, len(undeclarableFieldTypes)+9)
	for name := range undeclarableFieldTypes {
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatal("undeclarableFieldTypes is empty — the live half of this gate is gone")
	}
	names = append(names,
		"nullable_u4", "nullable_u8", "nullable_u16", "nullable_bool",
		"nullable_decimal128", "int", "float", "bool", "",
	)

	for _, name := range names {
		if isNumericFieldType(name) {
			t.Errorf("isNumericFieldType(%q) = true, but no Spec can declare that type", name)
		}
		if isBooleanFieldType(name) {
			t.Errorf("isBooleanFieldType(%q) = true, but no Spec can declare that type", name)
		}
		if isIntegerQuantizedFieldType(name) {
			t.Errorf("isIntegerQuantizedFieldType(%q) = true, but no Spec can declare that type", name)
		}
	}
}
