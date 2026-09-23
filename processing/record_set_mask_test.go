package processing

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
)

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// makeWideSetSchema builds a one-field schema whose "tags" column is the
// given set rung, backed by a dictionary of n entries named T0..T{n-1}.
// Bit i ↔ dictionary entry i ↔ label "T<i>".
func makeWideSetSchema(t *testing.T, ft encoding.FieldType, n int) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < n; i++ {
		if _, err := dict.Add(fmt.Sprintf("T%d", i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: ft, Dictionary: dict},
		},
	}
}

// makeWideSetRecord stores an encoding.SetMask (the wide-rung storage
// form) on field "tags".
func makeWideSetRecord(schema *encoding.Schema, m encoding.SetMask) *Record {
	return NewRecordWithWide(schema, map[string]float64{}, nil, map[string]any{"tags": m})
}

// maskWithBits builds a SetMask carrying exactly the listed bits.
func maskWithBits(bits ...int) encoding.SetMask {
	var m encoding.SetMask
	for _, b := range bits {
		m = m.WithBit(b)
	}
	return m
}

// ---------------------------------------------------------------------
// SetMaskValue — the single set accessor
// ---------------------------------------------------------------------

// Narrow rungs keep their uint64 storage; SetMaskValue widens on read.
func TestRecord_SetMaskValue_WidensNarrowUint64Storage(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU64, 64)
	cases := []struct {
		name string
		bit  int
	}{
		{"bit0", 0},
		{"bit7", 7},
		{"bit63", 63},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := uint64(1) << uint(tc.bit)
			r := NewRecordWithWide(schema, map[string]float64{}, nil,
				map[string]any{"tags": stored})

			m, ok := r.SetMaskValue("tags")
			if !ok {
				t.Fatalf("SetMaskValue ok = false, want true")
			}
			if !m.Has(tc.bit) {
				t.Errorf("Has(%d) = false, want true", tc.bit)
			}
			if got := m.PopCount(); got != 1 {
				t.Errorf("PopCount = %d, want 1", got)
			}
			low, fits := m.Uint64()
			if !fits {
				t.Errorf("Uint64 fits = false, want true for a narrow mask")
			}
			if low != stored {
				t.Errorf("Uint64 low = %#x, want %#x", low, stored)
			}
		})
	}
}

// The regression test for the bug this story closes. A wide mask must
// come back through the accessor with every bit intact — the old
// SetValue reported it as (0, false), byte-identical to a null field.
func TestRecord_SetMaskValue_WideBitsSurviveTheAccessor(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	for _, bit := range []int{0, 63, 64, 127, 128, 255} {
		t.Run(fmt.Sprintf("bit%d", bit), func(t *testing.T) {
			r := makeWideSetRecord(schema, maskWithBits(bit))

			m, ok := r.SetMaskValue("tags")
			if !ok {
				t.Fatalf("SetMaskValue ok = false for bit %d — a present mask read as null", bit)
			}
			if !m.Has(bit) {
				t.Errorf("Has(%d) = false, want true", bit)
			}
			if m.IsEmpty() {
				t.Errorf("IsEmpty = true for a mask with bit %d set", bit)
			}
			if got := m.PopCount(); got != 1 {
				t.Errorf("PopCount = %d, want 1", got)
			}
			if got := m.HighestBit(); got != bit {
				t.Errorf("HighestBit = %d, want %d", got, bit)
			}
		})
	}
}

func TestRecord_SetMaskValue_NullMissingAndNonSetValues(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)

	t.Run("null", func(t *testing.T) {
		r := NewRecordWithWide(schema, map[string]float64{},
			map[string]bool{"tags": true},
			map[string]any{"tags": maskWithBits(200)})
		if _, ok := r.SetMaskValue("tags"); ok {
			t.Errorf("SetMaskValue ok = true for a null field, want false")
		}
	})

	t.Run("missing", func(t *testing.T) {
		r := NewRecordWithWide(schema, map[string]float64{}, nil, map[string]any{})
		if _, ok := r.SetMaskValue("tags"); ok {
			t.Errorf("SetMaskValue ok = true for a missing field, want false")
		}
	})

	t.Run("non_set_wide_value", func(t *testing.T) {
		r := NewRecordWithWide(schema, map[string]float64{}, nil,
			map[string]any{"tags": encoding.Decimal128{}})
		if _, ok := r.SetMaskValue("tags"); ok {
			t.Errorf("SetMaskValue ok = true for a non-set wide value, want false")
		}
	})
}

