package regmmm

import (
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// bayes_curve.go is REG_MMM_BAYES's saturation curves
// (params.saturation): per curve its parameters — their priors.<channel>
// keys, coefficient prefixes, default priors and original-unit
// back-transforms — and its value with analytic partials in the scaled
// adstock and in each parameter. One curve serves every channel of a
// model; each runs on the channel's adstock scaled by its peak spend,
// is 0 at zero adstock and rises to 1, so the channel's β is its effect
// at full saturation under every curve.
//
//   - hill (default): h = 1 / (1 + (k/a)^s), params k (half-saturation)
//     and s (shape) — REG_MMM_HILL's curve.
//   - logistic: h = (1 − e^(−λa)) / (1 + e^(−λa)) = tanh(λa/2), param λ
//     (rate; half-saturation at log 3 / λ) — the reach-style concave
//     curve of PyMC-Marketing's logistic_saturation.
//   - michaelis_menten: h = a / (k + a), param k (half-saturation) —
//     Hill at s = 1.
//
// Every product feeding an add is float64()-barriered and every
// transcendental is detmath's, so a curve's bits are the same on every
// architecture; the Hill curve is hillGrad bit for bit, so the default
// model is unchanged.

// curveParam is one saturation-curve parameter.
type curveParam struct {
	// key is the parameter's priors.<channel> key (k, s, lam).
	key string
	// prefix names the parameter's coefficient: prefix + channel.
	prefix string
	// prior is the channel prior the parameter reads.
	prior func(*bayesChannelPriors) *prior
	// raw maps the parameter on the scaled adstock to original units for
	// a channel whose peak spend is xScale.
	raw func(v, xScale float64) float64
	// init is the parameter's start from the mean positive scaled
	// adstock; ok false starts it at its prior's center.
	init func(meanA float64) (v float64, ok bool)
}

// saturationCurve is one REG_MMM_BAYES saturation curve.
type saturationCurve interface {
	// Name is the params.saturation name.
	Name() string
	// Params are the curve's per-channel parameters, in θ and
	// coefficient-table order.
	Params() []curveParam
	// Eval returns h(a) at the scaled adstock a ≥ 0 and the constrained
	// parameters q (Params' order), with ∂h/∂a; a non-nil dq receives
	// ∂h/∂q_i.
	Eval(a float64, q, dq []float64) (h, dha float64)
	// Elasticity is Σ_t a_t·∂h/∂a at a_t over the scaled adstock a and
	// its curve values h: the derivative of Σ_t h(λ·a_t) with respect to
	// a uniform scaling λ of the spend, at λ = 1 (the marginal ROI's
	// numerator).
	Elasticity(a, h, q []float64) float64
}

// log3 is 2·atanh(1/2): the logistic curve is ½ at λa = log 3.
const log3 = 1.0986122886681098

// The curve parameters.
var (
	// halfSatParam is Hill's and Michaelis–Menten's k, started at the
	// mean positive scaled adstock (a curve half-saturated there).
	halfSatParam = curveParam{key: "k", prefix: pmodel.MMMHalfSaturationPrefix,
		prior: func(c *bayesChannelPriors) *prior { return &c.K },
		raw:   func(v, x float64) float64 { return float64(v * x) },
		init:  func(meanA float64) (float64, bool) { return meanA, true }}
	hillShapeParam = curveParam{key: "s", prefix: pmodel.MMMShapePrefix,
		prior: func(c *bayesChannelPriors) *prior { return &c.S },
		raw:   func(v, _ float64) float64 { return v },
		init:  func(float64) (float64, bool) { return 0, false }}
	// lambdaParam is the logistic rate, per unit of scaled adstock (so
	// λ / peak spend in raw adstock units), started half-saturated at
	// the mean positive scaled adstock.
	lambdaParam = curveParam{key: "lam", prefix: pmodel.MMMLogisticLambdaPrefix,
		prior: func(c *bayesChannelPriors) *prior { return &c.Lam },
		raw:   func(v, x float64) float64 { return v / x },
		init:  func(meanA float64) (float64, bool) { return log3 / meanA, true }}
)

// curveByName is the curve params.saturation names ("" is the default,
// hill), or nil for an unknown name.
func curveByName(name string) saturationCurve {
	switch name {
	case "", pmodel.MMMSaturationHill:
		return hillCurveModel{}
	case pmodel.MMMSaturationLogistic:
		return logisticCurve{}
	case pmodel.MMMSaturationMichaelisMenten:
		return mmCurve{}
	}
	return nil
}

// curveNames are the accepted params.saturation values, in manifest
// order.
func curveNames() []string { return pmodel.MMMBayesSaturations() }

// curveKeys are every curve's priors.<channel> keys, in a fixed order.
var curveKeys = []string{"k", "s", "lam"}

// hillCurveModel is h = 1 / (1 + (k/a)^s) (hillGrad).
type hillCurveModel struct{}

func (hillCurveModel) Name() string         { return pmodel.MMMSaturationHill }
func (hillCurveModel) Params() []curveParam { return []curveParam{halfSatParam, hillShapeParam} }
func (hillCurveModel) Eval(a float64, q, dq []float64) (float64, float64) {
	h, dha, dhk, dhs := hillGrad(a, q[0], q[1])
	if dq != nil {
		dq[0], dq[1] = dhk, dhs
	}
	return h, dha
}

// Elasticity is s·Σ_t h_t·(1 − h_t): a·∂h/∂a = s·h·(1 − h).
func (hillCurveModel) Elasticity(_, h, q []float64) float64 {
	slope := 0.0
	for _, x := range h {
		slope += float64(x * (1 - x))
	}
	return float64(q[1] * slope)
}

// logisticCurve is h = (1 − e^(−λa)) / (1 + e^(−λa)), written
// −expm1(−λa) / (1 + e^(−λa)) so it keeps its precision near 0. With
// E = e^(−λa) and g = 2E / (1 + E)²,
//
//	∂h/∂a = λ·g,   ∂h/∂λ = a·g.
type logisticCurve struct{}

func (logisticCurve) Name() string         { return pmodel.MMMSaturationLogistic }
func (logisticCurve) Params() []curveParam { return []curveParam{lambdaParam} }
func (logisticCurve) Eval(a float64, q, dq []float64) (float64, float64) {
	lam := q[0]
	u := float64(lam * a)
	e := detmath.Exp(-u)
	den := 1 + e
	h := -detmath.Expm1(-u) / den
	g := float64(2*e) / float64(den*den)
	if dq != nil {
		dq[0] = float64(a * g)
	}
	return h, float64(lam * g)
}

// Elasticity is Σ_t λ·a_t·g_t.
func (c logisticCurve) Elasticity(a, _, q []float64) float64 {
	sum := 0.0
	for _, x := range a {
		_, dha := c.Eval(x, q, nil)
		sum += float64(x * dha)
	}
	return sum
}

// mmCurve is h = a / (k + a), with
//
//	∂h/∂a = k / (k + a)²,   ∂h/∂k = −a / (k + a)².
type mmCurve struct{}

func (mmCurve) Name() string         { return pmodel.MMMSaturationMichaelisMenten }
func (mmCurve) Params() []curveParam { return []curveParam{halfSatParam} }
func (mmCurve) Eval(a float64, q, dq []float64) (float64, float64) {
	k := q[0]
	den := k + a
	d2 := float64(den * den)
	if dq != nil {
		dq[0] = -a / d2
	}
	return a / den, k / d2
}

// Elasticity is Σ_t h_t·(1 − h_t): a·∂h/∂a = a·k / (k + a)² = h·(1 − h).
func (mmCurve) Elasticity(_, h, _ []float64) float64 {
	sum := 0.0
	for _, x := range h {
		sum += float64(x * (1 - x))
	}
	return sum
}
