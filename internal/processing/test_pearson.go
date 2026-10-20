package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// pearsonRRow implements TEST_PEARSON_R as a streaming row test on the
// linear correlation between two numeric fields. State extends the
// Welford recurrence with a cross-product accumulator so the
// correlation coefficient is exact to float64 precision on
// well-conditioned inputs:
//
//	delta_x = x − mean_x
//	mean_x += delta_x / n
//	M2_x   += delta_x · (x − mean_x)
//	delta_y = y − mean_y
//	mean_y += delta_y / n
//	M2_y   += delta_y · (y − mean_y)
//	C      += delta_x · (y − mean_y)
//
// Then r = C / √(M2_x · M2_y); the t-statistic
// t = r · √((n−2) / (1−r²)) with df = n−2 drives the two-sided
// p-value through statdist.StudentTTwoSidedP. Confidence interval bounds use
// Fisher's z-transform with SE = 1/√(n−3).
type pearsonRRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field  string
	field2 string
	alpha  float64

	m pearsonMoments

	// testWeight: weighted, the moments are Σw-weighted (r is
	// scale-free, identical across the kinds) and t / df / the Fisher
	// CI read N* (df = N* − 2).
	testWeight
}

// pearsonMoments is the bivariate weighted Welford–West state: the raw
// row count, Σw, Σw², the weighted means, M2_x / M2_y (Σw·(x − x̄)²)
// and the co-moment C = Σw·(x − x̄)(y − ȳ). Every weight is 1
// unweighted, which reproduces the unweighted recurrence bit for bit.
type pearsonMoments struct {
	n                         int64
	sumW, sumWSq              float64
	meanX, meanY, m2X, m2Y, c float64
}

// add folds one (x, y) pair with weight w in the operation-order
// exactness form (w·δ)/Σw, (w·δ)·δ'.
func (m *pearsonMoments) add(x, y, w float64) {
	m.n++
	m.sumW += w
	m.sumWSq += w * w
	wdx := w * (x - m.meanX)
	m.meanX += wdx / m.sumW
	m.m2X += wdx * (x - m.meanX)
	wdy := w * (y - m.meanY)
	m.meanY += wdy / m.sumW
	m.m2Y += wdy * (y - m.meanY)
	// Cross-product uses the *original* deltaX and the *updated* deltaY
	// (matches Welford's numerically stable two-pass form for covariance).
	m.c += wdx * (y - m.meanY)
}

func newPearsonRRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if cerr := fieldaccept.RequiredSlotsRefusal(spec.Type, false, fieldaccept.TestSlotsOf(spec)); cerr != nil {
		return nil, cerr
	}
	alpha := spec.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	if alpha <= 0 || alpha >= 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INVALID_ALPHA,
			fmt.Sprintf("alpha %g not in (0, 1)", spec.Alpha),
			map[string]any{"alpha": spec.Alpha})
	}
	if schema != nil {
		if cerr := testFieldTypeRefusal(spec, schema); cerr != nil {
			return nil, cerr
		}
	}
	return &pearsonRRow{
		spec:       spec,
		schema:     schema,
		field:      spec.Field,
		field2:     spec.Field2,
		alpha:      alpha,
		testWeight: newTestWeight(spec),
	}, nil
}

func (p *pearsonRRow) UpdateRow(record *Record) error {
	x, xOk := record.NumericValue(p.field)
	if !xOk {
		return nil
	}
	y, yOk := record.NumericValue(p.field2)
	if !yOk {
		return nil
	}
	w, ok := p.rowWeight(record)
	if !ok {
		return nil
	}
	p.m.add(x, y, w)
	return nil
}

func (p *pearsonRRow) Finalize() (*types.TestResult, error) {
	defer p.reset()
	return finalizePearsonR(p.spec, "pearson", p.m, p.alpha, &p.testWeight)
}

func (p *pearsonRRow) reset() {
	p.m = pearsonMoments{}
}

// pearsonRPost implements TEST_PEARSON_R as a tier-2 post-test on the
// materialized result row set. Field / Field2 reference columns produced
// by upstream stages (aggregator labels, attribute labels, window
// outputs, grouper keys). The Welford cross-product recurrence is
// identical to the tier-1 variant; only the row source differs.
//
// Use cases: correlate per-group aggregates (e.g. AGG_SUM_revenue vs
// AGG_AVERAGE_basket_size across regions), windowed moving averages
// against one another, or any pair of numeric result columns. Note:
// correlation across per-group summaries is *ecological* — it answers a
// different question than the raw-row correlation and can disagree
// markedly. The Variant field is set to "pearson_post" so consumers can
// tell the two apart.
type pearsonRPost struct {
	spec   *types.Test
	field  string
	field2 string
	alpha  float64
}

func newPearsonRPost(spec *types.Test, _ *encoding.Schema) (PostTest, error) {
	if cerr := fieldaccept.RequiredSlotsRefusal(spec.Type, true, fieldaccept.TestSlotsOf(spec)); cerr != nil {
		return nil, cerr
	}
	alpha := spec.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	if alpha <= 0 || alpha >= 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INVALID_ALPHA,
			fmt.Sprintf("alpha %g not in (0, 1)", spec.Alpha),
			map[string]any{"alpha": spec.Alpha})
	}
	return &pearsonRPost{
		spec:   spec,
		field:  spec.Field,
		field2: spec.Field2,
		alpha:  alpha,
	}, nil
}

func (p *pearsonRPost) Run(rows []map[string]any) (*types.TestResult, error) {
	var m pearsonMoments
	for i, row := range rows {
		if postRowHasNull(row, p.field, p.field2) {
			continue
		}
		x, err := floatFromRow(row, p.field, i)
		if err != nil {
			return nil, err
		}
		y, err := floatFromRow(row, p.field2, i)
		if err != nil {
			return nil, err
		}
		m.add(x, y, 1)
	}
	return finalizePearsonR(p.spec, "pearson_post", m, p.alpha, nil)
}

