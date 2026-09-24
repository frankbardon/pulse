package synth

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// TestFieldTypeFromName_ResolvesEverySetRung pins that the spec type
// names for the set family resolve from the encoding enum rather than
// from a rung table maintained here. A missing rung is silent at the
// CLI (internal/cli/fieldtype.go falls back to f64 on an unknown name)
// and a hard refusal in the writer, so both directions are checked.
func TestFieldTypeFromName_ResolvesEverySetRung(t *testing.T) {
	for _, want := range []encoding.FieldType{
		encoding.FieldTypeSetU8,
		encoding.FieldTypeSetU16,
		encoding.FieldTypeSetU32,
		encoding.FieldTypeSetU64,
		encoding.FieldTypeSetU128,
		encoding.FieldTypeSetU256,
	} {
		name := want.String()
		got, ok := fieldTypeFromName(name)
		if !ok {
			t.Fatalf("fieldTypeFromName(%q) = not declarable; a spec naming this rung cannot generate", name)
		}
		if got != want {
			t.Fatalf("fieldTypeFromName(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestFieldTypeFromName_DatetimeStaysUndeclarable guards the blast
// radius of resolving set names through encoding.ParseFieldType:
// datetime deliberately has no synth write path, and widening the
// lookup to every encoding name would make a datetime spec reach the
// encoder. See undeclarableFieldTypes in constraints_internal_test.go.
func TestFieldTypeFromName_DatetimeStaysUndeclarable(t *testing.T) {
	if ft, ok := fieldTypeFromName("datetime"); ok {
		t.Fatalf("datetime became declarable as %v; synth has no write path for it", ft)
	}
}

// setFieldForTest builds an encoding.Field of the given set rung with a
// dictionary holding n entries, bypassing AddWithLimit so the caller
// can construct a dictionary WIDER than the rung addresses.
func setFieldForTest(t *testing.T, ft encoding.FieldType, n int) *encoding.Field {
	t.Helper()
	d := encoding.NewDictionary()
	for i := 0; i < n; i++ {
		if _, err := d.Add(optName(i)); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	return &encoding.Field{Name: "features", Type: ft, Dictionary: d}
}

func optName(i int) string {
	const digits = "0123456789"
	return "opt" + string([]byte{digits[(i/100)%10], digits[(i/10)%10], digits[i%10]})
}

// TestWriteFieldValueForField_SetRungWidths pins the on-wire width of
// every set rung against the schema-derived stride. A rung written at
// the wrong width shifts every following column by a fixed offset and
// the cohort still decodes into plausible values.
func TestWriteFieldValueForField_SetRungWidths(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU8,
		encoding.FieldTypeSetU16,
		encoding.FieldTypeSetU32,
		encoding.FieldTypeSetU64,
		encoding.FieldTypeSetU128,
		encoding.FieldTypeSetU256,
	} {
		t.Run(ft.String(), func(t *testing.T) {
			f := setFieldForTest(t, ft, int(ft.MaxSetEntries()))
			sel := map[string]bool{optName(0): true, optName(int(ft.MaxSetEntries()) - 1): true}
			var buf bytes.Buffer
			if err := writeFieldValueForField(&buf, f, sel, false); err != nil {
				t.Fatalf("write: %v", err)
			}
			if buf.Len() != ft.ByteSize() {
				t.Fatalf("wrote %d bytes, want ByteSize() = %d", buf.Len(), ft.ByteSize())
			}

			// A null must occupy the same width — set nulls ride the
			// bitmap only, with no in-band sentinel, so the payload is
			// an all-zero mask of full width.
			var nullBuf bytes.Buffer
			if err := writeFieldValueForField(&nullBuf, f, nil, true); err != nil {
				t.Fatalf("write null: %v", err)
			}
			if nullBuf.Len() != ft.ByteSize() {
				t.Fatalf("null wrote %d bytes, want %d", nullBuf.Len(), ft.ByteSize())
			}
			for i, b := range nullBuf.Bytes() {
				if b != 0 {
					t.Fatalf("null payload byte %d = %#x, want 0", i, b)
				}
			}
		})
	}
}

// TestWriteFieldValueForField_WideSetRoundTripsHighBits is the direct
// silent-failure guard: the high words must survive the write. A mask
// truncated to its low 64 bits writes the right NUMBER of bytes and
// reads back as a smaller, entirely plausible selection.
func TestWriteFieldValueForField_WideSetRoundTripsHighBits(t *testing.T) {
	for _, tc := range []struct {
		ft   encoding.FieldType
		bits []int
	}{
		{encoding.FieldTypeSetU128, []int{0, 63, 64, 127}},
		{encoding.FieldTypeSetU256, []int{0, 63, 64, 128, 191, 192, 255}},
	} {
		t.Run(tc.ft.String(), func(t *testing.T) {
			f := setFieldForTest(t, tc.ft, int(tc.ft.MaxSetEntries()))
			sel := make(map[string]bool, len(tc.bits))
			for _, b := range tc.bits {
				sel[optName(b)] = true
			}
			var buf bytes.Buffer
			if err := writeFieldValueForField(&buf, f, sel, false); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := encoding.SetMaskFromBytes(tc.ft, buf.Bytes())
			if err != nil {
				t.Fatalf("SetMaskFromBytes: %v", err)
			}
			if got.PopCount() != len(tc.bits) {
				t.Fatalf("PopCount = %d, want %d (mask %v)", got.PopCount(), len(tc.bits), got.Words())
			}
			for _, b := range tc.bits {
				if !got.Has(b) {
					t.Fatalf("bit %d did not survive the write (mask %v)", b, got.Words())
				}
			}
		})
	}
}

// TestWriteFieldValueForField_SetLabelSliceRoundTripsHighBits covers
// the []string arm — the re-encode path augment.go feeds from decoded
// rows — at the wide rungs.
func TestWriteFieldValueForField_SetLabelSliceRoundTripsHighBits(t *testing.T) {
	ft := encoding.FieldTypeSetU256
	f := setFieldForTest(t, ft, int(ft.MaxSetEntries()))
	labels := []string{optName(2), optName(70), optName(201)}
	var buf bytes.Buffer
	if err := writeFieldValueForField(&buf, f, labels, false); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := encoding.SetMaskFromBytes(ft, buf.Bytes())
	if err != nil {
		t.Fatalf("SetMaskFromBytes: %v", err)
	}
	for _, b := range []int{2, 70, 201} {
		if !got.Has(b) {
			t.Fatalf("bit %d did not survive the []string write (mask %v)", b, got.Words())
		}
	}
	if got.PopCount() != 3 {
		t.Fatalf("PopCount = %d, want 3", got.PopCount())
	}
}

// TestWriteFieldValueForField_NarrowSetRefusesOutOfRangeBit pins the
// guard on a dictionary wider than its declared rung. The old encoder
// OR'd `1 << id` into a uint64 and handed it to WriteFieldValue, which
// truncates to the rung width — the selection simply vanished. Refusing
// is the only honest outcome.
func TestWriteFieldValueForField_NarrowSetRefusesOutOfRangeBit(t *testing.T) {
	f := setFieldForTest(t, encoding.FieldTypeSetU8, 12)
	var buf bytes.Buffer
	err := writeFieldValueForField(&buf, f, map[string]bool{optName(10): true}, false)
	if err == nil {
		t.Fatal("expected a refusal for a bit beyond the rung's capacity")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("error %v does not carry ENCODING_INVALID", err)
	}
}

// TestDecodedFieldValue_WideSetResolvesSelectedLabels is the augment
// (re-encode) path's silent-failure guard: a wide rung lands in the
// wide map as an encoding.SetMask, not a uint64, so a uint64 type
// assertion yields the zero value and every re-encoded row loses its
// entire selection while the run reports success.
func TestDecodedFieldValue_WideSetResolvesSelectedLabels(t *testing.T) {
	ft := encoding.FieldTypeSetU256
	f := setFieldForTest(t, ft, int(ft.MaxSetEntries()))
	mask := encoding.SetMask{}.WithBit(1).WithBit(64).WithBit(200)

	values := map[string]float64{"features": 0}
	nulls := map[string]bool{}
	wide := map[string]any{"features": mask}

	val, isNull := decodedFieldValue(f, values, nulls, wide)
	if isNull {
		t.Fatal("decodedFieldValue reported null for a populated mask")
	}
	got, ok := val.([]string)
	if !ok {
		t.Fatalf("decodedFieldValue returned %T, want []string", val)
	}
	want := []string{optName(1), optName(64), optName(200)}
	if len(got) != len(want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("labels = %v, want %v", got, want)
		}
	}
}

// TestDecodedFieldValue_NarrowSetStillResolvesFromUint64 keeps the
// narrow rungs on their existing uint64 wide-map form, so the fix above
// cannot be "only SetMask works now".
func TestDecodedFieldValue_NarrowSetStillResolvesFromUint64(t *testing.T) {
	f := setFieldForTest(t, encoding.FieldTypeSetU8, 4)
	wide := map[string]any{"features": uint64(0b1010)}
	val, isNull := decodedFieldValue(f, map[string]float64{}, map[string]bool{}, wide)
	if isNull {
		t.Fatal("unexpected null")
	}
	got, ok := val.([]string)
	if !ok {
		t.Fatalf("decodedFieldValue returned %T, want []string", val)
	}
	want := []string{optName(1), optName(3)}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("labels = %v, want %v", got, want)
	}
}
