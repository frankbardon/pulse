// Package regmmm holds Pulse's built-in media-mix regressions, authored
// on the extension contract (extend.Regression) and registered by the
// root package as model regressions (internal/model.ModelRegression):
// REG_MMM_HILL's model, factory and row scorer. The registration's
// declaration (description, params, outputs, purpose) lives with the
// root package's registration and the descriptor's static twin.
package regmmm

import (
	stderrors "errors"
	"fmt"
	"math"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/optimize"
	"github.com/frankbardon/pulse/types"
)

// Name is the media-mix model's regression type.
const Name = types.REG_MMM_HILL

// The curve-parameter coefficient prefixes: channel c's Hill
// half-saturation is the coefficient "k:" + c, its shape "s:" + c
// (pmodel.ModelRegressionTerms is the static catalog). coef("fit",
// "k:tv_ad") reads one; ':' cannot clash with a predictor's own
// coefficient unless a predictor is itself named "k:…" or "s:…", which
// the factory refuses.
const (
	HalfSaturationPrefix = pmodel.MMMHalfSaturationPrefix
	ShapePrefix          = pmodel.MMMShapePrefix
)

// maxIterations caps the Levenberg–Marquardt outer iterations of one
// fit. A fit whose channels all carry an effect converges in about ten
// (about 1 ms on 312 rows); a channel the data barely informs crawls
// along a flat valley to a curve bound, measured at up to ~400
// iterations (~60 ms). Past the cap the slot fails alone with
// PROCESSING_REGRESSION_NO_CONVERGE instead of stalling a sweep.
const maxIterations = 1000

// kRange and sRange bound each channel's curve: k within
// [0.02, 20] × the channel's mean value, s within [0.5, 5]. Inside them
// a curve is a saturation curve the data can inform; outside, k → 0
// with s → 0 turns the curve into a "did it spend" step that soaks up
// noise, and k → ∞ is the linear ridge on which only β/k^s is
// identified. A curve parameter that ends on a bound has a NaN
// standard error.
var (
	kRange = [2]float64{0.02, 20}
	sRange = [2]float64{0.5, 5}
)

// hillPrior is params.prior.
type hillPrior struct {
	Channel   string  `json:"channel"`
	Mean      float64 `json:"mean"`
	Precision float64 `json:"precision"`
}

// hillParams is the params object.
type hillParams struct {
	Channels    []string   `json:"channels"`
	Nonnegative *bool      `json:"nonnegative"`
	Prior       *hillPrior `json:"prior"`
}

// configError is a PROCESSING_CONFIG refusal naming the regression.
func configError(spec *types.RegressionSpec, msg string, extra map[string]any) error {
	details := map[string]any{"regression": spec.Name, "type": string(Name)}
	for k, v := range extra {
		details[k] = v
	}
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG, string(Name)+": "+msg, details)
}

