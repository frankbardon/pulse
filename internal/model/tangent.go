package model

// tangent.go is the nonlinear half of the influence figures over a
// bound fit (U71 E2-S6, #732): a REG_MMM_HILL fit is a nonlinear least
// squares, so its leverage is the tangent-plane (Gauss–Newton) hat
// value h = gᵀ(JᵀJ)⁻¹g, g = ∂ŷ/∂θ the gradient of the row's fitted value
// in every estimated parameter at the optimum (J stacks the fit's own
// rows' g) — what R reads off an nls fit's gradient. The regression
// that owns the curve supplies the gradient (a Tangent, built by its
// TangentFactory and registered beside its scorer); the engine's
// BoundInfluence reads (JᵀJ)⁻¹ off the fit's Vcov / φ exactly as it does
// for a linear design. CheckFitDesign is the one admission rule.

// Tangent is a bound nonlinear fit's tangent plane at its optimum:
// immutable once built and safe for concurrent use.
type Tangent interface {
	// Keys are the coefficient names Gradient's entries follow — every
	// estimated parameter the fit's Vcov covers, in the units of its
	// coefficient table (a curve parameter in its own units, never a
	// log-scale search coordinate).
	Keys() []string
	// Gradient is row's fitted value ŷ — the fit's row score bit for
	// bit — and ∂ŷ/∂θ for each Keys entry; ok=false when a column it
	// reads is null or non-finite. A channel the row reads as its
	// carried adstock (an AdstockLagRow) takes its kernel parameters'
	// partials through the lags; read as given, ∂a/∂φ is 0.
	Gradient(row Row) (yhat float64, grad []float64, ok bool)
}

// TangentFactory builds a bound fit's Tangent.
type TangentFactory func(b *Bound) (Tangent, error)

// AdstockLagRow is a scored row whose channels are read as their
// carried adstock a = Σ_l w_l·spend(period − l·step) (the engine's
// carry-over Carrier): AdstockLags is channel's spend at lags
// 0..max_lag, NaN at a lag the sum skipped (no such period, or a null
// spend); ok=false when the row does not carry channel. A Tangent reads
// ∂a/∂φ = Σ_l ∂w_l/∂φ·spend_l off it.
type AdstockLagRow interface {
	AdstockLags(channel string) ([]float64, bool)
}
