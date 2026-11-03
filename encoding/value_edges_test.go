package encoding

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// The edges of the public value API that the engine's own suites never
// reach: the zero-value Decimal128 (a nil mantissa reads as 0 on every
// accessor), the decimal arithmetic refusals, the bitmap helpers'
// out-of-range guards, and the text forms of the group / vector kinds.
// Each case pins a documented contract, not a line.

func TestDecimal128_ZeroValueReadsAsZero(t *testing.T) {
	t.Parallel()

	var z Decimal128 // nil mantissa
	if got := z.Mantissa(); got.Sign() != 0 {
		t.Errorf("Mantissa() = %v, want 0", got)
	}
	if got := z.Sign(); got != 0 {
		t.Errorf("Sign() = %d, want 0", got)
	}
	if got := z.String(2); got != "0.00" {
		t.Errorf("String(2) = %q, want %q", got, "0.00")
	}
	if got := z.Float64(3); got != 0 {
		t.Errorf("Float64(3) = %v, want 0", got)
	}
	r, err := z.Rescale(0, 4)
	if err != nil || r.Sign() != 0 || r.mantissa == nil {
		t.Errorf("Rescale(0,4) = (%v, %v), want a non-nil zero", r.mantissa, err)
	}
	if !z.FitsPrecision(1) {
		t.Error("FitsPrecision(1) on zero = false, want true")
	}
	// Mantissa returns a copy: mutating it never reaches the value.
	d := NewDecimal128FromInt(7)
	d.Mantissa().SetInt64(99)
	if d.String(0) != "7" {
		t.Errorf("Mantissa() aliased the value: now %s", d.String(0))
	}
}