// Factory builds REG_MMM_HILL's model: a buffered joint
// nonlinear least-squares media-mix model,
//
//	y = β0 + Σ_c β_c · hill(a_c, k_c, s_c) + Σ_j γ_j · z_j,
//
// where each a_c is a channel predictor (an already-adstocked spend
// column, ≥ 0) and each z_j a control predictor entering linearly;
// hill(x, k, s) = x^s / (x^s + k^s), the curve of the hill() expression
// function, computed as 1/(1+(k/x)^s) with the same deterministic power.
//
// It minimises ½·Σ_i r_i² + ½·precision·(β_p − mean)² — the optional
// prior is a Gaussian MAP prior on one channel's β, its precision in
// units of the unit-variance residual — jointly over β0, every β_c, γ_j,
// k_c and s_c by optimize.LeastSquares (bounded Levenberg–Marquardt). k
// and s stay positive through k = mean(a_c)·exp(θk), s = exp(θs), bounded
// to k ∈ [0.02, 20]·mean(a_c) and s ∈ [0.5, 5] (kRange, sRange); under
// nonnegative (the default) each β_c is bounded below by 0, so a channel
// the data does not support comes back exactly 0.
//
// The start is fixed and data-derived: k at the channel's mean, s = 1,
// and [β0, β, γ] by least squares on the Hill columns there (a β at or
// below 0 starts at 1e-3 under nonnegative; the prior channel starts at
// its prior mean). The fit is serial, FMA-free and uses deterministic
// exp / log / pow, so the same rows give the same bits on every
// architecture.
//
// The coefficient table is "(intercept)", the channel betas (predictor
// order), the controls (predictor order), then "k:<channel>" for each
// channel, then "s:<channel>" for each channel. Standard errors are the
// Gauss–Newton σ²·(JᵀJ)⁻¹ (σ² = SSR / (n − p), p every parameter),
// mapped to k and s by the delta method. A parameter that ends on a
// bound has a NaN (null) standard error — a β_c held at 0 takes its k_c
// and s_c with it, which the data then cannot inform — and the rest come
// from the inverse over the free parameters (all NaN if that block does
// not invert). Each coefficient with a standard error carries the Wald
// statistic estimate / SE; p-values come from resample (bootstrap or
// jackknife), which refits the model per replicate. Vcov is the same
// covariance, NaN in a held parameter's row and column.
//
// The Scorer rebuilds the prediction from the fit alone (its
// coefficients), so holdout, ATTR_REG_PREDICT and saved models work.
// The Outputs k and s repeat the curve parameters as objects keyed by
// channel.
//
// Data refusals fail the slot alone: n ≤ 1 + 3·channels + controls rows
// (PROCESSING_REGRESSION_INSUFFICIENT_DATA), a channel that never spends
// or a singular start design (PROCESSING_REGRESSION_RANK_DEFICIENT), no
// convergence within the iteration cap
// (PROCESSING_REGRESSION_NO_CONVERGE). A negative channel value is
// PROCESSING_CONFIG (the column is not spend).
func Factory(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
	var p hillParams
	if len(spec.Params) > 0 {
		// Strict: a prior key typo is refused, never ignored.
		if err := paramread.DecodeStrict(paramread.Regression, string(Name), spec.Params, &p); err != nil {
			return nil, configError(spec, "params: "+err.Error(), map[string]any{"slot": "params"})
		}
	} else {
		paramread.Declare(paramread.Regression, string(Name), &p)
	}
	if len(p.Channels) == 0 {
		return nil, configError(spec, "params.channels needs at least one channel", map[string]any{"slot": "params.channels"})
	}
	m := &model{target: spec.Target, nonneg: p.Nonnegative == nil || *p.Nonnegative}
	isChannel := map[string]bool{}
	for _, c := range p.Channels {
		if !slices.Contains(spec.Predictors, c) || isChannel[c] {
			return nil, configError(spec, fmt.Sprintf("params.channels: %q is not a predictor, or is listed twice", c),
				map[string]any{"slot": "params.channels", "channel": c})
		}
		isChannel[c] = true
	}
	for _, x := range spec.Predictors {
		if len(x) >= 2 && (x[:2] == HalfSaturationPrefix || x[:2] == ShapePrefix) {
			return nil, configError(spec, fmt.Sprintf("predictor %q collides with the k:/s: curve coefficients", x),
				map[string]any{"slot": "predictors", "predictor": x})
		}
		if isChannel[x] {
			m.channels = append(m.channels, x)
		} else {
			m.controls = append(m.controls, x)
		}
	}
	if p.Prior != nil {
		pr := *p.Prior
		if !slices.Contains(m.channels, pr.Channel) || math.IsNaN(pr.Mean) || math.IsInf(pr.Mean, 0) || !(pr.Precision >= 0) || math.IsInf(pr.Precision, 0) {
			return nil, configError(spec, fmt.Sprintf("params.prior: channel %q must be a channel, mean finite, precision finite and ≥ 0", pr.Channel),
				map[string]any{"slot": "params.prior"})
		}
		m.prior = &pr
	}
	return m, nil
}

// SpecRefusal is Factory's refusal of spec, or nil when Factory builds:
// every check Factory makes reads the spec alone (params decode,
// channels, the k:/s: name collision, the prior), so predict raises the
// very error the runtime does (internal/descriptor's model-regression
// declaration calls it).
func SpecRefusal(spec *types.RegressionSpec) *perr.CodedError {
	_, err := Factory(spec, nil)
	if err == nil {
		return nil
	}
	var ce *perr.CodedError
	if stderrors.As(err, &ce) {
		return ce
	}
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG, err.Error(), map[string]any{"type": string(Name)})
}

