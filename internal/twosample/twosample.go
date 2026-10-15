// Package twosample is the one reading of the explicit per-side inputs
// the Compose two-sample overlays (OVERLAY_T_VS_REF, OVERLAY_Z_VS_REF,
// OVERLAY_T_CELL, OVERLAY_Z_CELL) take when a side's mean arrives as a
// bare scalar: its variance and its sample size, from the spec's
// params.
//
// A side whose mean comes from the slot's Components (an AGG_WELFORD
// row or a crosstab cell whose components carry mean and variance)
// needs no params. A scalar side does, and there is NO default: the
// var = 1, n = 2 fallback once computed p-values from figures nobody
// supplied (U46 E4-S16). Predict (internal/descriptor) and the runtime
// handlers (internal/processing) both read params through Inputs, so
// what one refuses the other refuses.
//
// Leaf: stdlib only. No-execute.
package twosample

import "math"

// Side is one leg of a two-sample overlay.
type Side string

const (
	// Target is the scored slot (the spec's targets).
	Target Side = "target"
	// Reference is the spec's reference slot.
	Reference Side = "reference"
)

// suffix is the per-side params key suffix.
func (s Side) suffix() string {
	if s == Reference {
		return "ref"
	}
	return "target"
}

// VarianceKey is side's variance param ("variance_target" /
// "variance_ref").
func VarianceKey(s Side) string { return "variance_" + s.suffix() }

// SampleSizeKey is side's sample-size param ("sample_size_target" /
// "sample_size_ref").
func SampleSizeKey(s Side) string { return "sample_size_" + s.suffix() }

// Symmetric keys: one value for both sides, read when the per-side key
// is absent.
const (
	SymmetricVarianceKey   = "variance"
	SymmetricSampleSizeKey = "sample_size"
)

// ParamKeys is every params key Inputs reads, both sides: the per-side
// keys then the symmetric ones.
func ParamKeys() []string {
	return []string{VarianceKey(Target), SampleSizeKey(Target), VarianceKey(Reference), SampleSizeKey(Reference),
		SymmetricVarianceKey, SymmetricSampleSizeKey}
}

// Inputs reads side's (variance, sample size) from params: the per-side
// key when present, else the symmetric one. A value counts only when it
// is a finite number > 0; a per-side key that is present but unusable
// does not fall through to the symmetric key. missing names, in that
// order, the per-side key of every input that is absent or unusable —
// nil when both are usable.
func Inputs(params map[string]any, side Side) (variance, n float64, missing []string) {
	variance, okV := read(params, VarianceKey(side), SymmetricVarianceKey)
	n, okN := read(params, SampleSizeKey(side), SymmetricSampleSizeKey)
	if !okV {
		missing = append(missing, VarianceKey(side))
	}
	if !okN {
		missing = append(missing, SampleSizeKey(side))
	}
	return variance, n, missing
}

func read(params map[string]any, key, symmetric string) (float64, bool) {
	v, ok := params[key]
	if !ok {
		if v, ok = params[symmetric]; !ok {
			return 0, false
		}
	}
	var x float64
	switch t := v.(type) {
	case float64:
		x = t
	case float32:
		x = float64(t)
	case int:
		x = float64(t)
	case int32:
		x = float64(t)
	case int64:
		x = float64(t)
	default:
		return 0, false
	}
	if !(x > 0) || math.IsInf(x, 0) {
		return 0, false
	}
	return x, true
}
