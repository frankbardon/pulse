package regression

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/types"
)

// bound_influence.go: the influence figures of a scoring row over a
// BOUND REG_OLS or REG_GLM fit (U65 E1-S2) — ATTR_REG_LEVERAGE,
// ATTR_REG_INFLUENCE and ATTR_REG_RESIDUAL kind standardized /
// studentized with params.fit. The fit's design is read off its
// coefficient covariance, never its rows: with x̃ = [1, x…] the fit's
// design row, η = x̃ᵀβ, μ = g⁻¹(η), w = (dμ/dη)²/V(μ) the IRLS working
// weight at μ (1 under OLS) and Vcov = φ·(XᵀWX)⁻¹,
//
//	h  = w · x̃ᵀ(Vcov/φ)x̃          R hatvalues
//	rP = (y − μ)/√V(μ)             the Pearson residual
//
// and the figures are InfluenceFigures(rP, h, φ, n − p′, p′, φ
// estimated, sign g′(μ)) — exactly R 4.6.1's lm / glm influence
// measures for the fit's own rows (a scoring row that is one of them),
// and the same formulas extended to any other row. φ is
// RegressionResult.Dispersion where it is estimated (REG_OLS σ̂², gamma,
// a fixed-θ negative binomial under dispersion "estimated") and 1 under
// binomial / poisson / negative binomial (V(μ) at the fit's θ,
// RegressionResult.Theta); model.CheckFitDesign refused every fit
// that carries no such design (no vcov, robust, penalized, selection,
// resample, weighted, …) before this is built.
//
// A REG_MMM_HILL fit (U71 E2-S6, #732) is a nonlinear least squares:
// its design row is the tangent g = ∂ŷ/∂θ at the optimum (the
// registered model.Tangent, RegisterModelTangent), Vcov = σ̂²·(JᵀJ)⁻¹ over
// the free parameters (a parameter held on a bound has a NaN row and
// joins nothing), and
//
//	h = gᵀ(Vcov/σ̂²)g          the Gauss–Newton (tangent-plane) hat value
//
// with the figures InfluenceFigures(y − ŷ, h, σ̂², df, p′, true, 1), p′
// the free parameter count and df the fit's residual df n − p (every
// parameter, the df its σ̂² = SSR/df divided by) — OLS's formulas on the
// linearised model, and on the fit's own rows the hat values of its own
// Jacobian (Σ h = p′). A carried row (the engine's carry-over) hands
// the tangent each channel's adstock lags, so the kernel parameters'
// partials see the carried history.
//
// FMA-free like influence.go (TestOLSQR_NoFMAContraction matches this
// file's name): every product feeding an add or subtract is wrapped.
// The linear predictor is the shared scoring kernel (LinearPredictor,
// called out of line so its possible fused multiply-add stays in
// score.go), so μ is the bound fit's ATTR_REG_FITTED score bit for bit.

// BoundInfluence is one bound fit's design, resolved once per slot;
// immutable and safe for concurrent use.
type BoundInfluence struct {
	intercept float64
	coefs     []float64
	columns   []string
	// fam is the GLM family (gaussian: OLS — μ = η, w = 1, V = 1).
	fam      glmFamily
	gaussian bool
	// m is (XᵀWX)⁻¹ = Vcov/φ over [intercept, columns…].
	m         [][]float64
	phi, df   float64
	p1        float64
	estimated bool
	// tangent is a nonlinear fit's gradient (nil on REG_OLS / REG_GLM);
	// free indexes its Keys over m's rows (the free parameters).
	tangent model.Tangent
	free    []int
}

