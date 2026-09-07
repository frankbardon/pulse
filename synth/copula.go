package synth

import (
	"fmt"
	"math"
	mrand "math/rand/v2"

	"github.com/frankbardon/pulse/errors"
)

// correlator induces pairwise Pearson correlations between numeric
// fields via a direct conditional-Gaussian construction: draw a
// correlated standard-normal vector u = L*z (L the Cholesky factor of
// the requested correlation matrix, z ~ N(0, I)), then set each
// participating field's value directly to mean_i + std_i*u_i. Because
// every field this transform can reach carries a known analytic
// (mean, std) for its own declared distribution — see fieldMoments —
// the resulting vector is exactly jointly Gaussian with the requested
// correlation matrix: a large draw's sample Pearson correlation
// converges to the target rho with no structural bias baked in, unlike
// the v0 approach this replaces.
//
// Why a parametric (mean, std) construction and not a rank-based
// empirical copula: a schema-mode spec (`synth from-schema`,
// Spec.Correlations) has no historical sample data to rank against —
// only each field's distribution and its declared params. A
// profile-derived spec (SpecFromProfile) always reconstructs numeric
// fields as `normal`, so both call sites already share the same shape
// of input (a distribution + params); one technique covers both
// without a second code path, and it is exact for the shape the
// profile pipeline actually produces — precisely what
// TestSynth_CorrelationReconstructionWithinTolerance and
// TestProfile_ConditionalThenSynth_ReconstructsCorrelation assert.
// Trade-off, stated rather than hidden: a correlated field whose OWN
// marginal is not normal (e.g. a schema-mode `lognormal` field named in
// `correlations`) has its univariate shape pulled toward Gaussian by
// this transform — mean and std survive, skew does not. See
// skills/synthetic-data.md (Pairwise correlations).
//
// v0 (removed): the original implementation drew a correlated normal
// vector and blended only a small fraction (±5%·std) of it into the
// field's own independently-drawn value. That preserved marginal shape
// almost exactly but induced only a fraction of the requested
// correlation, and its own doc comment flagged this as a "v1
// limitation" that nothing ever came back to fix — no test asserted
// how much of the requested correlation actually survived the blend,
// so the gap went unnoticed. This story exists to not repeat that.
type correlator struct {
	fieldNames []string
	means      []float64
	stds       []float64
	hasClamp   []bool
	clampMin   []float64
	clampMax   []float64
	// chol is the lower-triangular Cholesky factor of the requested
	// correlation matrix, sized N x N where N = len(fieldNames).
	chol [][]float64
}

func buildCorrelator(s *Spec, wfs []*writerField) (*correlator, error) {
	if len(s.Correlations) == 0 {
		return nil, nil
	}
	specs := make(map[string]FieldSpec, len(wfs))
	for _, wf := range wfs {
		specs[wf.spec.Name] = wf.spec
	}

	idx := make(map[string]int)
	var names []string
	var means, stds, clampMin, clampMax []float64
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
		idx[name] = len(names)
		names = append(names, name)
		means = append(means, mean)
		stds = append(stds, std)
		hasClamp = append(hasClamp, clamped)
		clampMin = append(clampMin, cMin)
		clampMax = append(clampMax, cMax)
		return nil
	}

	for _, c := range s.Correlations {
		if err := ensure(c.A); err != nil {
			return nil, err
		}
		if err := ensure(c.B); err != nil {
			return nil, err
		}
	}
	if len(names) < 2 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"correlations require at least two numeric fields")
	}

	n := len(names)
	mat := make([][]float64, n)
	for i := range mat {
		mat[i] = make([]float64, n)
		mat[i][i] = 1
	}
	for _, c := range s.Correlations {
		i, j := idx[c.A], idx[c.B]
		mat[i][j] = c.Correlation
		mat[j][i] = c.Correlation
	}
	chol, err := cholesky(mat)
	if err != nil {
		return nil, err
	}
	return &correlator{
		fieldNames: names,
		means:      means, stds: stds,
		hasClamp: hasClamp, clampMin: clampMin, clampMax: clampMax,
		chol: chol,
	}, nil
}

// transform overwrites row[name] for every participating field with a
// draw from the jointly-Gaussian construction described on correlator,
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
		v := c.means[i] + c.stds[i]*u[i]
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
// supported set that declares one). Only distributions with a
// closed-form (mean, std) can participate in a correlation: normal,
// uniform, lognormal, exponential. Anything else (weighted_categorical,
// bernoulli, poisson, pareto, regex, monotonic_from, constant,
// uniform_date, ...) refuses with SERVICE_VALIDATION naming the
// distribution rather than silently approximating — the v0 blend's
// "works for any distribution" was really "quietly distorts any
// distribution a little," which this story removes rather than
// preserves under a new name.
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
	default:
		err = errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: distribution %q does not support pairwise correlation", fs.Name, fs.Distribution),
			map[string]any{"field": fs.Name, "distribution": fs.Distribution})
	}
	return
}

// cholesky returns the lower-triangular factor L such that L L^T = M.
// If M is not positive semi-definite (within tolerance), a small ridge
// is added to the diagonal until the factorization succeeds. Returns
// SERVICE_VALIDATION if even the maximally ridge-shifted matrix fails.
func cholesky(m [][]float64) ([][]float64, error) {
	n := len(m)
	work := make([][]float64, n)
	for i := range work {
		work[i] = make([]float64, n)
		copy(work[i], m[i])
	}

	for ridge := 0; ridge < 8; ridge++ {
		L, ok := tryCholesky(work)
		if ok {
			return L, nil
		}
		// Add a small jitter on the diagonal and retry.
		jitter := math.Pow(10, float64(ridge-6)) // 1e-6 .. 1e-1
		for i := 0; i < n; i++ {
			work[i][i] += jitter
		}
	}
	return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
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
