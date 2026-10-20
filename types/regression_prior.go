package types

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// PriorPrecision is RegressionSpec.PriorPrecision (REG_BAYES_LINEAR):
// the prior precision Λ₀ of the coefficients. Three states:
//
//   - absent (the zero value, or a scalar 0): the engine default 1e-3 on
//     every coefficient, Λ₀ = 1e-3·I;
//   - a scalar ε > 0 (PrecisionScalar): Λ₀ = ε·I;
//   - a per-coefficient vector v (PrecisionVector): Λ₀ = diag(v), length
//     len(Predictors)+1, intercept FIRST then one entry per predictor in
//     Predictors order — PriorMu's order. An entry 0 is a flat prior on
//     that coefficient; a large entry pins it to its PriorMu entry.
//
// On the wire it is a JSON number or an array of numbers; the scalar
// form is byte-identical to the float64 field it replaced (an absent or
// 0 precision is omitted).
type PriorPrecision struct {
	scalar   float64
	vector   []float64
	isVector bool
}

// PrecisionScalar returns the scalar precision ε (Λ₀ = ε·I). 0 is the
// absent precision.
func PrecisionScalar(eps float64) PriorPrecision { return PriorPrecision{scalar: eps} }

// PrecisionVector returns the per-coefficient precision diag(v),
// intercept first. v is copied; a nil or empty v is a vector of length
// 0 (refused by the length rule, never read as absent).
func PrecisionVector(v []float64) PriorPrecision {
	return PriorPrecision{vector: append([]float64{}, v...), isVector: true}
}

// IsZero reports whether the precision is absent (the engine default).
// It is what `omitzero` consults.
func (p PriorPrecision) IsZero() bool { return !p.isVector && p.scalar == 0 }

// IsVector reports whether the precision is the per-coefficient form.
func (p PriorPrecision) IsVector() bool { return p.isVector }

// Scalar returns the scalar form's ε: 0 when absent or a vector.
func (p PriorPrecision) Scalar() float64 {
	if p.isVector {
		return 0
	}
	return p.scalar
}

// Vector returns a copy of the per-coefficient form, nil unless
// IsVector.
func (p PriorPrecision) Vector() []float64 {
	if !p.isVector {
		return nil
	}
	return append([]float64{}, p.vector...)
}

// MarshalJSON emits the number or the array. An absent precision is
// never marshalled (omitzero); marshalled directly it reads as 0.
func (p PriorPrecision) MarshalJSON() ([]byte, error) {
	if p.isVector {
		return json.Marshal(p.vector)
	}
	return json.Marshal(p.scalar)
}

// UnmarshalJSON accepts a number or an array of numbers; `null` leaves
// the precision absent. A null array entry or any other JSON shape is an
// error (a null entry is never read as a flat prior).
func (p *PriorPrecision) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	switch {
	case bytes.Equal(trimmed, []byte("null")):
		*p = PriorPrecision{}
		return nil
	case len(trimmed) > 0 && trimmed[0] == '[':
		var raw []*float64
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return fmt.Errorf("prior_precision: want a number or an array of numbers: %w", err)
		}
		v := make([]float64, len(raw))
		for i, e := range raw {
			if e == nil {
				return fmt.Errorf("prior_precision: entry %d is null; want a number (0 is a flat prior)", i)
			}
			v[i] = *e
		}
		*p = PriorPrecision{vector: v, isVector: true}
		return nil
	default:
		var x float64
		if err := json.Unmarshal(trimmed, &x); err != nil {
			return fmt.Errorf("prior_precision: want a number or an array of numbers: %w", err)
		}
		*p = PrecisionScalar(x)
		return nil
	}
}
