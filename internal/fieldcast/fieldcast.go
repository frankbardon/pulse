// Package fieldcast is the value-cast matrix a materialize `output`
// column's `type` runs (types.MaterializeOutputColumn.Type): which
// source → target field-type pairs are legal (Unsupported), which of
// them can fail on a stored value and on what condition (Check), and
// the conversion of one stored CohortRow value (Plan.Apply).
//
// It is a leaf: the no-execute descriptor package (the static matrix
// and predict's risks) and the materialize sink (the conversion) share
// it, so predict and the run cannot disagree on a pair. Every cast is
// EXACT or fails: an integer source (u64 above 2^53 included) never
// routes through float64 on its way to an integer or decimal128
// target, a float → integer cast refuses a fractional value, every
// target refuses a value past its range. The one rounding cast is a
// float target (an integer above 2^53 → f64, any value → f32), which
// rounds to nearest and refuses only an overflow to infinity.
//
// The numeric / bool / temporal half of the matrix:
//
//   - numeric (u4..u64, f32, f64, decimal128) → numeric;
//   - numeric → packed_bool (0 or 1 only), packed_bool → numeric (0 / 1);
//   - numeric (epoch) → date (epoch days, int32) / datetime (epoch
//     seconds, int64); date / datetime → numeric (the epoch count);
//   - date → datetime: local midnight in the plan's zone
//     (temporal.LocalMidnightUTC); datetime → date: the local day in
//     that zone (temporal.LocalDay, truncating toward the past);
//   - set_* → a wider set_* rung (the labels are unchanged);
//   - vec_* and categorical_* keep their type.
package fieldcast

import (
	"math"
	"math/big"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/temporal"
)

// class groups the field types a cast treats alike.
type class uint8

const (
	clsOther class = iota
	clsUint
	clsFloat
	clsDecimal
	clsBool
	clsDate
	clsDateTime
	clsSet
	clsVector
	clsCategorical
)

func classOf(t encoding.FieldType) class {
	switch t {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64:
		return clsUint
	case encoding.FieldTypeF32, encoding.FieldTypeF64:
		return clsFloat
	case encoding.FieldTypeDecimal128:
		return clsDecimal
	case encoding.FieldTypePackedBool:
		return clsBool
	case encoding.FieldTypeDate:
		return clsDate
	case encoding.FieldTypeDateTime:
		return clsDateTime
	}
	switch {
	case t.IsSet():
		return clsSet
	case t.IsVector():
		return clsVector
	case t.HasDictionary():
		return clsCategorical
	}
	return clsOther
}

func (c class) numeric() bool { return c == clsUint || c == clsFloat || c == clsDecimal }

// Unsupported reports why from → to is not a legal cast, or "" when it
// is (a same-type pair always is: it is no cast, or — decimal128 — a
// rescale). Schema-only.
func Unsupported(from, to encoding.FieldType) string {
	if from == to {
		return ""
	}
	cf, ct := classOf(from), classOf(to)
	switch {
	case cf == clsVector || ct == clsVector:
		return "a vector column has no cast: keep its type, or drop the column"
	case cf == clsCategorical || ct == clsCategorical:
		return "a categorical column keeps its type, and no other type casts to a categorical"
	case cf == clsSet && ct == clsSet:
		if to.MaxSetEntries() > from.MaxSetEntries() {
			return ""
		}
		return "a set column only widens (set_u8 → set_u16 → set_u32 → set_u64 → set_u128 → set_u256)"
	case cf == clsSet || ct == clsSet:
		return "a set column casts only to a wider set rung"
	case cf == clsOther || ct == clsOther:
		return "no cast is defined for this pair"
	case ct == clsBool:
		if cf.numeric() {
			return ""
		}
		return "only a numeric column (u4..u64, f32, f64, decimal128) casts to packed_bool"
	case cf == clsBool:
		if ct.numeric() {
			return ""
		}
		return "packed_bool casts only to a numeric type (u4..u64, f32, f64, decimal128)"
	}
	// Numeric and temporal classes remain: every pair among them is legal.
	return ""
}

// uintMax is the largest value of an unsigned integer type.
func uintMax(t encoding.FieldType) uint64 {
	switch t {
	case encoding.FieldTypeU4:
		return 15
	case encoding.FieldTypeU8:
		return math.MaxUint8
	case encoding.FieldTypeU16:
		return math.MaxUint16
	case encoding.FieldTypeU32:
		return math.MaxUint32
	}
	return math.MaxUint64
}

