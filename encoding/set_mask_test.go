package encoding

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// boundaryBits are the bit positions where a [4]uint64 implementation
// breaks: the first and last bit of every word. Every structural test
// below runs this table.
var boundaryBits = []int{0, 63, 64, 127, 128, 191, 192, 255}

// TestSetMask_BitBoundaries pins Has/WithBit/PopCount at every word
// boundary and asserts the bit lands in the word the little-endian word
// order promises — words[0] is the LOW 64 bits.
func TestSetMask_BitBoundaries(t *testing.T) {
	for _, bit := range boundaryBits {
		t.Run("bit"+itoa(bit), func(t *testing.T) {
			var zero SetMask
			m := zero.WithBit(bit)

			if !m.Has(bit) {
				t.Fatalf("Has(%d) = false after WithBit(%d)", bit, bit)
			}
			if got := m.PopCount(); got != 1 {
				t.Errorf("PopCount() = %d, want 1", got)
			}
			if m.IsEmpty() {
				t.Error("IsEmpty() = true for a mask with one bit set")
			}
			// The neighbours must stay clear — an off-by-one in the
			// word/bit split shows up here and nowhere else.
			if bit > 0 && m.Has(bit-1) {
				t.Errorf("Has(%d) = true, want false", bit-1)
			}
			if bit < SetMaskBits-1 && m.Has(bit+1) {
				t.Errorf("Has(%d) = true, want false", bit+1)
			}

			words := m.Words()
			wantWord, wantBit := bit/64, uint(bit%64)
			for w := range words {
				var want uint64
				if w == wantWord {
					want = uint64(1) << wantBit
				}
				if words[w] != want {
					t.Errorf("words[%d] = %#016x, want %#016x", w, words[w], want)
				}
			}

			if got := m.HighestBit(); got != bit {
				t.Errorf("HighestBit() = %d, want %d", got, bit)
			}
			if cleared := m.WithoutBit(bit); !cleared.IsEmpty() {
				t.Errorf("WithoutBit(%d) left %v set", bit, cleared.Words())
			}
		})
	}
}

// TestSetMask_OutOfRangeBitsAreNoOps keeps a bad index from corrupting a
// neighbouring word (the classic [4]uint64 index-out-of-range panic) or
// silently wrapping to bit 0.
func TestSetMask_OutOfRangeBitsAreNoOps(t *testing.T) {
	base := SetMask{}.WithBit(7)
	for _, bit := range []int{-1, -64, SetMaskBits, SetMaskBits + 1, 1 << 20} {
		if base.Has(bit) {
			t.Errorf("Has(%d) = true, want false", bit)
		}
		if got := base.WithBit(bit); !got.Equal(base) {
			t.Errorf("WithBit(%d) changed the mask: %v", bit, got.Words())
		}
		if got := base.WithoutBit(bit); !got.Equal(base) {
			t.Errorf("WithoutBit(%d) changed the mask: %v", bit, got.Words())
		}
	}
}

// TestSetMask_ValueSemantics is the load-bearing guarantee of the fixed
// [4]uint64 decision: a SetMask copies by assignment, and no method hands
// back a view that aliases the receiver's backing array. A slice-backed
// implementation would fail every assertion here.
func TestSetMask_ValueSemantics(t *testing.T) {
	original := SetMask{}.WithBit(3).WithBit(200)

	// Assignment copy, then mutate the copy's value.
	copied := original
	copied = copied.WithBit(100)
	if original.Has(100) {
		t.Error("WithBit on an assignment copy mutated the original")
	}
	if !copied.Has(3) || !copied.Has(200) {
		t.Error("assignment copy lost the original's bits")
	}

	// Words() must hand back a detached array.
	words := original.Words()
	words[0] = ^uint64(0)
	words[3] = ^uint64(0)
	if original.Words()[0] != (uint64(1) << 3) {
		t.Error("mutating the Words() result aliased back into the receiver")
	}

	// Union/Intersect must not mutate either operand.
	other := SetMask{}.WithBit(64)
	before := original.Words()
	_ = original.Union(other)
	_ = original.Intersect(other)
	if original.Words() != before {
		t.Error("Union/Intersect mutated the receiver")
	}
	if !other.Equal(SetMask{}.WithBit(64)) {
		t.Error("Union/Intersect mutated the argument")
	}
}

