package pulse_test

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/optimize"
	"github.com/frankbardon/pulse/types"
)

// extensions_testmmm_hill_test.go is the test-only reference extension
// REG_TESTMMM_HILL (U53 E4-S1, reused by the E4-S2 media-mix pipeline):
// a nonlinear Hill-saturation media-mix model written exactly as an
// embedder would write it — this file imports only Pulse's public
// packages (pulse, extend, encoding, errors, linalg, optimize, types)
// and the standard library.
//
// The model is buffered joint nonlinear least squares,
//
//	y = β0 + Σ_c β_c · hill(a_c, k_c, s_c) + Σ_j γ_j · z_j,
//
// where every a_c is a channel predictor (an already-adstocked spend
// column, WIN_ADSTOCK upstream) and every z_j a control predictor
// entering linearly. hill(x, k, s) = x^s / (x^s + k^s), the same curve
// as the hill() expression function. It minimises
//
//	½·Σ_i r_i² + ½·precision·(β_p − mean)²
//
// — a Gaussian MAP prior on one channel's β (prior.channel), with the
// precision in units of the unit-variance residual — over a
// reparametrised vector, so k_c = κ_c·exp(θ), s_c = exp(θ) stay
// positive and, under nonnegative, β_c = exp(θ) stays non-negative,
// by Pulse's public Levenberg–Marquardt, optimize.LeastSquares (fit),
// from a fixed, data-derived start (k at the channel's mean, s = 1, the
// linear part by least squares through linalg.SolveSPD).
//
// Coefficients are "(intercept)", then each predictor in spec order
// (β_c for a channel, γ_j for a control), with no standard error (null
// on the wire). The declared Outputs k and s are objects keyed by
// channel. The Scorer rebuilds the prediction from the fit alone.

// regTestMMMHill is the reference extension's registered name.
const regTestMMMHill types.RegressionType = "REG_TESTMMM_HILL"

// hillPrior is params.prior: a Gaussian MAP prior on one channel's β.
type hillPrior struct {
	Channel   string  `json:"channel"`
	Mean      float64 `json:"mean"`
	Precision float64 `json:"precision"`
}

// hillParams is the model's params object.
type hillParams struct {
	// Channels are the predictors entering through the Hill curve (at
	// least one, each a predictor); the rest are linear controls.
	Channels    []string   `json:"channels"`
	Nonnegative bool       `json:"nonnegative"`
	Prior       *hillPrior `json:"prior"`
}

// testMMMHillRegistration is REG_TESTMMM_HILL's registration.
func testMMMHillRegistration() pulse.RegressionRegistration {
	return pulse.RegressionRegistration{
		Name:        regTestMMMHill,
		Description: "Test-only media-mix model: joint nonlinear least squares over Hill-saturated channels plus linear controls.",
		Params: []pulse.ParamMeta{
			{Name: "channels", JSONType: "array", Required: true, Description: "Predictors entering through the Hill curve (at least one); the rest are linear controls."},
			{Name: "nonnegative", JSONType: "boolean", Description: "Constrain every channel β to be non-negative (β = exp θ)."},
			{Name: "prior", JSONType: "object", Description: "Gaussian MAP prior {channel, mean, precision} on one channel's β."},
		},
		OutputSchema: []pulse.RegressionOutputMeta{
			{Key: "k", JSONType: "object", Description: "Hill half-saturation point per channel."},
			{Key: "s", JSONType: "object", Description: "Hill shape (slope) per channel."},
		},
		Factory: testMMMHillFactory,
		Scorer:  testMMMHillScorer,
		// The model reports no p-value itself; under resample the engine
		// writes the replicate ones, which the bootstrap test reads.
		PValues: true,
	}
}

