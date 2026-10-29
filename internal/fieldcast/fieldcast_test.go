package fieldcast

import (
	"math"
	"math/big"
	"strconv"
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

// TestUnsupported_IllegalPairs: vectors, a categorical to or from
// anything but a categorical or numeric type, set narrowing and set ↔
// scalar, bool ↔ temporal are refused with a reason.
func TestUnsupported_IllegalPairs(t *testing.T) {
	t.Parallel()

	for _, pair := range [][2]encoding.FieldType{
		{encoding.FieldTypeVecF32, encoding.FieldTypeVecF64},
		{encoding.FieldTypeF64, encoding.FieldTypeVecF64},
		{encoding.FieldTypeCategoricalU8, encoding.FieldTypeDate},
		{encoding.FieldTypeCategoricalU8, encoding.FieldTypePackedBool},
		{encoding.FieldTypeCategoricalU8, encoding.FieldTypeSetU8},
		{encoding.FieldTypeSetU8, encoding.FieldTypeCategoricalU8},
		{encoding.FieldTypePackedBool, encoding.FieldTypeCategoricalU8},
		{encoding.FieldTypeDateTime, encoding.FieldTypeCategoricalU16},
		{encoding.FieldTypeVecF64, encoding.FieldTypeCategoricalU8},
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

func catDict(n int) *encoding.Dictionary {
	d := encoding.NewDictionary()
	for i := 0; i < n; i++ {
		if _, err := d.Add("L" + strconv.Itoa(i)); err != nil {
			panic(err)
		}
	}
	return d
}

func cat(t encoding.FieldType, d *encoding.Dictionary) encoding.Field {
	return encoding.Field{Type: t, Dictionary: d}
}

// TestRefused_CategoricalNarrowing: a narrowing whose known dictionary
// fits the target (256 labels into categorical_u8) is legal; one label
// over (257) is refused statically; a widen and an unknown dictionary
// are never refused here.
func TestRefused_CategoricalNarrowing(t *testing.T) {
	t.Parallel()

	u8, u16, u32 := encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32
	if why := Refused(cat(u16, catDict(256)), u8); why != "" {
		t.Errorf("256 labels → categorical_u8 refused: %s", why)
	}
	why := Refused(cat(u16, catDict(257)), u8)
	if want := "its dictionary holds 257 labels and categorical_u8 holds at most 256"; why != want {
		t.Errorf("257 labels → categorical_u8 = %q, want %q", why, want)
	}
	if why := Refused(cat(u8, catDict(200)), u32); why != "" {
		t.Errorf("widen refused: %s", why)
	}
	if why := Refused(cat(u16, nil), u8); why != "" {
		t.Errorf("unknown dictionary refused statically: %s", why)
	}
	if why := Refused(fld(encoding.FieldTypeF64), encoding.FieldTypeVecF64); why == "" {
		t.Error("Refused misses Unsupported's matrix")
	}
}

// TestApply_CategoricalToCategorical: a widen and a narrow keep the
// label; a narrowing over an unknown dictionary admits labels up to the
// target's capacity (256 for categorical_u8) and fails the 257th
// distinct label with overflow, a repeat of an admitted label still
// casting; a seeded dictionary's labels are always admitted.
func TestApply_CategoricalToCategorical(t *testing.T) {
	t.Parallel()

	u8, u16 := encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16
	widen := &Plan{From: cat(u8, catDict(3)), To: cat(u16, catDict(3))}
	if got, fail := widen.Apply("L2"); fail != nil || got != "L2" {
		t.Errorf("widen = %v, %+v", got, fail)
	}
	seeded := &Plan{From: cat(u16, catDict(256)), To: cat(u8, catDict(256))}
	for i := 255; i >= 0; i-- {
		if _, fail := seeded.Apply("L" + strconv.Itoa(i)); fail != nil {
			t.Fatalf("seeded label %d refused: %+v", i, fail)
		}
	}
	open := &Plan{From: cat(u16, nil), To: cat(u8, nil)}
	for i := 0; i < 256; i++ {
		if _, fail := open.Apply("L" + strconv.Itoa(i)); fail != nil {
			t.Fatalf("label %d of 256 refused: %+v", i, fail)
		}
	}
	if got, fail := open.Apply("L256"); got != nil || fail == nil || fail.Reason != "overflow" || fail.Value != "L256" {
		t.Errorf("257th label = %v, %+v, want overflow", got, fail)
	}
	if got, fail := open.Apply("L0"); fail != nil || got != "L0" {
		t.Errorf("an admitted label after the overflow = %v, %+v", got, fail)
	}
	if _, fail := open.Apply(uint64(3)); fail == nil || fail.Reason != "unexpected_value" {
		t.Errorf("a non-string = %+v, want unexpected_value", fail)
	}
}

// TestApply_CategoricalToNumeric: a label is parsed exactly — integers
// and decimals to an exact target (a fraction refused, a value past the
// range refused, a u64 past 2^53 exact), any decimal syntax to a float
// (rounded once) — and a label that is not a number is unparseable,
// its Value the label verbatim.
func TestApply_CategoricalToNumeric(t *testing.T) {
	t.Parallel()

	c := cat(encoding.FieldTypeCategoricalU8, nil)
	cases := []struct {
		name   string
		to     encoding.Field
		in     string
		want   any
		reason string
	}{
		{"int to u8", fld(encoding.FieldTypeU8), "255", uint64(255), ""},
		{"int over u8", fld(encoding.FieldTypeU8), "256", nil, "overflow"},
		{"negative to u8", fld(encoding.FieldTypeU8), "-1", nil, "overflow"},
		{"integral decimal to u8", fld(encoding.FieldTypeU8), "7.000", uint64(7), ""},
		{"exponent to u8", fld(encoding.FieldTypeU8), "1.5e2", uint64(150), ""},
		{"fraction to u8", fld(encoding.FieldTypeU8), "2.5", nil, "fractional"},
		{"tiny exponent to u8", fld(encoding.FieldTypeU8), "1e-400", nil, "fractional"},
		{"u64 past 2^53", fld(encoding.FieldTypeU64), "9007199254740993", uint64(9007199254740993), ""},
		{"float label to f64", fld(encoding.FieldTypeF64), "2.5", 2.5, ""},
		{"exponent to f64", fld(encoding.FieldTypeF64), "-1e3", -1000.0, ""},
		{"f64 overflow", fld(encoding.FieldTypeF64), "1e400", nil, "overflow"},
		{"f32 rounds once", fld(encoding.FieldTypeF32), "0.1", float32(0.1), ""},
		{"f32 overflow", fld(encoding.FieldTypeF32), "1e39", nil, "overflow"},
		{"decimal scale", dec(10, 2), "3.25", mustDec(t, "3.25"), ""},
		{"decimal too fine", dec(10, 2), "3.255", nil, "fractional"},
		{"decimal too wide", dec(4, 2), "100", nil, "overflow"},
		{"letters", fld(encoding.FieldTypeU8), "Low", nil, "unparseable"},
		{"empty", fld(encoding.FieldTypeF64), "", nil, "unparseable"},
		{"surrounding space", fld(encoding.FieldTypeU8), " 3", nil, "unparseable"},
		{"NaN is not a number", fld(encoding.FieldTypeF64), "NaN", nil, "unparseable"},
		{"Inf is not a number", fld(encoding.FieldTypeF64), "Inf", nil, "unparseable"},
		{"hex", fld(encoding.FieldTypeU8), "0x10", nil, "unparseable"},
		{"two signs", fld(encoding.FieldTypeU8), "1-2", nil, "unparseable"},
		{"huge exponent", fld(encoding.FieldTypeU8), "1e9999", nil, "unparseable"},
	}
	for _, tc := range cases {
		p := &Plan{From: c, To: tc.to}
		got, fail := p.Apply(tc.in)
		if tc.reason != "" {
			if fail == nil || fail.Reason != tc.reason || fail.Value != tc.in || got != nil {
				t.Errorf("%s: %q = %v, %+v, want %s", tc.name, tc.in, got, fail, tc.reason)
			}
			continue
		}
		if fail != nil {
			t.Errorf("%s: %q failed %+v", tc.name, tc.in, fail)
			continue
		}
		if d, ok := tc.want.(encoding.Decimal128); ok {
			if got.(encoding.Decimal128).Mantissa().Cmp(new(big.Int).Mul(d.Mantissa(), big.NewInt(1))) != 0 {
				t.Errorf("%s: %q = %v, want %v", tc.name, tc.in, got, d.Mantissa())
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%s: %q = %#v, want %#v", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestApply_NumericToCategorical: each value is stringified (Label) and
// admitted into a data-built dictionary in first-seen order; the first
// distinct value past the capacity fails with overflow, and NaN fails
// not_finite.
func TestApply_NumericToCategorical(t *testing.T) {
	t.Parallel()

	p := &Plan{From: fld(encoding.FieldTypeU16), To: cat(encoding.FieldTypeCategoricalU8, nil)}
	for i := 0; i < 256; i++ {
		got, fail := p.Apply(uint64(1000 + i))
		if fail != nil || got != strconv.Itoa(1000+i) {
			t.Fatalf("value %d = %v, %+v", 1000+i, got, fail)
		}
	}
	if got, fail := p.Apply(uint64(1256)); got != nil || fail == nil || fail.Reason != "overflow" || fail.Value != "1256" {
		t.Errorf("257th distinct value = %v, %+v, want overflow", got, fail)
	}
	if got, fail := p.Apply(uint64(1000)); fail != nil || got != "1000" {
		t.Errorf("a seen value after the overflow = %v, %+v", got, fail)
	}
	if p.dict.Labels()[0] != "1000" || p.dict.Labels()[255] != "1255" || p.dict.Len() != 256 {
		t.Errorf("dictionary is not first-seen: %v…", p.dict.Labels()[:3])
	}
	f := &Plan{From: fld(encoding.FieldTypeF64), To: cat(encoding.FieldTypeCategoricalU8, nil)}
	if _, fail := f.Apply(math.NaN()); fail == nil || fail.Reason != "not_finite" {
		t.Errorf("NaN = %+v, want not_finite", fail)
	}
}

// TestLabel_Rendering pins the stringification rule a numeric → categorical
// cast writes: integers as digits, a float as encoding/json writes it
// (no ".0" on an integral float, -0 as "0", an exponent only outside
// [1e-6, 1e21)), an f32 in its shortest 32-bit form, a decimal at its
// scale with trailing zeros.
func TestLabel_Rendering(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		v    any
		f    encoding.Field
		want string
	}{
		{uint64(7), fld(encoding.FieldTypeU8), "7"},
		{uint64(math.MaxUint64), fld(encoding.FieldTypeU64), "18446744073709551615"},
		{3.0, fld(encoding.FieldTypeF64), "3"},
		{-2.5, fld(encoding.FieldTypeF64), "-2.5"},
		{1e6, fld(encoding.FieldTypeF64), "1000000"},
		{1e20, fld(encoding.FieldTypeF64), "100000000000000000000"},
		{1e21, fld(encoding.FieldTypeF64), "1e+21"},
		{1e-7, fld(encoding.FieldTypeF64), "1e-7"},
		{0.000001, fld(encoding.FieldTypeF64), "0.000001"},
		{math.Copysign(0, -1), fld(encoding.FieldTypeF64), "0"},
		{0.1, fld(encoding.FieldTypeF64), "0.1"},
		{float32(0.1), fld(encoding.FieldTypeF32), "0.1"},
		{mustDec(t, "350"), dec(10, 2), "3.50"},
		{mustDec(t, "-5"), dec(10, 1), "-0.5"},
	} {
		got, ok := Label(c.v, c.f)
		if !ok || got != c.want {
			t.Errorf("Label(%v) = %q, %v, want %q", c.v, got, ok, c.want)
		}
	}
	if _, ok := Label(math.Inf(1), fld(encoding.FieldTypeF64)); ok {
		t.Error("Label(+Inf) has a label")
	}
}

// TestCheck_Categorical: the risk text for each categorical pair, and
// none where no stored value can fail (a widen, a known dictionary, an
// unsigned source whose every value fits the rung).
func TestCheck_Categorical(t *testing.T) {
	t.Parallel()

	u8c, u16c, u32c := encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32
	for _, c := range []struct {
		from, to encoding.Field
		want     string
	}{
		{cat(u16c, nil), cat(u8c, nil), "categorical_u16 → categorical_u8: at most 256 distinct labels"},
		{cat(u16c, catDict(3)), cat(u8c, nil), ""},
		{cat(u8c, nil), cat(u16c, nil), ""},
		{cat(u8c, nil), fld(encoding.FieldTypeU8), "categorical_u8 → u8: label is an integral number, 0 ≤ v ≤ 255"},
		{cat(u8c, nil), fld(encoding.FieldTypeF64), "categorical_u8 → f64: label is a number within f64"},
		{cat(u8c, nil), fld(encoding.FieldTypeF32), "categorical_u8 → f32: label is a number, |v| ≤ 3.4028235e+38 (within f32)"},
		{cat(u8c, nil), dec(10, 2), "categorical_u8 → decimal128(10,2): label is a number, at most 2 fractional digits, |v| < 1e8"},
		{fld(encoding.FieldTypeU8), cat(u8c, nil), ""},
		{fld(encoding.FieldTypeU16), cat(u8c, nil), "u16 → categorical_u8: at most 256 distinct values"},
		{fld(encoding.FieldTypeU16), cat(u16c, nil), ""},
		{fld(encoding.FieldTypeU32), cat(u32c, nil), "u32 → categorical_u32: at most 4294967295 distinct values"},
		{fld(encoding.FieldTypeF64), cat(u16c, nil), "f64 → categorical_u16: finite, at most 65536 distinct values"},
		{dec(10, 2), cat(u8c, nil), "decimal128(10,2) → categorical_u8: at most 256 distinct values"},
	} {
		if got := Check(c.from, c.to); got != c.want {
			t.Errorf("Check(%s → %s) = %q, want %q", name(c.from), name(c.to), got, c.want)
		}
	}
}