// The narrow path must not allocate — SetMask is a fixed array value and
// SetMaskFromUint64 is register work.
func TestRecord_SetMaskValue_NarrowPathZeroAlloc(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU64, 64)
	r := NewRecordWithWide(schema, map[string]float64{}, nil,
		map[string]any{"tags": uint64(0xDEADBEEF)})

	var sink int
	allocs := testing.AllocsPerRun(200, func() {
		m, ok := r.SetMaskValue("tags")
		if ok {
			sink += m.PopCount()
		}
	})
	if sink == 0 {
		t.Fatalf("benchmark body never ran")
	}
	if allocs != 0 {
		t.Errorf("SetMaskValue narrow path allocs = %v, want 0", allocs)
	}
}

// ---------------------------------------------------------------------
// The silent-null path is gone
// ---------------------------------------------------------------------

// The critical acceptance test. No exported Record accessor may report a
// present wide mask the way it reports a null, and the removed uint64
// accessor must stay removed.
func TestRecord_WideSetIsNeverReportedAsNull(t *testing.T) {
	// The old (uint64, bool) accessor is the whole hazard: it returned
	// (0, false) both for "null" and for "I could not type-assert this".
	// It must not exist.
	if _, found := reflect.TypeOf(&Record{}).MethodByName("SetValue"); found {
		t.Fatalf("Record.SetValue still exists — the uint64 set accessor reports a wide mask as a null")
	}

	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	for _, bit := range []int{0, 63, 64, 127, 128, 255} {
		t.Run(fmt.Sprintf("bit%d", bit), func(t *testing.T) {
			r := makeWideSetRecord(schema, maskWithBits(bit))
			wantLabel := fmt.Sprintf("T%d", bit)

			if r.IsNull("tags") {
				t.Errorf("IsNull = true for a present wide mask")
			}
			if _, ok := r.WideValue("tags"); !ok {
				t.Errorf("WideValue ok = false for a present wide mask")
			}
			m, ok := r.SetMaskValue("tags")
			if !ok || !m.Has(bit) {
				t.Errorf("SetMaskValue = (%v, %v), want the mask with bit %d", m.Words(), ok, bit)
			}

			labels, ok := r.SetLabels("tags")
			if !ok {
				t.Fatalf("SetLabels ok = false for a present wide mask")
			}
			if len(labels) != 1 || labels[0] != wantLabel {
				t.Errorf("SetLabels = %v, want [%s]", labels, wantLabel)
			}

			av, ok := r.AllValues()["tags"].([]string)
			if !ok {
				t.Fatalf("AllValues[tags] type = %T, want []string", r.AllValues()["tags"])
			}
			if len(av) != 1 || av[0] != wantLabel {
				t.Errorf("AllValues[tags] = %v, want [%s]", av, wantLabel)
			}
		})
	}
}

// The narrowing bridge the not-yet-widened operators use must REFUSE a
// mask it cannot represent, never hand back a plausible zero.
func TestNarrowSetValue_RefusesAMaskItCannotRepresent(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	for _, bit := range []int{64, 127, 128, 255} {
		t.Run(fmt.Sprintf("bit%d", bit), func(t *testing.T) {
			r := makeWideSetRecord(schema, maskWithBits(bit))
			low, ok, err := narrowSetValue(r, "tags")
			if err == nil {
				t.Fatalf("narrowSetValue err = nil for bit %d — silent truncation to (%#x, %v)", bit, low, ok)
			}
			if ok {
				t.Errorf("narrowSetValue ok = true alongside an error")
			}
			if !perrors.HasCode(err, perrors.PROCESSING_RUNTIME) {
				t.Errorf("error = %v, want a PROCESSING_RUNTIME coded error", err)
			}
		})
	}
}