// intDigits is the most decimal integer digits a value of field f can
// carry (its sign aside) — what a decimal128 target must hold before
// the point.
func intDigits(f encoding.Field) int {
	switch classOf(f.Type) {
	case clsUint:
		return len(strconv.FormatUint(uintMax(f.Type), 10))
	case clsBool:
		return 1
	case clsDate:
		return 10 // int32
	case clsDateTime:
		return 19 // int64
	case clsDecimal:
		return int(f.Precision) - int(f.Scale)
	}
	return encoding.MaxDecimalPrecision + 1 // a float: unbounded
}

// Check states the condition a stored value of from must meet to cast
// to to — e.g. "f64 → u8: integral, 0 ≤ v ≤ 255" — or "" when the cast
// cannot fail on any stored value (a widening integer cast, an integer
// → f64, date → datetime, a set widen, the same type). It is the text
// predict lists as a data-dependent risk; Plan.Apply fails exactly
// where it says. Both fields carry their decimal shape. Schema-only.
func Check(from, to encoding.Field) string {
	cond := check(from, to)
	if cond == "" {
		return ""
	}
	return name(from) + " → " + name(to) + ": " + cond
}

// name renders a field type, a decimal with its shape.
func name(f encoding.Field) string {
	if f.Type.IsDecimal() {
		return "decimal128(" + strconv.Itoa(int(f.Precision)) + "," + strconv.Itoa(int(f.Scale)) + ")"
	}
	return f.Type.String()
}

func check(from, to encoding.Field) string {
	if Unsupported(from.Type, to.Type) != "" {
		return ""
	}
	cf, ct := classOf(from.Type), classOf(to.Type)
	switch ct {
	case clsUint:
		limit := strconv.FormatUint(uintMax(to.Type), 10)
		switch cf {
		case clsUint:
			if uintMax(from.Type) > uintMax(to.Type) {
				return "v ≤ " + limit
			}
		case clsFloat:
			return "integral, 0 ≤ v ≤ " + limit
		case clsDecimal:
			if from.Scale == 0 {
				return "0 ≤ v ≤ " + limit
			}
			return "integral, 0 ≤ v ≤ " + limit
		case clsDate, clsDateTime:
			return "0 ≤ v ≤ " + limit
		}
	case clsFloat:
		if to.Type == encoding.FieldTypeF32 && from.Type == encoding.FieldTypeF64 {
			return "|v| ≤ 3.4028235e+38 (within f32)"
		}
	case clsDecimal:
		s, room := int(to.Scale), int(to.Precision)-int(to.Scale)
		var conds []string
		switch cf {
		case clsFloat:
			conds = append(conds, "finite")
			conds = append(conds, "at most "+strconv.Itoa(s)+" fractional digits")
		case clsDecimal:
			if s < int(from.Scale) {
				conds = append(conds, "at most "+strconv.Itoa(s)+" fractional digits")
			}
		}
		if intDigits(from) > room {
			conds = append(conds, "|v| < 1e"+strconv.Itoa(room))
		}
		return join(conds)
	case clsBool:
		if cf != clsBool {
			return "v is 0 or 1"
		}
	case clsDate:
		switch cf {
		case clsUint:
			if uintMax(from.Type) > math.MaxInt32 {
				return "v ≤ 2147483647 (int32 epoch days)"
			}
		case clsFloat, clsDecimal:
			return "integral, within int32 epoch days"
		case clsDateTime:
			return "the local day is within int32 epoch days"
		}
	case clsDateTime:
		switch cf {
		case clsUint:
			if from.Type == encoding.FieldTypeU64 {
				return "v ≤ 9223372036854775807 (int64 epoch seconds)"
			}
		case clsFloat, clsDecimal:
			return "integral, within int64 epoch seconds"
		}
	}
	return ""
}

func join(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += ", "
		}
		out += c
	}
	return out
}

// Failure is one stored value a cast refused: Reason is "fractional"
// (a fractional value to an integer / date-family target, or more
// fractional digits than a decimal's scale), "overflow" (past the
// target's range), "not_finite" (NaN or ±Inf to an exact target),
// "not_boolean" (neither 0 nor 1 to packed_bool) or "unexpected_value"
// (a Go value of the wrong type — an invariant violation); Value is the
// stored value, rendered exactly.
type Failure struct {
	Reason string
	Value  string
}

