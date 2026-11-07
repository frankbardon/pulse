package window

import (
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/internal/winformula"
	"github.com/frankbardon/pulse/types"
)

// formulaComputer implements WIN_FORMULA: params.expression evaluated
// on each row over the row's own columns — the group keys and
// aggregation labels of a grouped row, a record's fields on the record
// rows — and every earlier window's output, which ApplyWith has already
// written onto the row. Row-local: it compares no rows, so it takes no
// order_by, partition_by or frame (internal/windowparams).
//
// The compile is internal/winformula's, the one predict's
// field-reference walk runs: each column typed as WindowOptions.Columns
// declares it (a record field, or a built-in group key or aggregation
// label on the output rows), every other name `any`; the request-bound
// functions from WindowOptions.ExprBound.
// Which names exist at this window's point (a forward reference to a
// later window's label, a record column the output rows do not carry)
// is the walk's refusal, raised before any window runs.
//
// A row where any column the expression reads is null, absent or NaN
// reads null, without running the expression; a decimal cell — a
// record-row decimal or a decimal aggregate, each a SortValuer — reads
// as its float64 value (FormulaCell). A nil or non-finite result reads
// null; any other non-number fails the run (winformula.Program.Eval).
type formulaComputer struct {
	prog *winformula.Program
}

func init() {
	register(types.WIN_FORMULA, newFormula)
}

func newFormula(w *types.Window, opts WindowOptions) (WindowComputer, error) {
	src, ref := windowparams.Formula(w)
	if ref != nil {
		return nil, ref.Coded()
	}
	prog, ce := winformula.Compile(src, opts.Columns, opts.ExprBound, false)
	if ce != nil {
		return nil, ce
	}
	return &formulaComputer{prog: prog}, nil
}

func (c *formulaComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	reads := c.prog.Reads()
	env := make(map[string]any, len(reads))
	for _, part := range partitions {
		for _, rowIdx := range part {
			row := rows[rowIdx]
			null := false
			for _, name := range reads {
				v, ok := FormulaCell(row, name)
				if !ok {
					null = true
					break
				}
				env[name] = v
			}
			if null {
				row[label] = nil
				continue
			}
			f, ok, err := c.prog.Eval(env)
			if err != nil {
				return err
			}
			if !ok {
				row[label] = nil
				continue
			}
			row[label] = f
		}
	}
	return nil
}

// FormulaCell is the value a WIN_FORMULA reads for column name of row:
// a cell that orders by its value (SortValuer — a record-row decimal, a
// decimal aggregate) reads as that value, a decimal as its float64. ok
// is false for a null cell: absent, nil, NaN, or a nil SortValue.
func FormulaCell(row map[string]any, name string) (any, bool) {
	v, ok := row[name]
	if !ok {
		return nil, false
	}
	v = sortValue(v)
	if isNullCell(v) {
		return nil, false
	}
	if d, ok := v.(ScaledDecimal); ok {
		return d.Value.Float64(d.Scale), true
	}
	return v, true
}
