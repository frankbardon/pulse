package synth

import (
	"fmt"
	"math"
	mrand "math/rand/v2"

	"github.com/frankbardon/pulse/errors"
)

// correlator induces pairwise Pearson correlations between numeric
// fields via a Gaussian-copula construction: draw a correlated
// standard-normal vector u = L*z (L the Cholesky factor of the
// requested correlation matrix, z ~ N(0, I)), map each u_i through the
// standard normal CDF Φ to get p_i = Φ(u_i) ~ Uniform(0,1) — the
// copula's rank-preserving common source of randomness — then apply
// each participating field's own quantile function (inverse CDF)
// Q_i(p_i) (see quantileFor) to draw a value with the field's OWN
// declared marginal shape. This is the textbook Gaussian-copula
// construction: it targets Spearman (rank) correlation exactly, and for
// every distribution this transform supports the realized Pearson
// correlation lands within the established test tolerance of the
// requested rho too — see TestSynth_CorrelationReconstructionWithinTolerance
// and its lognormal sibling.
//
// For the normal case, Q(p) = mean + std*Φ⁻¹(p) — but Φ⁻¹(p_i) == u_i
// exactly by construction (p_i was built AS Φ(u_i)), so the normal
// branch reduces to precisely the pre-copula formula (mean_i +
// std_i*u_i) with no numerical inversion needed; lognormal similarly
// takes u_i directly (exp(mu + sigma*u_i)) rather than recomputing
// Φ⁻¹(Φ(u_i)) — see phi. Only uniform and exponential need the p_i
// value itself.
//
// Because every field this transform can reach carries a known
// closed-form quantile function for its own declared distribution — see
// quantileFor / fieldMoments — the resulting vector carries the
// requested rank-correlation structure while each field's own marginal
// shape (not just its mean/std) survives. Scoped to the distributions
// fieldMoments accepts: normal, uniform, lognormal, exponential, and
// (E4-S1, reachable through the model draw rather than through this
// correlator — see fieldMoments) mixture; poisson (no quantile at all
// for a discrete lattice under this construction) and bernoulli
// (degenerate quantile under a continuous copula draw) are deliberately
// out of scope. See skills/synthetic-data.md (Pairwise correlations).
//
// Why a parametric quantile-function construction and not a rank-based
// empirical copula: a schema-mode spec (`synth from-schema`,
// Spec.Correlations) has no historical sample data to rank against —
// only each field's distribution and its declared params. A
// profile-derived spec (SpecFromProfile) always reconstructs numeric
// fields as `normal`, so both call sites already share the same shape
// of input (a distribution + params); one technique covers both
// without a second code path.
//
// v0 (removed): the original implementation drew a correlated normal
// vector and blended only a small fraction (±5%·std) of it into the
// field's own independently-drawn value. That preserved marginal shape
// almost exactly but induced only a fraction of the requested
// correlation, and its own doc comment flagged this as a "v1
// limitation" that nothing ever came back to fix — no test asserted
// how much of the requested correlation actually survived the blend,
// so the gap went unnoticed.
//
// v1 (removed): replaced v0 with a direct mean_i + std_i*u_i override —
// exact for the requested correlation, but it forced every correlated
// field's marginal to Gaussian regardless of its own declared
// distribution (mean and std survived, skew did not). This (v2)
// Gaussian-copula construction keeps v1's exact correlation targeting
// while restoring each field's own marginal shape.
type correlator struct {
	fieldNames []string
	// quantiles holds each participating field's own quantile function
	// Q_i, closed over that field's distribution-specific params —
	// built once at construction by quantileFor, applied once per row
	// by transform.
	quantiles []quantileFunc
	hasClamp  []bool
	clampMin  []float64
	clampMax  []float64
	// chol is the lower-triangular Cholesky factor of the requested
	// correlation matrix, sized N x N where N = len(fieldNames).
	chol [][]float64
}

// quantileFunc is one field's quantile function (inverse CDF) Q(u, p):
// u is the field's own correlated standard-normal draw (u_i from the
// Cholesky-factor construction), p is Φ(u) — the same draw pushed
// through the standard normal CDF into Uniform(0,1). A quantileFunc
// takes both because normal and lognormal use u directly (Φ⁻¹(p) == u
// by construction — recomputing it through phi/erfinv would be exact
// but pointlessly indirect), while uniform and exponential need p.
type quantileFunc func(u, p float64) float64

