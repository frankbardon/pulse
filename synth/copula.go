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

// phiInv returns Φ⁻¹(p), the standard normal quantile — phi's inverse,
// via math.Erfinv: Φ⁻¹(p) = √2·erf⁻¹(2p−1). Nothing in GENERATION needs
// it (the copula construction starts from a normal draw and never has
// to go back), so it exists solely for latentFor below, which reads a
// generated value back onto the latent scale for E5-S1's model-recovery
// comparison.
//
// p is clipped into (eps, 1−eps) rather than allowed to return ±Inf. A
// generated value can legitimately sit at or beyond its marginal's
// support edge — a clamped draw, or a mixture whose fitted CDF
// saturates in float64 well before the observed maximum — and an
// infinite latent would poison the whole refit's Gram matrix with one
// row rather than costing that row a few thousandths of accuracy. The
// bound is ±7.03 standard deviations, far outside anything a 10,000-row
// sample reaches by chance, so the clip is only ever load-bearing for
// values the inversion genuinely cannot place.
func phiInv(p float64) float64 {
	const eps = 1e-12
	switch {
	case p <= eps:
		p = eps
	case p >= 1-eps:
		p = 1 - eps
	}
	return math.Sqrt2 * math.Erfinv(2*p-1)
}

// latentFunc maps one generated DATA-SCALE value back onto the standard
// normal latent u that produced it, reporting ok=false for a value the
// distribution cannot place at all (a non-positive lognormal draw, a
// negative exponential one). It is the exact inverse of the quantileFunc
// quantileFor returns for the same FieldSpec.
type latentFunc func(v float64) (float64, bool)

// latentFor returns fs's inverse quantile: the function taking a value
// this field's generation stage produced back to the standard-normal
// latent u it came from, so u = latentFor(fs)(quantileFor(fs)(u, phi(u)))
// for every u the field's support admits.
//
// # Why this exists at all
//
// A modelled numeric is drawn as value = Q(Φ(μ + σz)) — see
// synth/model_draw.go's header. The coefficients inside μ are therefore
// LATENT-scale quantities: for a `normal` target the round trip
// collapses and they read as data-scale too, but for a lognormal,
// uniform, exponential or captured-mixture target the map from u to
// value is non-linear and a coefficient moves the value by an amount
// that depends where in the distribution the row landed. Regressing a
// generated cohort's raw VALUES on the same dummies and comparing the
// result to a captured coefficient is therefore comparing a value-space
// effect to a latent one — the trap E5-S1 exists to avoid. Inverting
// each generated value through this function first puts both sides of
// that comparison on one scale, which is what makes an attenuated
// recovery evidence of a GENERATION fault rather than an artefact of
// the marginal's shape. See BuildModelFidelity.
//
// # Lockstep with quantileFor
//
// Every arm here is the algebraic inverse of the matching arm above,
// and the two MUST move together: a distribution admitted to
// quantileFor without an inverse here would silently drop every model
// targeting it out of the recovery section, and one inverted wrongly
// would report a false attenuation for a generation path that is
// correct. TestLatentFor_InvertsQuantileForEveryDistribution round-trips
// all five and fails if either side moves alone.
//
// Only called after fieldMoments has succeeded for fs (so mean/std are
// real and std > 0); the default arm refuses defensively with the same
// SERVICE_VALIDATION shape quantileFor uses rather than assuming.
func latentFor(fs FieldSpec, mean, std float64) (latentFunc, error) {
	switch fs.Distribution {
	case DistNormal:
		// Q(u) = mean + std*u.
		return func(v float64) (float64, bool) {
			return (v - mean) / std, true
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
		width := maxV - minV
		if width <= 0 {
			// A zero-width uniform has no latent to recover: every value
			// is the same one and p is undefined. fieldMoments computes
			// std = width/√12 for this arm, so buildModelDrawers has
			// already refused such a field its model — held defensively.
			return func(float64) (float64, bool) { return 0, false }, nil
		}
		// Q(p) = min + p*(max-min).
		return func(v float64) (float64, bool) {
			return phiInv((v - minV) / width), true
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
		if sigma <= 0 {
			return func(float64) (float64, bool) { return 0, false }, nil
		}
		// Q(u) = exp(mu + sigma*u).
		return func(v float64) (float64, bool) {
			if v <= 0 {
				return 0, false
			}
			return (math.Log(v) - mu) / sigma, true
		}, nil
	case DistExponential:
		lambda, _, err := paramFloat(fs.Name, fs.Params, "lambda", 1)
		if err != nil {
			return nil, err
		}
		if lambda <= 0 {
			return func(float64) (float64, bool) { return 0, false }, nil
		}
		// Q(p) = -log(1-p)/lambda, so p = 1 - exp(-lambda*v). Expm1
		// keeps the small-v end accurate, where 1-exp(-x) cancels.
		return func(v float64) (float64, bool) {
			if v < 0 {
				return 0, false
			}
			return phiInv(-math.Expm1(-lambda * v)), true
		}, nil
	case DistMixture:
		// The mixture's CDF is a closed-form weighted sum of erfs — it
		// is only its INVERSE that needs bisection — so the latent
		// direction is the cheap one here, exactly the reverse of
		// quantileFor's mixture arm.
		mc, err := parseMixtureComponents(fs)
		if err != nil {
			return nil, err
		}
		return func(v float64) (float64, bool) {
			return phiInv(mc.cdf(v)), true
		}, nil
	default:
		// Two different refusals share this branch and the message
		// distinguishes them, because a reader of a fidelity report's
		// `error` slot needs to know which one they are looking at.
		//
		// A distribution fieldMoments does not admit could never have
		// reached a compiled drawer at all — that refusal is purely
		// defensive. A distribution fieldMoments DOES admit but whose Q
		// is not invertible is a real, expected outcome: see
		// latentInvertible.
		if !latentInvertible(fs.Distribution) {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: distribution %q has a step quantile function, so a generated value does not identify the latent that produced it", fs.Name, fs.Distribution),
				map[string]any{"field": fs.Name, "distribution": fs.Distribution})
		}
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: distribution %q has no latent inverse", fs.Name, fs.Distribution),
			map[string]any{"field": fs.Name, "distribution": fs.Distribution})
	}
}

