package encoding

import (
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestDictionaryReadFrom_ForgedCountAllocatesNothing pins that a
// dictionary block's u32 count is a length the input merely declares: a
// block claiming 4 Mi entries over an empty tail fails ENCODING_INVALID
// without preallocating for them (it used to size its slice, map and
// string buffer from the count — about 400 MB here, 5 GB for a fuzzed
// cohort — before reading the first entry).
func TestDictionaryReadFrom_ForgedCountAllocatesNothing(t *testing.T) {
	// serial: measures process-wide heap allocation (runtime.MemStats),
	// which a parallel test's allocations would inflate.
	var block [4]byte
	binary.LittleEndian.PutUint32(block[:], 4<<20)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := NewDictionary().ReadFrom(bytes.NewReader(block[:]))
	runtime.ReadMemStats(&after)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID {
		t.Fatalf("err = %v, want ENCODING_INVALID", err)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 8<<20 {
		t.Fatalf("forged count allocated %d bytes before the stream ran dry, want < 8 MiB", got)
	}
}

// TestDictionaryReadFrom_LargeDictionaryRoundTrips pins that capping the
// preallocation does not cap the dictionary: one past the cap reads back
// whole, in order, with its lookup intact.
func TestDictionaryReadFrom_LargeDictionaryRoundTrips(t *testing.T) {
	t.Parallel()
	d := NewDictionary()
	for i := 0; i <= dictPreallocCap; i++ {
		d.Add(string(rune('a'+i%26)) + string(rune(0x100+i)))
	}
	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	got := NewDictionary()
	if _, err := got.ReadFrom(&buf); err != nil {
		t.Fatal(err)
	}
	if got.Count() != d.Count() {
		t.Fatalf("count = %d, want %d", got.Count(), d.Count())
	}
	for i := 0; i < d.Count(); i++ {
		want := d.Resolve(uint32(i))
		if v := got.Resolve(uint32(i)); v != want {
			t.Fatalf("entry %d = %q, want %q", i, v, want)
		}
		if id, ok := got.IDFor(want); !ok || id != uint32(i) {
			t.Fatalf("IDFor(%q) = %d, %v, want %d", want, id, ok, i)
		}
	}
}

// TestReadSchema_RefusesDecimalScaleOutOfRange pins that a decimal128
// field's stored scale past MaxDecimalPrecision is a corrupt schema
// block (ENCODING_INVALID, details.reason decimal_scale_out_of_range):
// no writer stores one, and decoding a value at it used to panic
// (index out of range on the power-of-ten table) on the first record.
// The maximum scale still reads.
func TestReadSchema_RefusesDecimalScaleOutOfRange(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		scale uint8
		ok    bool
	}{{MaxDecimalPrecision, true}, {MaxDecimalPrecision + 1, false}, {0x77, false}} {
		var buf bytes.Buffer
		schema := &Schema{Fields: []Field{{Name: "amount", Type: FieldTypeDecimal128, Precision: MaxDecimalPrecision, Scale: tc.scale}}}
		if err := WriteSchema(&buf, schema); err != nil {
			t.Fatal(err)
		}
		got, err := ReadSchema(&buf, FormatVersionV1)
		if tc.ok {
			if err != nil || got.Fields[0].Scale != tc.scale {
				t.Fatalf("scale %d: err = %v", tc.scale, err)
			}
			continue
		}
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID || ce.Details["reason"] != "decimal_scale_out_of_range" {
			t.Fatalf("scale %d: err = %v, want ENCODING_INVALID decimal_scale_out_of_range", tc.scale, err)
		}
	}
}
