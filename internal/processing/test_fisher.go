package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// fisherExactRow implements TEST_FISHER_EXACT as a tier-1 buffered
// row test: exact two-sided p-value for a 2×2 contingency table.
//
// Algorithm:
//  1. Build the 2×2 table from Rows × Cols categorical pair.
//  2. Hold the marginals (R₁, R₂, C₁, C₂) fixed.
//  3. Iterate every feasible value of cell[0][0] within
//     [max(0, R₁−C₂), min(R₁, C₁)] and sum hypergeometric
//     probabilities for tables at least as extreme as the observed.
//
// "At least as extreme" uses the two-sided rule: sum probabilities of
// every table whose hypergeometric probability is ≤ the observed.
//
// Use case: chi-square small-sample backstop. When any expected count
// is below 5 the chi-square approximation is unreliable;
// TEST_FISHER_EXACT is the canonical replacement.
//
// Frequency-weighted (ClassFrequencyOnly), each row stands for w
// identical rows: the cells are the Σw table (integers — a non-integer
// frequency weight is invalid and excluded), so the exact test runs on
// the expanded table and equals the unweighted test on the expanded
// rows. The p-value loop is O(min margin) lgamma evaluations, so a
// large Σw table needs no size guard. Details keep the raw `n` and add
// `sum_weights`; `contingency` reports the Σw table (floats, weighted
// only).
type fisherExactRow struct {
	spec   *types.Test
	schema *encoding.Schema

	rowsField string
	colsField string
	alpha     float64

	// counts[rowKey][colKey] = observed count
	counts map[string]map[string]int
	rowOrd []string
	colOrd []string
	rowSet map[string]struct{}
	colSet map[string]struct{}

	// cellW[rowKey][colKey] = Σw (equal to counts unweighted).
	cellW        map[string]map[string]float64
	sumW, sumWSq float64

	testWeight
}

func newFisherExactRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Rows == "" || spec.Cols == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_FISHER_EXACT requires rows and cols (categorical fields)")
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
		for axis, name := range map[string]string{"rows": spec.Rows, "cols": spec.Cols} {
			if f := schema.Field(name); f != nil && !f.Type.IsCategorical() {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("TEST_FISHER_EXACT %s %q must be categorical, got %s", axis, name, f.Type.String()),
					map[string]any{"axis": axis, "field": name, "field_type": f.Type.String()})
			}
		}
	}
	return &fisherExactRow{
		spec:      spec,
		schema:    schema,
		rowsField: spec.Rows,
		colsField: spec.Cols,
		alpha:     alpha,
		counts:    make(map[string]map[string]int),
		rowSet:    make(map[string]struct{}),
		colSet:    make(map[string]struct{}),
		cellW:     make(map[string]map[string]float64),

		testWeight: newTestWeight(spec),
	}, nil
}

func (f *fisherExactRow) UpdateRow(record *Record) error {
	rk, ok := record.StringValue(f.rowsField)
	if !ok {
		return nil
	}
	ck, ok := record.StringValue(f.colsField)
	if !ok {
		return nil
	}
	w, ok := f.rowWeight(record)
	if !ok {
		return nil
	}
	if _, seen := f.rowSet[rk]; !seen {
		f.rowSet[rk] = struct{}{}
		f.rowOrd = append(f.rowOrd, rk)
	}
	if _, seen := f.colSet[ck]; !seen {
		f.colSet[ck] = struct{}{}
		f.colOrd = append(f.colOrd, ck)
	}
	row, exists := f.counts[rk]
	if !exists {
		row = make(map[string]int)
		f.counts[rk] = row
	}
	row[ck]++
	rowW, exists := f.cellW[rk]
	if !exists {
		rowW = make(map[string]float64)
		f.cellW[rk] = rowW
	}
	rowW[ck] += w
	f.sumW += w
	f.sumWSq += w * w
	return nil
}