// A wide mask that happens to carry a low bit too must still be refused
// — truncating it would drop a real selection.
func TestNarrowSetValue_RefusesMixedLowAndHighBits(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	r := makeWideSetRecord(schema, maskWithBits(3, 200))
	if _, _, err := narrowSetValue(r, "tags"); err == nil {
		t.Fatalf("narrowSetValue err = nil for bits {3,200}; bit 3 alone would look like a valid selection")
	}
}

func TestNarrowSetValue_NarrowPassesThroughAndNullIsNotAnError(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU64, 64)

	r := NewRecordWithWide(schema, map[string]float64{}, nil,
		map[string]any{"tags": uint64(0b1011)})
	low, ok, err := narrowSetValue(r, "tags")
	if err != nil {
		t.Fatalf("narrowSetValue err = %v, want nil", err)
	}
	if !ok || low != 0b1011 {
		t.Errorf("narrowSetValue = (%#b, %v), want (0b1011, true)", low, ok)
	}

	null := NewRecordWithWide(schema, map[string]float64{},
		map[string]bool{"tags": true}, map[string]any{})
	low, ok, err = narrowSetValue(null, "tags")
	if err != nil {
		t.Fatalf("narrowSetValue on a null field err = %v, want nil", err)
	}
	if ok || low != 0 {
		t.Errorf("narrowSetValue on a null field = (%#x, %v), want (0, false)", low, ok)
	}
}

// narrowSetValue takes a fast path straight off the wide map for narrow
// uint64 storage. That shortcut must stay observationally identical to
// the canonical SetMaskValue accessor for every record state, or the two
// drift and the null semantics fork.
func TestNarrowSetValue_AgreesWithSetMaskValue(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	states := []struct {
		name  string
		nulls map[string]bool
		wide  map[string]any
	}{
		{"narrow_zero", nil, map[string]any{"tags": uint64(0)}},
		{"narrow_bits", nil, map[string]any{"tags": uint64(0b1011)}},
		{"narrow_high_bit", nil, map[string]any{"tags": uint64(1) << 63}},
		{"wide_low_only", nil, map[string]any{"tags": maskWithBits(3)}},
		{"wide_empty", nil, map[string]any{"tags": encoding.SetMask{}}},
		{"missing", nil, map[string]any{}},
		{"null_marked", map[string]bool{"tags": true}, map[string]any{}},
		{"null_marked_with_value", map[string]bool{"tags": true}, map[string]any{"tags": uint64(7)}},
		{"non_set_wide_value", nil, map[string]any{"tags": encoding.Decimal128{}}},
	}
	for _, st := range states {
		t.Run(st.name, func(t *testing.T) {
			r := NewRecordWithWide(schema, map[string]float64{}, st.nulls, st.wide)

			mask, maskOK := r.SetMaskValue("tags")
			low, narrowOK, err := narrowSetValue(r, "tags")
			if err != nil {
				t.Fatalf("narrowSetValue err = %v; no state here exceeds 64 bits", err)
			}
			if narrowOK != maskOK {
				t.Fatalf("narrowSetValue ok = %v, SetMaskValue ok = %v — the fast path forked",
					narrowOK, maskOK)
			}
			wantLow, fits := mask.Uint64()
			if !fits {
				t.Fatalf("test state %q unexpectedly exceeds 64 bits", st.name)
			}
			if !maskOK {
				wantLow = 0
			}
			if low != wantLow {
				t.Errorf("narrowSetValue low = %#x, SetMaskValue low = %#x", low, wantLow)
			}
		})
	}
}

// ---------------------------------------------------------------------
// Label resolution bounds on the dictionary, not on 64
// ---------------------------------------------------------------------

func TestRecord_SetLabels_206MemberDictionary(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 206)
	bits := []int{0, 1, 63, 64, 65, 127, 128, 191, 192, 205}
	r := makeWideSetRecord(schema, maskWithBits(bits...))

	labels, ok := r.SetLabels("tags")
	if !ok {
		t.Fatalf("SetLabels ok = false")
	}
	want := make([]string, 0, len(bits))
	for _, b := range bits {
		want = append(want, fmt.Sprintf("T%d", b))
	}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("SetLabels = %v, want %v", labels, want)
	}
}