// model is one slot's buffered fit.
type model struct {
	target             string
	channels, controls []string
	nonneg             bool
	prior              *hillPrior
}

// hillCurve is x^s/(x^s+k^s) for x ≥ 0, written 1/(1+(k/x)^s) — the
// hill() expression function's formula and power, bit for bit.
func hillCurve(x, k, s float64) float64 {
	return 1 / (1 + detmath.Pow(k/x, s))
}

// problem is the least-squares objective over the parameter vector
// x = [β0, β_c…, γ_j…, θk_c…, θs_c…] (k_c = kRef_c·exp θk_c,
// s_c = exp θs_c) — the coefficient table's order.
type problem struct {
	y        []float64
	a        [][]float64 // channel columns
	z        [][]float64 // control columns
	kRef     []float64   // per-channel k scale: the channel mean
	priorIdx int         // channel under the prior, -1 none
	prior    hillPrior
}

func (p *problem) dim() int { return 1 + 3*len(p.a) + len(p.z) }

// curve is (β, k, s, γ) at x.
func (p *problem) curve(x []float64) (b, k, s, g []float64) {
	nc, nz := len(p.a), len(p.z)
	b, g = x[1:1+nc], x[1+nc:1+nc+nz]
	k, s = make([]float64, nc), make([]float64, nc)
	for c := range nc {
		k[c] = float64(p.kRef[c] * detmath.Exp(x[1+nc+nz+c]))
		s[c] = detmath.Exp(x[1+2*nc+nz+c])
	}
	return b, k, s, g
}

// residuals fills r (one per row, plus the prior pseudo-residual
// √precision·(β_p − mean) when a prior is set) at x and, when jac is
// non-nil, the Jacobian rows. The objective is ½·Σ r².
func (p *problem) residuals(r []float64, jac [][]float64, x []float64) {
	nc, nz := len(p.a), len(p.z)
	b, k, s, g := p.curve(x)
	for i, yi := range p.y {
		pred := x[0]
		var row []float64
		if jac != nil {
			row = jac[i]
			clear(row)
			row[0] = 1
		}
		for c := range nc {
			h := hillCurve(p.a[c][i], k[c], s[c])
			pred += float64(b[c] * h)
			if row == nil {
				continue
			}
			row[1+c] = h
			if h > 0 && h < 1 {
				hh := float64(h * (1 - h))
				bs := float64(b[c] * s[c])
				// dh/dθk = −s·h(1−h); dh/dθs = s·ln(x/k)·h(1−h).
				row[1+nc+nz+c] = -float64(bs * hh)
				row[1+2*nc+nz+c] = float64(bs * float64(detmath.Log(p.a[c][i]/k[c])*hh))
			}
		}
		for j := range nz {
			pred += float64(g[j] * p.z[j][i])
			if row != nil {
				row[1+nc+j] = p.z[j][i]
			}
		}
		r[i] = pred - yi
	}
	if p.priorIdx < 0 {
		return
	}
	last, sq := len(p.y), math.Sqrt(p.prior.Precision)
	r[last] = float64(sq * (b[p.priorIdx] - p.prior.Mean))
	if jac != nil {
		clear(jac[last])
		jac[last][1+p.priorIdx] = sq
	}
}

