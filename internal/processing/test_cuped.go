package processing

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Covariate adjustment (CUPED, U48) of the two-group tests — TEST_T
// with split_by, TEST_WELCH, TEST_Z_TWO_SAMPLE and TEST_PROP_Z (y the
// success indicator; test_propz.go reads it by the Wald form) — under
// params.covariate. ONE pass: each split_by group folds the outcome y
// and the covariate x into a bivariate weighted Welford bucket
// (weighting.CoWelford: the moment sufficient statistics n, Σy, Σx,
// Σxy, Σx², Σy² in centred form). The buckets merge exactly (Chan), so
// the state streams and is mergeable; Finalize reads, pooled over both
// groups,
//
//	θ  = Cov(x, y) / Var(x) = CXY / M2X    (the pooled slope)
//	x̄  = the pooled covariate mean
//
// and adjusts each group a's moments in closed form —
//
//	mean′_a = ȳ_a − θ·(x̄_a − x̄)
//	M2′_a   = M2Y_a − 2θ·CXY_a + θ²·M2X_a
//
// — which are exactly the moments of y′ = y − θ·(x − x̄) within the
// group, so the test then runs unchanged on them: diff, its interval,
// the statistic, p and Cohen's d are the two-step's (θ estimated, then
// the test on y′; equally the single-call total() formula). Rows with
// a null covariate are dropped listwise (n_covariate_dropped) before
// their weight is judged. A constant covariate (M2X = 0) leaves y
// unadjusted: θ and variance_reduction are then undefined (NaN, null
// on the wire), never 0.
//
// Weighted (both kinds; .claude/reference/weighting.md, Weighted
// inference): the fold carries the row weight w, so θ and x̄ are the
// weighted (WLS) slope and mean on the RAW weights — scale-free, so
// identical under both kinds — and each group's adjusted moments keep
// its Σw / Σw², so the test reads its own N*_g and Kish scale c_g on
// y′ as on any weighted outcome: the weighted two-step. Grouped
// requests mint one instance per bucket, so θ is pooled per partition.

// meanTestParams is the params payload of TEST_WELCH and
// TEST_Z_TWO_SAMPLE: params.reference and params.covariate.
type meanTestParams struct {
	Reference string `json:"reference"`
	Covariate string `json:"covariate"`
}

// decodeMeanTestParams reads a mean test's params (absent = no
// reference, no covariate) and refuses a reference that names no
// split_by level, then an unusable covariate — the refusals predict
// raises, in the same order.
func decodeMeanTestParams(spec *types.Test, schema *encoding.Schema) (meanTestParams, error) {
	var p meanTestParams
	if err := paramread.DecodeOptional(paramread.Test, string(spec.Type), spec.Params, &p); err != nil {
		return p, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(spec.Type)+" params: "+err.Error())
	}
	if cerr := fieldaccept.ReferenceRefusal(string(spec.Type), spec.SplitBy, p.Reference, schema); cerr != nil {
		return p, cerr
	}
	if cerr := fieldaccept.CovariateRefusal(string(spec.Type), spec.Field, spec.SplitBy, p.Covariate, schema); cerr != nil {
		return p, cerr
	}
	return p, nil
}

// cupedArms is a covariate-adjusted mean test's per-group state.
type cupedArms struct {
	covariate string
	groups    map[string]*weighting.CoWelford
	// dropped counts rows with a present outcome and split key whose
	// covariate is null (listwise deletion).
	dropped int64
	// shifted (the mean tests, #637): each group's outcome accumulates
	// y − its own origin (origins, as the unadjusted test's groups do)
	// and the covariate x − one shared origin (xOrigin), so a large
	// common offset on either costs the adjusted difference nothing.
	// TEST_PROP_Z's 0/1 indicator is folded as it is.
	shifted bool
	xOrigin originShift
	origins map[string]*originShift
}

// newCupedArms is nil without a covariate (the unadjusted test).
func newCupedArms(covariate string) *cupedArms {
	if covariate == "" {
		return nil
	}
	return &cupedArms{covariate: covariate, groups: make(map[string]*weighting.CoWelford),
		origins: make(map[string]*originShift)}
}

// newShiftedCupedArms is newCupedArms for a mean test: the outcome and
// the covariate accumulate about origins (cupedArms.shifted).
func newShiftedCupedArms(covariate string) *cupedArms {
	c := newCupedArms(covariate)
	if c != nil {
		c.shifted = true
	}
	return c
}

// origin is key's outcome origin; nil when the arms are unshifted (its
// means are levels already).
func (c *cupedArms) origin(key string) *originShift {
	if !c.shifted {
		return nil
	}
	return c.origins[key]
}

// covariateValue reads the row's covariate, counting a null as a
// listwise drop.
func (c *cupedArms) covariateValue(r *Record) (float64, bool) {
	x, ok := r.NumericValue(c.covariate)
	if !ok {
		c.dropped++
	}
	return x, ok
}