// Bits past the dictionary are a corrupt or mid-remap payload: skipped,
// never resolved, never fatal.
func TestRecord_SetLabels_BitsBeyondDictionaryAreSkipped(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 206)
	r := makeWideSetRecord(schema, maskWithBits(2, 205, 206, 255))

	labels, ok := r.SetLabels("tags")
	if !ok {
		t.Fatalf("SetLabels ok = false")
	}
	want := []string{"T2", "T205"}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("SetLabels = %v, want %v", labels, want)
	}
}

// SetLabels must produce its result in ONE allocation: the walk yields
// at most min(popcount, dict.Count()) labels and encoding.SetMask.Labels
// sizes the slice up front. Without that hint the append chain
// reallocates log2(n) times, and AllValues pays it once per row per set
// field under expression evaluation.
func TestRecord_SetLabels_SingleAllocation(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 206)
	bits := make([]int, 0, 100)
	for i := 0; i < 200; i += 2 {
		bits = append(bits, i)
	}
	r := makeWideSetRecord(schema, maskWithBits(bits...))

	n := 0
	allocs := testing.AllocsPerRun(100, func() {
		labels, _ := r.SetLabels("tags")
		n = len(labels)
	})
	if n != len(bits) {
		t.Fatalf("SetLabels returned %d labels, want %d", n, len(bits))
	}
	if allocs != 1 {
		t.Errorf("SetLabels allocs = %v, want 1 (the pre-sized result slice)", allocs)
	}
}

func TestRecord_SetLabels_NarrowBehaviourUnchanged(t *testing.T) {
	schema := makeSetTestSchema(t) // set_u8, 4 entries: VISA MC AMEX DISC

	t.Run("selection", func(t *testing.T) {
		labels, ok := r0(schema, 0b0101).SetLabels("tags")
		if !ok {
			t.Fatalf("SetLabels ok = false")
		}
		if !reflect.DeepEqual(labels, []string{"VISA", "AMEX"}) {
			t.Errorf("SetLabels = %v, want [VISA AMEX]", labels)
		}
	})

	t.Run("empty_mask_is_present_not_null", func(t *testing.T) {
		labels, ok := r0(schema, 0).SetLabels("tags")
		if !ok {
			t.Fatalf("SetLabels ok = false for an empty mask; an empty selection is not a null")
		}
		if labels == nil || len(labels) != 0 {
			t.Errorf("SetLabels = %#v, want an empty non-nil slice", labels)
		}
	})

	t.Run("null", func(t *testing.T) {
		null := NewRecordWithWide(schema, map[string]float64{},
			map[string]bool{"tags": true}, map[string]any{})
		if labels, ok := null.SetLabels("tags"); ok || labels != nil {
			t.Errorf("SetLabels = (%v, %v), want (nil, false)", labels, ok)
		}
	})
}

func r0(schema *encoding.Schema, mask uint64) *Record {
	return NewRecordWithWide(schema, map[string]float64{}, nil, map[string]any{"tags": mask})
}

// resolveMaskLabelsWithIndices bounds on dict.Count(), not on 64.
func TestResolveMaskLabelsWithIndices_PastBit64(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 206)
	dict := schema.Fields[0].Dictionary

	labels, indices := resolveMaskLabelsWithIndices(maskWithBits(5, 64, 130, 205, 210), dict)
	wantLabels := []string{"T5", "T64", "T130", "T205"}
	wantIndices := []int{5, 64, 130, 205}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Errorf("labels = %v, want %v", labels, wantLabels)
	}
	if !reflect.DeepEqual(indices, wantIndices) {
		t.Errorf("indices = %v, want %v", indices, wantIndices)
	}
}

// ---------------------------------------------------------------------
// AllValues shape + cache
// ---------------------------------------------------------------------