// NewBoundInfluence resolves b's design. A fit CheckFitDesign admits
// always resolves; anything else (no vcov, a vcov missing a coefficient
// key) is an engine invariant violation (PROCESSING_INTERNAL).
func NewBoundInfluence(b *model.Bound) (*BoundInfluence, error) {
	if b == nil || b.Vcov == nil || b.Result == nil || len(b.Coefficients) != len(b.Predictors) {
		return nil, internalDesign(b, "carries no coefficient covariance")
	}
	d := &BoundInfluence{
		intercept: b.Intercept,
		coefs:     append([]float64(nil), b.Coefficients...),
		columns:   append([]string(nil), b.Predictors...),
		p1:        float64(len(b.Predictors) + 1),
	}
	switch b.Type {
	case types.REG_OLS:
		d.gaussian, d.estimated = true, true
	case types.REG_GLM:
		fam, err := familyAtTheta(b.Family, b.Link, b.Result.Theta)
		if err != nil {
			return nil, err
		}
		// A fixed-θ negative binomial fit under dispersion "estimated"
		// carries its φ̂ (RegressionResult.Dispersion, set only where φ
		// is estimated); every other one reads φ = 1.
		d.fam, d.estimated = fam, fam.EstimatesDispersion || b.Family == reglink.NegativeBinomial && b.Result.Dispersion != 0
	default:
		tf, ok := modelTangents[b.Type]
		if !ok {
			return nil, internalDesign(b, "is not a REG_OLS, REG_GLM or REG_MMM_HILL fit")
		}
		return newTangentInfluence(b, tf)
	}
	d.phi = 1
	if d.estimated {
		d.phi = b.Result.Dispersion
	}
	d.df = float64(b.Result.NObs) - d.p1
	// Vcov's keys are (intercept) then the predictors (fit order);
	// index by key so the design row lines up whatever the order.
	idx := make(map[string]int, len(b.Vcov.RowKeys))
	for i, k := range b.Vcov.RowKeys {
		idx[k] = i
	}
	keys := append([]string{model.InterceptKey}, b.Predictors...)
	pos := make([]int, len(keys))
	for i, k := range keys {
		j, ok := idx[k]
		if !ok || j >= len(b.Vcov.Values) {
			return nil, internalDesign(b, fmt.Sprintf("has no vcov row for %q", k))
		}
		pos[i] = j
	}
	d.m = make([][]float64, len(keys))
	for i := range keys {
		d.m[i] = make([]float64, len(keys))
		row := b.Vcov.Values[pos[i]]
		for j := range keys {
			if pos[j] >= len(row) {
				return nil, internalDesign(b, "has a ragged vcov")
			}
			d.m[i][j] = row[pos[j]] / d.phi
		}
	}
	return d, nil
}

func internalDesign(b *model.Bound, what string) error {
	name := ""
	if b != nil {
		name = b.Name
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		fmt.Sprintf("bound fit %q %s; its influence figures cannot be read", name, what),
		map[string]any{"name": name})
}

// Columns are the predictor columns the design row reads, fit order.
func (d *BoundInfluence) Columns() []string { return append([]string(nil), d.columns...) }

// newTangentInfluence resolves a nonlinear fit's design (tf, its
// tangent): the free parameters are the Vcov keys with a defined
// variance, M = Vcov/σ̂² over them.
func newTangentInfluence(b *model.Bound, tf model.TangentFactory) (*BoundInfluence, error) {
	tan, err := tf(b)
	if err != nil {
		return nil, internalDesign(b, "has no tangent plane ("+err.Error()+")")
	}
	d := &BoundInfluence{gaussian: true, estimated: true, phi: b.Result.Dispersion, tangent: tan}
	if !(d.phi > 0) {
		return nil, internalDesign(b, "carries no dispersion")
	}
	idx := make(map[string]int, len(b.Vcov.RowKeys))
	for i, k := range b.Vcov.RowKeys {
		idx[k] = i
	}
	keys := tan.Keys()
	pos := make([]int, len(keys))
	for i, k := range keys {
		j, ok := idx[k]
		if !ok || j >= len(b.Vcov.Values) || j >= len(b.Vcov.Values[j]) {
			return nil, internalDesign(b, fmt.Sprintf("has no vcov row for %q", k))
		}
		pos[i] = j
		// A parameter held on a bound has a NaN variance: it is not
		// estimated, so it joins neither the hat matrix nor p′.
		if v := b.Vcov.Values[j][j]; !math.IsNaN(v) {
			d.free = append(d.free, i)
		}
	}
	d.m = make([][]float64, len(d.free))
	for a, i := range d.free {
		d.m[a] = make([]float64, len(d.free))
		row := b.Vcov.Values[pos[i]]
		for c, j := range d.free {
			if pos[j] >= len(row) {
				return nil, internalDesign(b, "has a ragged vcov")
			}
			d.m[a][c] = row[pos[j]] / d.phi
		}
	}
	d.p1 = float64(len(d.free))
	d.df = b.Result.DF
	if !(d.df > 0) {
		d.df = float64(b.Result.NObs - len(keys))
	}
	return d, nil
}

