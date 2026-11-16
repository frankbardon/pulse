package regmmm

import (
	"fmt"
	"math"
	"strings"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/distlagparams"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// hill_adstock.go is REG_MMM_HILL's in-model adstock (params.adstock):
// the kernel's parameters join the joint Levenberg–Marquardt vector, so
// the fit adstocks each channel's raw spend itself and one Jacobian
// gives every standard error, the decay↔curve covariances included. The
// kernels — all four of WIN_DISTRIBUTED_LAG's — are REG_MMM_BAYES's (bayes_adstock.go — WIN_DISTRIBUTED_LAG's
// weights with normalize false, and their analytic derivatives); this
// file holds what is HILL's own: which kernels the fit takes, each
// parameter's box and start, and the refusals.

// The params.adstock refusal reasons (PROCESSING_CONFIG details.reason).
const (
	reasonKernelUnknown         = "kernel_unknown"
	reasonMaxLagInvalid         = "max_lag_invalid"
	reasonMaxLagRequired        = "max_lag_required"
	reasonMaxLagWithoutAdstock  = "max_lag_without_adstock"
	reasonOrderPartitionAdstock = "order_partition_unsupported"
)

// hillDecayStarts are the decays the start scans (every kernel with a
// decay): the start keeps the one whose linear start fits best, so the
// joint solve begins in the right basin of a decay the data inform.
var hillDecayStarts = []float64{0.1, 0.3, 0.5, 0.7, 0.9}

// hillDecayRefStart is the decay the channels' k scale is read at (the
// adstocked spend's mean there), fixed before the start scan.
const hillDecayRefStart = 0.5

// hillKernel is one kernel REG_MMM_HILL fits in-model: the shared kernel
// and each parameter's box, in Params' order.
type hillKernel struct {
	adstockKernel
	maxLag int
	lo, hi []float64
}

// hillKernelFor is params.adstock's kernel at max_lag, or the refusal
// reason kernel_unknown ("" is unknown). The boxes: geometric decay in
// [0, 0.99]; delayed decay in [0.01, 0.99] (its peak derivative carries
// log(decay)) and peak in [0, max_lag]; a Weibull kernel's shape in
// [0.1, 10] (the density's shape derivative carries 1/shape) and scale
// in [0.1, max_lag + 1] (the density reads lag l at l + 1, so past that
// scale every weight on the window is the rising flank of one curve).
func hillKernelFor(name string, maxLag int) (*hillKernel, string) {
	k := kernelByName(name)
	if k == nil || name == "" {
		// "" is REG_MMM_BAYES's default; here params.adstock itself is
		// the switch, so it names its kernel.
		return nil, reasonKernelUnknown
	}
	switch k.Name() {
	case distlagparams.KernelGeometric:
		return &hillKernel{adstockKernel: k, maxLag: maxLag, lo: []float64{0}, hi: []float64{0.99}}, ""
	case distlagparams.KernelDelayed:
		return &hillKernel{adstockKernel: k, maxLag: maxLag, lo: []float64{0.01, 0}, hi: []float64{0.99, float64(maxLag)}}, ""
	}
	return &hillKernel{adstockKernel: k, maxLag: maxLag, lo: []float64{hillShapeMin, hillScaleMin}, hi: []float64{hillShapeMax, float64(maxLag + 1)}}, ""
}

// The Weibull kernels' boxes (the scale's top is max_lag + 1).
const (
	hillShapeMin, hillShapeMax = 0.1, 10
	hillScaleMin               = 0.1
)

// hillKernelNames are the kernels REG_MMM_HILL fits in-model: every
// WIN_DISTRIBUTED_LAG kernel.
func hillKernelNames() []string { return distlagparams.Kernels() }

// start is the kernel's start at decay d: the decay; the delayed
// kernel's peak one lag on (min(1, max_lag)); a Weibull kernel at shape
// 1 — where both are geometric in the lag (the survival e^(−l/scale),
// the density e^(−(l+1)/scale)/scale) — with the scale whose per-lag
// ratio e^(−1/scale) is d, −1/log d, held to its box.
func (k *hillKernel) start(d float64) []float64 {
	switch k.Name() {
	case distlagparams.KernelDelayed:
		return []float64{d, math.Min(1, float64(k.maxLag))}
	case distlagparams.KernelWeibullCDF, distlagparams.KernelWeibullPDF:
		return []float64{1, math.Min(math.Max(-1/detmath.Log(d), k.lo[1]), k.hi[1])}
	}
	return []float64{d}
}

// prefixes are the kernel's coefficient prefixes, in Params' order.
func (k *hillKernel) prefixes() []string {
	out := make([]string, 0, len(k.Params()))
	for _, p := range k.Params() {
		out = append(out, p.prefix)
	}
	return out
}

// hillAdstockConfig reads params.adstock / params.max_lag into the
// model's kernel (nil when params.adstock is absent), refusing an
// unknown kernel, a max_lag below 1 or above the window's ceiling, a
// max_lag without params.adstock, a delayed or Weibull kernel without
// max_lag (only geometric defaults it, to 13), an order.partition (the
// fit reads one market's series) and a params.carryover without
// params.adstock (carryover_unused: nothing carries).
func hillAdstockConfig(spec *types.RegressionSpec, p hillParams) (*hillKernel, error) {
	if p.Adstock == nil {
		if p.MaxLag != nil {
			return nil, configError(spec, "params.max_lag is the in-model adstock's window; set params.adstock, or drop params.max_lag",
				map[string]any{"slot": "params.max_lag", "reason": reasonMaxLagWithoutAdstock})
		}
		if p.Carryover != nil {
			return nil, configError(spec, "params.carryover carries the in-model adstock into scored rows; set params.adstock, or drop params.carryover",
				map[string]any{"slot": "params.carryover", "reason": pmodel.ReasonCarryoverUnused})
		}
		return nil, nil
	}
	maxLag := pmodel.MMMBayesDefaultMaxLag
	if p.MaxLag != nil {
		if *p.MaxLag < 1 || *p.MaxLag > distlagparams.MaxLagCeiling {
			return nil, configError(spec, fmt.Sprintf("params.max_lag must be an integer in [1, %d], got %d", distlagparams.MaxLagCeiling, *p.MaxLag),
				map[string]any{"slot": "params.max_lag", "reason": reasonMaxLagInvalid})
		}
		maxLag = *p.MaxLag
	}
	k, reason := hillKernelFor(*p.Adstock, maxLag)
	if reason != "" {
		return nil, configError(spec, fmt.Sprintf("params.adstock %q is not one of: %s", *p.Adstock, strings.Join(hillKernelNames(), ", ")),
			map[string]any{"slot": "params.adstock", "reason": reason, "adstock": *p.Adstock, "valid": hillKernelNames()})
	}
	if p.MaxLag == nil && k.Name() != distlagparams.KernelGeometric {
		return nil, configError(spec, fmt.Sprintf("params.adstock %q needs params.max_lag (an integer ≥ 1); only geometric defaults it", k.Name()),
			map[string]any{"slot": "params.max_lag", "reason": reasonMaxLagRequired, "adstock": k.Name()})
	}
	if spec.Order != nil && spec.Order.Partition != "" {
		return nil, configError(spec, fmt.Sprintf("order.partition %q splits the rows into series, but params.adstock fits one market's series — drop order.partition", spec.Order.Partition),
			map[string]any{"slot": "order", "reason": reasonOrderPartitionAdstock, "partition": spec.Order.Partition})
	}
	return k, nil
}

// hillAdstock is a fit's in-model adstock state: the kernel, each
// channel's raw spend, and the scratch adstockAt fills.
type hillAdstock struct {
	kernel *hillKernel
	spend  [][]float64   // [channel][row], time order
	da     [][][]float64 // [channel][kernel param][row]: ∂a/∂φ
}

// np is the kernel's parameter count per channel.
func (h *hillAdstock) np() int { return len(h.kernel.Params()) }

// phiIndex is channel c's kernel parameter i in the parameter vector:
// after the θs block, grouped by parameter (every channel's decay, then
// every channel's peak) — the coefficient table's order.
func (p *problem) phiIndex(i, c int) int {
	nc := len(p.a)
	return 1 + 3*nc + len(p.z) + i*nc + c
}

// phi is channel c's kernel parameters at x.
func (p *problem) phi(x []float64, c int) []float64 {
	out := make([]float64, p.ad.np())
	for i := range out {
		out[i] = x[p.phiIndex(i, c)]
	}
	return out
}

// adstockAt writes each channel's adstock of its raw spend at x's kernel
// parameters into p.a and, with grad, each ∂a/∂φ into p.ad.da — every
// sum in lag order with each product barriered (adstockGrad,
// WIN_DISTRIBUTED_LAG's loop).
func (p *problem) adstockAt(x []float64, grad bool) {
	for c, spend := range p.ad.spend {
		w, dw := p.ad.kernel.Weights(p.phi(x, c), p.ad.kernel.maxLag, grad)
		if grad {
			adstockGrad(spend, w, dw, p.a[c], p.ad.da[c])
			continue
		}
		adstock(spend, w, nil, p.a[c], nil)
	}
}

// startAdstock is the in-model adstock's start: per decay of
// hillDecayStarts (every channel at once), the kernel start there and
// the linear start on its adstock; the one with the smallest residual
// sum of squares wins (the first on a tie). A singular start design at
// every decay is the last one's refusal.
func (p *problem) startAdstock(nonneg bool) ([]float64, error) {
	var best []float64
	bestSSR := math.Inf(1)
	var lastErr error
	r := make([]float64, p.rows())
	for _, d := range hillDecayStarts {
		x := make([]float64, p.dim())
		for c := range p.a {
			for i, v := range p.ad.kernel.start(d) {
				x[p.phiIndex(i, c)] = v
			}
		}
		p.adstockAt(x, false)
		lin, err := p.start(nonneg)
		if err != nil {
			lastErr = err
			continue
		}
		copy(x, lin[:1+len(p.a)+len(p.z)])
		p.residuals(r, nil, x)
		ssr := 0.0
		for _, v := range r {
			ssr += float64(v * v)
		}
		if ssr < bestSSR {
			best, bestSSR = x, ssr
		}
	}
	if best == nil {
		if lastErr == nil {
			lastErr = perr.NewCodedError(perr.PROCESSING_REGRESSION_NO_CONVERGE, string(Name)+": no adstock start has a finite residual")
		}
		return nil, lastErr
	}
	return best, nil
}

// initAdstock moves the raw spend Fit read into p.a over to the
// adstock's spend, gives p.a and the derivative scratch their own rows,
// and fixes each channel's k scale at its adstocked spend's mean at
// hillDecayRefStart (before the start scan, so the scale never moves
// with the decay the solver tries).
func (p *problem) initAdstock() {
	n, np := len(p.y), p.ad.np()
	for c := range p.a {
		p.ad.spend[c] = p.a[c]
		p.a[c] = make([]float64, n)
		p.ad.da[c] = make([][]float64, np)
		for i := range np {
			p.ad.da[c][i] = make([]float64, n)
		}
	}
	x := make([]float64, p.dim())
	for c := range p.a {
		for i, v := range p.ad.kernel.start(hillDecayRefStart) {
			x[p.phiIndex(i, c)] = v
		}
	}
	p.adstockAt(x, false)
	for c, a := range p.a {
		sum := 0.0
		for _, v := range a {
			sum += v
		}
		p.kRef[c] = sum / float64(n)
	}
}

// conditionNumber is the 1-norm condition number ‖R‖₁·‖R⁻¹‖₁ of the
// correlation matrix R of the free-parameter covariance (σ² cancels):
// with d_a = √inv_aa, R_ab = inv_ab / (d_a·d_b) and R⁻¹_ab =
// d_a·JTJ_ab·d_b, so no second inversion. Scale-free, so a decay and a
// half-saturation in spend units compare. Every product is barriered and
// the sums run in index order (the same bits on every architecture). A
// non-positive diagonal of the inverse is +Inf.
func conditionNumber(jtj, inv *linalg.Sym) float64 {
	n := jtj.N()
	d := make([]float64, n)
	for a := range n {
		v := inv.At(a, a)
		if !(v > 0) {
			return math.Inf(1)
		}
		d[a] = math.Sqrt(v)
	}
	var normR, normInv float64
	for b := range n {
		var colR, colInv float64
		for a := range n {
			colR += math.Abs(inv.At(a, b) / float64(d[a]*d[b]))
			colInv += math.Abs(float64(float64(d[a]*jtj.At(a, b)) * d[b]))
		}
		normR, normInv = math.Max(normR, colR), math.Max(normInv, colInv)
	}
	return float64(normR * normInv)
}