// latentInvertible reports whether a distribution that fieldMoments
// admits ALSO has a usable value -> latent inverse in latentFor.
//
// The two sets were identical until a boolean marginal was admitted, and
// the split is a property of the mathematics rather than a gap waiting
// to be closed. Every INVERTIBLE supported Q is strictly monotone and
// continuous, so a generated value names exactly one latent and the
// inverse is a reparameterisation. The two refusals are the two
// DISCONTINUOUS ones, and they are the same shape at different widths:
// DistBernoulli's Q is a STEP (every latent above the threshold produces
// 1, every latent below it produces 0) and DistDiscrete's is a
// STAIRCASE, of which that step is the two-level case. Either way a
// generated value pins the latent only to an INTERVAL, and no function
// of the value can recover the point inside it.
//
// The tempting move in the boolean case — placing each arm at the
// conditional mean of its half (E[u | u > c] and E[u | u <= c], the
// inverse-Mills construction) — is REJECTED. It returns a two-valued
// "latent", which makes the recovery refit a linear probability model on
// two points dressed up as a latent-scale comparison: it would report a
// large attenuation for a generation path that is exactly correct, which
// the latent round-trip gate's own comment names as worse than reporting
// nothing. The K-level generalisation (the interval-midpoint probit
// score, Phi^-1 of each band's midpoint) is a BETTER estimator than that
// — it is monotone in the value and converges on the exact inverse as
// the levels multiply — but it still reports attenuation for a
// generation path that is exactly right, it degenerates to precisely the
// rejected two-valued construction at K = 2, and choosing per-K would
// leave this function answering "is it identified" with "sort of". So
// both discontinuous arms refuse, uniformly.
//
// So a modelled bernoulli or discrete target gets a fidelity entry
// carrying its captured coefficients and an `error` saying the recovery
// is not identified, rather than a fabricated delta. Recovering an
// ordered-probit coefficient properly needs an ordered-probit refit,
// which processing/regression does not offer; when it does, this is the
// one place that changes.
func latentInvertible(distribution string) bool {
	switch distribution {
	case DistBernoulli, DistDiscrete:
		return false
	}
	return true
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

	chol, warnings, err := factorCorrelations("correlation", "correlated field", names, idx, correlations)
	if err != nil {
		return nil, warnings, err
	}
	return &correlator{
		fieldNames: names,
		quantiles:  quantiles,
		hasClamp:   hasClamp, clampMin: clampMin, clampMax: clampMax,
		chol: chol,
	}, warnings, nil
}

