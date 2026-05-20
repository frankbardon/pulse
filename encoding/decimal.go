package encoding

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// Decimal128 is a fixed-point decimal value with up to 38 digits of
// precision. Logically it is a signed 128-bit two's-complement integer
// mantissa, paired with a per-field scale that places the implicit
// decimal point. Internally it is carried as *big.Int (always copied,
// never aliased) bounded to the decimal128 representable range
// [-(10^38 - 1), 10^38 - 1].
type Decimal128 struct {
	// mantissa carries the unscaled integer value. Non-nil after
	// construction; the zero value is returned by ZeroDecimal128.
	mantissa *big.Int
}

// MaxDecimalPrecision is the upper bound on decimal128 precision.
const MaxDecimalPrecision = 38

// MinDecimalScale is the floor on division result scale (matches Arrow).
const MinDecimalScale = 4

// decimal128MaxAbs is 10^38 - the first integer that overflows decimal128.
// Any |mantissa| >= decimal128MaxAbs is overflow.
var decimal128MaxAbs = new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil)

// pow10 caches 10^n for n in [0, MaxDecimalPrecision].
var pow10 [MaxDecimalPrecision + 1]*big.Int

func init() {
	for i := 0; i <= MaxDecimalPrecision; i++ {
		pow10[i] = new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(i)), nil)
	}
}

// ZeroDecimal128 returns a Decimal128 with mantissa 0.
func ZeroDecimal128() Decimal128 {
	return Decimal128{mantissa: new(big.Int)}
}

// NewDecimal128FromInt builds a Decimal128 with the given integer mantissa.
func NewDecimal128FromInt(i int64) Decimal128 {
	return Decimal128{mantissa: big.NewInt(i)}
}

// NewDecimal128FromBigInt builds a Decimal128 from a big.Int mantissa.
// Returns PULSE_DECIMAL_OVERFLOW if |m| >= 10^38.
func NewDecimal128FromBigInt(m *big.Int) (Decimal128, error) {
	if m == nil {
		return ZeroDecimal128(), nil
	}
	abs := new(big.Int).Abs(m)
	if abs.Cmp(decimal128MaxAbs) >= 0 {
		return Decimal128{}, errors.NewCodedError(errors.PULSE_DECIMAL_OVERFLOW,
			"value exceeds decimal128(38) representable range")
	}
	return Decimal128{mantissa: new(big.Int).Set(m)}, nil
}

// Mantissa returns a copy of the unscaled integer mantissa.
func (d Decimal128) Mantissa() *big.Int {
	if d.mantissa == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(d.mantissa)
}

// Sign returns -1, 0, or +1 depending on the mantissa sign.
func (d Decimal128) Sign() int {
	if d.mantissa == nil {
		return 0
	}
	return d.mantissa.Sign()
}

// Cmp compares two Decimal128 values with the same scale. Comparing values
// at different scales requires the caller to align scales first.
func (d Decimal128) Cmp(o Decimal128) int {
	if d.mantissa == nil && o.mantissa == nil {
		return 0
	}
	if d.mantissa == nil {
		return -o.mantissa.Sign()
	}
	if o.mantissa == nil {
		return d.mantissa.Sign()
	}
	return d.mantissa.Cmp(o.mantissa)
}

// String renders the decimal at the given scale. Trailing zeros are
// preserved; the leading sign is included only for negative values.
func (d Decimal128) String(scale uint8) string {
	if d.mantissa == nil {
		return formatScaledInt(big.NewInt(0), scale)
	}
	return formatScaledInt(d.mantissa, scale)
}

// Float64 returns the decimal as a float64 at the given scale. Lossy
// for values that exceed float64 precision; callers preserving precision
// should keep using Decimal128 directly.
func (d Decimal128) Float64(scale uint8) float64 {
	if d.mantissa == nil {
		return 0
	}
	f := new(big.Float).SetInt(d.mantissa)
	if scale > 0 {
		divisor := new(big.Float).SetInt(pow10[scale])
		f.Quo(f, divisor)
	}
	v, _ := f.Float64()
	return v
}

