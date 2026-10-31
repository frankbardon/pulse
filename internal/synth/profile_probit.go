package synth

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// ModelLinkProbit marks a captured FieldModel whose coefficients are
// PROBIT coefficients: P(target = 1 | row) = Φ(intercept + Σ coefficient
// · x). `profile create --fit-models` fits it for every packed_bool
// target, whose generation draw is a probit (copula.go's bernoulli Q).
const ModelLinkProbit = "probit"

// probitEtaBound clamps the linear predictor where Φ and its tails are
// evaluated. Φ is computed from detmath.Erf (portable, FMA-free), whose
// 1 + erf(x) form loses relative accuracy deep in the lower tail; at
// |η| = 7 a row contributes Φ ≈ 1e-12 to the likelihood, so the clamp
// costs nothing a 10,000-row snapshot can resolve.
const probitEtaBound = 7

// probitMaxIter and probitTol bound the Fisher-scoring loop. A fit that
// has not converged by then is refused rather than shipped with
// coefficients still running away (see fitProbit on separation).
const (
	probitMaxIter = 100
	probitTol     = 1e-9
)

// probitFitResult is one converged probit fit.
type probitFitResult struct {
	beta []float64
	// loglik and loglik0 are the fitted and intercept-only
	// log-likelihoods; 1 − loglik/loglik0 is McFadden's pseudo-R².
	loglik, loglik0 float64
}

// probitPdfCdf returns φ(η) and Φ(η) at the clamped predictor.
func probitPdfCdf(eta float64) (pdf, cdf float64) {
	if eta > probitEtaBound {
		eta = probitEtaBound
	} else if eta < -probitEtaBound {
		eta = -probitEtaBound
	}
	pdf = detmath.Exp(-0.5*float64(eta*eta)) / math.Sqrt(2*math.Pi)
	cdf = phi(eta)
	return pdf, cdf
}

// probitLoglik is Σ y·log Φ(η) + (1 − y)·log(1 − Φ(η)) − ridge/2 · Σ_{j≥1} β_j².
func probitLoglik(x, y []float64, k int, beta []float64, ridge float64) (float64, []float64) {
	n := len(y)
	eta := make([]float64, n)
	var ll float64
	for i := 0; i < n; i++ {
		row := x[i*k : (i+1)*k]
		var e float64
		for j := 0; j < k; j++ {
			e += float64(row[j] * beta[j])
		}
		eta[i] = e
		_, cdf := probitPdfCdf(e)
		if y[i] > 0.5 {
			ll += detmath.Log(cdf)
		} else {
			ll += detmath.Log(1 - cdf)
		}
	}
	var pen float64
	for j := 1; j < k; j++ {
		pen += float64(beta[j] * beta[j])
	}
	return ll - float64(0.5*float64(ridge*pen)), eta
}

