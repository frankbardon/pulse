package regmmm

import (
	"fmt"
	"math"
	"strings"

	"github.com/frankbardon/pulse/extend"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// hill_tangent.go is REG_MMM_HILL's tangent plane (U71 E2-S6, #732):
// the gradient of a row's fitted value
//
//	ŷ = β0 + Σ_c β_c·hill(a_c, k_c, s_c) + Σ_j γ_j·z_j
//
// in every coefficient at the fit's optimum, in the coefficient table's
// own units (k and s as fitted, not their log-scale search coordinates;
// the kernel parameters in theirs) — so gᵀ(Vcov/σ̂²)g is the
// Gauss–Newton hat value R reads off nls's gradient, and on the fit's
// own rows the g's stack the fit's own Jacobian (the leverages sum to
// the free parameter count). The engine's BoundInfluence reads it
// (registered through internal/regadapt); the influence admission rule
// is internal/model.CheckFitDesign.
//
//	∂ŷ/∂β0 = 1,  ∂ŷ/∂β_c = hill,  ∂ŷ/∂γ_j = z_j,
//	∂ŷ/∂k_c = β_c·∂hill/∂k,  ∂ŷ/∂s_c = β_c·∂hill/∂s,
//	∂ŷ/∂φ_c = β_c·∂hill/∂a · Σ_l ∂w_l/∂φ·spend_l
//
// the last over a row whose channels are read as their carried adstock
// (model.AdstockLagRow, the engine's carry-over); read as given (no
// lags), ∂a/∂φ is 0. Every product feeding a sum is barriered and the
// transcendentals are detmath's (hillGrad, the kernels' Weights), so
// the same row gives the same bits on every architecture.

// hillTangent is one bound REG_MMM_HILL fit's tangent.
type hillTangent struct {
	keys               []string
	b0                 float64
	channels, controls []string
	b, k, s, g         []float64
	// kernel is the in-model adstock's kernel (nil when the fit reads
	// pre-adstocked channels, or holds no adstock state); dw[c] its
	// weight derivatives at channel c's fitted parameters.
	kernel adstockKernel
	dw     [][][]float64
	// kIdx, sIdx, phiIdx index the keys: channel c's k, s and kernel
	// parameter i.
	kIdx, sIdx []int
	phiIdx     [][]int
}

// HillTangent is REG_MMM_HILL's model.TangentFactory: the tangent of
// the bound fit b, read off its coefficients (a channel is a predictor
// with a k:<name> coefficient, as Scorer reads them) and — when it
// adstocks in-model — its outputs.adstock_state's kernel.
func HillTangent(b *pmodel.Bound) (pmodel.Tangent, error) {
	if b == nil || b.Result == nil {
		return nil, fmt.Errorf("%s tangent: no fit result", Name)
	}
	coefs := b.Result.Coefficients
	t := &hillTangent{b0: coefs[pmodel.InterceptKey]}
	for _, x := range b.Predictors {
		kv, isChannel := coefs[HalfSaturationPrefix+x]
		if !isChannel {
			t.controls = append(t.controls, x)
			t.g = append(t.g, coefs[x])
			continue
		}
		sv, ok := coefs[ShapePrefix+x]
		if !ok {
			return nil, fmt.Errorf("%s tangent: no %s%s coefficient", Name, ShapePrefix, x)
		}
		t.channels = append(t.channels, x)
		t.b, t.k, t.s = append(t.b, coefs[x]), append(t.k, kv), append(t.s, sv)
	}
	t.keys = append(t.keys, pmodel.InterceptKey)
	t.keys = append(t.keys, t.channels...)
	t.keys = append(t.keys, t.controls...)
	for _, c := range t.channels {
		t.kIdx = append(t.kIdx, len(t.keys))
		t.keys = append(t.keys, HalfSaturationPrefix+c)
	}
	for _, c := range t.channels {
		t.sIdx = append(t.sIdx, len(t.keys))
		t.keys = append(t.keys, ShapePrefix+c)
	}
	if !hillAdstocked(coefs) {
		return t, nil
	}
	st, ok, err := pmodel.ReadAdstockState(b.Result.Outputs)
	if err != nil {
		return nil, err
	}
	if !ok {
		// A model file saved before carry-over: no carried row reaches
		// it (carryover_state_missing), so no lag needs the kernel. Its
		// parameters still join the keys (the vcov covers them).
		st = nil
	}
	name := ""
	if st != nil {
		name = st.Kernel
	} else {
		name = kernelFromCoefficients(coefs, t.channels)
	}
	kern := kernelByName(name)
	if kern == nil || name == "" {
		return nil, fmt.Errorf("%s tangent: unknown adstock kernel %q", Name, name)
	}
	t.phiIdx = make([][]int, len(t.channels))
	for _, p := range kern.Params() {
		for c, ch := range t.channels {
			t.phiIdx[c] = append(t.phiIdx[c], len(t.keys))
			t.keys = append(t.keys, p.prefix+ch)
		}
	}
	if st == nil {
		return t, nil
	}
	t.kernel = kern
	t.dw = make([][][]float64, len(t.channels))
	for c, ch := range t.channels {
		phi := make([]float64, len(kern.Params()))
		for i, p := range kern.Params() {
			v, ok := coefs[p.prefix+ch]
			if !ok {
				return nil, fmt.Errorf("%s tangent: no %s%s coefficient", Name, p.prefix, ch)
			}
			phi[i] = v
		}
		_, t.dw[c] = kern.Weights(phi, st.MaxLag, true)
	}
	return t, nil
}

// hillAdstocked reports whether a fit's coefficient table names a
// kernel parameter (params.adstock).
func hillAdstocked(coefs map[string]float64) bool {
	for k := range coefs {
		if strings.HasPrefix(k, pmodel.MMMDecayPrefix) || strings.HasPrefix(k, pmodel.MMMAdstockShapePrefix) {
			return true
		}
	}
	return false
}

// kernelFromCoefficients names the kernel a coefficient table carries
// (its parameter prefixes) for a fit without adstock state: geometric
// (decay:), delayed (decay: and peak:) or a Weibull kernel — whose two
// kernels share their prefixes, so either serves the keys alone.
func kernelFromCoefficients(coefs map[string]float64, channels []string) string {
	if len(channels) == 0 {
		return ""
	}
	ch := channels[0]
	if _, ok := coefs[pmodel.MMMAdstockShapePrefix+ch]; ok {
		return "weibull_cdf"
	}
	if _, ok := coefs[pmodel.MMMPeakPrefix+ch]; ok {
		return "delayed"
	}
	return "geometric"
}

// Keys satisfies model.Tangent.
func (t *hillTangent) Keys() []string { return append([]string(nil), t.keys...) }

// Gradient satisfies model.Tangent: ŷ in hillScorer.Score's order (the
// fit's row score bit for bit) and its gradient.
func (t *hillTangent) Gradient(row pmodel.Row) (float64, []float64, bool) {
	grad := make([]float64, len(t.keys))
	grad[0] = 1
	yhat := t.b0
	lagRow, carried := row.(pmodel.AdstockLagRow)
	for c, name := range t.channels {
		v, ok := finiteValue(row, name)
		if !ok {
			return 0, nil, false
		}
		yhat += float64(t.b[c] * hillCurve(v, t.k[c], t.s[c]))
		h, dha, dhk, dhs := hillGrad(v, t.k[c], t.s[c])
		grad[1+c] = h
		grad[t.kIdx[c]] = float64(t.b[c] * dhk)
		grad[t.sIdx[c]] = float64(t.b[c] * dhs)
		if t.phiIdx == nil || !carried {
			continue
		}
		lags, ok := lagRow.AdstockLags(name)
		if !ok {
			continue
		}
		if t.kernel == nil {
			return 0, nil, false
		}
		dpa := float64(t.b[c] * dha)
		for i, idx := range t.phiIdx[c] {
			da := 0.0
			for l, d := range t.dw[c][i] {
				if l >= len(lags) || math.IsNaN(lags[l]) {
					continue
				}
				da += float64(d * lags[l])
			}
			grad[idx] = float64(dpa * da)
		}
	}
	for j, name := range t.controls {
		v, ok := finiteValue(row, name)
		if !ok {
			return 0, nil, false
		}
		yhat += float64(t.g[j] * v)
		grad[1+len(t.channels)+j] = v
	}
	return yhat, grad, true
}

// finiteValue is row's finite value of name (ok=false when null, NaN or
// ±Inf).
func finiteValue(row pmodel.Row, name string) (float64, bool) {
	v, ok := row.NumericValue(name)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// HillDispersion is the estimated dispersion σ̂² = SSR/(n − p) a
// REG_MMM_HILL fit's classical (tangent-plane) Vcov carries — the very
// σ² it scaled (JᵀJ)⁻¹ by — and true; false under params.prior (a
// penalty: the Vcov is σ²·(JᵀJ + P)⁻¹, so no φ̂·(JᵀJ)⁻¹ dispersion) or
// when the fit reports no deviance / df. The engine adapter attaches it
// as RegressionResult.Dispersion beside the Vcov (vcov: true), the
// figure the influence slots read.
func HillDispersion(spec *types.RegressionSpec, out extend.RegressionOutput) (float64, bool) {
	if pmodel.MMMHillPrior(spec) || out.Fit.Deviance == nil || out.Fit.DF == nil {
		return 0, false
	}
	df := *out.Fit.DF
	if !(df > 0) {
		return 0, false
	}
	return *out.Fit.Deviance / df, true
}