// phi returns Φ(x), the standard normal CDF — the first step of the
// Gaussian-copula construction, mapping a correlated standard-normal
// draw to a Uniform(0,1) value every field's own quantile function can
// invert. Implemented via math.Erf (Go stdlib since Go 1.10, no new
// dependency): Φ(x) = 0.5*(1 + erf(x/√2)).
func phi(x float64) float64 {
	return 0.5 * (1 + math.Erf(x/math.Sqrt2))
}

// buildCorrelator builds a correlator from correlations — the SURVIVING
// CorrelationSpec entries after resolveConflicts has pruned any
// participant field already claimed by an earlier drawRow stage (or by a
// captured-shape pre-claim), NOT necessarily the full Spec.Correlations
// list verbatim. Passing an already-filtered list here is what makes a
// partial exclusion from a multi-field correlation matrix work: the
// Cholesky factor is rebuilt from whatever pairs survive, with no
// special-casing needed in this function itself.
//
// # The unmeasured-entry policy: ASSUME AND RECORD
//
// The input is a LIST of pairs and the output needs a MATRIX, so every
// pair among participants that the list does not name has to be filled
// with something. This function fills it with zero — the participants
// are drawn as independent on that pair — and it says so, returning one
// warning naming how many of the matrix's pairs were completed by
// assumption rather than supplied.
//
// The alternative considered and rejected was to REFUSE a matrix
// carrying unmeasured pairs. Two things decide it against refusal.
// First, an incomplete list is the NORMAL input, not a pathological
// one: a spec correlating a with b and b with c has never said anything
// about a and c, and a profile-derived one is capped at
// CorrelationTopK pairs by construction, so refusal would reject
// nearly every real correlation request outright. Second, zero is not
// an arbitrary fill — it is the only completion that adds no structure
// the caller did not ask for, so "assume independence" is the
// conservative reading rather than a convenient one.
//
// What was NOT acceptable is doing it silently, which is what this
// function did before: on a ten-field matrix built from a top-16 pair
// list, 29 of the 45 pairs were asserted independent with nothing
// anywhere saying they had been asserted rather than measured, and
// cholesky's ridge then bent the partly-invented result into
// factorizable shape without comment. Both halves now speak — see the
// ridge warning below. The document side of the same distinction is
// ResidualCorrelationProfile (synth/residual_corr.go), which keeps
// measured-zero and unmeasured structurally apart so a future caller
// can hand this function a matrix that knows which of its zeros were
// real.
func buildCorrelator(correlations []CorrelationSpec, wfs []*writerField) (*correlator, []string, error) {
	if len(correlations) == 0 {
		return nil, nil, nil
	}
	specs := make(map[string]FieldSpec, len(wfs))
	for _, wf := range wfs {
		specs[wf.spec.Name] = wf.spec
	}

	idx := make(map[string]int)
	var names []string
	var quantiles []quantileFunc
	var clampMin, clampMax []float64
	var hasClamp []bool

	ensure := func(name string) error {
		if _, ok := idx[name]; ok {
			return nil
		}
		fs, ok := specs[name]
		if !ok || !isNumericFieldType(fs.Type) {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"correlation references non-numeric field",
				map[string]any{"field": name})
		}
		mean, std, cMin, cMax, clamped, err := fieldMoments(fs)
		if err != nil {
			return err
		}
		q, err := quantileFor(fs, mean, std)
		if err != nil {
			return err
		}
		idx[name] = len(names)
		names = append(names, name)
		quantiles = append(quantiles, q)
		hasClamp = append(hasClamp, clamped)
		clampMin = append(clampMin, cMin)
		clampMax = append(clampMax, cMax)
		return nil
	}

	for _, c := range correlations {
		if err := ensure(c.A); err != nil {
			return nil, nil, err
		}
		if err := ensure(c.B); err != nil {
			return nil, nil, err
		}
	}
	if len(names) < 2 {
		return nil, nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"correlations require at least two numeric fields")
	}

	n := len(names)
	mat := make([][]float64, n)
	// supplied[i][j] records whether the caller actually named the (i, j)
	// pair. It is what separates a zero the caller asked for from a zero
	// this function invented, and it exists purely so the count in the
	// warning below is a real count rather than an estimate.
	supplied := make([][]bool, n)
	for i := range mat {
		mat[i] = make([]float64, n)
		mat[i][i] = 1
		supplied[i] = make([]bool, n)
		supplied[i][i] = true
	}
	for _, c := range correlations {
		i, j := idx[c.A], idx[c.B]
		mat[i][j] = c.Correlation
		mat[j][i] = c.Correlation
		supplied[i][j] = true
		supplied[j][i] = true
	}

	var warnings []string
	assumed := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if !supplied[i][j] {
				assumed++
			}
		}
	}
	if assumed > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"correlation matrix completed by assumption: %d of %d pair(s) among %d correlated field(s) "+
				"were never supplied and are drawn as independent (rho = 0); an absent pair is unknown, "+
				"not known to be uncorrelated",
			assumed, n*(n-1)/2, n))
	}

	chol, ridge, err := cholesky(mat)
	if err != nil {
		return nil, warnings, err
	}
	if ridge > 0 {
		// A matrix that needed regularization is telling the caller
		// something: the pairwise correlations it asked for are not
		// jointly realizable (a = 0.9 b, b = 0.9 c, a = -0.9 c has no
		// joint distribution), so the draw will honour something OTHER
		// than what was requested. Saying which pairs are impossible is
		// beyond a Cholesky factorization; saying that the request was
		// bent, and by how much, is not.
		warnings = append(warnings, fmt.Sprintf(
			"correlation matrix is not positive definite as requested and was ridge-regularized "+
				"(diagonal jitter %g) to factorize: the requested pairwise correlations are not jointly "+
				"consistent, so realized correlations will be pulled toward zero",
			ridge))
	}
	return &correlator{
		fieldNames: names,
		quantiles:  quantiles,
		hasClamp:   hasClamp, clampMin: clampMin, clampMax: clampMax,
		chol: chol,
	}, warnings, nil
}