// factorCorrelations turns a LIST of pairs over an ordered participant
// set into the Cholesky factor of the matrix they imply, completing
// every pair the list never named and saying so.
//
// It is the one place the assemble → complete → factorize → report
// sequence exists, shared by both consumers of a correlation structure:
// buildCorrelator, which correlates field VALUES through each field's
// own quantile function, and buildResidualCorrelator
// (synth/residual_draw.go), which correlates the RESIDUALS of modelled
// fields and hands each drawer its own component of the resulting
// vector. There is deliberately no second Cholesky and no second
// completion policy in this package — the two callers differ only in
// what they do with L and in the two nouns they pass in for the warning
// text (subject "correlation" / "residual correlation", participant
// "correlated field" / "modelled field"), so a change to the policy
// cannot land on one scale and miss the other.
//
// See buildCorrelator's own doc for WHY the completion policy is assume
// -and-record rather than refusal; it applies verbatim on both scales,
// and on the residual scale it is if anything more load-bearing, since
// the captured submatrix names its unmeasured pairs explicitly
// (ResidualCorrelationProfile.Unmeasured) and they arrive here as
// absences.
func factorCorrelations(subject, participant string, names []string, idx map[string]int, correlations []CorrelationSpec) ([][]float64, []string, error) {
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
			"%s matrix completed by assumption: %d of %d pair(s) among %d %s(s) "+
				"were never supplied and are drawn as independent (rho = 0); an absent pair is unknown, "+
				"not known to be uncorrelated",
			subject, assumed, n*(n-1)/2, n, participant))
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
			"%s matrix is not positive definite as requested and was ridge-regularized "+
				"(diagonal jitter %g) to factorize: the requested pairwise correlations are not jointly "+
				"consistent, so realized correlations will be pulled toward zero",
			subject, ridge))
	}
	return chol, warnings, nil
}

// correlatedNormals fills u with L*z for a freshly drawn standard normal
// z, the shared first half of both scales' draw: on the value scale the
// components go on to each field's own quantile function
// (correlator.transform), on the residual scale each component IS the
// modelled field's z (residualCorrelator.draw).
//
// z is consumed in COMPONENT ORDER, one rng.NormFloat64() per
// participant, and both callers fix that order at construction time
// from the Spec rather than from a map — the determinism contract rests
// on it. See skills/synthetic-data.md (Determinism).
func correlatedNormals(rng *mrand.Rand, chol [][]float64, z, u []float64) {
	for i := range z {
		z[i] = rng.NormFloat64()
	}
	for i := range u {
		sum := 0.0
		for j := 0; j <= i; j++ {
			sum += float64(chol[i][j] * z[j])
		}
		u[i] = sum
	}
}