func TestDecimal128_CmpTreatsNilAsZero(t *testing.T) {
	t.Parallel()

	var z Decimal128
	pos, neg := NewDecimal128FromInt(5), NewDecimal128FromInt(-5)
	cases := []struct {
		name string
		a, b Decimal128
		want int
	}{
		{"nil vs nil", z, z, 0},
		{"nil vs positive", z, pos, -1},
		{"nil vs negative", z, neg, 1},
		{"positive vs nil", pos, z, 1},
		{"negative vs nil", neg, z, -1},
		{"negative vs positive", neg, pos, -1},
	}
	for _, tc := range cases {
		if got := tc.a.Cmp(tc.b); got != tc.want {
			t.Errorf("%s: Cmp = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestNewDecimal128FromBigInt_NilAndOverflow(t *testing.T) {
	t.Parallel()

	d, err := NewDecimal128FromBigInt(nil)
	if err != nil || d.Sign() != 0 {
		t.Fatalf("nil mantissa = (%v, %v), want zero", d.mantissa, err)
	}
	max := new(big.Int).Sub(new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil), big.NewInt(1))
	if _, err := NewDecimal128FromBigInt(max); err != nil {
		t.Errorf("10^38-1 refused: %v", err)
	}
	over := new(big.Int).Neg(new(big.Int).Add(max, big.NewInt(1)))
	if _, err := NewDecimal128FromBigInt(over); !errors.HasCode(err, errors.PULSE_DECIMAL_OVERFLOW) {
		t.Errorf("-10^38: err = %v, want PULSE_DECIMAL_OVERFLOW", err)
	}
	// The value keeps its own copy of the caller's mantissa.
	m := big.NewInt(12)
	d, _ = NewDecimal128FromBigInt(m)
	m.SetInt64(0)
	if d.String(0) != "12" {
		t.Errorf("constructor aliased the caller's big.Int: now %s", d.String(0))
	}
}

func TestDecimal128_Div(t *testing.T) {
	t.Parallel()

	ten := NewDecimal128FromInt(1000)  // 10.00 at scale 2
	three := NewDecimal128FromInt(300) // 3.00 at scale 2
	cases := []struct {
		name       string
		a, b       Decimal128
		s1, s2, rs uint8
		want       string
		code       errors.Code
	}{
		{"by zero", ten, ZeroDecimal128(), 2, 2, 2, "", errors.PULSE_DECIMAL_DIVIDE_BY_ZERO},
		{"by nil zero", ten, Decimal128{}, 2, 2, 2, "", errors.PULSE_DECIMAL_DIVIDE_BY_ZERO},
		{"positive shift", ten, three, 2, 2, 4, "3.3333", ""},
		// shift = rs - s1 + s2 = 0 - 4 + 0 < 0: the dividend is rounded
		// down to the result scale BEFORE the division.
		{"negative shift", NewDecimal128FromInt(125000), NewDecimal128FromInt(5), 4, 0, 0, "2", ""},
		{"nil dividend", Decimal128{}, three, 2, 2, 2, "0.00", ""},
		{"shift too large", ten, three, 0, 38, 38, "", errors.PULSE_DECIMAL_OVERFLOW},
		{"shift too negative", ten, three, 39, 0, 0, "", errors.PULSE_DECIMAL_OVERFLOW},
	}
	for _, tc := range cases {
		got, err := tc.a.Div(tc.b, tc.s1, tc.s2, tc.rs)
		if tc.code != "" {
			if !errors.HasCode(err, tc.code) {
				t.Errorf("%s: err = %v, want %s", tc.name, err, tc.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if s := got.String(tc.rs); s != tc.want {
			t.Errorf("%s: = %s, want %s", tc.name, s, tc.want)
		}
	}
}

func TestDecimal128_Sqrt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		in     int64
		ss, ts uint8
		want   string
		code   errors.Code
	}{
		{"negative", -4, 0, 0, "", errors.PROCESSING_RUNTIME},
		{"zero", 0, 2, 2, "0.00", ""},
		{"exact", 16, 0, 0, "4", ""},
		{"scale up", 2, 0, 3, "1.414", ""},
		// source scale above 2*target: the mantissa is divided down
		// first (sqrt(1.2100) at scale 1).
		{"scale down", 12100, 4, 1, "1.1", ""},
		{"round up", 8, 0, 0, "3", ""},
		// The rounding step sees the digits a scale-down drops:
		// sqrt(2.30) = 1.517 rounds to 2 at scale 0, and sqrt(2.25) =
		// 1.5 is a tie that rounds to the even 2 (the truncated
		// radicand 2 would give 1 for both).
		{"dropped digits round up", 230, 2, 0, "2", ""},
		{"tie to even up", 225, 2, 0, "2", ""},
		// sqrt(0.0625) = 0.25 at scale 1: the tie rounds to the even 0.2.
		{"tie to even down", 625, 4, 1, "0.2", ""},
		// sqrt(0.2025) = 0.45 at scale 1: tie, rounds to the even 0.4.
		{"tie to even stays", 2025, 4, 1, "0.4", ""},
		// sqrt(0.2026) = 0.4501 at scale 1: just past the tie, 0.5.
		{"just past tie", 2026, 4, 1, "0.5", ""},
		{"intermediate overflow", 2, 0, 39, "", errors.PULSE_DECIMAL_OVERFLOW},
	}
	for _, tc := range cases {
		got, err := NewDecimal128FromInt(tc.in).Sqrt(tc.ss, tc.ts)
		if tc.code != "" {
			if !errors.HasCode(err, tc.code) {
				t.Errorf("%s: err = %v, want %s", tc.name, err, tc.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if s := got.String(tc.ts); s != tc.want {
			t.Errorf("%s: = %s, want %s", tc.name, s, tc.want)
		}
	}
}

func TestDecimal128_FitsPrecision(t *testing.T) {
	t.Parallel()

	d := NewDecimal128FromInt(-999)
	cases := []struct {
		p    uint8
		want bool
	}{{0, false}, {39, false}, {2, false}, {3, true}, {38, true}}
	for _, tc := range cases {
		if got := d.FitsPrecision(tc.p); got != tc.want {
			t.Errorf("FitsPrecision(%d) = %v, want %v", tc.p, got, tc.want)
		}
	}
	// Precision 0 is no decimal type at all: even zero does not fit it.
	if (Decimal128{}).FitsPrecision(0) || ZeroDecimal128().FitsPrecision(0) {
		t.Error("zero fits precision 0")
	}
}

func TestDecimal128_StreamIOErrors(t *testing.T) {
	t.Parallel()

	if err := WriteDecimal128(errWriter{}, NewDecimal128FromInt(1)); !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("WriteDecimal128: err = %v, want ENCODING_IO", err)
	}
	if _, err := ReadDecimal128(bytes.NewReader(make([]byte, 15))); !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("ReadDecimal128 short: err = %v, want ENCODING_IO", err)
	}
	var buf bytes.Buffer
	if err := WriteDecimal128(&buf, NewDecimal128FromInt(-42)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDecimal128(&buf)
	if err != nil || got.String(1) != "-4.2" {
		t.Errorf("round trip = (%s, %v), want -4.2", got.String(1), err)
	}
}

func TestBitmapHelpers_Bounds(t *testing.T) {
	t.Parallel()

	bm := make([]byte, 2)
	// Out-of-range indexes are ignored on write and read as present.
	for _, i := range []int{-1, 16, 99} {
		BitmapSetNull(bm, i)
		if BitmapIsNull(bm, i) {
			t.Errorf("BitmapIsNull(%d) = true on an out-of-range index", i)
		}
	}
	if !bytes.Equal(bm, []byte{0, 0}) {
		t.Fatalf("out-of-range SetNull wrote a bit: %v", bm)
	}
	BitmapSetNull(nil, 0) // empty bitmap: a no-op, never a panic
	if BitmapIsNull(nil, 0) {
		t.Error("BitmapIsNull(nil, 0) = true")
	}
	BitmapSetNull(bm, 9) // byte 1, bit 1 (LSB-first)
	if bm[1] != 0x02 || !BitmapIsNull(bm, 9) || BitmapIsNull(bm, 8) {
		t.Errorf("SetNull(9) = %v, want [0 2]", bm)
	}

	if got, err := ReadBitmap(bytes.NewReader(nil), 0); got != nil || err != nil {
		t.Errorf("ReadBitmap(0) = (%v, %v), want (nil, nil)", got, err)
	}
	if _, err := ReadBitmap(bytes.NewReader([]byte{1}), 2); !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("ReadBitmap short: err = %v, want ENCODING_IO", err)
	}
	if err := WriteBitmap(errWriter{}, nil); err != nil {
		t.Errorf("WriteBitmap(empty) touched the writer: %v", err)
	}
	if err := WriteBitmap(errWriter{}, bm); !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("WriteBitmap: err = %v, want ENCODING_IO", err)
	}
}

func TestKindText_UnknownNamesRefused(t *testing.T) {
	t.Parallel()

	var gk GroupKind
	for name, want := range map[string]GroupKind{"indexed": GroupKindIndexed, "constant": GroupKindConstant} {
		if err := gk.UnmarshalText([]byte(name)); err != nil || gk != want {
			t.Errorf("GroupKind %q = (%v, %v), want %v", name, gk, err, want)
		}
	}
	if err := gk.UnmarshalText([]byte("bogus")); err == nil {
		t.Error("GroupKind accepted an unknown name")
	}
	if s := GroupKind(9).String(); s != "kind(9)" {
		t.Errorf("GroupKind(9).String() = %q", s)
	}

	vk := VectorKindScale
	if err := vk.UnmarshalText(nil); err != nil || vk != VectorKindNone {
		t.Errorf("VectorKind \"\" = (%v, %v), want none", vk, err)
	}
	if err := vk.UnmarshalText([]byte("binary")); err != nil || vk != VectorKindBinary {
		t.Errorf("VectorKind binary = (%v, %v)", vk, err)
	}
	if err := vk.UnmarshalText([]byte("tensor")); err == nil {
		t.Error("VectorKind accepted an unknown name")
	}
}

func TestVectorStreamAPI_Refusals(t *testing.T) {
	t.Parallel()

	if err := WriteVector(&bytes.Buffer{}, FieldTypeF64, 3, nil); !errors.HasCode(err, errors.ENCODING_TYPE_MISMATCH) {
		t.Errorf("WriteVector non-vector type: err = %v, want ENCODING_TYPE_MISMATCH", err)
	}
	if err := WriteVector(&bytes.Buffer{}, FieldTypeVecF64, 3, []float64{1, 2}); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("WriteVector wrong length: err = %v, want ENCODING_INVALID", err)
	}
	if err := WriteVector(errWriter{}, FieldTypeVecF32, 2, []float64{1, 2}); !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("WriteVector io: err = %v, want ENCODING_IO", err)
	}
	if _, err := VectorFromBytes(make([]byte, 7), FieldTypeVecF32, 2, nil); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("VectorFromBytes short: err = %v, want ENCODING_INVALID", err)
	}
	if _, err := VectorFromBytes(make([]byte, 64), FieldTypeVecF64, 1, nil); err == nil {
		t.Error("VectorFromBytes accepted dim 1")
	}
	src := make([]byte, 8)
	if err := PutVector(src, FieldTypeVecF32, 2, []float64{1.5, -2}); err != nil {
		t.Fatal(err)
	}
	got, err := VectorFromBytes(src, FieldTypeVecF32, 2, nil)
	if err != nil || len(got) != 2 || got[0] != 1.5 || got[1] != -2 {
		t.Errorf("VectorFromBytes = (%v, %v), want [1.5 -2]", got, err)
	}
}