// transform overwrites row[name] for every participating field with a
// draw from the Gaussian-copula construction described on correlator,
// replacing the field's independently-drawn value outright rather than
// blending a fraction of it in — see the type doc for why that is
// correct here rather than destructive.
func (c *correlator) transform(rng *mrand.Rand, row map[string]any) {
	if c == nil || len(c.fieldNames) == 0 {
		return
	}
	z := make([]float64, len(c.fieldNames))
	for i := range z {
		z[i] = rng.NormFloat64()
	}
	u := make([]float64, len(c.fieldNames))
	for i := 0; i < len(c.fieldNames); i++ {
		sum := 0.0
		for j := 0; j <= i; j++ {
			sum += c.chol[i][j] * z[j]
		}
		u[i] = sum
	}
	for i, name := range c.fieldNames {
		p := phi(u[i])
		v := c.quantiles[i](u[i], p)
		if c.hasClamp[i] {
			if v < c.clampMin[i] {
				v = c.clampMin[i]
			}
			if v > c.clampMax[i] {
				v = c.clampMax[i]
			}
		}
		row[name] = v
	}
}

// fieldMoments returns the analytic mean and standard deviation of a
// FieldSpec's declared distribution, plus an optional clamp range
// (normal's min/max params only — the one distribution among the
// supported set that declares one). Only distributions with EXACT
// moments and a usable quantile function can be driven through the
// copula construction: normal, uniform, lognormal, exponential, and —
// since E4-S1 — mixture. Anything else (weighted_categorical,
// bernoulli, poisson, pareto, regex, monotonic_from, constant,
// uniform_date, ...) refuses with SERVICE_VALIDATION naming the
// distribution rather than silently approximating — the v0 blend's
// "works for any distribution" was really "quietly distorts any
// distribution a little," which this story removes rather than
// preserves under a new name. Mixture joins the set on that same
// standard and not by relaxing it: its moments are exact and its
// quantile is inverted numerically to the last representable bit.
//
// This function has TWO callers and they reach different subsets of it.
// buildCorrelator calls it for every correlation participant; a
// modelled field calls it for its own target (buildModelDrawers,
// synth/model_draw.go). In practice only the second ever sees a
// mixture: resolveConflicts still pre-claims an unmodelled DistMixture
// field before any correlation stage bids, and a MODELLED one is
// reported as not-yet-honoured by the correlation arm, so a mixture
// reaches buildCorrelator through neither path today. Nothing here
// depends on that — the construction is sound for a mixture on either
// path — but do not read a passing correlation suite as evidence the
// correlation half is exercised.
func fieldMoments(fs FieldSpec) (mean, std, clampMin, clampMax float64, hasClamp bool, err error) {
	clampMin, clampMax = math.Inf(-1), math.Inf(1)
	switch fs.Distribution {
	case DistNormal:
		if mean, _, err = paramFloat(fs.Name, fs.Params, "mean", 0); err != nil {
			return
		}
		if std, _, err = paramFloat(fs.Name, fs.Params, "std", 1); err != nil {
			return
		}
		var hasMin, hasMax bool
		if clampMin, hasMin, err = paramFloat(fs.Name, fs.Params, "min", math.Inf(-1)); err != nil {
			return
		}
		if clampMax, hasMax, err = paramFloat(fs.Name, fs.Params, "max", math.Inf(1)); err != nil {
			return
		}
		hasClamp = hasMin || hasMax
	case DistUniform:
		var minV, maxV float64
		if minV, _, err = paramFloat(fs.Name, fs.Params, "min", 0); err != nil {
			return
		}
		if maxV, _, err = paramFloat(fs.Name, fs.Params, "max", 1); err != nil {
			return
		}
		mean = (minV + maxV) / 2
		std = (maxV - minV) / math.Sqrt(12)
	case DistLogNormal:
		var mu, sigma float64
		if mu, _, err = paramFloat(fs.Name, fs.Params, "mu", 0); err != nil {
			return
		}
		if sigma, _, err = paramFloat(fs.Name, fs.Params, "sigma", 1); err != nil {
			return
		}
		mean = math.Exp(mu + sigma*sigma/2)
		std = math.Sqrt((math.Exp(sigma*sigma) - 1) * math.Exp(2*mu+sigma*sigma))
	case DistExponential:
		var lambda float64
		if lambda, _, err = paramFloat(fs.Name, fs.Params, "lambda", 1); err != nil {
			return
		}
		mean = 1 / lambda
		std = 1 / lambda
	case DistMixture:
		// A captured shape (`profile create --fit-shape`) admitted at
		// E4-S1. Its moments are EXACT — the law-of-total-variance form,
		// see mixtureComponents.moments — so this is not the "quietly
		// distorts any distribution a little" widening the refusal below
		// exists to prevent. What a mixture lacks is a closed-form
		// INVERSE, which quantileFor supplies numerically; that is a
		// cost in cycles, not in fidelity.
		//
		// No clamp: DistMixture declares no min/max params (unlike
		// DistNormal), so hasClamp stays false and a modelled mixture
		// target takes its bound from FieldModelSpec.Min/Max instead —
		// the observed range SpecFromProfile carries for exactly this
		// purpose. See buildModelDrawers' clamping note.
		var mc mixtureComponents
		if mc, err = parseMixtureComponents(fs); err != nil {
			return
		}
		mean, std = mc.moments()
	default:
		err = errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: distribution %q does not support pairwise correlation", fs.Name, fs.Distribution),
			map[string]any{"field": fs.Name, "distribution": fs.Distribution})
	}
	return
}

