package regmmm

import (
	"github.com/frankbardon/pulse/internal/distlagparams"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// bayes_adstock.go is REG_MMM_BAYES's adstock kernels (params.adstock):
// per kernel its parameters — their priors.<channel> keys, coefficient
// prefixes, supports and default priors — and its lag weights with
// their analytic derivatives in each parameter. One kernel serves every
// channel of a model.
//
// Every weight is WIN_DISTRIBUTED_LAG's own (internal/distlagparams'
// per-lag functions, normalize false), so the model's adstock of a
// channel is the window's column bit for bit and a fit's adstock record
// (pmodel.Adstock) reproduces it. The derivatives are the model's
// alone: every product feeding an add is float64()-barriered and every
// transcendental is detmath's, so they too are the same bits on every
// architecture.

// kernelParam is one adstock parameter of a kernel.
type kernelParam struct {
	// key is the parameter's priors.<channel> key and its adstock
	// record field (decay, peak, shape, scale).
	key string
	// prefix names the parameter's coefficient: prefix + channel.
	prefix string
	// sup is the support its prior must live on.
	sup support
	// prior is the channel prior the parameter reads.
	prior func(*bayesChannelPriors) *prior
	// def is the default prior at max_lag.
	def func(maxLag int) prior
}

// adstockKernel is one REG_MMM_BAYES adstock kernel.
type adstockKernel interface {
	// Name is the params.adstock (and WIN_DISTRIBUTED_LAG kernel) name.
	Name() string
	// Params are the kernel's per-channel parameters, in θ and
	// coefficient-table order.
	Params() []kernelParam
	// Weights returns w_l for lags 0..maxLag at the constrained
	// parameters p (Params' order) and, with grad, dw[i][l] = ∂w_l/∂p_i.
	Weights(p []float64, maxLag int, grad bool) (w []float64, dw [][]float64)
}

// The parameters the kernels share.
var (
	decayParam = kernelParam{key: "decay", prefix: pmodel.MMMDecayPrefix, sup: supportUnit,
		prior: func(c *bayesChannelPriors) *prior { return &c.Decay },
		def:   func(int) prior { return betaPrior(1, 1) }}
	// peakParam's support is (0, ∞) for the prior's family; the factory
	// further holds its prior inside [0, max_lag] (peakPriorRefusal).
	peakParam = kernelParam{key: "peak", prefix: pmodel.MMMPeakPrefix, sup: supportPositive,
		prior: func(c *bayesChannelPriors) *prior { return &c.Peak },
		def:   defaultPeakPrior}
	shapeParam = kernelParam{key: "shape", prefix: pmodel.MMMAdstockShapePrefix, sup: supportPositive,
		prior: func(c *bayesChannelPriors) *prior { return &c.WShape },
		def:   func(int) prior { return logNormalPrior(0, 0.5) }}
	scaleParam = kernelParam{key: "scale", prefix: pmodel.MMMAdstockScalePrefix, sup: supportPositive,
		prior: func(c *bayesChannelPriors) *prior { return &c.WScale },
		def:   func(int) prior { return logNormalPrior(1, 0.75) }}
)

// defaultPeakPrior is the delayed kernel's peak prior,
// TruncNormal(1, 2, 0, max_lag): centred one lag on (a carry-over that
// peaks the period after the spend), sd two lags, held to the window.
func defaultPeakPrior(maxLag int) prior { return truncNormalPrior(1, 2, 0, float64(maxLag)) }

// kernelByName is the kernel params.adstock names ("" is the default,
// geometric), or nil for an unknown name.
func kernelByName(name string) adstockKernel {
	switch name {
	case "", distlagparams.KernelGeometric:
		return geometricKernel{}
	case distlagparams.KernelDelayed:
		return delayedKernel{}
	case distlagparams.KernelWeibullCDF:
		return weibullKernel{pdf: false}
	case distlagparams.KernelWeibullPDF:
		return weibullKernel{pdf: true}
	}
	return nil
}

// kernelNames are the accepted params.adstock values, in manifest order.
func kernelNames() []string { return distlagparams.Kernels() }

// newDW is np derivative rows of maxLag + 1 lags each.
func newDW(np, maxLag int) [][]float64 {
	dw := make([][]float64, np)
	for i := range dw {
		dw[i] = make([]float64, maxLag+1)
	}
	return dw
}

// geometricKernel is w_l = decay^l (geometricWeights).
type geometricKernel struct{}

func (geometricKernel) Name() string          { return distlagparams.KernelGeometric }
func (geometricKernel) Params() []kernelParam { return []kernelParam{decayParam} }
func (geometricKernel) Weights(p []float64, maxLag int, grad bool) ([]float64, [][]float64) {
	w, dw := geometricWeights(p[0], maxLag)
	if !grad {
		return w, nil
	}
	return w, [][]float64{dw}
}

// delayedKernel is w_l = decay^((l − peak)²) (Jin et al. 2017), with
//
//	∂w_l/∂decay = (l − peak)²·w_l / decay,
//	∂w_l/∂peak  = −2·(l − peak)·log(decay)·w_l.
type delayedKernel struct{}

func (delayedKernel) Name() string          { return distlagparams.KernelDelayed }
func (delayedKernel) Params() []kernelParam { return []kernelParam{decayParam, peakParam} }
func (delayedKernel) Weights(p []float64, maxLag int, grad bool) ([]float64, [][]float64) {
	decay, peak := p[0], p[1]
	w := make([]float64, maxLag+1)
	var dw [][]float64
	if grad {
		dw = newDW(2, maxLag)
	}
	logDecay := detmath.Log(decay)
	for l := range w {
		w[l] = distlagparams.DelayedWeight(decay, peak, l)
		if !grad {
			continue
		}
		d := float64(l) - peak
		q := float64(d * d)
		dw[0][l] = float64(q*w[l]) / decay
		dw[1][l] = float64(float64(float64(-2*d)*logDecay) * w[l])
	}
	return w, dw
}

// weibullKernel is the Weibull survival w_l = exp(−(l/scale)^shape)
// (pdf false) or the Weibull density at l + 1 (pdf true). With
// u = (l/scale)^shape, the survival's partials are
//
//	∂w_l/∂shape = −w_l·u·log(l/scale),   ∂w_l/∂scale = w_l·u·shape/scale
//
// (0 at lag 0, where w_0 = 1 for every shape and scale); with
// z = (l + 1)/scale, the density's are w_l times
//
//	∂log w_l/∂shape = 1/shape + log z·(1 − z^shape),
//	∂log w_l/∂scale = shape·(z^shape − 1)/scale.
type weibullKernel struct{ pdf bool }

func (k weibullKernel) Name() string {
	if k.pdf {
		return distlagparams.KernelWeibullPDF
	}
	return distlagparams.KernelWeibullCDF
}

func (weibullKernel) Params() []kernelParam { return []kernelParam{shapeParam, scaleParam} }

func (k weibullKernel) Weights(p []float64, maxLag int, grad bool) ([]float64, [][]float64) {
	shape, scale := p[0], p[1]
	w := make([]float64, maxLag+1)
	var dw [][]float64
	if grad {
		dw = newDW(2, maxLag)
	}
	for l := range w {
		if k.pdf {
			w[l] = distlagparams.WeibullPDFWeight(shape, scale, l)
			if !grad {
				continue
			}
			z := float64(l+1) / scale
			lz := detmath.Log(z)
			zk := detmath.Pow(z, shape)
			dw[0][l] = float64(w[l] * (1/shape + float64(lz*(1-zk))))
			dw[1][l] = float64(w[l] * (float64(shape*(zk-1)) / scale))
			continue
		}
		w[l] = distlagparams.WeibullCDFWeight(shape, scale, l)
		if !grad || l == 0 {
			continue
		}
		r := float64(l) / scale
		u := detmath.Pow(r, shape)
		wu := float64(w[l] * u)
		dw[0][l] = -float64(wu * detmath.Log(r))
		dw[1][l] = float64(wu*shape) / scale
	}
	return w, dw
}
