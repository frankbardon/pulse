package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// spearmanRRow implements TEST_SPEARMAN_R as a buffered row test:
// rank-based correlation between Field and Field2 (monotonic
// association). Buffer paired values, mid-rank each column independently,
// then compute Pearson r on the ranks.
//
// p-value via the t-statistic
//
//	t = ρ · √((n−2) / (1−ρ²))   with df = n − 2
//
// driven by statdist.StudentTTwoSidedP. Tie handling is the standard mid-rank
// correction; degenerate edge cases (zero variance in ranks, |ρ|=1)
// match the parametric TEST_PEARSON_R behavior.
//
// Frequency-weighted (ClassFrequencyOnly), each pair stands for w
// identical pairs: weighted mid-ranks per column, the Σw-weighted
// Pearson r on the ranks around the mean rank (N+1)/2 with N = Σw, and
// df = N − 2 — exactly the unweighted test on the expanded pairs.
// Details keep the raw `n` and add `sum_weights`; ties_x / ties_y are
// the expanded tie sizes.
type spearmanRRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field  string
	field2 string
	alpha  float64

	xs rankSample
	ys []float64

	testWeight
}

func newSpearmanRRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" || spec.Field2 == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_SPEARMAN_R requires field and field2")
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
				fmt.Sprintf("TEST_SPEARMAN_R field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.Field2); f != nil && (f.Type.IsCategorical()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD2_NOT_NUMERIC,
				fmt.Sprintf("TEST_SPEARMAN_R field2 %q has non-numeric type %s", spec.Field2, f.Type.String()),
				map[string]any{"field2": spec.Field2, "field_type": f.Type.String()})
		}
	}
	return &spearmanRRow{
		spec:       spec,
		schema:     schema,
		field:      spec.Field,
		field2:     spec.Field2,
		alpha:      alpha,
		testWeight: newTestWeight(spec),
	}, nil
}

func (s *spearmanRRow) UpdateRow(record *Record) error {
	x, xOk := record.NumericValue(s.field)
	if !xOk {
		return nil
	}
	y, yOk := record.NumericValue(s.field2)
	if !yOk {
		return nil
	}
	w, ok := s.rowWeight(record)
	if !ok {
		return nil
	}
	s.xs.add(x, w)
	s.ys = append(s.ys, y)
	return nil
}

func (s *spearmanRRow) Finalize() (*types.TestResult, error) {
	defer s.reset()
	n := s.xs.n()
	if n < 3 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_SPEARMAN_R requires n ≥ 3, got %d", n),
			map[string]any{"n": n, "min_required": 3})
	}
	ws := s.xs.weights
	rx, tiesX := weightedMidRanks(s.xs.values, ws)
	ry, tiesY := weightedMidRanks(s.ys, ws)
	nW := s.xs.sumW // n unweighted
	rho, ok := rankPearson(rx, ry, ws, nW)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_CORRELATION_UNDEFINED,
			"TEST_SPEARMAN_R: at least one column collapses to a single rank; ρ is undefined",
			map[string]any{"n": n})
	}
	df := nW - 2
	var t, p float64
	switch {
	case rho == 1 || rho == -1:
		t = math.Inf(int(math.Copysign(1, rho)))
		p = 0
	default:
		t = rho * math.Sqrt(df/(1-rho*rho))
		p = statdist.StudentTTwoSidedP(t, df)
	}
	res := &types.TestResult{
		Label:      testLabel(s.spec),
		Type:       types.TEST_SPEARMAN_R,
		Variant:    "rank_pearson",
		Statistic:  rho,
		DF:         df,
		PValue:     p,
		Alpha:      s.alpha,
		RejectNull: p < s.alpha,
		Details: map[string]any{
			"n":      n,
			"t":      t,
			"ties_x": tieSizes(tiesX, s.basis.Weighted()),
			"ties_y": tieSizes(tiesY, s.basis.Weighted()),
		},
	}
	s.noteScalar(res.Details, s.xs.sumW, s.xs.sumWSq)
	if tiesDominateW(tiesX, nW) || tiesDominateW(tiesY, nW) {
		res.Warnings = append(res.Warnings, string(errors.PULSE_TEST_TIES_DOMINATE)+
			": ≥ 50% of values are tied in at least one column; asymptotic p-value is unreliable")
	}
	return res, nil
}

func (s *spearmanRRow) reset() {
	s.xs = rankSample{}
	s.ys = nil
}

// rankPearson is Spearman's ρ off two columns of (weighted) mid-ranks:
// the Σw-weighted Pearson r around the mean rank (N+1)/2, N = nW = Σw,
// clamped to [−1, 1]. ok is false (ρ undefined) when either column
// collapses to a single rank. TEST_SPEARMAN_R and MAT_CORRELATION
// params.method "spearman" both read it, so a matrix cell and the
// test's ρ over the same rows are one computation, bit for bit.
func rankPearson(rx, ry, ws []float64, nW float64) (rho float64, ok bool) {
	// Mean rank is (N+1)/2 regardless of ties (N = Σw).
	mean := (nW + 1) / 2
	var sxx, syy, sxy float64
	for i := range rx {
		dx := rx[i] - mean
		dy := ry[i] - mean
		// (w·d)·d': exact at w = 1 (op-order exactness rule).
		wdx := ws[i] * dx
		sxx += wdx * dx
		syy += ws[i] * dy * dy
		sxy += wdx * dy
	}
	denom := math.Sqrt(sxx * syy)
	if denom == 0 {
		return math.NaN(), false
	}
	rho = sxy / denom
	if rho > 1 {
		rho = 1
	} else if rho < -1 {
		rho = -1
	}
	return rho, true
}
