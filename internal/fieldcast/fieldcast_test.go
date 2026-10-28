package fieldcast

import (
	"math"
	"math/big"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/temporal"
)

func fld(t encoding.FieldType) encoding.Field { return encoding.Field{Type: t} }

func dec(p, s uint8) encoding.Field {
	return encoding.Field{Type: encoding.FieldTypeDecimal128, Precision: p, Scale: s}
}

func mustDec(t *testing.T, s string) encoding.Decimal128 {
	t.Helper()
	d, _, err := encoding.ParseDecimal128(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestApply_LegalPairs: every legal numeric / bool / temporal / set pair
// at its boundary values — the last value that fits casts exactly, the
// first that does not fails with its reason.
func TestApply_LegalPairs(t *testing.T) {
	t.Parallel()

	const big53 = uint64(1)<<53 + 1 // not representable as a float64
	u := encoding.FieldTypeU8
	cases := []struct {
		name     string
		from, to encoding.Field
		in       any
		want     any
		reason   string
	}{
		// unsigned → unsigned
		{"u8→u4 max", fld(u), fld(encoding.FieldTypeU4), uint64(15), uint64(15), ""},
		{"u8→u4 over", fld(u), fld(encoding.FieldTypeU4), uint64(16), nil, "overflow"},
		{"u16→u8 max", fld(encoding.FieldTypeU16), fld(u), uint64(255), uint64(255), ""},
		{"u16→u8 over", fld(encoding.FieldTypeU16), fld(u), uint64(256), nil, "overflow"},
		{"u32→u16 over", fld(encoding.FieldTypeU32), fld(encoding.FieldTypeU16), uint64(65536), nil, "overflow"},
		{"u64→u32 max", fld(encoding.FieldTypeU64), fld(encoding.FieldTypeU32), uint64(math.MaxUint32), uint64(math.MaxUint32), ""},
		{"u64→u32 over", fld(encoding.FieldTypeU64), fld(encoding.FieldTypeU32), uint64(math.MaxUint32) + 1, nil, "overflow"},
		{"u8→u64", fld(u), fld(encoding.FieldTypeU64), uint64(200), uint64(200), ""},
		// float → unsigned
		{"f64→u8 integral", fld(encoding.FieldTypeF64), fld(u), 255.0, uint64(255), ""},
		{"f64→u8 over", fld(encoding.FieldTypeF64), fld(u), 256.0, nil, "overflow"},
		{"f64→u8 negative", fld(encoding.FieldTypeF64), fld(u), -1.0, nil, "overflow"},
		{"f64→u8 negative zero", fld(encoding.FieldTypeF64), fld(u), math.Copysign(0, -1), uint64(0), ""},
		{"f64→u8 fractional", fld(encoding.FieldTypeF64), fld(u), 2.5, nil, "fractional"},
		{"f64→u8 NaN", fld(encoding.FieldTypeF64), fld(u), math.NaN(), nil, "not_finite"},
		{"f64→u64 inf", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeU64), math.Inf(1), nil, "not_finite"},
		{"f64→u64 2^64", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeU64), math.Ldexp(1, 64), nil, "overflow"},
		{"f64→u64 below 2^64", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeU64), math.Nextafter(math.Ldexp(1, 64), 0), uint64(18446744073709549568), ""},
		{"f32→u16", fld(encoding.FieldTypeF32), fld(encoding.FieldTypeU16), float32(65535), uint64(65535), ""},
		{"f32→u16 fractional", fld(encoding.FieldTypeF32), fld(encoding.FieldTypeU16), float32(0.5), nil, "fractional"},
		// decimal → unsigned
		{"dec→u8 integral", dec(10, 2), fld(u), mustDec(t, "255.00"), uint64(255), ""},
		{"dec→u8 fractional", dec(10, 2), fld(u), mustDec(t, "2.50"), nil, "fractional"},
		{"dec→u8 negative", dec(10, 2), fld(u), mustDec(t, "-1.00"), nil, "overflow"},
		{"dec→u8 over", dec(10, 2), fld(u), mustDec(t, "256.00"), nil, "overflow"},
		{"dec→u64 exact", dec(20, 0), fld(encoding.FieldTypeU64), mustDec(t, "18446744073709551615"), uint64(math.MaxUint64), ""},
		// bool / temporal → unsigned
		{"bool→u4", fld(encoding.FieldTypePackedBool), fld(encoding.FieldTypeU4), true, uint64(1), ""},
		{"bool→u8 false", fld(encoding.FieldTypePackedBool), fld(u), false, uint64(0), ""},
		{"date→u16", fld(encoding.FieldTypeDate), fld(encoding.FieldTypeU16), int32(19000), uint64(19000), ""},
		{"date→u32 negative", fld(encoding.FieldTypeDate), fld(encoding.FieldTypeU32), int32(-1), nil, "overflow"},
		{"datetime→u64", fld(encoding.FieldTypeDateTime), fld(encoding.FieldTypeU64), int64(1700000000), uint64(1700000000), ""},
		// → float
		{"u64→f64 rounds", fld(encoding.FieldTypeU64), fld(encoding.FieldTypeF64), big53, float64(big53), ""},
		{"u8→f32", fld(u), fld(encoding.FieldTypeF32), uint64(7), float32(7), ""},
		{"f64→f32", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeF32), 0.5, float32(0.5), ""},
		{"f64→f32 max", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeF32), float64(math.MaxFloat32), float32(math.MaxFloat32), ""},
		{"f64→f32 over", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeF32), 1e39, nil, "overflow"},
		{"f64→f32 inf stays", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeF32), math.Inf(-1), float32(math.Inf(-1)), ""},
		{"f32→f64", fld(encoding.FieldTypeF32), fld(encoding.FieldTypeF64), float32(0.25), 0.25, ""},
		{"dec→f64", dec(10, 2), fld(encoding.FieldTypeF64), mustDec(t, "12.50"), 12.5, ""},
		{"bool→f64", fld(encoding.FieldTypePackedBool), fld(encoding.FieldTypeF64), true, 1.0, ""},
		{"date→f64", fld(encoding.FieldTypeDate), fld(encoding.FieldTypeF64), int32(-3), -3.0, ""},
		// → decimal128
		{"u64>2^53→dec exact", fld(encoding.FieldTypeU64), dec(38, 0), big53, mustDec(t, "9007199254740993"), ""},
		{"u64 max→dec(20,0)", fld(encoding.FieldTypeU64), dec(20, 0), uint64(math.MaxUint64), mustDec(t, "18446744073709551615"), ""},
		{"u64 max→dec(19,0) over", fld(encoding.FieldTypeU64), dec(19, 0), uint64(math.MaxUint64), nil, "overflow"},
		{"u8→dec(5,2)", fld(u), dec(5, 2), uint64(999), mustDec(t, "99900"), ""},
		{"u8→dec(4,2) over", fld(u), dec(4, 2), uint64(100), nil, "overflow"},
		{"f64→dec(10,2)", fld(encoding.FieldTypeF64), dec(10, 2), 12.34, mustDec(t, "1234"), ""},
		{"f64→dec(10,2) too fine", fld(encoding.FieldTypeF64), dec(10, 2), 12.345, nil, "fractional"},
		{"f64→dec(38,0) fractional", fld(encoding.FieldTypeF64), dec(38, 0), 0.5, nil, "fractional"},
		{"f64→dec NaN", fld(encoding.FieldTypeF64), dec(38, 0), math.NaN(), nil, "not_finite"},
		{"f64→dec huge", fld(encoding.FieldTypeF64), dec(38, 0), 1e300, nil, "overflow"},
		{"f32→dec shortest", fld(encoding.FieldTypeF32), dec(10, 1), float32(0.1), mustDec(t, "1"), ""},
		{"dec(10,2)→dec(10,4) rescale", dec(10, 2), dec(10, 4), mustDec(t, "1.25"), mustDec(t, "12500"), ""},
		{"dec(10,2)→dec(10,1) exact", dec(10, 2), dec(10, 1), mustDec(t, "1.20"), mustDec(t, "12"), ""},
		{"dec(10,2)→dec(10,1) inexact", dec(10, 2), dec(10, 1), mustDec(t, "1.25"), nil, "fractional"},
		{"dec(10,2)→dec(3,2) over", dec(10, 2), dec(3, 2), mustDec(t, "10.00"), nil, "overflow"},
		{"bool→dec", fld(encoding.FieldTypePackedBool), dec(2, 1), true, mustDec(t, "10"), ""},
		{"bool→dec(1,1) over", fld(encoding.FieldTypePackedBool), dec(1, 1), true, nil, "overflow"},
		{"datetime→dec", fld(encoding.FieldTypeDateTime), dec(38, 0), int64(-5), mustDec(t, "-5"), ""},
		// → packed_bool
		{"u8→bool 1", fld(u), fld(encoding.FieldTypePackedBool), uint64(1), true, ""},
		{"u8→bool 0", fld(u), fld(encoding.FieldTypePackedBool), uint64(0), false, ""},
		{"u8→bool 2", fld(u), fld(encoding.FieldTypePackedBool), uint64(2), nil, "not_boolean"},
		{"f64→bool 1", fld(encoding.FieldTypeF64), fld(encoding.FieldTypePackedBool), 1.0, true, ""},
		{"f64→bool 0.5", fld(encoding.FieldTypeF64), fld(encoding.FieldTypePackedBool), 0.5, nil, "not_boolean"},
		{"f64→bool NaN", fld(encoding.FieldTypeF64), fld(encoding.FieldTypePackedBool), math.NaN(), nil, "not_boolean"},
		{"dec→bool 1.00", dec(5, 2), fld(encoding.FieldTypePackedBool), mustDec(t, "1.00"), true, ""},
		{"dec→bool 0.00", dec(5, 2), fld(encoding.FieldTypePackedBool), mustDec(t, "0.00"), false, ""},
		{"dec→bool 1.01", dec(5, 2), fld(encoding.FieldTypePackedBool), mustDec(t, "1.01"), nil, "not_boolean"},
		// numeric epoch → date / datetime
		{"u32→date max", fld(encoding.FieldTypeU32), fld(encoding.FieldTypeDate), uint64(math.MaxInt32), int32(math.MaxInt32), ""},
		{"u32→date over", fld(encoding.FieldTypeU32), fld(encoding.FieldTypeDate), uint64(math.MaxInt32) + 1, nil, "overflow"},
		{"f64→date", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeDate), -10.0, int32(-10), ""},
		{"f64→date fractional", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeDate), 1.5, nil, "fractional"},
		{"dec→date", dec(10, 0), fld(encoding.FieldTypeDate), mustDec(t, "19000"), int32(19000), ""},
		{"u64→datetime max", fld(encoding.FieldTypeU64), fld(encoding.FieldTypeDateTime), uint64(math.MaxInt64), int64(math.MaxInt64), ""},
		{"u64→datetime over", fld(encoding.FieldTypeU64), fld(encoding.FieldTypeDateTime), uint64(math.MaxInt64) + 1, nil, "overflow"},
		{"f64→datetime", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeDateTime), 1.7e9, int64(1700000000), ""},
		{"f64→datetime 2^63", fld(encoding.FieldTypeF64), fld(encoding.FieldTypeDateTime), math.Ldexp(1, 63), nil, "overflow"},
		// date ↔ datetime under UTC
		{"date→datetime", fld(encoding.FieldTypeDate), fld(encoding.FieldTypeDateTime), int32(1), int64(86400), ""},
		{"datetime→date", fld(encoding.FieldTypeDateTime), fld(encoding.FieldTypeDate), int64(86399), int32(0), ""},
		{"datetime→date before epoch", fld(encoding.FieldTypeDateTime), fld(encoding.FieldTypeDate), int64(-1), int32(-1), ""},
		{"datetime→date over", fld(encoding.FieldTypeDateTime), fld(encoding.FieldTypeDate), int64(math.MaxInt64), nil, "overflow"},
		// sets widen with their labels
		{"set_u8→set_u16", fld(encoding.FieldTypeSetU8), fld(encoding.FieldTypeSetU16), []string{"a"}, []string{"a"}, ""},
		// same type: no cast
		{"vec same", fld(encoding.FieldTypeVecF64), fld(encoding.FieldTypeVecF64), []float64{1, 2}, []float64{1, 2}, ""},
		{"null stays null", fld(encoding.FieldTypeF64), fld(u), nil, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if why := Unsupported(c.from.Type, c.to.Type); why != "" {
				t.Fatalf("Unsupported = %q, want legal", why)
			}
			p := &Plan{From: c.from, To: c.to}
			got, fail := p.Apply(c.in)
			if c.reason != "" {
				if fail == nil || fail.Reason != c.reason {
					t.Fatalf("Apply(%v) = %v, %+v; want failure %q", c.in, got, fail, c.reason)
				}
				if got != nil {
					t.Errorf("failed cast returned %v, want nil", got)
				}
				if Check(c.from, c.to) == "" {
					t.Errorf("a cast that failed has no Check condition")
				}
				return
			}
			if fail != nil {
				t.Fatalf("Apply(%v) failed: %+v", c.in, fail)
			}
			if !same(got, c.want) {
				t.Fatalf("Apply(%v) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

func same(a, b any) bool {
	switch x := a.(type) {
	case encoding.Decimal128:
		y, ok := b.(encoding.Decimal128)
		return ok && x.Cmp(y) == 0
	case []string:
		y, ok := b.([]string)
		return ok && len(x) == len(y) && (len(x) == 0 || x[0] == y[0])
	case []float64:
		y, ok := b.([]float64)
		return ok && len(x) == len(y)
	case float32:
		y, ok := b.(float32)
		return ok && (x == y || (math.IsNaN(float64(x)) && math.IsNaN(float64(y))))
	}
	return a == b
}

// TestApply_U64AboveTwoTo53StaysExact: a u64 past 2^53 cast to u64 /
// decimal128 is the exact stored integer — never a float64 echo, which
// would round 2^53+1 to 2^53.
func TestApply_U64AboveTwoTo53StaysExact(t *testing.T) {
	t.Parallel()

	v := uint64(1)<<53 + 1
	got, fail := (&Plan{From: fld(encoding.FieldTypeU64), To: dec(38, 0)}).Apply(v)
	if fail != nil {
		t.Fatal(fail)
	}
	if m := got.(encoding.Decimal128).Mantissa(); m.Cmp(new(big.Int).SetUint64(v)) != 0 {
		t.Fatalf("decimal mantissa = %s, want %d", m, v)
	}
	// A decimal128 holding it casts back to u64 exactly.
	back, fail := (&Plan{From: dec(38, 0), To: fld(encoding.FieldTypeU64)}).Apply(got)
	if fail != nil || back != v {
		t.Fatalf("decimal → u64 = %v (%+v), want %d", back, fail, v)
	}
}

// TestApply_DatetimeToDateInZone: datetime → date truncates in the
// plan's zone, across a DST transition (Europe/Berlin, 2024-03-31
// 02:00 CET → 03:00 CEST): the instant one second before local
// midnight is the previous local day, local midnight the next, on both
// sides of the switch — and date → datetime is local midnight.
func TestApply_DatetimeToDateInZone(t *testing.T) {
	t.Parallel()

	z, err := temporal.LoadZone("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	toDate := &Plan{From: fld(encoding.FieldTypeDateTime), To: fld(encoding.FieldTypeDate), Zone: z}
	toDateTime := &Plan{From: fld(encoding.FieldTypeDate), To: fld(encoding.FieldTypeDateTime), Zone: z}
	const (
		mar31 = 19813 // 2024-03-31, the spring-forward day
		apr1  = 19814
	)
	// Local midnight of 2024-03-31 is 23:00 UTC on 03-30 (CET, +1);
	// of 2024-04-01, 22:00 UTC on 03-31 (CEST, +2).
	mid31 := int64(mar31)*86400 - 3600
	mid01 := int64(apr1)*86400 - 7200
	for _, c := range []struct {
		sec  int64
		want int32
	}{
		{mid31 - 1, mar31 - 1},
		{mid31, mar31},
		{mid31 + 3*3600, mar31}, // 03:00 CEST, after the gap
		{mid01 - 1, mar31},
		{mid01, apr1},
	} {
		got, fail := toDate.Apply(c.sec)
		if fail != nil || got != c.want {
			t.Errorf("datetime %d → date = %v (%+v), want %d", c.sec, got, fail, c.want)
		}
	}
	for day, want := range map[int32]int64{mar31: mid31, apr1: mid01} {
		got, fail := toDateTime.Apply(day)
		if fail != nil || got != want {
			t.Errorf("date %d → datetime = %v (%+v), want %d", day, got, fail, want)
		}
	}
	// UTC (a nil zone) is the plain epoch arithmetic.
	if got, _ := (&Plan{From: fld(encoding.FieldTypeDateTime), To: fld(encoding.FieldTypeDate)}).Apply(mid31); got != int32(mar31-1) {
		t.Errorf("UTC datetime → date = %v, want %d", got, mar31-1)
	}
}

// TestUnsupported_IllegalPairs: vectors, categoricals, set narrowing and
// set ↔ scalar, bool ↔ temporal are refused with a reason.
func TestUnsupported_IllegalPairs(t *testing.T) {
	t.Parallel()

	for _, pair := range [][2]encoding.FieldType{
		{encoding.FieldTypeVecF32, encoding.FieldTypeVecF64},
		{encoding.FieldTypeF64, encoding.FieldTypeVecF64},
		{encoding.FieldTypeCategoricalU8, encoding.FieldTypeU8},
		{encoding.FieldTypeU8, encoding.FieldTypeCategoricalU8},
		{encoding.FieldTypeSetU16, encoding.FieldTypeSetU8},
		{encoding.FieldTypeSetU8, encoding.FieldTypeU8},
		{encoding.FieldTypeU64, encoding.FieldTypeSetU64},
		{encoding.FieldTypePackedBool, encoding.FieldTypeDate},
		{encoding.FieldTypeDateTime, encoding.FieldTypePackedBool},
	} {
		if Unsupported(pair[0], pair[1]) == "" {
			t.Errorf("%s → %s is legal, want refused", pair[0], pair[1])
		}
		if Check(fld(pair[0]), fld(pair[1])) != "" {
			t.Errorf("%s → %s has a Check condition", pair[0], pair[1])
		}
	}
}

// TestCheck_Conditions: the risk text names the condition; a cast that
// cannot fail has none.
func TestCheck_Conditions(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		from, to encoding.Field
		want     string
	}{
		{fld(encoding.FieldTypeF64), fld(encoding.FieldTypeU8), "f64 → u8: integral, 0 ≤ v ≤ 255"},
		{fld(encoding.FieldTypeU16), fld(encoding.FieldTypeU8), "u16 → u8: v ≤ 255"},
		{fld(encoding.FieldTypeU8), fld(encoding.FieldTypeU16), ""},
		{fld(encoding.FieldTypeU64), fld(encoding.FieldTypeF64), ""},
		{fld(encoding.FieldTypeF64), fld(encoding.FieldTypeF32), "f64 → f32: |v| ≤ 3.4028235e+38 (within f32)"},
		{fld(encoding.FieldTypeU64), dec(38, 0), ""},
		{fld(encoding.FieldTypeU64), dec(10, 0), "u64 → decimal128(10,0): |v| < 1e10"},
		{fld(encoding.FieldTypeF64), dec(10, 2), "f64 → decimal128(10,2): finite, at most 2 fractional digits, |v| < 1e8"},
		{dec(10, 2), dec(12, 4), ""},
		{dec(10, 2), dec(10, 1), "decimal128(10,2) → decimal128(10,1): at most 1 fractional digits"},
		{fld(encoding.FieldTypeU8), fld(encoding.FieldTypePackedBool), "u8 → packed_bool: v is 0 or 1"},
		{fld(encoding.FieldTypePackedBool), fld(encoding.FieldTypeU4), ""},
		{fld(encoding.FieldTypeDate), fld(encoding.FieldTypeDateTime), ""},
		{fld(encoding.FieldTypeDateTime), fld(encoding.FieldTypeDate), "datetime → date: the local day is within int32 epoch days"},
		{fld(encoding.FieldTypeDate), fld(encoding.FieldTypeU32), "date → u32: 0 ≤ v ≤ 4294967295"},
		{fld(encoding.FieldTypeU16), fld(encoding.FieldTypeDate), ""},
		{fld(encoding.FieldTypeU64), fld(encoding.FieldTypeDate), "u64 → date: v ≤ 2147483647 (int32 epoch days)"},
		{fld(encoding.FieldTypeU32), fld(encoding.FieldTypeDateTime), ""},
		{fld(encoding.FieldTypeF64), fld(encoding.FieldTypeDateTime), "f64 → datetime: integral, within int64 epoch seconds"},
		{fld(encoding.FieldTypeSetU8), fld(encoding.FieldTypeSetU256), ""},
	} {
		if got := Check(c.from, c.to); got != c.want {
			t.Errorf("Check(%s → %s) = %q, want %q", name(c.from), name(c.to), got, c.want)
		}
	}
}

// TestApply_UnexpectedValue: a Go value of the wrong type is a failure,
// never a panic.
func TestApply_UnexpectedValue(t *testing.T) {
	t.Parallel()

	for _, p := range []*Plan{
		{From: fld(encoding.FieldTypeF64), To: fld(encoding.FieldTypeU8)},
		{From: fld(encoding.FieldTypeDate), To: fld(encoding.FieldTypeDateTime)},
		{From: fld(encoding.FieldTypeDateTime), To: fld(encoding.FieldTypeDate)},
	} {
		if _, fail := p.Apply("x"); fail == nil || fail.Reason != "unexpected_value" {
			t.Errorf("%s → %s on a string = %+v, want unexpected_value", p.From.Type, p.To.Type, fail)
		}
	}
}