// formatScaledInt renders an arbitrary-precision signed integer with an
// implicit decimal point at `scale` digits from the right.
func formatScaledInt(m *big.Int, scale uint8) string {
	if scale == 0 {
		return m.String()
	}
	neg := m.Sign() < 0
	abs := new(big.Int).Abs(m)
	digits := abs.String()
	for len(digits) <= int(scale) {
		digits = "0" + digits
	}
	cut := len(digits) - int(scale)
	out := digits[:cut] + "." + digits[cut:]
	if neg {
		out = "-" + out
	}
	return out
}

// ParseDecimal128 parses a strict decimal string into a (Decimal128, scale)
// pair. The scale is inferred from the input — exactly the count of
// digits after the decimal point. Accepts an optional single leading
// sign char. Rejects whitespace, thousand separators, scientific
// notation, currency symbols.
func ParseDecimal128(s string) (Decimal128, uint8, error) {
	if s == "" {
		return Decimal128{}, 0, errors.NewCodedError(errors.PULSE_IMPORT_ROW_ERROR,
			"empty decimal string")
	}
	if s != strings.TrimSpace(s) {
		return Decimal128{}, 0, errors.NewCodedError(errors.PULSE_IMPORT_ROW_ERROR,
			"decimal contains leading or trailing whitespace")
	}
	neg := false
	idx := 0
	switch s[0] {
	case '-':
		neg = true
		idx = 1
	case '+':
		idx = 1
	}
	if idx >= len(s) {
		return Decimal128{}, 0, errors.NewCodedError(errors.PULSE_IMPORT_ROW_ERROR,
			"decimal contains only a sign character")
	}
	body := s[idx:]
	dot := -1
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '.' {
			if dot >= 0 {
				return Decimal128{}, 0, errors.NewCodedError(errors.PULSE_IMPORT_ROW_ERROR,
					"decimal contains more than one '.'")
			}
			dot = i
			continue
		}
		if c < '0' || c > '9' {
			return Decimal128{}, 0, errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_ROW_ERROR,
				"decimal contains non-digit character",
				map[string]any{"input": s, "char": string(c)})
		}
	}
	var digits string
	var scale uint8
	if dot < 0 {
		digits = body
	} else {
		intPart := body[:dot]
		fracPart := body[dot+1:]
		if intPart == "" && fracPart == "" {
			return Decimal128{}, 0, errors.NewCodedError(errors.PULSE_IMPORT_ROW_ERROR,
				"decimal '.' has no digits")
		}
		if len(fracPart) > MaxDecimalPrecision {
			return Decimal128{}, 0, errors.NewCodedError(errors.PULSE_DECIMAL_OVERFLOW,
				"decimal scale exceeds 38 digits")
		}
		scale = uint8(len(fracPart))
		digits = intPart + fracPart
		if digits == "" {
			return Decimal128{}, 0, errors.NewCodedError(errors.PULSE_IMPORT_ROW_ERROR,
				"decimal has no digits")
		}
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		digits = "0"
	}
	m := new(big.Int)
	if _, ok := m.SetString(digits, 10); !ok {
		return Decimal128{}, 0, errors.NewCodedError(errors.PULSE_IMPORT_ROW_ERROR,
			"decimal failed to parse mantissa")
	}
	if neg {
		m.Neg(m)
	}
	dec, err := NewDecimal128FromBigInt(m)
	if err != nil {
		return Decimal128{}, 0, err
	}
	return dec, scale, nil
}

// Rescale converts d at sourceScale to targetScale using banker's rounding.
// Returns PULSE_DECIMAL_OVERFLOW if the rescaled mantissa exceeds 10^38-1.
func (d Decimal128) Rescale(sourceScale, targetScale uint8) (Decimal128, error) {
	if d.mantissa == nil {
		return ZeroDecimal128(), nil
	}
	if sourceScale == targetScale {
		return Decimal128{mantissa: new(big.Int).Set(d.mantissa)}, nil
	}
	if targetScale > sourceScale {
		// Multiply by 10^(target-source).
		shift := pow10[targetScale-sourceScale]
		out := new(big.Int).Mul(d.mantissa, shift)
		return NewDecimal128FromBigInt(out)
	}
	// targetScale < sourceScale — round half-to-even.
	divisor := pow10[sourceScale-targetScale]
	q, r := new(big.Int).QuoRem(d.mantissa, divisor, new(big.Int))
	out := bankerRound(q, r, divisor)
	return NewDecimal128FromBigInt(out)
}