func testMMMHillFactory(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
	var p hillParams
	if len(spec.Params) > 0 {
		if err := json.Unmarshal(spec.Params, &p); err != nil {
			return nil, err
		}
	}
	m := &hillModel{target: spec.Target, nonneg: p.Nonnegative, prior: p.Prior}
	if len(p.Channels) == 0 {
		return nil, fmt.Errorf("params.channels: at least one channel is required")
	}
	isChannel := map[string]bool{}
	for _, c := range p.Channels {
		if !slices.Contains(spec.Predictors, c) {
			return nil, fmt.Errorf("params.channels: %q is not a predictor", c)
		}
		isChannel[c] = true
	}
	for _, x := range spec.Predictors {
		if isChannel[x] {
			m.channels = append(m.channels, x)
		} else {
			m.controls = append(m.controls, x)
		}
	}
	m.predictors = spec.Predictors
	if p.Prior != nil {
		if !slices.Contains(m.channels, p.Prior.Channel) || !(p.Prior.Precision >= 0) {
			return nil, fmt.Errorf("params.prior: channel %q must be a channel and precision >= 0", p.Prior.Channel)
		}
	}
	return m, nil
}

// hillModel is one slot's buffered fit.
type hillModel struct {
	target             string
	predictors         []string
	channels, controls []string
	nonneg             bool
	prior              *hillPrior
}

// hillCurve is x^s / (x^s + k^s) for x ≥ 0, written 1/(1+(k/x)^s).
func hillCurve(x, k, s float64) float64 {
	if x <= 0 {
		return 0
	}
	return 1 / (1 + math.Pow(k/x, s))
}

// hillProblem is the objective over the parameter vector
// θ = [β0, (θβ, θk, θs) per channel, γ per control].
type hillProblem struct {
	y        []float64
	a        [][]float64 // channel columns
	z        [][]float64 // control columns
	kRef     []float64   // per-channel k scale (the channel mean)
	nonneg   bool
	priorIdx int // channel index under the prior, -1 none
	prior    hillPrior
}

func (p *hillProblem) beta(th float64) float64 {
	if p.nonneg {
		return math.Exp(th)
	}
	return th
}

func (p *hillProblem) unpack(x []float64) (b0 float64, b, k, s, g []float64) {
	nc := len(p.a)
	b, k, s = make([]float64, nc), make([]float64, nc), make([]float64, nc)
	for c := 0; c < nc; c++ {
		b[c] = p.beta(x[1+3*c])
		k[c] = p.kRef[c] * math.Exp(x[2+3*c])
		s[c] = math.Exp(x[3+3*c])
	}
	return x[0], b, k, s, x[1+3*nc:]
}

// residuals fills r (one entry per row, plus one prior pseudo-residual
// √precision·(β_p − mean) when a prior is set) at x and, when jac is
// non-nil, the Jacobian row by row. The objective is ½·Σ r².
func (p *hillProblem) residuals(r []float64, jac [][]float64, x []float64) {
	b0, b, k, s, g := p.unpack(x)
	nc := len(p.a)
	for i, yi := range p.y {
		pred := b0
		var row []float64
		if jac != nil {
			row = jac[i]
			clear(row)
			row[0] = 1
		}
		for c := 0; c < nc; c++ {
			hc := hillCurve(p.a[c][i], k[c], s[c])
			pred += float64(b[c] * hc)
			if row == nil {
				continue
			}
			dB := hc
			if p.nonneg {
				dB = float64(hc * b[c])
			}
			row[1+3*c] = dB
			if hc > 0 && hc < 1 {
				hh := float64(hc * (1 - hc))
				// dh/dθk = −s·h(1−h); dh/dθs = s·ln(x/k)·h(1−h).
				row[2+3*c] = -float64(float64(b[c]*s[c]) * hh)
				row[3+3*c] = float64(float64(b[c]*s[c]) * float64(math.Log(p.a[c][i]/k[c])*hh))
			}
		}
		for j := range g {
			pred += float64(g[j] * p.z[j][i])
			if row != nil {
				row[1+3*nc+j] = p.z[j][i]
			}
		}
		r[i] = pred - yi
	}
	if p.priorIdx < 0 {
		return
	}
	c, last := p.priorIdx, len(p.y)
	sq := math.Sqrt(p.prior.Precision)
	r[last] = float64(sq * (b[c] - p.prior.Mean))
	if jac != nil {
		row := jac[last]
		clear(row)
		row[1+3*c] = sq
		if p.nonneg {
			row[1+3*c] = float64(sq * b[c])
		}
	}
}

