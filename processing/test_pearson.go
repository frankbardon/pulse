package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
// p-value through studentTTwoSidedP. Confidence interval bounds use
// Fisher's z-transform with SE = 1/√(n−3).
type pearsonRRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field  string
	field2 string
	alpha  float64

	n     int64
	meanX float64
	meanY float64
	m2X   float64
	m2Y   float64
	c     float64
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
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical() || f.Type.IsGeo()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_PEARSON_R field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.Field2); f != nil && (f.Type.IsCategorical() || f.Type.IsGeo()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD2_NOT_NUMERIC,
				fmt.Sprintf("TEST_PEARSON_R field2 %q has non-numeric type %s", spec.Field2, f.Type.String()),
				map[string]any{"field2": spec.Field2, "field_type": f.Type.String()})
		}
	}
	return &pearsonRRow{
		spec:   spec,
		schema: schema,
		field:  spec.Field,
		field2: spec.Field2,
		alpha:  alpha,
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
	p.n++
	deltaX := x - p.meanX
	p.meanX += deltaX / float64(p.n)
	p.m2X += deltaX * (x - p.meanX)
	deltaY := y - p.meanY
	p.meanY += deltaY / float64(p.n)
	p.m2Y += deltaY * (y - p.meanY)
	// Cross-product uses the *original* deltaX and the *updated* deltaY
	// (matches Welford's numerically stable two-pass form for covariance).
	p.c += deltaX * (y - p.meanY)
	return nil
}

func (p *pearsonRRow) Finalize() (*types.TestResult, error) {
	defer p.reset()
	return finalizePearsonR(p.spec, "pearson", p.n, p.meanX, p.meanY, p.m2X, p.m2Y, p.c, p.alpha)
}

func (p *pearsonRRow) reset() {
	p.n = 0
	p.meanX, p.meanY = 0, 0
	p.m2X, p.m2Y = 0, 0
	p.c = 0
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
	var (
		n                         int64
		meanX, meanY, m2X, m2Y, c float64
	)
	for i, row := range rows {
		x, err := floatFromRow(row, p.field, i)
		if err != nil {
			return nil, err
		}
		y, err := floatFromRow(row, p.field2, i)
		if err != nil {
			return nil, err
		}
		n++
		deltaX := x - meanX
		meanX += deltaX / float64(n)
		m2X += deltaX * (x - meanX)
		deltaY := y - meanY
		meanY += deltaY / float64(n)
		m2Y += deltaY * (y - meanY)
		c += deltaX * (y - meanY)
	}
	return finalizePearsonR(p.spec, "pearson_post", n, meanX, meanY, m2X, m2Y, c, p.alpha)
}

// finalizePearsonR reduces Welford moments to a TestResult. Shared
// between tier-1 (row stream) and tier-2 (post-pipeline rows).
func finalizePearsonR(spec *types.Test, variant string, n int64, meanX, meanY, m2X, m2Y, c, alpha float64) (*types.TestResult, error) {
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
	df := float64(n - 2)
	var t, pvalue float64
	switch {
	case r == 1 || r == -1:
		t = math.Inf(int(math.Copysign(1, r)))
		pvalue = 0
	default:
		t = r * math.Sqrt(df/(1-r*r))
		pvalue = studentTTwoSidedP(t, df)
	}
	var ciLow, ciHigh float64
	if n >= 4 && math.Abs(r) < 1 {
		zr := math.Atanh(r)
		seZ := 1.0 / math.Sqrt(float64(n-3))
		zCrit := math.Sqrt2 * inverseErf(1-alpha)
		ciLow = math.Tanh(zr - zCrit*seZ)
		ciHigh = math.Tanh(zr + zCrit*seZ)
	} else {
		ciLow, ciHigh = r, r
	}
	return &types.TestResult{
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
			"variance_x": m2X / float64(n-1),
			"variance_y": m2Y / float64(n-1),
			"covariance": c / float64(n-1),
		},
	}, nil
}