// bankerRound applies half-to-even rounding given the truncated quotient
// `q`, the remainder `r`, and the divisor used. Sign of q matches the
// dividend; r has the same sign as the dividend (per big.Int.QuoRem).
func bankerRound(q, r, divisor *big.Int) *big.Int {
	if r.Sign() == 0 {
		return q
	}
	// Compare 2*|r| with |divisor|.
	twoAbsR := new(big.Int).Lsh(new(big.Int).Abs(r), 1)
	cmp := twoAbsR.Cmp(new(big.Int).Abs(divisor))
	out := new(big.Int).Set(q)
	switch {
	case cmp < 0:
		// |r| < half — truncate.
		return out
	case cmp > 0:
		// |r| > half — round away from zero.
		if r.Sign() > 0 {
			out.Add(out, big.NewInt(1))
		} else {
			out.Sub(out, big.NewInt(1))
		}
		return out
	default:
		// Exactly half — round to even.
		if out.Bit(0) == 0 {
			return out
		}
		if r.Sign() > 0 {
			out.Add(out, big.NewInt(1))
		} else {
			out.Sub(out, big.NewInt(1))
		}
		return out
	}
}

// Add returns d + o, treating both at the same scale. The result has the
// same scale; the caller is responsible for SQL:2016 precision propagation
// at the schema level.
func (d Decimal128) Add(o Decimal128) (Decimal128, error) {
	a := mantissaOrZero(d)
	b := mantissaOrZero(o)
	out := new(big.Int).Add(a, b)
	return NewDecimal128FromBigInt(out)
}

// Sub returns d - o.
func (d Decimal128) Sub(o Decimal128) (Decimal128, error) {
	a := mantissaOrZero(d)
	b := mantissaOrZero(o)
	out := new(big.Int).Sub(a, b)
	return NewDecimal128FromBigInt(out)
}

// Mul returns d * o. The product mantissa is the product of mantissas;
// callers are responsible for tracking scale propagation (s1 + s2).
func (d Decimal128) Mul(o Decimal128) (Decimal128, error) {
	a := mantissaOrZero(d)
	b := mantissaOrZero(o)
	out := new(big.Int).Mul(a, b)
	return NewDecimal128FromBigInt(out)
}

// Div divides d by o using banker's rounding to produce a result at
// scale `resultScale` given the operands at scales s1 and s2. Returns
// PULSE_DECIMAL_DIVIDE_BY_ZERO when o is zero.
func (d Decimal128) Div(o Decimal128, s1, s2, resultScale uint8) (Decimal128, error) {
	if o.Sign() == 0 {
		return Decimal128{}, errors.NewCodedError(errors.PULSE_DECIMAL_DIVIDE_BY_ZERO,
			"decimal divide by zero")
	}
	a := mantissaOrZero(d)
	b := mantissaOrZero(o)
	// Result mantissa = round(a * 10^(resultScale - s1 + s2) / b).
	shift := int(resultScale) - int(s1) + int(s2)
	num := new(big.Int).Set(a)
	if shift > 0 {
		if shift > MaxDecimalPrecision {
			return Decimal128{}, errors.NewCodedError(errors.PULSE_DECIMAL_OVERFLOW,
				"decimal divide intermediate scale exceeds 38")
		}
		num.Mul(num, pow10[shift])
	} else if shift < 0 {
		if -shift > MaxDecimalPrecision {
			return Decimal128{}, errors.NewCodedError(errors.PULSE_DECIMAL_OVERFLOW,
				"decimal divide intermediate scale exceeds 38")
		}
		divisor := pow10[-shift]
		q, r := new(big.Int).QuoRem(num, divisor, new(big.Int))
		num = bankerRound(q, r, divisor)
	}
	q, r := new(big.Int).QuoRem(num, b, new(big.Int))
	out := bankerRound(q, r, b)
	return NewDecimal128FromBigInt(out)
}

func mantissaOrZero(d Decimal128) *big.Int {
	if d.mantissa == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(d.mantissa)
}