// TestSetMask_SetOps exercises Union/Intersect/Equal across word
// boundaries, where a loop that stops at one word passes a single-word
// test and fails here.
func TestSetMask_SetOps(t *testing.T) {
	a := SetMask{}.WithBit(0).WithBit(64).WithBit(191)
	b := SetMask{}.WithBit(64).WithBit(192).WithBit(255)

	union := a.Union(b)
	for _, bit := range []int{0, 64, 191, 192, 255} {
		if !union.Has(bit) {
			t.Errorf("Union missing bit %d", bit)
		}
	}
	if got := union.PopCount(); got != 5 {
		t.Errorf("Union PopCount = %d, want 5", got)
	}

	inter := a.Intersect(b)
	if got := inter.PopCount(); got != 1 || !inter.Has(64) {
		t.Errorf("Intersect = %v, want only bit 64", inter.Words())
	}

	if !a.Equal(SetMask{}.WithBit(0).WithBit(64).WithBit(191)) {
		t.Error("Equal = false for two identically built masks")
	}
	if a.Equal(b) {
		t.Error("Equal = true for differing masks")
	}
	// Equality must compare every word — differ only in the top word.
	if (SetMask{}).WithBit(255).Equal(SetMask{}) {
		t.Error("Equal ignored the high word")
	}

	var empty SetMask
	if !empty.IsEmpty() || empty.PopCount() != 0 || empty.HighestBit() != -1 {
		t.Errorf("zero value is not empty: pop=%d highest=%d", empty.PopCount(), empty.HighestBit())
	}
}

// TestSetMask_AscendingIterator asserts the iterator visits set bits in
// ascending order across all four words and that NextBit agrees with it.
func TestSetMask_AscendingIterator(t *testing.T) {
	want := []int{0, 5, 63, 64, 127, 128, 191, 192, 255}
	m := SetMask{}
	for _, bit := range want {
		m = m.WithBit(bit)
	}

	var got []int
	for bit := range m.Bits() {
		got = append(got, bit)
	}
	if len(got) != len(want) {
		t.Fatalf("Bits() yielded %d bits (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Bits() = %v, want %v", got, want)
		}
	}

	// NextBit is the allocation-free primitive the iterator rides on.
	var walked []int
	for bit, ok := m.NextBit(0); ok; bit, ok = m.NextBit(bit + 1) {
		walked = append(walked, bit)
	}
	for i := range want {
		if walked[i] != want[i] {
			t.Fatalf("NextBit walk = %v, want %v", walked, want)
		}
	}
	if _, ok := m.NextBit(SetMaskBits); ok {
		t.Error("NextBit past the ceiling reported a bit")
	}
	if _, ok := (SetMask{}).NextBit(0); ok {
		t.Error("NextBit on an empty mask reported a bit")
	}

	// Early termination must stop the walk.
	count := 0
	for range m.Bits() {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("early break yielded %d bits, want 2", count)
	}
}

