package processing

import (
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// chiSqRow implements TEST_CHISQ as a streaming row test on a 2D
// contingency table built from two categorical fields (Rows × Cols).
// State is two running label sets (insertion-ordered) and a map of
// (row,col)→count keyed by composite index. After the iterator drains,
// Finalize sorts both label sets byte-wise (the order every other test
// and the goodness-of-fit form report), materializes a dense observed
// matrix indexed by the sorted labels, computes
// expected counts under independence, and emits the χ² statistic with
// (R-1)(C-1) degrees of freedom.
//
// Weighted (.claude/reference/weighting.md, Weighted inference) the
// table holds Σw per cell. Frequency: ordinary Pearson on the Σw table
// (the expanded rows). Probability: the cell proportions scaled to the
// table's Kish n_eff, T* = n_eff·Σw_ij/Σw, then ordinary Pearson on T*
// — a first-order Kish approximation, NOT the Rao-Scott design-effect
// correction. Cramér's V, φ and expected_min read the scaled table.
type chiSqRow struct {
	spec   *types.Test
	schema *encoding.Schema

	rowsField string
	colsField string
	alpha     float64

	rowLabels  []string
	rowIndex   map[string]int
	colLabels  []string
	colIndex   map[string]int
	cellCounts map[int]int64 // key = rowIdx*1<<32 | colIdx
	rowTotals  []int64
	colTotals  []int64
	grandTotal int64

	// Weighted state: Σw per cell / margin / table and the table's Σw².
	cellW        map[int]float64
	rowW, colW   []float64
	sumW, sumWSq float64
	testWeight
}

func newChiSqRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	params, err := decodeChiSqParams(spec)
	if err != nil {
		return nil, err
	}
	// params.expected selects the goodness-of-fit form, which reads
	// rows alone (test_chisq_gof.go).
	if params.Expected != nil {
		return newChiSqGOFRow(spec, schema, params.Expected)
	}
	if spec.Rows == "" || spec.Cols == "" {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"TEST_CHISQ requires rows and cols",
			map[string]any{"rows": spec.Rows, "cols": spec.Cols})
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
	if cerr := contingencyAxisRefusal(spec, schema); cerr != nil {
		return nil, cerr
	}
	return &chiSqRow{
		spec:       spec,
		schema:     schema,
		rowsField:  spec.Rows,
		colsField:  spec.Cols,
		alpha:      alpha,
		rowIndex:   make(map[string]int),
		colIndex:   make(map[string]int),
		cellCounts: make(map[int]int64),
		cellW:      make(map[int]float64),
		testWeight: newTestWeight(spec),
	}, nil
}

func (c *chiSqRow) UpdateRow(record *Record) error {
	rowVal, rOk := record.LevelValue(c.rowsField)
	if !rOk {
		return nil
	}
	colVal, cOk := record.LevelValue(c.colsField)
	if !cOk {
		return nil
	}
	w, ok := c.rowWeight(record)
	if !ok {
		return nil
	}
	ri, exists := c.rowIndex[rowVal]
	if !exists {
		ri = len(c.rowLabels)
		c.rowLabels = append(c.rowLabels, rowVal)
		c.rowIndex[rowVal] = ri
		c.rowTotals = append(c.rowTotals, 0)
		c.rowW = append(c.rowW, 0)
	}
	ci, exists := c.colIndex[colVal]
	if !exists {
		ci = len(c.colLabels)
		c.colLabels = append(c.colLabels, colVal)
		c.colIndex[colVal] = ci
		c.colTotals = append(c.colTotals, 0)
		c.colW = append(c.colW, 0)
	}
	key := ri*(1<<20) + ci
	c.cellCounts[key]++
	c.rowTotals[ri]++
	c.colTotals[ci]++
	c.grandTotal++
	c.cellW[key] += w
	c.rowW[ri] += w
	c.colW[ci] += w
	c.sumW += w
	c.sumWSq += w * w
	return nil
}