// PromoteAdd returns the (precision, scale) of a SUM/SUB result given two
// operand types per SQL:2016 / Arrow Decimal128 rules:
//
//	(p1, s1) ± (p2, s2) => (max(p1-s1, p2-s2) + max(s1, s2) + 1, max(s1, s2))
//
// The result precision is clamped at MaxDecimalPrecision; clamping
// callers must check ClampedPrecision and emit PULSE_DECIMAL_OVERFLOW
// when overflow surfaces at runtime.
func PromoteAdd(p1, s1, p2, s2 uint8) (uint8, uint8) {
	intDigits := maxU8(p1-s1, p2-s2)
	resScale := maxU8(s1, s2)
	resPrec := intDigits + resScale + 1
	if resPrec > MaxDecimalPrecision {
		resPrec = MaxDecimalPrecision
	}
	return resPrec, resScale
}

// PromoteMul returns the (precision, scale) of a MUL result.
//
//	(p1, s1) × (p2, s2) => (p1 + p2, s1 + s2)
func PromoteMul(p1, s1, p2, s2 uint8) (uint8, uint8) {
	resScale := s1 + s2
	resPrec := p1 + p2
	if resPrec > MaxDecimalPrecision {
		resPrec = MaxDecimalPrecision
	}
	return resPrec, resScale
}

// PromoteDiv returns the (precision, scale) of a DIV result.
//
//	(p1, s1) ÷ (p2, s2) => (p1 + s2 + 1, max(s1+s2, MIN_SCALE))
func PromoteDiv(p1, s1, p2, s2 uint8) (uint8, uint8) {
	resScale := maxU8(s1+s2, MinDecimalScale)
	resPrec := p1 + s2 + 1
	if resPrec > MaxDecimalPrecision {
		resPrec = MaxDecimalPrecision
	}
	return resPrec, resScale
}

func maxU8(a, b uint8) uint8 {
	if a > b {
		return a
	}
	return b
}

// EncodeDecimal128 serializes a Decimal128 as 16 bytes of two's-complement
// little-endian integer.
func EncodeDecimal128(d Decimal128) [16]byte {
	var out [16]byte
	if d.mantissa == nil || d.mantissa.Sign() == 0 {
		return out
	}
	abs := new(big.Int).Abs(d.mantissa)
	absBytes := abs.Bytes()
	if d.mantissa.Sign() < 0 {
		// Two's complement: invert + 1 of the 16-byte big-endian then
		// reverse to little-endian.
		var be [16]byte
		copy(be[16-len(absBytes):], absBytes)
		for i := range be {
			be[i] = ^be[i]
		}
		// add 1
		carry := uint16(1)
		for i := 15; i >= 0 && carry > 0; i-- {
			s := uint16(be[i]) + carry
			be[i] = byte(s & 0xFF)
			carry = s >> 8
		}
		for i := 0; i < 16; i++ {
			out[i] = be[15-i]
		}
		return out
	}
	// Positive: big-endian -> little-endian.
	var be [16]byte
	copy(be[16-len(absBytes):], absBytes)
	for i := 0; i < 16; i++ {
		out[i] = be[15-i]
	}
	return out
}

// DecodeDecimal128 deserializes 16 bytes of two's-complement little-endian
// integer into a Decimal128. Null state is now carried by the per-record
// bitmap (see encoding.Schema.HasBitmap), so this function does not flag
// null values.
func DecodeDecimal128(buf [16]byte) Decimal128 {
	// Convert little-endian to big-endian.
	var be [16]byte
	for i := 0; i < 16; i++ {
		be[i] = buf[15-i]
	}
	negative := be[0]&0x80 != 0
	if !negative {
		m := new(big.Int).SetBytes(be[:])
		return Decimal128{mantissa: m}
	}
	// Negative: reverse two's complement.
	for i := range be {
		be[i] = ^be[i]
	}
	carry := uint16(1)
	for i := 15; i >= 0 && carry > 0; i-- {
		s := uint16(be[i]) + carry
		be[i] = byte(s & 0xFF)
		carry = s >> 8
	}
	m := new(big.Int).SetBytes(be[:])
	m.Neg(m)
	return Decimal128{mantissa: m}
}

// WriteDecimal128 writes a Decimal128 to the .pulse record stream.
func WriteDecimal128(w io.Writer, d Decimal128) error {
	enc := EncodeDecimal128(d)
	if _, err := w.Write(enc[:]); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing decimal128")
	}
	return nil
}

// ReadDecimal128 reads 16 bytes of decimal128 from r and decodes them.
// Null state is carried by the per-record bitmap, not by the payload
// bytes, so this function has no null channel.
func ReadDecimal128(r io.Reader) (Decimal128, error) {
	var buf [16]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return Decimal128{}, errors.WrapCodedError(err, errors.ENCODING_IO, "reading decimal128")
	}
	return DecodeDecimal128(buf), nil
}