// fitProbit maximises the probit log-likelihood of y (0/1) on the rows
// of x (n × k, column 0 the intercept's constant 1) by Fisher scoring
// with step halving, under an optional ridge penalty on every non-
// intercept coefficient (the thin-level shrinkage the linear fit uses,
// at the same ridge = alpha·n pseudo-observation scale).
//
// The whole loop is FMA-free — every product is an explicit
// float64(a*b) and Φ, φ and log come from linalg/detmath — so the fitted
// coefficients, and the profile document carrying them, are the same
// bits on every architecture.
//
// ok is false when the target does not vary, the information matrix is
// singular, or the fit does not converge.
//
// SEPARATION — a predictor that decides the target on some rows — has no
// finite MLE, and the ±probitEtaBound clamp is what bounds it: the rows
// the predictor decides are driven to the clamp, where they stop pulling.
// Either the fit settles there, a near-step whose decided rows draw at
// P ≈ Φ(±7), i.e. the observed 0 or 1 (a flag that is a band of a
// count), or it never settles and is refused, and the capture keeps the
// linear fit with a warning. Both outcomes are deterministic.
func fitProbit(x, y []float64, k int, ridge float64) (probitFitResult, bool) {
	n := len(y)
	if n < 2 || k < 1 {
		return probitFitResult{}, false
	}
	var ones float64
	for _, v := range y {
		ones += v
	}
	ybar := ones / float64(n)
	if ybar <= 0 || ybar >= 1 {
		return probitFitResult{}, false
	}
	beta := make([]float64, k)
	beta[0] = phiInv(ybar)
	ll0, _ := probitLoglik(x, y, k, beta, 0)

	ll, eta := probitLoglik(x, y, k, beta, ridge)
	info := make([]float64, k*k)
	score := make([]float64, k)
	for iter := 0; iter < probitMaxIter; iter++ {
		for j := range info {
			info[j] = 0
		}
		for j := range score {
			score[j] = 0
		}
		for i := 0; i < n; i++ {
			pdf, cdf := probitPdfCdf(eta[i])
			q := 1 - cdf
			if cdf <= 0 || q <= 0 {
				continue
			}
			// Fisher weight φ²/(Φ(1−Φ)) and score φ(y−Φ)/(Φ(1−Φ)).
			den := float64(cdf * q)
			w := float64(pdf*pdf) / den
			g := float64(pdf*(y[i]-cdf)) / den
			row := x[i*k : (i+1)*k]
			for a := 0; a < k; a++ {
				ra := row[a]
				if ra == 0 {
					continue
				}
				score[a] += float64(g * ra)
				wa := float64(w * ra)
				for b := 0; b <= a; b++ {
					info[a*k+b] += float64(wa * row[b])
				}
			}
		}
		for a := 1; a < k; a++ {
			score[a] -= float64(ridge * beta[a])
			info[a*k+a] += ridge
		}
		for a := 0; a < k; a++ {
			for b := 0; b < a; b++ {
				info[b*k+a] = info[a*k+b]
			}
		}
		step, ok := choleskySolve(info, score, k)
		if !ok {
			return probitFitResult{}, false
		}
		next := make([]float64, k)
		var nextLL float64
		var nextEta []float64
		accepted := false
		for half := 0; half < 30; half++ {
			for j := 0; j < k; j++ {
				next[j] = beta[j] + step[j]
			}
			nextLL, nextEta = probitLoglik(x, y, k, next, ridge)
			if !math.IsNaN(nextLL) && nextLL >= ll-float64(1e-12*math.Abs(ll)) {
				accepted = true
				break
			}
			for j := range step {
				step[j] = 0.5 * step[j]
			}
		}
		if !accepted {
			return probitFitResult{}, false
		}
		var maxStep, maxBeta float64
		for j := 0; j < k; j++ {
			maxStep = math.Max(maxStep, math.Abs(next[j]-beta[j]))
			maxBeta = math.Max(maxBeta, math.Abs(next[j]))
		}
		beta, ll, eta = next, nextLL, nextEta
		if maxStep <= float64(probitTol*(1+maxBeta)) {
			raw, _ := probitLoglik(x, y, k, beta, 0)
			return probitFitResult{beta: beta, loglik: raw, loglik0: ll0}, true
		}
	}
	return probitFitResult{}, false
}

// choleskySolve solves A·x = b for a symmetric positive-definite A
// (k × k, row-major), FMA-free. ok is false when A is not positive
// definite.
func choleskySolve(a, b []float64, k int) ([]float64, bool) {
	l := make([]float64, k*k)
	for i := 0; i < k; i++ {
		for j := 0; j <= i; j++ {
			s := a[i*k+j]
			for p := 0; p < j; p++ {
				s -= float64(l[i*k+p] * l[j*k+p])
			}
			if i == j {
				if !(s > 0) {
					return nil, false
				}
				l[i*k+i] = math.Sqrt(s)
			} else {
				l[i*k+j] = s / l[j*k+j]
			}
		}
	}
	z := make([]float64, k)
	for i := 0; i < k; i++ {
		s := b[i]
		for p := 0; p < i; p++ {
			s -= float64(l[i*k+p] * z[p])
		}
		z[i] = s / l[i*k+i]
	}
	out := make([]float64, k)
	for i := k - 1; i >= 0; i-- {
		s := z[i]
		for p := i + 1; p < k; p++ {
			s -= float64(l[p*k+i] * out[p])
		}
		out[i] = s / l[i*k+i]
	}
	return out, true
}