// TestSetMask_LabelsAreDictionaryBounded holds the walk to dict.Count()
// rather than to the type's bit width. A mask carrying a bit past the
// dictionary is a corrupt or mid-remap payload, not a panic.
func TestSetMask_LabelsAreDictionaryBounded(t *testing.T) {
	dict := NewDictionary()
	for _, v := range []string{"alpha", "beta", "gamma"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}

	m := SetMask{}.WithBit(0).WithBit(2).WithBit(3).WithBit(64).WithBit(255)
	got := m.Labels(dict)
	want := []string{"alpha", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("Labels() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Labels() = %v, want %v", got, want)
		}
	}

	if got := (SetMask{}).Labels(dict); len(got) != 0 {
		t.Errorf("Labels() on an empty mask = %v, want empty", got)
	}
	if got := m.Labels(nil); len(got) != 0 {
		t.Errorf("Labels(nil) = %v, want empty", got)
	}
	if got := m.Labels(NewDictionary()); len(got) != 0 {
		t.Errorf("Labels() against an empty dictionary = %v, want empty", got)
	}

	// A dictionary wider than one word: the walk must be bounded by
	// dict.Count(), not by a hardcoded 64-bit width. The narrow-rung
	// helper this replaces stops at bit 64, so every selection above the
	// first word would silently lose its label.
	wide := NewDictionary()
	for i := 0; i < 200; i++ {
		if _, err := wide.Add("v" + itoa(i)); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	wideMask := SetMask{}.WithBit(0).WithBit(64).WithBit(127).WithBit(128).WithBit(199).WithBit(200).WithBit(255)
	wideWant := []string{"v0", "v64", "v127", "v128", "v199"}
	wideGot := wideMask.Labels(wide)
	if len(wideGot) != len(wideWant) {
		t.Fatalf("Labels() over a 200-entry dictionary = %v, want %v", wideGot, wideWant)
	}
	for i := range wideWant {
		if wideGot[i] != wideWant[i] {
			t.Fatalf("Labels() over a 200-entry dictionary = %v, want %v", wideGot, wideWant)
		}
	}
}

// TestSetMask_Uint64Bridge covers the narrow-rung bridge: a mask that
// originated in a uint64 converts back losslessly, and one carrying a bit
// above 64 reports ok=false instead of handing back a truncated value.
func TestSetMask_Uint64Bridge(t *testing.T) {
	narrow := SetMaskFromUint64(^uint64(0))
	if got := narrow.PopCount(); got != 64 {
		t.Errorf("PopCount() = %d, want 64", got)
	}
	v, ok := narrow.Uint64()
	if !ok || v != ^uint64(0) {
		t.Errorf("Uint64() = (%#x, %v), want (%#x, true)", v, ok, ^uint64(0))
	}
	if narrow.Has(64) {
		t.Error("SetMaskFromUint64 leaked a bit into word 1")
	}

	wide := narrow.WithBit(64)
	v, ok = wide.Uint64()
	if ok {
		t.Error("Uint64() reported ok for a mask wider than 64 bits")
	}
	if v != ^uint64(0) {
		t.Errorf("Uint64() low word = %#x, want %#x", v, ^uint64(0))
	}

	if got := SetMaskFromWords([SetMaskWords]uint64{1, 2, 3, 4}).Words(); got != [SetMaskWords]uint64{1, 2, 3, 4} {
		t.Errorf("SetMaskFromWords round trip = %v", got)
	}
}

// TestSetMask_FitsFieldType gates the write path: a mask must not carry a
// bit the declared rung cannot store, because the excess would be dropped
// silently on the wire.
func TestSetMask_FitsFieldType(t *testing.T) {
	cases := []struct {
		ft   FieldType
		bit  int
		want bool
	}{
		{FieldTypeSetU128, 127, true},
		{FieldTypeSetU128, 128, false},
		{FieldTypeSetU256, 255, true},
		{FieldTypeSetU64, 63, true},
		{FieldTypeSetU64, 64, false},
		{FieldTypeSetU8, 7, true},
		{FieldTypeSetU8, 8, false},
		{FieldTypeU64, 0, false}, // not a set type: nothing fits
	}
	for _, tc := range cases {
		m := SetMask{}.WithBit(tc.bit)
		if got := m.FitsFieldType(tc.ft); got != tc.want {
			t.Errorf("bit %d FitsFieldType(%s) = %v, want %v", tc.bit, tc.ft, got, tc.want)
		}
	}
	if !(SetMask{}).FitsFieldType(FieldTypeSetU128) {
		t.Error("an empty mask must fit set_u128")
	}
}