// Plan is one column's cast: From is the field read (its type and, for
// a decimal, its scale), To the field written (type and decimal shape).
// Zone is the zone date ↔ datetime casts read local days in; nil is UTC.
type Plan struct {
	From, To encoding.Field
	Zone     *temporal.Zone
}

// number is one stored value lifted to the cast's working form: an
// exact unsigned or signed integer, a float, or a decimal mantissa at
// a scale.
type number struct {
	kind  uint8 // 'u', 'i', 'f', 'd'
	u     uint64
	i     int64
	f     float64
	f32   bool // the float came from an f32 (its shortest repr is 32-bit)
	m     *big.Int
	scale uint8
}

func (n number) String() string {
	switch n.kind {
	case 'u':
		return strconv.FormatUint(n.u, 10)
	case 'i':
		return strconv.FormatInt(n.i, 10)
	case 'f':
		bits := 64
		if n.f32 {
			bits = 32
		}
		return strconv.FormatFloat(n.f, 'g', -1, bits)
	}
	d, _ := encoding.NewDecimal128FromBigInt(n.m)
	return d.String(n.scale)
}

// Apply casts one stored value (the CohortRow type of p.From) to the
// CohortRow type of p.To. A nil value stays nil. A value the cast
// refuses returns its Failure, the value itself nil.
func (p *Plan) Apply(v any) (any, *Failure) {
	if v == nil {
		return nil, nil
	}
	if p.From.Type == p.To.Type && !p.From.Type.IsDecimal() {
		return v, nil
	}
	cf, ct := classOf(p.From.Type), classOf(p.To.Type)
	if cf == clsSet && ct == clsSet {
		// A widen: the labels are the value, unchanged.
		return v, nil
	}
	zone := p.Zone
	if zone == nil {
		zone = temporal.UTC
	}
	// The two zone-reading casts.
	switch {
	case cf == clsDate && ct == clsDateTime:
		day, ok := v.(int32)
		if !ok {
			return nil, unexpected(v)
		}
		return temporal.LocalMidnightUTC(int64(day), zone), nil
	case cf == clsDateTime && ct == clsDate:
		sec, ok := v.(int64)
		if !ok {
			return nil, unexpected(v)
		}
		day := temporal.LocalDay(sec, zone)
		if day < math.MinInt32 || day > math.MaxInt32 {
			return nil, &Failure{Reason: "overflow", Value: strconv.FormatInt(sec, 10)}
		}
		return int32(day), nil
	}
	n, ok := lift(v, p.From)
	if !ok {
		return nil, unexpected(v)
	}
	var (
		out    any
		reason string
	)
	switch ct {
	case clsUint:
		var u uint64
		u, reason = toUint(n, uintMax(p.To.Type))
		out = u
	case clsFloat:
		out, reason = toFloat(n, p.To.Type == encoding.FieldTypeF32)
	case clsDecimal:
		out, reason = toDecimal(n, p.To.Precision, p.To.Scale)
	case clsBool:
		out, reason = toBool(n)
	case clsDate:
		var i int64
		i, reason = toInt(n, math.MinInt32, math.MaxInt32)
		out = int32(i)
	case clsDateTime:
		out, reason = toInt(n, math.MinInt64, math.MaxInt64)
	default:
		return nil, unexpected(v)
	}
	if reason != "" {
		return nil, &Failure{Reason: reason, Value: n.String()}
	}
	return out, nil
}

func unexpected(v any) *Failure {
	return &Failure{Reason: "unexpected_value", Value: typeName(v)}
}

// typeName names a Go value's type without fmt (a value of the wrong
// type is an invariant violation; its type is the useful detail).
func typeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case []string:
		return "[]string"
	case []float32:
		return "[]float32"
	case []float64:
		return "[]float64"
	}
	return "unexpected Go type"
}

// lift reads a stored value of field f into a number.
func lift(v any, f encoding.Field) (number, bool) {
	switch x := v.(type) {
	case uint64:
		return number{kind: 'u', u: x}, true
	case bool:
		if x {
			return number{kind: 'u', u: 1}, true
		}
		return number{kind: 'u'}, true
	case int32:
		return number{kind: 'i', i: int64(x)}, true
	case int64:
		return number{kind: 'i', i: x}, true
	case float64:
		return number{kind: 'f', f: x}, true
	case float32:
		return number{kind: 'f', f: float64(x), f32: true}, true
	case encoding.Decimal128:
		return number{kind: 'd', m: x.Mantissa(), scale: f.Scale}, true
	}
	return number{}, false
}