// fit minimises ½·Σ r² from x0 with optimize.LeastSquares, unbounded
// (positivity rides the θ reparametrisation), and returns the iteration
// count. The solver's schedule is fixed and serial, so the same rows
// give the same bits; an iteration-limit stop is
// PROCESSING_REGRESSION_NO_CONVERGE.
func (p *hillProblem) fit(x0 []float64) ([]float64, int, error) {
	m := len(p.y)
	if p.priorIdx >= 0 {
		m++
	}
	r, scratch := make([]float64, m), make([]float64, m)
	jac := make([][]float64, m)
	for i := range jac {
		jac[i] = make([]float64, len(x0))
	}
	res, err := optimize.LeastSquares(
		func(x []float64) []float64 { p.residuals(r, nil, x); return r },
		func(x []float64) [][]float64 { p.residuals(scratch, jac, x); return jac },
		x0, nil, nil, optimize.LeastSquaresOptions{})
	switch {
	case perr.HasCode(err, perr.PULSE_OPTIMIZE_NOT_CONVERGED):
		return nil, res.Iterations, perr.NewCodedError(perr.PROCESSING_REGRESSION_NO_CONVERGE,
			fmt.Sprintf("REG_TESTMMM_HILL did not converge in %d iterations", res.Iterations))
	case err != nil:
		return nil, 0, err
	}
	return res.X, res.Iterations, nil
}

