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
}

// newCupedArms is nil without a covariate (the unadjusted test).
func newCupedArms(covariate string) *cupedArms {
	if covariate == "" {
		return nil
	}
	return &cupedArms{covariate: covariate, groups: make(map[string]*weighting.CoWelford)}
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
	}
	b.Add(x, y, w)
	return !ok
}

func (c *cupedArms) reset() {
	if c != nil {
		c.groups = make(map[string]*weighting.CoWelford)
		c.dropped = 0
	}
}

// cupedFacts are a covariate adjustment's own figures.
type cupedFacts struct {
	// raw are the two groups' unadjusted outcome buckets (the listwise
	// sample's).
	raw [2]weighting.Welford
	// theta is the pooled slope, NaN when the covariate is constant.
	theta float64
	// xbar is the pooled covariate mean.
	xbar    float64
	dropped int64
}

// adjust returns the covariate-adjusted outcome buckets of groups a
// and b (the test's order) and the adjustment's figures.
func (c *cupedArms) adjust(a, b *weighting.CoWelford) (weighting.Welford, weighting.Welford, cupedFacts) {
	pooled := *a
	pooled.Merge(*b)
	f := cupedFacts{raw: [2]weighting.Welford{a.YWelford(), b.YWelford()}, theta: math.NaN(), xbar: pooled.MeanX, dropped: c.dropped}
	theta := 0.0 // a constant covariate adjusts nothing (x − x̄ ≡ 0)
	if pooled.M2X > 0 {
		theta = pooled.CXY / pooled.M2X
		f.theta = theta
	}
	adj := func(g *weighting.CoWelford) weighting.Welford {
		w := g.YWelford()
		w.Mean = g.MeanY - theta*(g.MeanX-f.xbar)
		// Σw·(y′ − ȳ′)² ≥ 0; the clamp absorbs rounding residue when y
		// is (nearly) linear in x.
		w.M2 = math.Max(0, g.M2Y-2*theta*g.CXY+theta*theta*g.M2X)
		return w
	}
	return adj(a), adj(b), f
}

// rawWelch is the mean tests' unadjusted reading of the listwise
// sample: the raw difference of means and its Welch SE², Σ s²_g/N*_g.
func (f cupedFacts) rawWelch(basis weighting.Basis) (diff, se2 float64) {
	return f.raw[0].Mean - f.raw[1].Mean,
		f.raw[0].Variance(basis)/f.raw[0].NStar(basis) + f.raw[1].Variance(basis)/f.raw[1].NStar(basis)
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