func (f *fisherExactRow) Finalize() (*types.TestResult, error) {
	defer f.reset()
	if len(f.rowOrd) != 2 || len(f.colOrd) != 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_CONTINGENCY_DEGENERATE,
			fmt.Sprintf("TEST_FISHER_EXACT supports 2×2 tables only; got %d×%d", len(f.rowOrd), len(f.colOrd)),
			map[string]any{"rows": len(f.rowOrd), "cols": len(f.colOrd)})
	}
	// Build the 2×2 table with deterministic ordering: the Σw cells
	// (the raw counts unweighted, where every w is 1). Frequency weights
	// are integers, so each Σw is an exact integer (the round is a
	// no-op kept for safety).
	cell := func(i, j int) int { return int(math.Round(f.cellW[f.rowOrd[i]][f.colOrd[j]])) }
	a, b, c, d := cell(0, 0), cell(0, 1), cell(1, 0), cell(1, 1)
	n := a + b + c + d
	rawN := f.counts[f.rowOrd[0]][f.colOrd[0]] + f.counts[f.rowOrd[0]][f.colOrd[1]] +
		f.counts[f.rowOrd[1]][f.colOrd[0]] + f.counts[f.rowOrd[1]][f.colOrd[1]]
	if n == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_CONTINGENCY_DEGENERATE,
			"TEST_FISHER_EXACT: contingency table is empty", nil)
	}
	row1 := a + b
	col1 := a + c
	col2 := b + d
	// Range of feasible cell[0][0] holding marginals fixed.
	lo := 0
	if row1-col2 > 0 {
		lo = row1 - col2
	}
	hi := row1
	if col1 < hi {
		hi = col1
	}
	// log-probability of the observed table.
	logPObs := logHypergeometric(a, row1, col1, n)
	// Sum probabilities of every table with prob ≤ observed (two-sided).
	var sum float64
	for x := lo; x <= hi; x++ {
		logP := logHypergeometric(x, row1, col1, n)
		if logP <= logPObs+1e-12 {
			sum += math.Exp(logP)
		}
	}
	if sum > 1 {
		sum = 1
	}
	// Sample odds ratio (a·d)/(b·c), informational. No continuity
	// correction: a zero b or c cell yields +Inf, otherwise a zero a or
	// d cell yields 0.
	var oddsRatio float64
	switch {
	case b == 0 || c == 0:
		oddsRatio = math.Inf(1)
	case a == 0 || d == 0:
		oddsRatio = 0
	default:
		oddsRatio = float64(a*d) / float64(b*c)
	}
	details := map[string]any{
		"row_labels":  f.rowOrd,
		"col_labels":  f.colOrd,
		"contingency": [][]int{{a, b}, {c, d}},
		"odds_ratio":  oddsRatio,
		"n":           rawN,
	}
	if f.basis.Weighted() {
		details["contingency"] = [][]float64{{float64(a), float64(b)}, {float64(c), float64(d)}}
	}
	f.noteScalar(details, f.sumW, f.sumWSq)
	return &types.TestResult{
		Label:      testLabel(f.spec),
		Type:       types.TEST_FISHER_EXACT,
		Variant:    "two_sided_2x2",
		Statistic:  oddsRatio,
		PValue:     sum,
		Alpha:      f.alpha,
		RejectNull: sum < f.alpha,
		Details:    details,
	}, nil
}

func (f *fisherExactRow) reset() {
	f.counts = make(map[string]map[string]int)
	f.rowOrd = nil
	f.colOrd = nil
	f.rowSet = make(map[string]struct{})
	f.colSet = make(map[string]struct{})
	f.cellW = make(map[string]map[string]float64)
	f.sumW, f.sumWSq = 0, 0
}

// logHypergeometric returns log P(X = x) for X ~ Hypergeometric(N, K, n)
// expressed via the 2×2 table form: x = cell[0][0], K = row1 total,
// n = col1 total, N = grand total. log space avoids overflow when n is
// moderate.
func logHypergeometric(x, row1, col1, n int) float64 {
	if x < 0 || x > row1 || x > col1 {
		return math.Inf(-1)
	}
	if (row1 - x) > (n - col1) {
		return math.Inf(-1)
	}
	return logBinomial(col1, x) + logBinomial(n-col1, row1-x) - logBinomial(n, row1)
}

// logBinomial returns log C(n, k) via lgamma.
func logBinomial(n, k int) float64 {
	if k < 0 || k > n {
		return math.Inf(-1)
	}
	lgN, _ := math.Lgamma(float64(n + 1))
	lgK, _ := math.Lgamma(float64(k + 1))
	lgNK, _ := math.Lgamma(float64(n - k + 1))
	return lgN - lgK - lgNK
}