func (m *hillModel) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	n := rows.Len()
	nc, nz := len(m.channels), len(m.controls)
	dim := 1 + 3*nc + nz
	if n <= dim {
		return extend.RegressionOutput{}, perr.NewCodedError(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			fmt.Sprintf("REG_TESTMMM_HILL needs more than %d rows, got %d", dim, n))
	}
	prob := &hillProblem{y: make([]float64, n), a: make([][]float64, nc), z: make([][]float64, nz),
		kRef: make([]float64, nc), nonneg: m.nonneg, priorIdx: -1}
	for c := range prob.a {
		prob.a[c] = make([]float64, n)
	}
	for j := range prob.z {
		prob.z[j] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		rec := rows.At(i)
		prob.y[i], _ = rec.NumericValue(m.target)
		for c, name := range m.channels {
			v, _ := rec.NumericValue(name)
			if v < 0 {
				return extend.RegressionOutput{}, fmt.Errorf("REG_TESTMMM_HILL: channel %q is negative (%v)", name, v)
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
		if prob.kRef[c] <= 0 {
			return extend.RegressionOutput{}, perr.NewCodedError(perr.PROCESSING_REGRESSION_RANK_DEFICIENT,
				fmt.Sprintf("REG_TESTMMM_HILL: channel %q never spends", m.channels[c]))
		}
	}
	if m.prior != nil {
		prob.priorIdx = slices.Index(m.channels, m.prior.Channel)
		prob.prior = *m.prior
	}

	x0, err := prob.start()
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	xHat, iters, err := prob.fit(x0)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	b0, b, k, s, g := prob.unpack(xHat)

	coefs := []extend.RegressionCoefficient{{Name: "(intercept)", Estimate: b0}}
	ks, ss := map[string]any{}, map[string]any{}
	for c, name := range m.channels {
		ks[name], ss[name] = k[c], s[c]
	}
	for _, name := range m.predictors {
		if c := slices.Index(m.channels, name); c >= 0 {
			coefs = append(coefs, extend.RegressionCoefficient{Name: name, Estimate: b[c]})
		} else {
			coefs = append(coefs, extend.RegressionCoefficient{Name: name, Estimate: g[slices.Index(m.controls, name)]})
		}
	}
	var ssr, my, sst float64
	for i, yi := range prob.y {
		my += yi
		pred := b0
		for c := range m.channels {
			pred += b[c] * hillCurve(prob.a[c][i], k[c], s[c])
		}
		for j := range g {
			pred += g[j] * prob.z[j][i]
		}
		ssr += (yi - pred) * (yi - pred)
	}
	my /= float64(n)
	for _, yi := range prob.y {
		sst += (yi - my) * (yi - my)
	}
	return extend.RegressionOutput{
		Coefficients: coefs,
		Fit: extend.RegressionFit{
			R2:             extend.Figure(1 - ssr/sst),
			ResidualStdErr: extend.Figure(math.Sqrt(ssr / float64(n-dim))),
			Deviance:       extend.Figure(ssr),
			Iterations:     iters,
		},
		Outputs: map[string]any{"k": ks, "s": ss},
	}, nil
}

// start is the fixed starting vector: k at the channel mean (θk = 0),
// s = 1 (θs = 0), and the linear part [β0, β, γ] by least squares on
// the Hill columns at that (k, s) — under nonnegative a β at or below
// zero starts at 1e-3, under the prior the prior channel starts at its
// mean.
func (p *hillProblem) start() ([]float64, error) {
	nc, nz := len(p.a), len(p.z)
	cols := make([][]float64, 0, 1+nc+nz)
	ones := make([]float64, len(p.y))
	for i := range ones {
		ones[i] = 1
	}
	cols = append(cols, ones)
	for c := 0; c < nc; c++ {
		h := make([]float64, len(p.y))
		for i, v := range p.a[c] {
			h[i] = hillCurve(v, p.kRef[c], 1)
		}
		cols = append(cols, h)
	}
	cols = append(cols, p.z...)
	coef, err := leastSquares(cols, p.y)
	if err != nil {
		return nil, err
	}
	x := make([]float64, 1+3*nc+nz)
	x[0] = coef[0]
	for c := 0; c < nc; c++ {
		bc := coef[1+c]
		if c == p.priorIdx && p.prior.Precision > 0 {
			bc = p.prior.Mean
		}
		if p.nonneg {
			bc = math.Log(math.Max(bc, 1e-3))
		}
		x[1+3*c] = bc
	}
	copy(x[1+3*nc:], coef[1+nc:])
	return x, nil
}

// leastSquares solves the normal equations of y on cols through
// linalg.SolveSPD.
func leastSquares(cols [][]float64, y []float64) ([]float64, error) {
	p := len(cols)
	packed := make([]float64, 0, p*(p+1)/2)
	rhs := make([]float64, p)
	for r := 0; r < p; r++ {
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
		return nil, perr.NewCodedError(perr.PROCESSING_REGRESSION_RANK_DEFICIENT, "REG_TESTMMM_HILL: the start design is singular")
	}
	return out.Slice(), nil
}

// hillScorer scores β0 + Σ β_c·hill(a_c, k_c, s_c) + Σ γ_j·z_j from a
// fit alone.
type hillScorer struct {
	b0       float64
	channels []string
	b, k, s  []float64
	controls []string
	g        []float64
}

func (h hillScorer) Columns() []string { return append(slices.Clone(h.channels), h.controls...) }

func (h hillScorer) Score(row extend.ScoreRow) (float64, bool) {
	out := h.b0
	for c, name := range h.channels {
		v, ok := row.NumericValue(name)
		if !ok {
			return 0, false
		}
		out += h.b[c] * hillCurve(v, h.k[c], h.s[c])
	}
	for j, name := range h.controls {
		v, ok := row.NumericValue(name)
		if !ok {
			return 0, false
		}
		out += h.g[j] * v
	}
	return out, true
}

// testMMMHillScorer is the registration's Scorer: a predictor carrying
// a k output is a channel, any other a control.
func testMMMHillScorer(fit extend.FittedRegression) (extend.RowScorer, error) {
	ks, _ := fit.Outputs["k"].(map[string]any)
	ss, _ := fit.Outputs["s"].(map[string]any)
	sc := hillScorer{b0: fit.Coefficients["(intercept)"]}
	for _, x := range fit.Predictors {
		kv, isChannel := ks[x].(float64)
		if !isChannel {
			sc.controls = append(sc.controls, x)
			sc.g = append(sc.g, fit.Coefficients[x])
			continue
		}
		sv, ok := ss[x].(float64)
		if !ok {
			return nil, fmt.Errorf("REG_TESTMMM_HILL scorer: no s output for channel %q", x)
		}
		sc.channels = append(sc.channels, x)
		sc.b, sc.k, sc.s = append(sc.b, fit.Coefficients[x]), append(sc.k, kv), append(sc.s, sv)
	}
	return sc, nil
}