func TestGroupAccessors_Degenerate(t *testing.T) {
	t.Parallel()

	s := &Schema{
		Fields: []Field{{Name: "a", Type: FieldTypeU8}, {Name: "b", Type: FieldTypeU16}},
		Groups: []Group{
			{Members: nil, Entries: []byte{1, 2, 3}},
			{Members: []GroupMember{{Field: 0}, {Field: 1}}, Entries: []byte{7, 1, 0, 8, 2, 0}},
		},
	}
	if n := s.GroupEntryCount(0); n != 0 {
		t.Errorf("memberless group entry count = %d, want 0", n)
	}
	if n := s.GroupEntryCount(1); n != 2 {
		t.Errorf("entry count = %d, want 2", n)
	}
	e := s.GroupEntry(1, 1)
	if b := s.GroupMemberBytes(1, 1, e); !bytes.Equal(b, []byte{2, 0}) {
		t.Errorf("member 1 of entry 1 = %v, want [2 0]", b)
	}
	if b := s.GroupMemberBytes(1, 5, e); b != nil {
		t.Errorf("absent member = %v, want nil", b)
	}
}

func TestParseDecimal128_Edges(t *testing.T) {
	t.Parallel()

	long := "0." + strings.Repeat("1", 39)
	wide := strings.Repeat("9", 39)
	cases := []struct {
		in    string
		want  string
		scale uint8
		code  errors.Code
	}{
		{"+7", "7", 0, ""},
		{"-000.50", "-0.50", 2, ""},
		{"1.", "1", 0, ""},
		{".5", "0.5", 1, ""},
		{"000", "0", 0, ""},
		{".", "", 0, errors.PULSE_IMPORT_ROW_ERROR},
		{"-", "", 0, errors.PULSE_IMPORT_ROW_ERROR},
		{"1.2.3", "", 0, errors.PULSE_IMPORT_ROW_ERROR},
		{"1e3", "", 0, errors.PULSE_IMPORT_ROW_ERROR},
		{long, "", 0, errors.PULSE_DECIMAL_OVERFLOW},
		{wide, "", 0, errors.PULSE_DECIMAL_OVERFLOW},
	}
	for _, tc := range cases {
		d, scale, err := ParseDecimal128(tc.in)
		if tc.code != "" {
			if !errors.HasCode(err, tc.code) {
				t.Errorf("%q: err = %v, want %s", tc.in, err, tc.code)
			}
			continue
		}
		if err != nil || scale != tc.scale || d.String(scale) != tc.want {
			t.Errorf("%q = (%s, %d, %v), want (%s, %d)", tc.in, d.String(scale), scale, err, tc.want, tc.scale)
		}
	}
}