// transform overwrites row[name] for every participating field with a
// draw from the Gaussian-copula construction described on correlator,
// replacing the field's independently-drawn value outright rather than
// blending a fraction of it in.
//
// # Why overwriting is right HERE, and what it is not
//
// A participant of this stage is a field with no other account of
// itself: it was drawn a moment earlier from its own marginal sampler,
// that draw carried no structure the correlated one lacks, and
// resolveConflicts has already excluded any field some other stage
// claimed. Replacing the value outright is therefore not destroying
// information — it is the same marginal, redrawn from a shared source
// of randomness so it lands at a rank the matrix asked for.
//
// That reasoning stops at a MODELLED field, and this stage no longer
// reaches one. A modelled numeric already has an account of itself: its
// value is the composed model draw (synth/model_draw.go), and
// overwriting it would delete every predictor's contribution and leave
// the surviving coefficients describing nothing that was drawn. The
// correlation structure among modelled fields is applied instead at the
// place it belongs — as the correlation of their RESIDUALS, supplying
// each drawer's z rather than replacing its output (see
// synth/residual_draw.go). resolveConflicts excludes a modelled field
// from Spec.Correlations for that reason and says so.
func (c *correlator) transform(rng *mrand.Rand, row map[string]any) {
	if c == nil || len(c.fieldNames) == 0 {
		return
	}
	z := make([]float64, len(c.fieldNames))
	u := make([]float64, len(c.fieldNames))
	correlatedNormals(rng, c.chol, z, u)
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
// buildCorrelator calls it for every VALUE-scale correlation
// participant; a modelled field calls it for its own target
// (buildModelDrawers, synth/model_draw.go). In practice only the second
// ever sees a mixture: resolveConflicts pre-claims an unmodelled
// DistMixture field before any correlation stage bids, and a MODELLED
// one is excluded from the value-scale matrix permanently (its
// correlation structure rides its residual instead — see
// synth/residual_draw.go), so a mixture reaches buildCorrelator through
// neither path. Nothing here depends on that — the construction is
// sound for a mixture on either path — but do not read a passing
// correlation suite as evidence the correlation half is exercised.
//
// buildResidualCorrelator is deliberately NOT a third caller: a residual
// correlation needs no marginal at all, only the Cholesky factor, so it
// shares factorCorrelations and stops there.
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
		sigmaSq := float64(sigma * sigma)
		mean = math.Exp(mu + float64(sigmaSq/2))
		std = math.Sqrt(float64((math.Exp(sigmaSq) - 1) * math.Exp(float64(2*mu)+sigmaSq)))
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
	case DistDiscrete:
		// A small-integer marginal: the field's own per-level histogram,
		// captured exactly (SpecFromProfile's discrete arm). Admitted on
		// the same standard as every other arm here — its moments are
		// EXACT closed-form sums over the declared support, not an
		// approximation — and for the same reason bernoulli was: a u4 /
		// u8 / u16 column is a model TARGET on real survey data (the
		// motivating cohort's nps is a u4), and refusing it here would
		// drop the model, which SpecFromProfile's per-target retirement
		// rule has already traded the field's conditional pair away for.
		//
		// Q is a staircase, so the composed draw is an ORDERED PROBIT
		// rather than a linear model in value space; quantileFor's arm
		// carries that argument.
		//
		// No clamp. hasClamp stays false because Q emits only declared
		// levels and there is nothing outside the support for a clamp to
		// pull back — the same relationship bernoulli has with
		// FieldModelSpec.Min/Max, where applying the model's own bound
		// is a no-op by construction.
		var levels discreteLevels
		if levels, err = parseDiscreteLevels(fs); err != nil {
			return
		}
		mean, std = levels.mean, levels.std
		// std is 0 for a single-level support — a constant column. Left
		// unfloored for the same reason bernoulli's is: buildModelDrawers'
		// std <= 0 guard turns it into a dropped model WITH a warning
		// rather than an infinite invStd.
	case DistBernoulli:
		// A boolean marginal. Admitted so that a packed_bool field —
		// which SpecFromProfile reconstructs as bernoulli, because its
		// on-wire value is one bit and a continuous reconstruction
		// cannot round-trip through it (see the type switch there) —
		// can still be the TARGET of a captured linear model. Q is a
		// step function, which makes the composed draw a probit rather
		// than a linear model; quantileFor's arm carries that argument.
		//
		// The moments are exact and closed form, not an approximation:
		// a Bernoulli(p) has mean p and variance p(1-p).
		//
		// No clamp. hasClamp stays false because Q emits exactly 0 or
		// 1 and there is nothing outside the support for a clamp to
		// pull back — unlike DistNormal, whose declared min/max are
		// doing real work. A modelled bernoulli target still carries
		// FieldModelSpec.Min/Max from the observed range; applying it
		// is a no-op, which is the intended relationship.
		var prev float64
		if prev, _, err = paramFloat(fs.Name, fs.Params, "p", 0.5); err != nil {
			return
		}
		if prev < 0 || prev > 1 {
			err = errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: bernoulli p must be in [0, 1]", fs.Name),
				map[string]any{"field": fs.Name, "p": prev})
			return
		}
		mean = prev
		// std is 0 at p == 0 and p == 1. That is the honest answer for
		// a constant field, and buildModelDrawers' std <= 0 guard is
		// what turns it into a dropped model with a warning rather
		// than an infinite invStd — deliberately not floored here, so
		// the degeneracy stays visible to its one caller that cares.
		std = math.Sqrt(float64(prev * (1 - prev)))
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
// (which validates fs.Distribution is one of the kinds supported here and
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
			return mean + float64(std*u)
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
			return minV + float64(p*(maxV-minV))
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
			return math.Exp(mu + float64(sigma*u))
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
	case DistDiscrete:
		// A STAIRCASE Q — the second non-invertible arm, and the general
		// form of which bernoulli's step is the two-level special case.
		//
		// For the field's own independent draw this is exact: p is Phi(u)
		// and u is standard normal across rows, so p is uniform on (0,1)
		// and each level comes out at exactly its declared share. That is
		// the whole reason a small-integer field is reconstructed this way
		// rather than as a clamped normal: the writer rounds, and no
		// continuous marginal survives rounding with its per-level shares
		// intact (measured on a 7-level scale: 0.2526 -> 0.1373 at the
		// modal level, with the MEAN still correct to three digits, which
		// is how it went unnoticed).
		//
		// For a MODELLED target the composed draw becomes an ORDERED
		// PROBIT: the predictors shift the latent, the staircase decides
		// which level the shifted latent lands on, and the marginal is
		// held exactly at the captured histogram. Direction and ordering
		// carry through from the coefficients; magnitude in field units
		// does NOT, and a coefficient here may not be read as "this many
		// points on the scale". Same latent-scale caveat as mixture and
		// bernoulli, on the type family a survey cohort is mostly made of.
		levels, err := parseDiscreteLevels(fs)
		if err != nil {
			return nil, err
		}
		return func(u, p float64) float64 {
			return levels.quantile(p)
		}, nil
	case DistBernoulli:
		// The one arm whose Q is a STEP function, and the one whose
		// composed draw is therefore not a linear model in value space
		// at all: value = Q(Phi(mu + sigma*z)) with this Q is exactly a
		// PROBIT, P(1 | row) = Phi((mu - Phi^-1(1-prev)) / sigma).
		// Direction and ordering carry through from the coefficients;
		// magnitude does not, and no coefficient on a bernoulli target
		// may be read as a change in probability. That is the same
		// latent-scale caveat every non-normal Q carries, in its
		// sharpest form.
		//
		// Q(p) = 1 iff p > 1-prev. Since p is Phi(u) and u is standard
		// normal across rows, p is uniform on (0,1) and P(1) == prev
		// EXACTLY — which is the whole reason a packed_bool target is
		// reconstructed as bernoulli rather than as a clamped normal.
		// A continuous marginal written through a one-bit field cannot
		// preserve its own prevalence: the writer has to threshold, and
		// every threshold over a clamped normal lands in the wrong
		// place (see toBool and the profile type switch).
		//
		// Degenerate p is exact rather than special-cased: prev == 1
		// gives threshold 0 and p > 0 always holds, prev == 0 gives
		// threshold 1 and p > 1 never does.
		prev, _, err := paramFloat(fs.Name, fs.Params, "p", 0.5)
		if err != nil {
			return nil, err
		}
		threshold := 1 - prev
		return func(u, p float64) float64 {
			if p > threshold {
				return 1
			}
			return 0
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
				sum -= float64(L[i][k] * L[j][k])
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
// isBooleanFieldType reports whether a schema type name denotes a
// single-bit boolean column.
//
// `packed_bool` is the only spelling, deliberately: it is what
// encoding.FieldType.String() emits and the only boolean name
// fieldTypeFromName can build, so a spec naming anything else never
// reaches the writer. `nullable_bool` appears in some descriptor
// capability lists as a legacy alias and is NOT accepted here — matching
// it would claim support for a type synth cannot construct.
func isBooleanFieldType(typeName string) bool {
	return typeName == "packed_bool"
}

func isNumericFieldType(typeName string) bool {
	switch typeName {
	case "u8", "u16", "u32", "u64", "f32", "f64",
		"nullable_u4", "nullable_u8", "nullable_u16",
		"date":
		return true
	}
	return false
}
