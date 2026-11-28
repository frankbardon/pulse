package processing

// decimalCellFloat reads a decimal cell — a decimal aggregate result
// (decimalAggResult) or a record-row decimal (decimalCell) — as the
// float64 a float64 column's cell holds at that value
// (Decimal128.Float64 at its scale; NaN for an undefined figure), and
// reports whether v is one. Every numeric reader of a row or crosstab
// cell consults it before its own type switch, so a decimal column reads
// as the float64 column with the same values: group / series overlays
// (toFloat64), crosstab-host overlays (scalarFromCell), crosstab
// normalization (coerceFloat64), post-tests (floatValue), the chain
// hand-off (coerceFloat) and the stage / compose value readers
// (coerceNumericValue). Before, each switch fell through on the opaque
// struct: a silent null figure, an absent cell, or a post-test refused
// PULSE_TEST_FIELD_NOT_NUMERIC. Windows read it through
// window.SortValuer (cellFloat); types.MatrixCell.Scalar through the
// CellFloat64 method.
func decimalCellFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case decimalAggResult:
		return x.CellFloat64(), true
	case decimalCell:
		return x.CellFloat64(), true
	}
	return 0, false
}
