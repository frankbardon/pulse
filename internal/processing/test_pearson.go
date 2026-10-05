package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PEARSON_R requires field")
	}
	if spec.Field2 == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PEARSON_R requires field2 (second numeric column)")
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
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_PEARSON_R field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.Field2); f != nil && (f.Type.IsCategorical()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD2_NOT_NUMERIC,
				fmt.Sprintf("TEST_PEARSON_R field2 %q has non-numeric type %s", spec.Field2, f.Type.String()),
				map[string]any{"field2": spec.Field2, "field_type": f.Type.String()})
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
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PEARSON_R (post): requires field")
	}
	if spec.Field2 == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PEARSON_R (post): requires field2 (second numeric column)")
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
	if n < 3 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_PEARSON_R requires n ≥ 3, got %d", n),
			map[string]any{"n": n, "min_required": 3})
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
	var t, pvalue float64
	switch {
	case r == 1 || r == -1:
		t = math.Inf(int(math.Copysign(1, r)))
		pvalue = 0
	default:
		t = r * math.Sqrt(df/(1-r*r))
		pvalue = statdist.StudentTTwoSidedP(t, df)
	}
	var ciLow, ciHigh float64
	if n >= 4 && nStar > 3 && math.Abs(r) < 1 {
		zr := math.Atanh(r)
		seZ := 1.0 / math.Sqrt(nStar-3)
		zCrit := math.Sqrt2 * inverseErf(1-alpha)
		ciLow = math.Tanh(zr - zCrit*seZ)
		ciHigh = math.Tanh(zr + zCrit*seZ)
	} else {
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
