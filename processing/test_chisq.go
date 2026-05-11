package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// chiSqRow implements TEST_CHISQ as a streaming row test on a 2D
// contingency table built from two categorical fields (Rows × Cols).
// State is two running label sets (insertion-ordered) and a map of
// (row,col)→count keyed by composite string. After the iterator
// drains, Finalize materializes a dense observed matrix, computes
// expected counts under independence, and emits the χ² statistic with
// (R-1)(C-1) degrees of freedom.
type chiSqRow struct {
	spec   *types.Test
	schema *encoding.Schema

	rowsField string
	colsField string
	alpha     float64

	rowLabels   []string
	rowIndex    map[string]int
	colLabels   []string
	colIndex    map[string]int
	cellCounts  map[int]int64 // key = rowIdx*1<<32 | colIdx
	rowTotals   []int64
	colTotals   []int64
	grandTotal  int64
}

func newChiSqRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
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
	if schema != nil {
		for axis, name := range map[string]string{"rows": spec.Rows, "cols": spec.Cols} {
			if f := schema.Field(name); f != nil && !f.Type.IsCategorical() {
				return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
					fmt.Sprintf("TEST_CHISQ %s field %q must be categorical, got %s", axis, name, f.Type.String()),
					map[string]any{"axis": axis, "field": name, "field_type": f.Type.String()})
			}
		}
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
	}, nil
}

func (c *chiSqRow) UpdateRow(record *Record) error {
	rowVal, rOk := record.StringValue(c.rowsField)
	if !rOk {
		return nil
	}
	colVal, cOk := record.StringValue(c.colsField)
	if !cOk {
		return nil
	}
	ri, exists := c.rowIndex[rowVal]
	if !exists {
		ri = len(c.rowLabels)
		c.rowLabels = append(c.rowLabels, rowVal)
		c.rowIndex[rowVal] = ri
		c.rowTotals = append(c.rowTotals, 0)
	}
	ci, exists := c.colIndex[colVal]
	if !exists {
		ci = len(c.colLabels)
		c.colLabels = append(c.colLabels, colVal)
		c.colIndex[colVal] = ci
		c.colTotals = append(c.colTotals, 0)
	}
	key := ri*(1<<20) + ci
	c.cellCounts[key]++
	c.rowTotals[ri]++
	c.colTotals[ci]++
	c.grandTotal++
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
	observed := make([][]int64, r)
	for i := range observed {
		observed[i] = make([]int64, col)
	}
	for key, count := range c.cellCounts {
		ri := key / (1 << 20)
		ci := key % (1 << 20)
		observed[ri][ci] = count
	}
	var stat, expectedMin float64
	expectedMin = -1
	lowExpectedCells := 0
	N := float64(c.grandTotal)
	for ri := range r {
		for ci := range col {
			expected := float64(c.rowTotals[ri]) * float64(c.colTotals[ci]) / N
			if expectedMin < 0 || expected < expectedMin {
				expectedMin = expected
			}
			if expected < 5 {
				lowExpectedCells++
			}
			obs := float64(observed[ri][ci])
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
	return &types.TestResult{
		Label:      testLabel(c.spec),
		Type:       types.TEST_CHISQ,
		Variant:    "independence",
		Statistic:  stat,
		DF:         df,
		PValue:     p,
		Alpha:      c.alpha,
		RejectNull: p < c.alpha,
		Details: map[string]any{
			"row_labels":   c.rowLabels,
			"col_labels":   c.colLabels,
			"contingency":  observed,
			"row_totals":   c.rowTotals,
			"col_totals":   c.colTotals,
			"n":            c.grandTotal,
			"expected_min": expectedMin,
		},
		Warnings: warnings,
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
}
