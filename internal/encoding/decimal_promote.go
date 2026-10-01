package encoding

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// PromoteAdd returns the (precision, scale) of a SUM/SUB result given two
// operand types per SQL:2016 / Arrow Decimal128 rules:
//
//	(p1, s1) ± (p2, s2) => (max(p1-s1, p2-s2) + max(s1, s2) + 1, max(s1, s2))
//
// The result precision is clamped at MaxDecimalPrecision; clamping
// callers must check ClampedPrecision and emit PULSE_DECIMAL_OVERFLOW
// when overflow surfaces at runtime.
func PromoteAdd(p1, s1, p2, s2 uint8) (uint8, uint8) {
	intDigits := max(p1-s1, p2-s2)
	resScale := max(s1, s2)
	resPrec := intDigits + resScale + 1
	if resPrec > encoding.MaxDecimalPrecision {
		resPrec = encoding.MaxDecimalPrecision
	}
	return resPrec, resScale
}

// PromoteMul returns the (precision, scale) of a MUL result.
//
//	(p1, s1) × (p2, s2) => (p1 + p2, s1 + s2)
func PromoteMul(p1, s1, p2, s2 uint8) (uint8, uint8) {
	resScale := s1 + s2
	resPrec := p1 + p2
	if resPrec > encoding.MaxDecimalPrecision {
		resPrec = encoding.MaxDecimalPrecision
	}
	return resPrec, resScale
}

// PromoteDiv returns the (precision, scale) of a DIV result.
//
//	(p1, s1) ÷ (p2, s2) => (p1 + s2 + 1, max(s1+s2, MIN_SCALE))
func PromoteDiv(p1, s1, p2, s2 uint8) (uint8, uint8) {
	resScale := max(s1+s2, encoding.MinDecimalScale)
	resPrec := p1 + s2 + 1
	if resPrec > encoding.MaxDecimalPrecision {
		resPrec = encoding.MaxDecimalPrecision
	}
	return resPrec, resScale
}

// ValidatePrecisionScale reports whether (precision, scale) form a legal
// decimal128 type spec (1 ≤ precision ≤ 38, 0 ≤ scale ≤ precision).
func ValidatePrecisionScale(precision, scale uint8) error {
	if precision < 1 || precision > encoding.MaxDecimalPrecision {
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