// Fit reads the admitted rows once, then fits.
func (m *model) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	n, nc, nz := rows.Len(), len(m.channels), len(m.controls)
	prob := &problem{y: make([]float64, n), a: make([][]float64, nc), z: make([][]float64, nz),
		kRef: make([]float64, nc), priorIdx: -1}
	dim := prob.dim()
	if n <= dim {
		return extend.RegressionOutput{}, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			fmt.Sprintf("%s needs more than %d rows (1 + 3·channels + controls), got %d", Name, dim, n),
			map[string]any{"n": n, "required": dim + 1})
	}
	for c := range prob.a {
		prob.a[c] = make([]float64, n)
	}
	for j := range prob.z {
		prob.z[j] = make([]float64, n)
	}
	for i := range n {
		rec := rows.At(i)
		prob.y[i], _ = rec.NumericValue(m.target)
		for c, name := range m.channels {
			v, _ := rec.NumericValue(name)
			if v < 0 {
				return extend.RegressionOutput{}, perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG,
					fmt.Sprintf("%s: channel %q has a negative value (%v); a channel is adstocked spend, ≥ 0", Name, name, v),
					map[string]any{"type": string(Name), "channel": name})
			}
			prob.a[c][i] = v
			prob.kRef[c] += v
		}
		for j, name := range m.controls {
			prob.z[j][i], _ = rec.NumericValue(name)
		}
	}
	for c := range prob.kRef {
		prob.kRef[c] /= float64(n)
		if !(prob.kRef[c] > 0) {
			return extend.RegressionOutput{}, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_RANK_DEFICIENT,
				fmt.Sprintf("%s: channel %q never spends", Name, m.channels[c]), map[string]any{"channel": m.channels[c]})
		}
	}
	if m.prior != nil {
		prob.priorIdx = slices.Index(m.channels, m.prior.Channel)
		prob.prior = *m.prior
	}

	x0, err := prob.start(m.nonneg)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	res, err := prob.solve(x0, m.nonneg, maxIterations)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	return m.output(prob, res), nil
}

// solve runs optimize.LeastSquares from x0, β_c ≥ 0 under nonneg; an
// iteration-cap stop is PROCESSING_REGRESSION_NO_CONVERGE (the slot
// fails alone).
func (p *problem) solve(x0 []float64, nonneg bool, maxIter int) (*optimize.LeastSquaresResult, error) {
	m, dim := len(p.y), p.dim()
	if p.priorIdx >= 0 {
		m++
	}
	r, scratch := make([]float64, m), make([]float64, m)
	jac := make([][]float64, m)
	for i := range jac {
		jac[i] = make([]float64, dim)
	}
	lower, upper := make([]float64, dim), make([]float64, dim)
	for i := range lower {
		lower[i], upper[i] = math.Inf(-1), math.Inf(1)
	}
	nc, nz := len(p.a), len(p.z)
	for c := range nc {
		if nonneg {
			lower[1+c] = 0
		}
		lower[1+nc+nz+c], upper[1+nc+nz+c] = detmath.Log(kRange[0]), detmath.Log(kRange[1])
		lower[1+2*nc+nz+c], upper[1+2*nc+nz+c] = detmath.Log(sRange[0]), detmath.Log(sRange[1])
	}
	res, err := optimize.LeastSquares(
		func(x []float64) []float64 { p.residuals(r, nil, x); return r },
		func(x []float64) [][]float64 { p.residuals(scratch, jac, x); return jac },
		x0, lower, upper, optimize.LeastSquaresOptions{MaxIter: maxIter})
	switch {
	case perr.HasCode(err, perr.PULSE_OPTIMIZE_NOT_CONVERGED):
		return nil, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_NO_CONVERGE,
			fmt.Sprintf("%s did not converge in %d iterations (a channel the data cannot identify?)", Name, res.Iterations),
			map[string]any{"iterations": res.Iterations})
	case perr.HasCode(err, perr.PULSE_OPTIMIZE_INVALID):
		// A non-finite residual at the start or a non-finite Jacobian:
		// the data, not the request (an extreme spend scale).
		return nil, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_NO_CONVERGE,
			fmt.Sprintf("%s: the solver met a non-finite residual or Jacobian: %v", Name, err), nil)
	case err != nil:
		return nil, err
	}
	return res, nil
}