// TestSetMask_WireWordOrder is the normative word-order test: words[0] is
// the low 64 bits and the words are laid down low-word-first. Swap the
// word order in PutSetMask and every expectation here moves.
func TestSetMask_WireWordOrder(t *testing.T) {
	cases := []struct {
		bit      int
		wantByte int
	}{
		{0, 0}, {7, 0}, {8, 1}, {63, 7},
		{64, 8}, {127, 15},
		{128, 16}, {191, 23},
		{192, 24}, {255, 31},
	}
	for _, tc := range cases {
		t.Run("bit"+itoa(tc.bit), func(t *testing.T) {
			ft := FieldTypeSetU256
			if tc.bit < 128 {
				ft = FieldTypeSetU128
			}
			buf := make([]byte, ft.ByteSize())
			if err := PutSetMask(buf, ft, SetMask{}.WithBit(tc.bit)); err != nil {
				t.Fatalf("PutSetMask: %v", err)
			}
			for i, b := range buf {
				var want byte
				if i == tc.wantByte {
					want = 1 << uint(tc.bit%8)
				}
				if b != want {
					t.Fatalf("byte %d = %#02x, want %#02x (bit %d)", i, b, want, tc.bit)
				}
			}
		})
	}
}

// TestSetMask_WireRoundTrip round-trips both wide rungs byte-identically,
// including the all-zero and all-ones masks at each width.
func TestSetMask_WireRoundTrip(t *testing.T) {
	allOnes128 := SetMaskFromWords([SetMaskWords]uint64{^uint64(0), ^uint64(0), 0, 0})
	allOnes256 := SetMaskFromWords([SetMaskWords]uint64{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)})

	cases := []struct {
		name string
		ft   FieldType
		mask SetMask
	}{
		{"u128/zero", FieldTypeSetU128, SetMask{}},
		{"u128/all-ones", FieldTypeSetU128, allOnes128},
		{"u128/boundaries", FieldTypeSetU128, SetMask{}.WithBit(0).WithBit(63).WithBit(64).WithBit(127)},
		{"u256/zero", FieldTypeSetU256, SetMask{}},
		{"u256/all-ones", FieldTypeSetU256, allOnes256},
		{"u256/boundaries", FieldTypeSetU256, SetMask{}.WithBit(0).WithBit(128).WithBit(191).WithBit(255)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			width := tc.ft.ByteSize()

			buf := make([]byte, width)
			if err := PutSetMask(buf, tc.ft, tc.mask); err != nil {
				t.Fatalf("PutSetMask: %v", err)
			}
			back, err := SetMaskFromBytes(tc.ft, buf)
			if err != nil {
				t.Fatalf("SetMaskFromBytes: %v", err)
			}
			if !back.Equal(tc.mask) {
				t.Fatalf("slice round trip = %v, want %v", back.Words(), tc.mask.Words())
			}

			var w bytes.Buffer
			if err := WriteSetMask(&w, tc.ft, tc.mask); err != nil {
				t.Fatalf("WriteSetMask: %v", err)
			}
			if w.Len() != width {
				t.Fatalf("WriteSetMask wrote %d bytes, want %d", w.Len(), width)
			}
			if !bytes.Equal(w.Bytes(), buf) {
				t.Fatalf("stream bytes %x differ from slice bytes %x", w.Bytes(), buf)
			}
			streamed, err := ReadSetMask(&w, tc.ft)
			if err != nil {
				t.Fatalf("ReadSetMask: %v", err)
			}
			if !streamed.Equal(tc.mask) {
				t.Fatalf("stream round trip = %v, want %v", streamed.Words(), tc.mask.Words())
			}
			if got := streamed.PopCount(); got != tc.mask.PopCount() {
				t.Fatalf("PopCount after round trip = %d, want %d", got, tc.mask.PopCount())
			}
		})
	}
}

// TestSetMask_WireRejectsNonWideTypes keeps the narrow rungs (which keep
// their uint64 wire layout) and every non-set type out of the wide API.
func TestSetMask_WireRejectsNonWideTypes(t *testing.T) {
	for _, ft := range []FieldType{
		FieldTypeSetU8, FieldTypeSetU16, FieldTypeSetU32, FieldTypeSetU64,
		FieldTypeU64, FieldTypeDecimal128, FieldTypeCategoricalU32, FieldTypePackedBool,
	} {
		t.Run(ft.String(), func(t *testing.T) {
			buf := make([]byte, 32)
			if err := PutSetMask(buf, ft, SetMask{}); !errors.HasCode(err, errors.ENCODING_TYPE_MISMATCH) {
				t.Errorf("PutSetMask err = %v, want ENCODING_TYPE_MISMATCH", err)
			}
			if _, err := SetMaskFromBytes(ft, buf); !errors.HasCode(err, errors.ENCODING_TYPE_MISMATCH) {
				t.Errorf("SetMaskFromBytes err = %v, want ENCODING_TYPE_MISMATCH", err)
			}
			if err := WriteSetMask(&bytes.Buffer{}, ft, SetMask{}); !errors.HasCode(err, errors.ENCODING_TYPE_MISMATCH) {
				t.Errorf("WriteSetMask err = %v, want ENCODING_TYPE_MISMATCH", err)
			}
			if _, err := ReadSetMask(bytes.NewReader(buf), ft); !errors.HasCode(err, errors.ENCODING_TYPE_MISMATCH) {
				t.Errorf("ReadSetMask err = %v, want ENCODING_TYPE_MISMATCH", err)
			}
		})
	}
}