// finalizePearsonR reduces Welford moments to a TestResult. Shared
// between tier-1 (row stream) and tier-2 (post-pipeline rows, tw nil:
// unweighted). Weighted, t, df and the Fisher CI read N* and the
// variances / covariance are on w*.
func finalizePearsonR(spec *types.Test, variant string, m pearsonMoments, alpha float64, tw *testWeight) (*types.TestResult, error) {
	n, meanX, meanY, m2X, m2Y, c := m.n, m.meanX, m.meanY, m.m2X, m.m2Y, m.c
	basis := weighting.Unweighted
	if tw != nil {
		basis = tw.basis
	}
	nStar := basis.NStar(m.sumW, m.sumWSq)
	// scaled maps a Σw-weighted second moment onto w* (c = N*/Σw; no
	// multiply unless probability).
	scaled := func(x float64) float64 {
		if basis != weighting.Probability {
			return x
		}
		return basis.Scale(m.sumW, m.sumWSq) * x
	}
	if basis.FloorN(int64(n), m.sumW) < 3 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_PEARSON_R requires n ≥ 3, got %d", n),
			map[string]any{"n": n, "min_required": 3})
	}
	// Probability weights: Kish n_eff can sit in [1, 2] while n ≥ 3,
	// leaving df = N* − 2 ≤ 0 and an undefined t / p — refused like
	// TEST_ANOVA_F's N* ≤ k (U12 review WS-12). Never under frequency
	// or unweighted, where N* ≥ n ≥ 3.
	if df := nStar - 2; !(df > 0) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_PEARSON_R: effective sample size n_eff = %.4g leaves df = n_eff − 2 ≤ 0; the test needs n_eff > 2", nStar),
			map[string]any{"n": n, "n_eff": nStar, "df": df, "min_required": 3})
	}
	denom := math.Sqrt(m2X * m2Y)
	if denom == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_CORRELATION_UNDEFINED,
			"TEST_PEARSON_R: at least one column has zero variance; r is undefined",
			map[string]any{"n": n, "m2_x": m2X, "m2_y": m2Y})
	}
	r := c / denom
	if r > 1 {
		r = 1
	} else if r < -1 {
		r = -1
	}
	df := nStar - 2
	// The interval's row floor reads Σw under frequency (Basis.FloorN).
	inf := correlationInference(r, df, nStar-3, basis.FloorN(int64(n), m.sumW) >= 4 && nStar > 3, alpha)
	t, pvalue := inf.T, inf.P
	ciLow, ciHigh := inf.Lo, inf.Hi
	if !inf.CI {
		// The test's historical form: an undefined interval collapses
		// to r (the matrix auxiliaries leave it null instead).
		ciLow, ciHigh = r, r
	}
	res := &types.TestResult{
		Label:      testLabel(spec),
		Type:       types.TEST_PEARSON_R,
		Variant:    variant,
		Statistic:  r,
		DF:         df,
		PValue:     pvalue,
		Alpha:      alpha,
		RejectNull: pvalue < alpha,
		Details: map[string]any{
			"n":          n,
			"t":          t,
			"ci_low":     ciLow,
			"ci_high":    ciHigh,
			"mean_x":     meanX,
			"mean_y":     meanY,
			"variance_x": scaled(m2X) / (nStar - 1),
			"variance_y": scaled(m2Y) / (nStar - 1),
			"covariance": scaled(c) / (nStar - 1),
		},
	}
	if tw != nil {
		tw.noteScalar(res.Details, m.sumW, m.sumWSq)
		tw.checkNEff(spec, false, "", weighting.KishNEff(m.sumW, m.sumWSq), 3)
	}
	return res, nil
}

// corrInference is one correlation's inference: the t statistic, its
// two-sided Student-t p and the Fisher-z confidence bounds (CI false —
// and Lo / Hi NaN — when the interval is undefined).
type corrInference struct {
	T, P   float64
	Lo, Hi float64
	CI     bool
}

// correlationInference is the ONE (partial) correlation inference
// arithmetic TEST_PEARSON_R and the MAT_CORRELATION /
// MAT_PARTIAL_CORRELATION auxiliaries share: t = r·√(df/(1 − r²)),
// p = statdist.StudentTTwoSidedP(t, df) (|r| = 1: t = ±Inf, p = 0), and,
// when ciOK, the Fisher-z interval tanh(atanh(r) ∓ z_{α/2}·se) with
// se = 1/√zDF (|r| = 1: the degenerate [r, r], the formula's limit and
// R cor.test's). df and zDF are the caller's: N* − 2 and N* − 3 for a
// plain r, N* − 2 − g and N* − 3 − g for a partial r over g controls.
// The caller guarantees df > 0.
func correlationInference(r, df, zDF float64, ciOK bool, alpha float64) corrInference {
	out := corrInference{Lo: math.NaN(), Hi: math.NaN()}
	if r == 1 || r == -1 {
		out.T = math.Inf(int(math.Copysign(1, r)))
		out.P = 0
	} else {
		out.T = r * math.Sqrt(df/(1-r*r))
		out.P = statdist.StudentTTwoSidedP(out.T, df)
	}
	if !ciOK {
		return out
	}
	out.CI = true
	if math.Abs(r) >= 1 {
		out.Lo, out.Hi = r, r
		return out
	}
	zr := math.Atanh(r)
	seZ := 1.0 / math.Sqrt(zDF)
	zCrit := normalCriticalTwoSided(alpha)
	out.Lo = math.Tanh(zr - zCrit*seZ)
	out.Hi = math.Tanh(zr + zCrit*seZ)
	return out
}
