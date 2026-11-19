package labelcodes

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// TestCodeMax: the integer-coded set is exactly u4 / u8 / u16 / u32, each
// with its largest stored value; u64, floats and categoricals are not.
func TestCodeMax(t *testing.T) {
	t.Parallel()
	for ft, want := range map[encoding.FieldType]uint64{
		encoding.FieldTypeU4: 15, encoding.FieldTypeU8: 255, encoding.FieldTypeU16: 65535, encoding.FieldTypeU32: 1<<32 - 1,
	} {
		if got, ok := CodeMax(ft); !ok || got != want {
			t.Errorf("CodeMax(%s) = %d, %v; want %d, true", ft, got, ok, want)
		}
	}
	for _, ft := range []encoding.FieldType{encoding.FieldTypeU64, encoding.FieldTypeF64, encoding.FieldTypeCategoricalU8, encoding.FieldTypeDate} {
		if _, ok := CodeMax(ft); ok {
			t.Errorf("CodeMax(%s) reported integer-coded", ft)
		}
	}
}

// TestCheckKeys: a key is accepted only as the plain decimal spelling of a
// value within max; the first offending key in sorted order is named, with
// the caller's scope and option.
func TestCheckKeys(t *testing.T) {
	t.Parallel()
	if ce := CheckKeys("labels[0]", "labels", "f", encoding.FieldTypeU4, 15, []string{"15", "0", "7"}); ce != nil {
		t.Fatalf("valid keys refused: %v", ce)
	}
	for _, bad := range []string{"16", "01", "-1", "+1", "1.0", "x", ""} {
		ce := CheckKeys("labels[0]", "labels", "f", encoding.FieldTypeU4, 15, []string{"3", bad})
		if ce == nil || ce.Code != errors.PULSE_LABEL_KEY_INVALID {
			t.Errorf("key %q: got %v, want PULSE_LABEL_KEY_INVALID", bad, ce)
			continue
		}
		if ce.Details["key"] != bad || ce.Details["option"] != "labels" || ce.Details["max"] != uint64(15) {
			t.Errorf("key %q: details %v", bad, ce.Details)
		}
	}
	ce := CheckKeys("export", "--labels", "f", encoding.FieldTypeU8, 255, []string{"z", "a"})
	if ce == nil || ce.Details["key"] != "a" || ce.Details["option"] != "--labels" {
		t.Errorf("sorted first key / option: %v", ce)
	}
}