// TestSetMask_WriteRejectsOverWidthMask is the anti-silent-truncation
// gate: storing a 129th selection in a set_u128 must fail loudly.
func TestSetMask_WriteRejectsOverWidthMask(t *testing.T) {
	tooWide := SetMask{}.WithBit(128)
	buf := make([]byte, 16)
	err := PutSetMask(buf, FieldTypeSetU128, tooWide)
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("PutSetMask err = %v, want ENCODING_INVALID", err)
	}
	if !bytes.Equal(buf, make([]byte, 16)) {
		t.Errorf("rejected write still touched the buffer: %x", buf)
	}
	if err := WriteSetMask(&bytes.Buffer{}, FieldTypeSetU128, tooWide); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("WriteSetMask err = %v, want ENCODING_INVALID", err)
	}
}

// TestSetMask_ShortBufferRejected keeps a truncated destination or source
// from being half-written or read out of a neighbouring field.
func TestSetMask_ShortBufferRejected(t *testing.T) {
	for _, ft := range []FieldType{FieldTypeSetU128, FieldTypeSetU256} {
		short := make([]byte, ft.ByteSize()-1)
		if err := PutSetMask(short, ft, SetMask{}.WithBit(0)); !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("%s: PutSetMask short buffer err = %v, want ENCODING_INVALID", ft, err)
		}
		if _, err := SetMaskFromBytes(ft, short); !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("%s: SetMaskFromBytes short buffer err = %v, want ENCODING_INVALID", ft, err)
		}
		if _, err := ReadSetMask(bytes.NewReader(short), ft); err == nil {
			t.Errorf("%s: ReadSetMask on a truncated stream returned nil error", ft)
		}
	}
}

// TestSetMask_NeighbourFieldsUntouched is the stride test: a set_u128
// sitting between a u64 and a u32 reads and writes exactly its own 16
// bytes. An off-by-one width silently corrupts the column next door.
func TestSetMask_NeighbourFieldsUntouched(t *testing.T) {
	schema := &Schema{Fields: []Field{
		{Name: "before", Type: FieldTypeU64, ByteOffset: 0},
		{Name: "tags", Type: FieldTypeSetU128, ByteOffset: 8},
		{Name: "after", Type: FieldTypeU32, ByteOffset: 24},
	}}
	if got := schema.RecordByteSize(); got != 28 {
		t.Fatalf("RecordByteSize = %d, want 28", got)
	}
	off := schema.Field("tags").ByteOffset
	width := FieldTypeSetU128.ByteSize()

	// Write: neighbours pre-filled with a recognisable pattern.
	rec := bytes.Repeat([]byte{0xAA}, schema.RecordByteSize())
	mask := SetMask{}.WithBit(0).WithBit(127)
	if err := PutSetMask(rec[off:off+width], FieldTypeSetU128, mask); err != nil {
		t.Fatalf("PutSetMask: %v", err)
	}
	for i := 0; i < off; i++ {
		if rec[i] != 0xAA {
			t.Fatalf("byte %d before the field = %#02x, want 0xAA", i, rec[i])
		}
	}
	for i := off + width; i < len(rec); i++ {
		if rec[i] != 0xAA {
			t.Fatalf("byte %d after the field = %#02x, want 0xAA", i, rec[i])
		}
	}

	// Read: neighbours set to all-ones, the field itself zeroed. A read
	// that overruns either edge shows up as a non-empty mask.
	rec = bytes.Repeat([]byte{0xFF}, schema.RecordByteSize())
	for i := off; i < off+width; i++ {
		rec[i] = 0
	}
	got, err := SetMaskFromBytes(FieldTypeSetU128, rec[off:off+width])
	if err != nil {
		t.Fatalf("SetMaskFromBytes: %v", err)
	}
	if !got.IsEmpty() {
		t.Fatalf("read bled into a neighbour: %v", got.Words())
	}

	// And the written value survives a full-record round trip.
	rec = make([]byte, schema.RecordByteSize())
	if err := PutSetMask(rec[off:off+width], FieldTypeSetU128, mask); err != nil {
		t.Fatalf("PutSetMask: %v", err)
	}
	got, err = SetMaskFromBytes(FieldTypeSetU128, rec[off:off+width])
	if err != nil {
		t.Fatalf("SetMaskFromBytes: %v", err)
	}
	if !got.Equal(mask) {
		t.Fatalf("in-record round trip = %v, want %v", got.Words(), mask.Words())
	}
}