// gatherProbitRow appends the row currently bound to the fit's Record
// adapter to its probit design, under the linear engine's own listwise
// rule: a null target or predictor source drops the row.
func (f *fieldFit) gatherProbitRow() {
	y, ok := f.rec.NumericValue(f.target)
	if !ok {
		return
	}
	row := make([]float64, 0, len(f.columns)+1)
	row = append(row, 1)
	for i := range f.columns {
		x, ok := f.rec.NumericValue(f.columns[i].Name)
		if !ok {
			return
		}
		row = append(row, x)
	}
	f.px = append(f.px, row...)
	if y > 0.5 {
		f.py = append(f.py, 1)
	} else {
		f.py = append(f.py, 0)
	}
}

// probitModel fits the probit over the gathered design and renders it
// as the target's FieldModel. ok is false when the fit is refused
// (probitFailure says why); the caller then keeps the linear fit.
func (f *fieldFit) probitModel(nObs int) (*FieldModel, bool) {
	k := len(f.columns) + 1
	res, ok := fitProbit(f.px, f.py, k, float64(f.alpha*float64(len(f.py))))
	f.px, f.py = nil, nil
	if !ok {
		f.probitFailure = "the probit fit did not converge (a predictor separates the target, or the design is singular)"
		return nil, false
	}
	r2 := 0.0
	if res.loglik0 < 0 {
		r2 = 1 - res.loglik/res.loglik0
	}
	model := &FieldModel{
		Field:          f.target,
		Intercept:      res.beta[0],
		Predictors:     make([]ModelPredictor, 0, len(f.columns)),
		References:     f.references,
		NObs:           nObs,
		R2:             r2,
		ResidualStd:    1,
		ShrinkageAlpha: f.alpha,
		Link:           ModelLinkProbit,
	}
	for i, c := range f.columns {
		model.Predictors = append(model.Predictors, ModelPredictor{
			Column:      c.Name,
			Kind:        modelPredictorKind(c.Kind),
			Field:       c.Field,
			Level:       c.Level,
			Coefficient: res.beta[i+1],
		})
	}
	return model, true
}

// probitSpecScale translates a captured probit model onto the composed
// draw generation already runs for a bernoulli target,
//
//	value = 1  iff  (prediction − p)/std + (residual_std/std)·z > Φ⁻¹(1 − p),
//
// with p the target's prevalence and std = √(p(1−p)) (fieldMoments).
// Choosing
//
//	intercept' = p + std·(a + Φ⁻¹(1 − p)),  coefficient' = std·b,  residual_std' = std
//
// makes that inequality a + b·x + z > 0, i.e. P(1 | row) = Φ(a + b·x):
// the captured probit exactly. Generation and the fidelity recovery
// read the spec through the same moments, so neither needs a probit arm
// of its own.
func probitSpecScale(a, p float64) (scale, intercept, residualStd float64, ok bool) {
	if !(p > 0 && p < 1) {
		return 0, 0, 0, false
	}
	std := math.Sqrt(float64(p * (1 - p)))
	return std, p + float64(std*(a+phiInv(1-p))), std, true
}

// probitFallbackWarning reports a packed_bool target that kept its
// linear-probability fit because the probit fit was refused. The model
// still generates — a linear fit read through the bernoulli draw is the
// pre-probit behaviour — but its effects are attenuated, so it is an
// attention item rather than an expected outcome.
func probitFallbackWarning(field, reason string) string {
	return fmt.Sprintf("model for numeric field %q kept its linear fit: %s", field, reason)
}