// start is the fixed starting vector: θk = θs = 0 (k at the channel
// mean, s = 1) and [β0, β, γ] by least squares on the Hill columns
// there; under nonneg a β ≤ 0 starts at 1e-3, the prior channel at its
// prior mean (when its precision is positive).
func (p *problem) start(nonneg bool) ([]float64, error) {
	nc, nz := len(p.a), len(p.z)
	cols := make([][]float64, 0, 1+nc+nz)
	ones := make([]float64, len(p.y))
	for i := range ones {
		ones[i] = 1
	}
	cols = append(cols, ones)
	for c := range nc {
		h := make([]float64, len(p.y))
		for i, v := range p.a[c] {
			h[i] = hillCurve(v, p.kRef[c], 1)
		}
		cols = append(cols, h)
	}
	cols = append(cols, p.z...)
	coef, err := normalSolve(cols, p.y)
	if err != nil {
		return nil, err
	}
	x := make([]float64, p.dim())
	copy(x, coef) // β0, β, γ; θk = θs = 0
	for c := range nc {
		if c == p.priorIdx && p.prior.Precision > 0 {
			x[1+c] = p.prior.Mean
		}
		if nonneg && !(x[1+c] > 0) {
			x[1+c] = 1e-3
		}
	}
	return x, nil
}

// normalSolve solves the normal equations of y on cols through
// linalg.SolveSPD.
func normalSolve(cols [][]float64, y []float64) ([]float64, error) {
	p := len(cols)
	packed := make([]float64, 0, p*(p+1)/2)
	rhs := make([]float64, p)
	for r := range p {
		for c := r; c < p; c++ {
			v := 0.0
			for i := range y {
				v += float64(cols[r][i] * cols[c][i])
			}
			packed = append(packed, v)
		}
		for i := range y {
			rhs[r] += float64(cols[r][i] * y[i])
		}
	}
	a, err := linalg.NewSym(p, packed)
	if err != nil {
		return nil, err
	}
	out, err := linalg.SolveSPD(a, linalg.NewVec(rhs))
	if err != nil {
		return nil, perr.NewCodedError(perr.PROCESSING_REGRESSION_RANK_DEFICIENT,
			string(Name)+": the start design is singular (a constant or collinear predictor)")
	}
	return out.Slice(), nil
}

// coefNames is the coefficient table's names, in x's order.
func (m *model) coefNames() []string {
	names := make([]string, 0, 1+3*len(m.channels)+len(m.controls))
	names = append(names, "(intercept)")
	names = append(names, m.channels...)
	names = append(names, m.controls...)
	for _, c := range m.channels {
		names = append(names, HalfSaturationPrefix+c)
	}
	for _, c := range m.channels {
		names = append(names, ShapePrefix+c)
	}
	return names
}

// output maps the solution onto the regression output.
func (m *model) output(prob *problem, res *optimize.LeastSquaresResult) extend.RegressionOutput {
	n, nc, nz, dim := len(prob.y), len(m.channels), len(m.controls), prob.dim()
	b, k, s, g := prob.curve(res.X)
	est := slices.Clone(res.X)
	copy(est[1+nc+nz:], k)
	copy(est[1+2*nc+nz:], s)

	var ssr, my, sst float64
	for i, yi := range prob.y {
		pred := res.X[0]
		for c := range nc {
			pred += float64(b[c] * hillCurve(prob.a[c][i], k[c], s[c]))
		}
		for j := range nz {
			pred += float64(g[j] * prob.z[j][i])
		}
		d := yi - pred
		ssr += float64(d * d)
		my += yi
	}
	my /= float64(n)
	for _, yi := range prob.y {
		d := yi - my
		sst += float64(d * d)
	}
	df := float64(n - dim)
	vcov := prob.covariance(res, ssr/df, k, s)

	names := m.coefNames()
	coefs := make([]extend.RegressionCoefficient, dim)
	for i, name := range names {
		coefs[i] = extend.RegressionCoefficient{Name: name, Estimate: est[i]}
		if se := math.Sqrt(vcov[i][i]); !math.IsNaN(se) {
			coefs[i].StdErr = extend.Figure(se)
			coefs[i].Statistic = extend.Figure(est[i] / se)
		} else {
			coefs[i].StdErr = extend.Figure(math.NaN())
		}
	}
	ks, ss := map[string]any{}, map[string]any{}
	for c, name := range m.channels {
		ks[name], ss[name] = k[c], s[c]
	}
	return extend.RegressionOutput{
		Coefficients: coefs,
		Vcov:         vcov,
		Fit: extend.RegressionFit{
			R2:             extend.Figure(1 - ssr/sst),
			ResidualStdErr: extend.Figure(math.Sqrt(ssr / df)),
			DF:             extend.Figure(df),
			Deviance:       extend.Figure(ssr),
			Iterations:     res.Iterations,
		},
		Outputs: map[string]any{"k": ks, "s": ss},
	}
}