// add folds (x, y) with weight w into key's bucket; isNew reports a
// key seen for the first time.
func (c *cupedArms) add(key string, x, y, w float64) (isNew bool) {
	b, ok := c.groups[key]
	if !ok {
		b = &weighting.CoWelford{}
		c.groups[key] = b
		c.origins[key] = &originShift{}
	}
	if c.shifted {
		x, y = c.xOrigin.of(x), c.origins[key].of(y)
	}
	b.Add(x, y, w)
	return !ok
}

func (c *cupedArms) reset() {
	if c != nil {
		c.groups = make(map[string]*weighting.CoWelford)
		c.origins = make(map[string]*originShift)
		c.xOrigin.reset()
		c.dropped = 0
	}
}

// cupedFacts are a covariate adjustment's own figures.
type cupedFacts struct {
	// raw are the two groups' unadjusted outcome buckets (the listwise
	// sample's).
	raw [2]weighting.Welford
	// origins are the raw buckets' outcome origins (nil unshifted).
	origins [2]*originShift
	// theta is the pooled slope, NaN when the covariate is constant.
	theta float64
	// xbar is the pooled covariate mean (a level).
	xbar    float64
	dropped int64
}

// adjust returns the covariate-adjusted outcome buckets of groups a
// and b (the test's order) and the adjustment's figures.
//
// Shifted, each group's outcome is held about its own origin: b's is
// re-expressed about a's before the pooled merge (only the pooled
// co-moment reads the outcome means' gap), and the adjusted buckets
// stay about their own origins, so the caller differences them through
// groupMeans with the origins origin(key) returns.
func (c *cupedArms) adjust(keyA, keyB string) (weighting.Welford, weighting.Welford, cupedFacts) {
	a, b := c.groups[keyA], c.groups[keyB]
	oa, ob := c.origin(keyA), c.origin(keyB)
	pooled := *a
	other := *b
	if oa != nil {
		other.MeanY += ob.origin - oa.origin
	}
	pooled.Merge(other)
	f := cupedFacts{raw: [2]weighting.Welford{a.YWelford(), b.YWelford()}, origins: [2]*originShift{oa, ob},
		theta: math.NaN(), xbar: c.xOrigin.level(pooled.MeanX), dropped: c.dropped}
	xbar := pooled.MeanX
	theta := 0.0 // a constant covariate adjusts nothing (x − x̄ ≡ 0)
	if pooled.M2X > 0 {
		theta = pooled.CXY / pooled.M2X
		f.theta = theta
	}
	adj := func(g *weighting.CoWelford) weighting.Welford {
		w := g.YWelford()
		w.Mean = g.MeanY - float64(theta*(g.MeanX-xbar))
		// Σw·(y′ − ȳ′)² ≥ 0; the clamp absorbs rounding residue when y
		// is (nearly) linear in x.
		w.M2 = math.Max(0, g.M2Y-float64(2*theta*g.CXY)+float64(theta*theta*g.M2X))
		return w
	}
	return adj(a), adj(b), f
}

// rawWelch is the mean tests' unadjusted reading of the listwise
// sample: the raw difference of means and its Welch SE², Σ s²_g/N*_g.
func (f cupedFacts) rawWelch(basis weighting.Basis) (diff, se2 float64) {
	_, _, diff = groupMeans(&f.raw[0], &f.raw[1], f.origins[0], f.origins[1])
	return diff, f.raw[0].Variance(basis)/f.raw[0].NStar(basis) + f.raw[1].Variance(basis)/f.raw[1].NStar(basis)
}

// rawPooledSE2 is rawWelch's SE² under TEST_T params.variance
// "pooled": the Student form on the unadjusted buckets.
func (f cupedFacts) rawPooledSE2(basis weighting.Basis) float64 {
	_, se2, _ := pooledVariance(f.raw[0].Variance(basis), f.raw[0].NStar(basis), f.raw[1].Variance(basis), f.raw[1].NStar(basis))
	return se2
}

// note writes the adjustment's Details: diff_unadjusted (the listwise
// sample's raw difference), theta, covariate_mean, variance_reduction
// (1 − SE²_adjusted / SE²_unadjusted of the difference, both read by
// the test's own SE form: the share of its sampling variance the
// covariate removed; undefined with theta) and n_covariate_dropped.
func (f cupedFacts) note(details map[string]any, diffRaw, seAdj2, seRaw2 float64) {
	details["diff_unadjusted"] = diffRaw
	details["theta"] = f.theta
	details["covariate_mean"] = f.xbar
	reduction := math.NaN()
	if !math.IsNaN(f.theta) && seRaw2 > 0 {
		reduction = 1 - seAdj2/seRaw2
	}
	details["variance_reduction"] = reduction
	details["n_covariate_dropped"] = f.dropped
}