func TestRecord_AllValues_SetFieldsStayStringSlices(t *testing.T) {
	schema := makeSetTestSchema(t)
	r := r0(schema, 0b0110) // MC, AMEX

	v, present := r.AllValues()["tags"]
	if !present {
		t.Fatalf("AllValues has no entry for tags")
	}
	labels, ok := v.([]string)
	if !ok {
		t.Fatalf("AllValues[tags] type = %T, want []string", v)
	}
	if !reflect.DeepEqual(labels, []string{"MC", "AMEX"}) {
		t.Errorf("AllValues[tags] = %v, want [MC AMEX]", labels)
	}
}

func TestRecord_AllValues_CacheInvalidatesAfterAttributeInjection(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	r := makeWideSetRecord(schema, maskWithBits(130))

	first := r.AllValues()
	if _, present := first["derived"]; present {
		t.Fatalf("derived present before injection")
	}
	if labels, ok := first["tags"].([]string); !ok || len(labels) != 1 || labels[0] != "T130" {
		t.Fatalf("AllValues[tags] = %v, want [T130]", first["tags"])
	}

	r.Set("derived", 42)
	second := r.AllValues()
	if got, present := second["derived"]; !present || got != float64(42) {
		t.Errorf("AllValues[derived] = (%v, %v), want (42, true)", got, present)
	}
	if labels, ok := second["tags"].([]string); !ok || len(labels) != 1 || labels[0] != "T130" {
		t.Errorf("AllValues[tags] after invalidation = %v, want [T130]", second["tags"])
	}

	// SetWide must invalidate too.
	r.SetWide("tags", maskWithBits(7))
	third := r.AllValues()
	if labels, ok := third["tags"].([]string); !ok || len(labels) != 1 || labels[0] != "T7" {
		t.Errorf("AllValues[tags] after SetWide = %v, want [T7]", third["tags"])
	}
}

// ---------------------------------------------------------------------
// Benchmarks — narrow decode path, before/after comparison
// ---------------------------------------------------------------------

func BenchmarkRecord_SetMaskValue_Narrow(b *testing.B) {
	dict := encoding.NewDictionary()
	for i := 0; i < 64; i++ {
		if _, err := dict.Add(fmt.Sprintf("T%d", i)); err != nil {
			b.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: encoding.FieldTypeSetU64, Dictionary: dict},
	}}
	r := NewRecordWithWide(schema, map[string]float64{}, nil,
		map[string]any{"tags": uint64(0x0F0F0F0F0F0F0F0F)})

	b.ReportAllocs()
	b.ResetTimer()
	acc := 0
	for i := 0; i < b.N; i++ {
		m, ok := r.SetMaskValue("tags")
		if ok && !m.IsEmpty() {
			acc++
		}
	}
	if acc != b.N {
		b.Fatalf("SetMaskValue succeeded %d/%d times", acc, b.N)
	}
}

func BenchmarkNarrowSetValue(b *testing.B) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: encoding.FieldTypeSetU64},
	}}
	r := NewRecordWithWide(schema, map[string]float64{}, nil,
		map[string]any{"tags": uint64(0x0F0F0F0F0F0F0F0F)})

	b.ReportAllocs()
	b.ResetTimer()
	acc := 0
	for i := 0; i < b.N; i++ {
		m, ok, err := narrowSetValue(r, "tags")
		if err == nil && ok && m != 0 {
			acc++
		}
	}
	if acc != b.N {
		b.Fatalf("narrowSetValue succeeded %d/%d times", acc, b.N)
	}
}

func BenchmarkRecord_SetLabels_Narrow(b *testing.B) {
	dict := encoding.NewDictionary()
	for i := 0; i < 64; i++ {
		if _, err := dict.Add(fmt.Sprintf("T%d", i)); err != nil {
			b.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: encoding.FieldTypeSetU64, Dictionary: dict},
	}}
	r := NewRecordWithWide(schema, map[string]float64{}, nil,
		map[string]any{"tags": uint64(0x0F0F0F0F0F0F0F0F)})

	b.ReportAllocs()
	b.ResetTimer()
	n := 0
	for i := 0; i < b.N; i++ {
		labels, _ := r.SetLabels("tags")
		n += len(labels)
	}
	if n == 0 {
		b.Fatal("unreachable")
	}
}