// covariance is σ²·(JᵀJ)⁻¹ over the free parameters, in coefficient
// units (the θk / θs rows scaled by k / s, the delta method), NaN in
// the row and column of a held β_c and of its k_c and s_c. A free block
// that does not invert leaves every entry NaN.
func (p *problem) covariance(res *optimize.LeastSquaresResult, sigma2 float64, k, s []float64) [][]float64 {
	nc, nz, dim := len(p.a), len(p.z), p.dim()
	scale := make([]float64, dim)
	for i := range scale {
		scale[i] = 1
	}
	copy(scale[1+nc+nz:], k)
	copy(scale[1+2*nc+nz:], s)
	held := make([]bool, dim)
	for i, a := range res.Active {
		held[i] = a != optimize.NotActive
	}
	for c := range nc {
		if held[1+c] {
			held[1+nc+nz+c], held[1+2*nc+nz+c] = true, true
		}
	}
	var free []int
	for i := range dim {
		if !held[i] {
			free = append(free, i)
		}
	}
	out := make([][]float64, dim)
	for i := range out {
		out[i] = make([]float64, dim)
		for j := range out[i] {
			out[i][j] = math.NaN()
		}
	}
	if len(free) == 0 || !(sigma2 >= 0) {
		return out
	}
	packed := make([]float64, 0, len(free)*(len(free)+1)/2)
	for a, i := range free {
		for _, j := range free[a:] {
			packed = append(packed, res.JTJ.At(i, j))
		}
	}
	sym, err := linalg.NewSym(len(free), packed)
	if err != nil {
		return out
	}
	inv, err := linalg.InverseSPD(sym)
	if err != nil {
		return out
	}
	for a, i := range free {
		for c := a; c < len(free); c++ {
			j := free[c]
			out[i][j] = float64(float64(sigma2*inv.At(a, c))*scale[i]) * scale[j]
			out[j][i] = out[i][j]
		}
	}
	return out
}

// hillScorer scores β0 + Σ β_c·hill(a_c, k_c, s_c) + Σ γ_j·z_j.
type hillScorer struct {
	b0                 float64
	channels, controls []string
	b, k, s, g         []float64
}

func (h hillScorer) Columns() []string { return append(slices.Clone(h.channels), h.controls...) }

func (h hillScorer) Score(row extend.ScoreRow) (float64, bool) {
	out := h.b0
	for c, name := range h.channels {
		v, ok := row.NumericValue(name)
		if !ok {
			return 0, false
		}
		out += float64(h.b[c] * hillCurve(v, h.k[c], h.s[c]))
	}
	for j, name := range h.controls {
		v, ok := row.NumericValue(name)
		if !ok {
			return 0, false
		}
		out += float64(h.g[j] * v)
	}
	return out, true
}

// Scorer is the registration's Scorer: a predictor with a k:<name>
// coefficient is a channel, any other a control. It reads only the
// fit's coefficients, so a saved model scores from its file alone.
func Scorer(fit extend.FittedRegression) (extend.RowScorer, error) {
	sc := hillScorer{b0: fit.Coefficients["(intercept)"]}
	for _, x := range fit.Predictors {
		kv, isChannel := fit.Coefficients[HalfSaturationPrefix+x]
		if !isChannel {
			sc.controls = append(sc.controls, x)
			sc.g = append(sc.g, fit.Coefficients[x])
			continue
		}
		sv, ok := fit.Coefficients[ShapePrefix+x]
		if !ok {
			return nil, fmt.Errorf("%s scorer: no %s%s coefficient", Name, ShapePrefix, x)
		}
		sc.channels = append(sc.channels, x)
		sc.b, sc.k, sc.s = append(sc.b, fit.Coefficients[x]), append(sc.k, kv), append(sc.s, sv)
	}
	return sc, nil
}