// quantileFor returns fs's own quantile function (inverse CDF) Q,
// closed over that distribution's declared params — the second half of
// the Gaussian-copula construction on correlator, paired with fieldMoments
// (which validates fs.Distribution is one of the five supported here and
// supplies mean/std for the normal case). Only called after fieldMoments
// has already succeeded for fs, so the default branch below is
// unreachable in practice; it still refuses defensively with the same
// SERVICE_VALIDATION shape rather than assuming.
//
// normal and lognormal deliberately ignore the p argument and use u
// directly: Q_normal(p) = mean + std*Φ⁻¹(p) and Q_lognormal(p) =
// exp(mu + sigma*Φ⁻¹(p)), but Φ⁻¹(p) == u exactly by construction (p was
// built as Φ(u) — see phi), so recomputing it would be exact but
// pointlessly indirect. uniform, exponential and mixture have no such
// shortcut and use p.
//
// Every arm but mixture is O(1) closed form. Mixture pays a fixed
// bisection per call (synth/mixture_quantile.go) — the price of letting
// a `--fit-shape` marginal be driven by a linear predictor at all.
func quantileFor(fs FieldSpec, mean, std float64) (quantileFunc, error) {
	switch fs.Distribution {
	case DistNormal:
		return func(u, p float64) float64 {
			return mean + std*u
		}, nil
	case DistUniform:
		minV, _, err := paramFloat(fs.Name, fs.Params, "min", 0)
		if err != nil {
			return nil, err
		}
		maxV, _, err := paramFloat(fs.Name, fs.Params, "max", 1)
		if err != nil {
			return nil, err
		}
		return func(u, p float64) float64 {
			return minV + p*(maxV-minV)
		}, nil
	case DistLogNormal:
		mu, _, err := paramFloat(fs.Name, fs.Params, "mu", 0)
		if err != nil {
			return nil, err
		}
		sigma, _, err := paramFloat(fs.Name, fs.Params, "sigma", 1)
		if err != nil {
			return nil, err
		}
		return func(u, p float64) float64 {
			return math.Exp(mu + sigma*u)
		}, nil
	case DistExponential:
		lambda, _, err := paramFloat(fs.Name, fs.Params, "lambda", 1)
		if err != nil {
			return nil, err
		}
		return func(u, p float64) float64 {
			return -math.Log(1-p) / lambda
		}, nil
	case DistMixture:
		// The one arm with no closed form. A Gaussian mixture's CDF is a
		// weighted sum of erfs and has no elementary inverse, so Q is
		// computed by a fixed-count bisection whose bracket is decided
		// once here rather than per call — see synth/mixture_quantile.go
		// for why the count is fixed and why bisection rather than
		// Newton. Uses p, like uniform and exponential; there is no u
		// shortcut to take.
		mc, err := parseMixtureComponents(fs)
		if err != nil {
			return nil, err
		}
		lo, hi := mc.bracket()
		return func(u, p float64) float64 {
			return mc.quantile(p, lo, hi)
		}, nil
	default:
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: distribution %q does not support pairwise correlation", fs.Name, fs.Distribution),
			map[string]any{"field": fs.Name, "distribution": fs.Distribution})
	}
}

