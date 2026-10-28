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
//   - vec_* keeps its type.
//
// The categorical half:
//
//   - categorical_* → categorical_*: the labels and the whole dictionary
//     (order, codes, unused entries) unchanged; a narrowing whose known
//     dictionary does not fit the target is refused statically
//     (Refused), else each label is admitted against the target's
//     capacity (Dict);
//   - categorical_* → numeric: the label parsed exactly — an optional
//     sign, decimal digits, an optional fraction and exponent, no
//     surrounding space — then cast as a number of that value; a label
//     that is not one fails "unparseable";
//   - numeric → categorical_*: the value stringified (Label) into a
//     data-built dictionary in first-seen order; a distinct value past
//     the target's capacity fails "overflow".
package fieldcast

import (
	stderrors "errors"
	"math"
	"math/big"
	"strconv"
	"strings"

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
	case cf == clsCategorical && (ct == clsCategorical || ct.numeric()):
		return ""
	case ct == clsCategorical && cf.numeric():
		return ""
	case cf == clsCategorical:
		return "a categorical column casts only to another categorical width or to a numeric type (u4..u64, f32, f64, decimal128)"
	case ct == clsCategorical:
		return "only a numeric column (u4..u64, f32, f64, decimal128) or another categorical casts to a categorical"
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

// Refused reports why from (the column read, its dictionary included)
// cannot cast to to, or "" when it can: Unsupported's matrix, then a
// categorical narrowing whose dictionary is known (non-nil) and holds
// more entries than to's capacity — refused before any record is read.
// A narrowing whose dictionary is unknown is a run-time check instead
// (Check). Schema-only.
func Refused(from encoding.Field, to encoding.FieldType) string {
	if why := Unsupported(from.Type, to); why != "" {
		return why
	}
	if from.Type == to || classOf(from.Type) != clsCategorical || classOf(to) != clsCategorical || from.Dictionary == nil {
		return ""
	}
	if n, capacity := from.Dictionary.Count(), to.MaxCategoricalEntries(); uint64(n) > uint64(capacity) {
		return "its dictionary holds " + strconv.Itoa(n) + " labels and " + to.String() + " holds at most " +
			strconv.FormatUint(uint64(capacity), 10)
	}
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
	if cf == clsCategorical || ct == clsCategorical {
		return categoricalCheck(from, to, cf, ct)
	}
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

// categoricalCheck is check for a pair with a categorical side.
func categoricalCheck(from, to encoding.Field, cf, ct class) string {
	capacity := to.Type.MaxCategoricalEntries()
	labels := strconv.FormatUint(uint64(capacity), 10)
	switch {
	case cf == clsCategorical && ct == clsCategorical:
		// A known dictionary is settled statically (Refused); only an
		// unknown one narrowing is a run-time check.
		if from.Dictionary == nil && capacity < from.Type.MaxCategoricalEntries() {
			return "at most " + labels + " distinct labels"
		}
	case ct == clsCategorical:
		var conds []string
		if cf == clsFloat {
			conds = append(conds, "finite")
		}
		// An unsigned type holds uintMax+1 values; any other numeric
		// type more than any dictionary.
		if cf != clsUint || uintMax(from.Type) >= uint64(capacity) {
			conds = append(conds, "at most "+labels+" distinct values")
		}
		return join(conds)
	default: // categorical → numeric
		switch ct {
		case clsUint:
			return "label is an integral number, 0 ≤ v ≤ " + strconv.FormatUint(uintMax(to.Type), 10)
		case clsFloat:
			if to.Type == encoding.FieldTypeF32 {
				return "label is a number, |v| ≤ 3.4028235e+38 (within f32)"
			}
			return "label is a number within f64"
		case clsDecimal:
			s, room := int(to.Scale), int(to.Precision)-int(to.Scale)
			return "label is a number, at most " + strconv.Itoa(s) + " fractional digits, |v| < 1e" + strconv.Itoa(room)
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
// target's range, or a new label past a categorical target's
// capacity), "not_finite" (NaN or ±Inf to an exact or categorical
// target), "not_boolean" (neither 0 nor 1 to packed_bool),
// "unparseable" (a categorical label that is not a number) or
// "unexpected_value" (a Go value of the wrong type — an invariant
// violation); Value is the stored value, rendered exactly (a
// categorical source's label verbatim).
type Failure struct {
	Reason string
	Value  string
}

// Plan is one column's cast: From is the field read (its type and, for
// a decimal, its scale), To the field written (type and decimal shape).
// Zone is the zone date ↔ datetime casts read local days in; nil is UTC.
//
// A categorical target admits each label written into its dictionary
// (Dict, seeded with To.Dictionary), so a Plan holds state: one Plan per
// column per run, its values applied in output row order.
type Plan struct {
	From, To encoding.Field
	Zone     *temporal.Zone

	dict *Dict
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
	// text is a parsed categorical label (parseLabel): a float target
	// rounds it directly, once.
	text string
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
	if ct == clsCategorical {
		return p.toCategorical(v, cf)
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
	var (
		n      number
		ok     bool
		label  string
		reason string
		out    any
	)
	if cf == clsCategorical {
		if label, ok = v.(string); !ok {
			return nil, unexpected(v)
		}
		if n, reason = parseLabel(label, ct != clsFloat); reason != "" {
			return nil, &Failure{Reason: reason, Value: label}
		}
	} else if n, ok = lift(v, p.From); !ok {
		return nil, unexpected(v)
	}
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
		if cf == clsCategorical {
			return nil, &Failure{Reason: reason, Value: label}
		}
		return nil, &Failure{Reason: reason, Value: n.String()}
	}
	return out, nil
}

// toCategorical casts v to the plan's categorical target: a categorical
// label as is, a number stringified (Label); the label must be admitted
// by the target's dictionary (Dict), else "overflow".
func (p *Plan) toCategorical(v any, cf class) (any, *Failure) {
	var label string
	if cf == clsCategorical {
		s, ok := v.(string)
		if !ok {
			return nil, unexpected(v)
		}
		label = s
	} else {
		n, ok := lift(v, p.From)
		if !ok {
			return nil, unexpected(v)
		}
		var reason string
		if label, reason = categoryLabel(n); reason != "" {
			return nil, &Failure{Reason: reason, Value: n.String()}
		}
	}
	if p.dict == nil {
		p.dict = NewDict(p.To.Type.MaxCategoricalEntries(), p.To.Dictionary)
	}
	if !p.dict.Admit(label) {
		return nil, &Failure{Reason: "overflow", Value: label}
	}
	return label, nil
}

// Label is the dictionary label a numeric cast to a categorical writes
// for v (a stored value of field f), or ok false for a value with none
// (NaN, ±Inf, a Go value of the wrong type). The rule:
//
//   - an unsigned integer: its decimal digits ("7", "18446744073709551615");
//   - a float: its shortest round-trip form as encoding/json writes it —
//     plain digits for 1e-6 ≤ |v| < 1e21 (an integral float has no ".0":
//     3.0 → "3", 1e6 → "1000000"), an exponent outside it ("1e+21",
//     "1e-7"); an f32 its shortest 32-bit form; -0 is "0";
//   - a decimal128: its digits at the column's scale, trailing zeros
//     kept (3.5 at scale 2 → "3.50");
//   - packed_bool "0" / "1", an integer (date-family) its digits.
func Label(v any, f encoding.Field) (string, bool) {
	n, ok := lift(v, f)
	if !ok {
		return "", false
	}
	s, reason := categoryLabel(n)
	return s, reason == ""
}

func categoryLabel(n number) (string, string) {
	if n.kind != 'f' {
		return n.String(), ""
	}
	f := n.f
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", "not_finite"
	}
	if f == 0 {
		return "0", ""
	}
	bits := 64
	if n.f32 {
		bits = 32
	}
	// encoding/json's float rendering (encode.go floatEncoder).
	fmtByte := byte('f')
	if a := math.Abs(f); a < 1e-6 || a >= 1e21 {
		fmtByte = 'e'
	}
	b := strconv.AppendFloat(nil, f, fmtByte, -1, bits)
	if fmtByte == 'e' {
		// clean up e-09 to e-9
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return string(b), ""
}

// parseLabel reads a categorical label as a number: decimal syntax
// only — an optional sign, digits with an optional fraction, an
// optional exponent (|e| ≤ 1000); no surrounding space, hex,
// underscore, "Inf" or "NaN" ("unparseable"). For an exact target
// (integer, decimal128) the value is read exactly — an integer, or a
// decimal mantissa at the fewest fractional digits that hold it (more
// than 255 is "fractional": no exact target holds it); for a float
// target (exact false) the label is rounded once, by strconv, and a
// value past f64 is "overflow".
func parseLabel(s string, exact bool) (number, string) {
	if s == "" {
		return number{}, "unparseable"
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9', c == '+', c == '-', c == '.', c == 'e', c == 'E':
		default:
			return number{}, "unparseable"
		}
	}
	if u, err := strconv.ParseUint(s, 10, 64); err == nil {
		return number{kind: 'u', u: u, text: s}, ""
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return number{kind: 'i', i: i, text: s}, ""
	}
	mant, exp := s, 0
	if k := strings.IndexAny(s, "eE"); k >= 0 {
		e, err := strconv.Atoi(s[k+1:])
		if err != nil || e < -1000 || e > 1000 || k == 0 {
			return number{}, "unparseable"
		}
		mant, exp = s[:k], e
	}
	if strings.ContainsAny(mant, "eE") || strings.LastIndexAny(mant, "+-") > 0 {
		return number{}, "unparseable"
	}
	r, ok := new(big.Rat).SetString(mant)
	if !ok {
		return number{}, "unparseable"
	}
	if !exact {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			if stderrors.Is(err, strconv.ErrRange) {
				return number{}, "overflow"
			}
			return number{}, "unparseable"
		}
		return number{kind: 'f', f: f, text: s}, ""
	}
	p := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt(exp))), nil))
	if exp >= 0 {
		r.Mul(r, p)
	} else {
		r.Quo(r, p)
	}
	if r.IsInt() {
		return number{kind: 'd', m: new(big.Int).Set(r.Num()), text: s}, ""
	}
	// A decimal's denominator is 2^a·5^b: max(a, b) fractional digits
	// hold it exactly.
	den := new(big.Int).Set(r.Denom())
	twos := int(den.TrailingZeroBits())
	den.Rsh(den, uint(twos))
	fives := 0
	five, rem := big.NewInt(5), new(big.Int)
	for den.Cmp(big.NewInt(1)) != 0 {
		q, m := new(big.Int).QuoRem(den, five, rem)
		if m.Sign() != 0 {
			return number{}, "unparseable" // not a decimal (cannot happen for decimal syntax)
		}
		den, fives = q, fives+1
		if fives > 255 {
			return number{}, "fractional"
		}
	}
	scale := max(twos, fives)
	if scale > 255 {
		return number{}, "fractional"
	}
	m := new(big.Int).Mul(r.Num(), pow10(uint8(scale)))
	m.Quo(m, r.Denom())
	return number{kind: 'd', m: m, scale: uint8(scale), text: s}, ""
}