// Leverage is row's leverage h; ok=false (a null) when a predictor is
// null or non-finite (InputValue).
func (d *BoundInfluence) Leverage(row model.Row) (float64, bool) {
	if d.tangent != nil {
		h, _, ok := d.atTangent(row)
		return h, ok
	}
	x, ok := d.read(row)
	if !ok {
		return 0, false
	}
	h, _, _ := d.at(x)
	return h, true
}

// atTangent is a nonlinear fit's design at row: the hat value
// h = gᵀ M g over the free parameters (NaN with none) and the fitted
// value ŷ; ok=false when a column the tangent reads is null.
func (d *BoundInfluence) atTangent(row model.Row) (h, mu float64, ok bool) {
	mu, g, ok := d.tangent.Gradient(row)
	if !ok {
		return 0, 0, false
	}
	if len(d.free) == 0 {
		return math.NaN(), mu, true
	}
	q := 0.0
	for a, i := range d.free {
		r := 0.0
		for c, j := range d.free {
			r += float64(d.m[a][c] * g[j])
		}
		q += float64(g[i] * r)
	}
	return finiteOrNaN(q), mu, true
}

// Row is row's influence figures with response y; ok=false (a null)
// when a predictor is null or non-finite.
func (d *BoundInfluence) Row(row model.Row, y float64) (RowInfluence, bool) {
	if d.tangent != nil {
		h, mu, ok := d.atTangent(row)
		if !ok {
			return RowInfluence{}, false
		}
		return InfluenceFigures(y-mu, h, d.phi, d.df, d.p1, true, 1), true
	}
	x, ok := d.read(row)
	if !ok {
		return RowInfluence{}, false
	}
	h, mu, sign := d.at(x)
	e := y - mu
	if d.gaussian {
		return InfluenceFigures(e, h, d.phi, d.df, d.p1, d.estimated, sign), true
	}
	v := d.fam.Variance(mu)
	if !(v > 0) {
		nan := math.NaN()
		return RowInfluence{Leverage: h, Standardized: nan, Studentized: nan, Cooks: nan, DFFITS: nan, StandardizedDeviance: nan}, true
	}
	out := InfluenceFigures(e/math.Sqrt(v), h, d.phi, d.df, d.p1, d.estimated, sign)
	if !math.IsNaN(out.Standardized) {
		// The deviance residual sign(y − μ)·√dᵢ (2× the unit deviance,
		// residuals.glm's), standardized as the Pearson one is.
		dv := 2 * d.fam.Deviance(y, mu)
		if dv < 0 {
			dv = 0 // rounding residue at y = μ
		}
		rD := math.Copysign(math.Sqrt(dv), e)
		out.StandardizedDeviance = finiteOrNaN(rD / float64(math.Sqrt(d.phi)*math.Sqrt(1-h)))
	}
	return out, true
}

// read is row's predictor values in fit order.
func (d *BoundInfluence) read(row model.Row) ([]float64, bool) {
	x := make([]float64, len(d.columns))
	for i, c := range d.columns {
		v, ok := InputValue(row, c)
		if !ok {
			return nil, false
		}
		x[i] = v
	}
	return x, true
}

// at is the design at predictors x: the leverage h = w·x̃ᵀ M x̃, the
// fitted mean μ and the sign of g′(μ).
func (d *BoundInfluence) at(x []float64) (h, mu, sign float64) {
	eta := linearPredictorOutOfLine(d.intercept, d.coefs, x)
	w, sign := 1.0, 1.0
	mu = eta
	if !d.gaussian {
		mu = d.fam.InverseLink(eta)
		g := d.fam.DmuDeta(eta)
		v := d.fam.Variance(mu)
		w = float64(g*g) / v
		if g < 0 {
			sign = -1
		}
	}
	// x̃ᵀ M x̃ over x̃ = [1, x…], summed row by row.
	q := 0.0
	for i := range d.m {
		xi := 1.0
		if i > 0 {
			xi = x[i-1]
		}
		r := 0.0
		for j := range d.m[i] {
			xj := 1.0
			if j > 0 {
				xj = x[j-1]
			}
			r += float64(d.m[i][j] * xj)
		}
		q += float64(xi * r)
	}
	return finiteOrNaN(float64(w * q)), mu, sign
}