// cholesky returns the lower-triangular factor L such that L L^T = M,
// plus the TOTAL diagonal jitter it had to add to get there (0 when M
// factorized as given). If M is not positive semi-definite (within
// tolerance), a small ridge is added to the diagonal until the
// factorization succeeds. Returns SERVICE_VALIDATION if even the
// maximally ridge-shifted matrix fails.
//
// The ridge is REPORTED rather than merely applied. It is a genuine
// safety net for a matrix whose measured entries are jointly
// inconsistent — a real and common condition, since pairwise
// correlations are estimated independently and nothing forces the
// resulting matrix to be a valid one — but a caller whose request was
// bent into shape has to be able to find that out, and until E3-S1 the
// only trace was in the numbers that came back out. The returned jitter
// is the accumulated shift, not the last increment, so it reads as "how
// far from realizable was this" rather than as an iteration counter.
func cholesky(m [][]float64) ([][]float64, float64, error) {
	n := len(m)
	work := make([][]float64, n)
	for i := range work {
		work[i] = make([]float64, n)
		copy(work[i], m[i])
	}

	total := 0.0
	for ridge := 0; ridge < 8; ridge++ {
		L, ok := tryCholesky(work)
		if ok {
			return L, total, nil
		}
		// Add a small jitter on the diagonal and retry.
		jitter := math.Pow(10, float64(ridge-6)) // 1e-6 .. 1e-1
		for i := 0; i < n; i++ {
			work[i][i] += jitter
		}
		total += jitter
	}
	return nil, total, errors.NewCodedError(errors.SERVICE_VALIDATION,
		"correlation matrix is not positive semi-definite even after ridge regularization")
}

func tryCholesky(m [][]float64) ([][]float64, bool) {
	n := len(m)
	L := make([][]float64, n)
	for i := range L {
		L[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := m[i][j]
			for k := 0; k < j; k++ {
				sum -= L[i][k] * L[j][k]
			}
			if i == j {
				if sum <= 0 {
					return nil, false
				}
				L[i][j] = math.Sqrt(sum)
			} else {
				L[i][j] = sum / L[j][j]
			}
		}
	}
	return L, true
}

// isNumericFieldType reports whether a spec-string type is a numeric
// type the copula code can blend.
func isNumericFieldType(typeName string) bool {
	switch typeName {
	case "u8", "u16", "u32", "u64", "f32", "f64",
		"nullable_u4", "nullable_u8", "nullable_u16",
		"date":
		return true
	}
	return false
}