func TestDecimal128_RescaleBankerRounding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in     int64
		ss, ts uint8
		want   string
	}{
		{125, 2, 1, "1.2"},   // tie, even stays
		{135, 2, 1, "1.4"},   // tie, odd rounds up
		{-125, 2, 1, "-1.2"}, // negative tie, even stays
		{-135, 2, 1, "-1.4"}, // negative tie, away from zero
		{126, 2, 1, "1.3"},   // past half
		{-126, 2, 1, "-1.3"}, // past half, negative
		{124, 2, 1, "1.2"},   // below half
		{120, 2, 1, "1.2"},   // exact
		{12, 1, 3, "1.200"},  // scale up
		{12, 1, 1, "1.2"},    // same scale
	}
	for _, tc := range cases {
		got, err := NewDecimal128FromInt(tc.in).Rescale(tc.ss, tc.ts)
		if err != nil || got.String(tc.ts) != tc.want {
			t.Errorf("Rescale(%d@%d → %d) = (%s, %v), want %s", tc.in, tc.ss, tc.ts, got.String(tc.ts), err, tc.want)
		}
	}
	big37, _, _ := ParseDecimal128(strings.Repeat("9", 37))
	if _, err := big37.Rescale(0, 2); !errors.HasCode(err, errors.PULSE_DECIMAL_OVERFLOW) {
		t.Errorf("scale-up overflow: err = %v, want PULSE_DECIMAL_OVERFLOW", err)
	}
	max, _, _ := ParseDecimal128(strings.Repeat("9", 38))
	if _, err := max.Add(NewDecimal128FromInt(1)); !errors.HasCode(err, errors.PULSE_DECIMAL_OVERFLOW) {
		t.Errorf("Add overflow: err = %v", err)
	}
	if got, err := (Decimal128{}).Sub(NewDecimal128FromInt(3)); err != nil || got.String(0) != "-3" {
		t.Errorf("nil - 3 = (%s, %v), want -3", got.String(0), err)
	}
	if got, err := NewDecimal128FromInt(-4).Mul(Decimal128{}); err != nil || got.Sign() != 0 {
		t.Errorf("-4 * nil = (%s, %v), want 0", got.String(0), err)
	}
}