func absInt(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

// Dict is a data-built (or rewritten) dictionary: the labels a column
// writes, in first-admitted order, bounded by a categorical rung's
// capacity. A seed's entries come first, in its order (so a carried
// dictionary keeps its codes). It mirrors the cohort builder's own
// first-seen dictionary growth, so a label Dict admits is one the
// builder accepts — a cast refuses an over-capacity label with its own
// code before the append does.
type Dict struct {
	capacity uint64
	ids      map[string]uint32
	labels   []string
}

// NewDict returns an empty Dict bounded by capacity labels (a
// categorical type's MaxCategoricalEntries), seeded with seed's entries
// when seed is non-nil.
func NewDict(capacity uint32, seed *encoding.Dictionary) *Dict {
	d := &Dict{capacity: uint64(capacity), ids: map[string]uint32{}}
	if seed != nil {
		for _, l := range seed.Values() {
			if _, ok := d.ids[l]; !ok {
				d.ids[l] = uint32(len(d.labels))
				d.labels = append(d.labels, l)
			}
		}
	}
	return d
}

// Admit reports whether label is in the dictionary, adding it when it
// is new and the dictionary has room; false (label not added) when it
// is new and the dictionary is full.
func (d *Dict) Admit(label string) bool {
	if _, ok := d.ids[label]; ok {
		return true
	}
	if uint64(len(d.labels)) >= d.capacity {
		return false
	}
	d.ids[label] = uint32(len(d.labels))
	d.labels = append(d.labels, label)
	return true
}

// Len is the number of labels admitted (seed included).
func (d *Dict) Len() int { return len(d.labels) }

// Labels is a copy of the labels in code order.
func (d *Dict) Labels() []string { return append([]string(nil), d.labels...) }

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
	if n.text != "" {
		// A parsed label rounds once, straight to the target width.
		bits := 64
		if f32 {
			bits = 32
		}
		f, err := strconv.ParseFloat(n.text, bits)
		if err != nil {
			return nil, "overflow"
		}
		if f32 {
			return float32(f), ""
		}
		return f, ""
	}
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