// TestSetMask_WideTypesStillRejectedByScalarAPI keeps E1-S1's branch point
// honest: the scalar uint64 API must keep pointing at this one.
func TestSetMask_WideTypesStillRejectedByScalarAPI(t *testing.T) {
	for _, ft := range []FieldType{FieldTypeSetU128, FieldTypeSetU256} {
		_, err := ReadFieldValue(bytes.NewReader(make([]byte, 32)), ft)
		if !errors.HasCode(err, errors.ENCODING_TYPE_MISMATCH) {
			t.Errorf("%s: ReadFieldValue err = %v, want ENCODING_TYPE_MISMATCH", ft, err)
		}
		if err != nil && !strings.Contains(err.Error(), "wide-set API") {
			t.Errorf("%s: error %q should point at the wide-set API", ft, err)
		}
	}
}

// TestSetMask_ZeroAllocation backs the benchmarks: Has and PopCount on a
// narrow-origin mask must not allocate, which is the whole reason the
// value type is a fixed array rather than a slice.
func TestSetMask_ZeroAllocation(t *testing.T) {
	m := SetMaskFromUint64(0xDEADBEEFCAFEF00D).WithBit(200)
	if got := testing.AllocsPerRun(100, func() {
		sinkBool = m.Has(63)
		sinkInt = m.PopCount()
		sinkBool = m.Union(m).Equal(m)
	}); got != 0 {
		t.Errorf("Has/PopCount/Union allocated %v times per run, want 0", got)
	}
	if got := testing.AllocsPerRun(100, func() {
		bit, ok := m.NextBit(0)
		sinkInt = bit
		sinkBool = ok
	}); got != 0 {
		t.Errorf("NextBit allocated %v times per run, want 0", got)
	}
}

var (
	sinkBool bool
	sinkInt  int
)

// BenchmarkSetMask_Has measures the per-bit probe on a narrow-origin mask.
func BenchmarkSetMask_Has(b *testing.B) {
	m := SetMaskFromUint64(0xDEADBEEFCAFEF00D)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		sinkBool = m.Has(i % SetMaskBits)
	}
}

// BenchmarkSetMask_PopCount measures the full four-word population count.
func BenchmarkSetMask_PopCount(b *testing.B) {
	m := SetMaskFromUint64(0xDEADBEEFCAFEF00D)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sinkInt = m.PopCount()
	}
}

// BenchmarkSetMask_RoundTrip measures the set_u256 wire round trip.
func BenchmarkSetMask_RoundTrip(b *testing.B) {
	m := SetMaskFromUint64(0xDEADBEEFCAFEF00D).WithBit(200)
	buf := make([]byte, FieldTypeSetU256.ByteSize())
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := PutSetMask(buf, FieldTypeSetU256, m); err != nil {
			b.Fatal(err)
		}
		got, err := SetMaskFromBytes(FieldTypeSetU256, buf)
		if err != nil {
			b.Fatal(err)
		}
		sinkInt = got.PopCount()
	}
}