// integral reduces n to an exact integer: ok false with the reason when
// it is not one (a fractional or non-finite float, a decimal with a
// fractional part).
func integral(n number) (*big.Int, string) {
	switch n.kind {
	case 'u':
		return new(big.Int).SetUint64(n.u), ""
	case 'i':
		return big.NewInt(n.i), ""
	case 'f':
		if math.IsNaN(n.f) || math.IsInf(n.f, 0) {
			return nil, "not_finite"
		}
		if n.f != math.Trunc(n.f) {
			return nil, "fractional"
		}
		b, _ := new(big.Float).SetFloat64(n.f).Int(nil)
		return b, ""
	}
	q, r := new(big.Int).QuoRem(n.m, pow10(n.scale), new(big.Int))
	if r.Sign() != 0 {
		return nil, "fractional"
	}
	return q, ""
}

func pow10(n uint8) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

func toUint(n number, limit uint64) (uint64, string) {
	if n.kind == 'u' {
		if n.u > limit {
			return 0, "overflow"
		}
		return n.u, ""
	}
	b, reason := integral(n)
	if reason != "" {
		return 0, reason
	}
	if b.Sign() < 0 || !b.IsUint64() || b.Uint64() > limit {
		return 0, "overflow"
	}
	return b.Uint64(), ""
}

func toInt(n number, lo, hi int64) (int64, string) {
	b, reason := integral(n)
	if reason != "" {
		return 0, reason
	}
	if !b.IsInt64() || b.Int64() < lo || b.Int64() > hi {
		return 0, "overflow"
	}
	return b.Int64(), ""
}

func toFloat(n number, f32 bool) (any, string) {
	var f float64
	switch n.kind {
	case 'u':
		f = float64(n.u)
	case 'i':
		f = float64(n.i)
	case 'f':
		f = n.f
	default:
		d, _ := encoding.NewDecimal128FromBigInt(n.m)
		f = d.Float64(n.scale)
	}
	if !f32 {
		return f, ""
	}
	g := float32(f)
	if math.IsInf(float64(g), 0) && !math.IsInf(f, 0) {
		return nil, "overflow"
	}
	return g, ""
}

func toBool(n number) (any, string) {
	var zero, one bool
	switch n.kind {
	case 'u':
		zero, one = n.u == 0, n.u == 1
	case 'i':
		zero, one = n.i == 0, n.i == 1
	case 'f':
		zero, one = n.f == 0, n.f == 1
	default:
		zero, one = n.m.Sign() == 0, n.m.Cmp(pow10(n.scale)) == 0
	}
	switch {
	case zero:
		return false, ""
	case one:
		return true, ""
	}
	return nil, "not_boolean"
}

// toDecimal scales n to a decimal128(precision, scale) mantissa, exactly.
func toDecimal(n number, precision, scale uint8) (any, string) {
	var m *big.Int
	var at uint8
	switch n.kind {
	case 'u':
		m = new(big.Int).SetUint64(n.u)
	case 'i':
		m = big.NewInt(n.i)
	case 'f':
		if math.IsNaN(n.f) || math.IsInf(n.f, 0) {
			return nil, "not_finite"
		}
		bits := 64
		if n.f32 {
			bits = 32
		}
		// The float's shortest decimal representation — what it prints
		// as — is the value cast; a binary tail past it is not data.
		d, sc, err := encoding.ParseDecimal128(strconv.FormatFloat(n.f, 'f', -1, bits))
		if err != nil {
			return nil, "overflow"
		}
		m, at = d.Mantissa(), sc
	default:
		m, at = n.m, n.scale
	}
	switch {
	case at < scale:
		m = new(big.Int).Mul(m, pow10(scale-at))
	case at > scale:
		q, r := new(big.Int).QuoRem(m, pow10(at-scale), new(big.Int))
		if r.Sign() != 0 {
			return nil, "fractional"
		}
		m = q
	}
	if new(big.Int).Abs(m).Cmp(pow10(precision)) >= 0 {
		return nil, "overflow"
	}
	d, err := encoding.NewDecimal128FromBigInt(m)
	if err != nil {
		return nil, "overflow"
	}
	return d, ""
}