func (c *chiSqRow) Finalize() (*types.TestResult, error) {
	defer c.reset()
	r := len(c.rowLabels)
	col := len(c.colLabels)
	if r < 2 || col < 2 || c.grandTotal == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_CONTINGENCY_DEGENERATE,
			fmt.Sprintf("TEST_CHISQ contingency must be ≥ 2x2 with non-zero counts; got %dx%d, N=%d", r, col, c.grandTotal),
			map[string]any{"rows": r, "cols": col, "n": c.grandTotal})
	}
	// Sort both axes byte-wise; rowPos / colPos map an insertion index
	// to its sorted position, and every per-index slice is permuted.
	rowLabels, rowPos := sortedLabelPerm(c.rowLabels)
	colLabels, colPos := sortedLabelPerm(c.colLabels)
	rowTotals := make([]int64, r)
	rowW := make([]float64, r)
	for i, p := range rowPos {
		rowTotals[p] = c.rowTotals[i]
		rowW[p] = c.rowW[i]
	}
	colTotals := make([]int64, col)
	colW := make([]float64, col)
	for i, p := range colPos {
		colTotals[p] = c.colTotals[i]
		colW[p] = c.colW[i]
	}
	observed := make([][]int64, r)
	for i := range observed {
		observed[i] = make([]int64, col)
	}
	for key, count := range c.cellCounts {
		ri := rowPos[key/(1<<20)]
		ci := colPos[key%(1<<20)]
		observed[ri][ci] = count
	}
	// The table the statistic reads: Σw per cell, scaled by
	// c = N*/Σw (exactly 1 — no multiply — unweighted and under
	// frequency, where Σw cells are the exact integer counts).
	scale := c.basis.Scale(c.sumW, c.sumWSq)
	N := c.basis.NStar(c.sumW, c.sumWSq)
	scaled := func(x float64) float64 {
		if c.basis == weighting.Probability {
			return scale * x
		}
		return x
	}
	cellW := make([][]float64, r)
	for i := range cellW {
		cellW[i] = make([]float64, col)
	}
	for key, w := range c.cellW {
		cellW[rowPos[key/(1<<20)]][colPos[key%(1<<20)]] = w
	}
	var stat, expectedMin float64
	expectedMin = -1
	lowExpectedCells := 0
	for ri := range r {
		for ci := range col {
			expected := scaled(rowW[ri]) * scaled(colW[ci]) / N
			if expectedMin < 0 || expected < expectedMin {
				expectedMin = expected
			}
			if expected < 5 {
				lowExpectedCells++
			}
			obs := scaled(cellW[ri][ci])
			if expected > 0 {
				diff := obs - expected
				stat += diff * diff / expected
			}
		}
	}
	df := float64((r - 1) * (col - 1))
	p := chiSquareSurvival(stat, df)
	var warnings []string
	if lowExpectedCells > 0 {
		warnings = append(warnings, fmt.Sprintf("%s: %d cell(s) have expected count < 5; χ² approximation may be unreliable",
			errors.PULSE_TEST_EXPECTED_COUNT_TOO_LOW, lowExpectedCells))
	}
	details := map[string]any{
		"row_labels":   rowLabels,
		"col_labels":   colLabels,
		"contingency":  observed,
		"row_totals":   rowTotals,
		"col_totals":   colTotals,
		"n":            c.grandTotal,
		"expected_min": expectedMin,
	}
	if c.basis.Weighted() {
		// The observed table and margins as Σw (unscaled weighted counts).
		details["contingency"] = cellW
		details["row_totals"] = rowW
		details["col_totals"] = colW
	}
	c.noteScalar(details, c.sumW, c.sumWSq)
	setEffectSize(details, "cramers_v", cramersV(stat, N, r, col))
	if r == 2 && col == 2 {
		setEffectSize(details, "phi", phiCoefficient(stat, N))
	}
	return &types.TestResult{
		Label:      testLabel(c.spec),
		Type:       types.TEST_CHISQ,
		Variant:    "independence",
		Statistic:  stat,
		DF:         df,
		PValue:     p,
		Alpha:      c.alpha,
		RejectNull: p < c.alpha,
		Details:    details,
		Warnings:   warnings,
	}, nil
}

func (c *chiSqRow) reset() {
	c.rowLabels = nil
	c.rowIndex = make(map[string]int)
	c.colLabels = nil
	c.colIndex = make(map[string]int)
	c.cellCounts = make(map[int]int64)
	c.rowTotals = nil
	c.colTotals = nil
	c.grandTotal = 0
	c.cellW = make(map[int]float64)
	c.rowW = nil
	c.colW = nil
	c.sumW, c.sumWSq = 0, 0
}

// sortedLabelPerm returns labels sorted byte-wise (a fresh slice) and
// pos, where pos[i] is the sorted position of labels[i]. Labels are
// distinct, so the order is total.
func sortedLabelPerm(labels []string) ([]string, []int) {
	sorted := append([]string(nil), labels...)
	sort.Strings(sorted)
	at := make(map[string]int, len(sorted))
	for i, l := range sorted {
		at[l] = i
	}
	pos := make([]int, len(labels))
	for i, l := range labels {
		pos[i] = at[l]
	}
	return sorted, pos
}