// Sqrt returns floor-banker-rounded sqrt(d) at the target scale, given
// the source value at sourceScale. The result is computed entirely in
// decimal arithmetic via big.Int.Sqrt and a half-to-even rounding step
// on the residual. Returns PULSE_DECIMAL_OVERFLOW if intermediate state
// cannot fit in the integer representation; returns PROCESSING_RUNTIME
// for negative inputs (sqrt of a negative decimal is undefined).
func (d Decimal128) Sqrt(sourceScale, targetScale uint8) (Decimal128, error) {
	if d.Sign() < 0 {
		return Decimal128{}, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			"decimal sqrt of negative value")
	}
	if d.Sign() == 0 {
		return ZeroDecimal128(), nil
	}
	// Want r such that r * r ≈ d, with r at targetScale.
	// (r_mantissa * 10^-target)^2 ≈ d_mantissa * 10^-source
	// r_mantissa^2 * 10^(2*target - source) ≈ d_mantissa
	// r_mantissa = sqrt(d_mantissa * 10^(source - 2*target)) when source >= 2*target,
	//            = sqrt(d_mantissa / 10^(2*target - source)) otherwise.
	//
	// We instead scale d_mantissa to a value M whose integer sqrt has
	// the right number of digits: M = d_mantissa * 10^(2*target - source).
	shift := 2*int(targetScale) - int(sourceScale)
	mantissa := new(big.Int).Set(d.mantissa)
	if shift > 0 {
		if shift > 2*MaxDecimalPrecision {
			return Decimal128{}, errors.NewCodedError(errors.PULSE_DECIMAL_OVERFLOW,
				"decimal sqrt intermediate scale exceeds 76")
		}
		factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(shift)), nil)
		mantissa.Mul(mantissa, factor)
	} else if shift < 0 {
		factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-shift)), nil)
		mantissa.Quo(mantissa, factor)
	}
	floor := new(big.Int).Sqrt(mantissa)
	// Banker's-rounding toward the nearest integer using the residual:
	// d - floor^2; compare with floor + 1 candidate (floor+1)^2 - floor^2 = 2*floor + 1.
	floorSq := new(big.Int).Mul(floor, floor)
	residual := new(big.Int).Sub(mantissa, floorSq)
	// Half-distance is (2*floor + 1) / 2. Compare 2*residual with (2*floor + 1).
	twoR := new(big.Int).Lsh(residual, 1)
	threshold := new(big.Int).Add(new(big.Int).Lsh(floor, 1), big.NewInt(1))
	cmp := twoR.Cmp(threshold)
	out := new(big.Int).Set(floor)
	switch {
	case cmp > 0:
		out.Add(out, big.NewInt(1))
	case cmp == 0:
		// Tie: round to even.
		if out.Bit(0) != 0 {
			out.Add(out, big.NewInt(1))
		}
	}
	return NewDecimal128FromBigInt(out)
}

// ValidatePrecisionScale reports whether (precision, scale) form a legal
// decimal128 type spec (1 ≤ precision ≤ 38, 0 ≤ scale ≤ precision).
func ValidatePrecisionScale(precision, scale uint8) error {
	if precision < 1 || precision > MaxDecimalPrecision {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"decimal128 precision out of range",
			map[string]any{"precision": precision})
	}
	if scale > precision {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"decimal128 scale exceeds precision",
			map[string]any{"precision": precision, "scale": scale})
	}
	return nil
}

// FitsPrecision reports whether the mantissa fits in `precision` digits.
func (d Decimal128) FitsPrecision(precision uint8) bool {
	if precision == 0 || precision > MaxDecimalPrecision {
		return false
	}
	if d.mantissa == nil {
		return true
	}
	abs := new(big.Int).Abs(d.mantissa)
	return abs.Cmp(pow10[precision]) < 0
}

// _ keeps the binary import in scope when no other path needs it; the
// encoding/binary import is conditionally used by tests.
var _ = binary.LittleEndian

// ensureFmt suppresses lint when fmt is otherwise unused in some build
// contexts; ParseDecimal128 uses fmt indirectly via errors detail maps.
var _ = fmt.Sprintf
