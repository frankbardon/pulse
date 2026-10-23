package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// matrix_computed.go carries a window output a matrix reads as a
// computed member (vectors.ComputedColumns, sem-contribution E2-S2) onto
// the records the matrix folds. Features and attributes already write
// their outputs on the record before any matrix fold; a window runs over
// the record ROWS (recordsToRows) instead, so its label is copied back
// per record, converted exactly as the materialize route stores it
// (derivedFloat: a number widened to float64, NaN kept, nil null) — the
// fold then reads the same value a materialized column would hold. A
// regression reading a window label (target, predictors, covariance
// keys, jackknife_by — vectors.RegressionWindowReads) rides the same
// copy, so its fit reads the materialized column's value too.

// windowRecordColumn is one window label copied onto the records, and
// the consumer that reads it there ("matrix" or "regression").
type windowRecordColumn struct {
	label    string
	consumer string
}

// copyWindowMembers writes, onto filtered[i], every window label a
// matrix of req reads as a computed member or a regression of req reads
// per record (windowRecordColumns), from rows[i] (the windowed record
// rows, still in record order). A request whose matrices and
// regressions read no window label copies nothing.
func (p *Processor) copyWindowMembers(req *types.Request, filtered []*Record, rows []map[string]any) error {
	cols := p.windowRecordColumns(req)
	if len(cols) == 0 {
		return nil
	}
	for i, rec := range filtered {
		for _, c := range cols {
			l := c.label
			v, err := derivedFloat(l, rows[i][l])
			if err != nil {
				vt := fmt.Sprintf("%T", rows[i][l])
				if c.consumer == "regression" {
					return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
						"regression input "+l+" holds a non-numeric window value, which a regression cannot read",
						map[string]any{"field": l, "value_type": vt})
				}
				return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					"matrix computed member "+l+" holds a non-numeric window value, which a matrix cannot read",
					map[string]any{"member": l, "value_type": vt})
			}
			if v == nil {
				rec.SetNull(l)
			} else {
				rec.Set(l, v.(float64))
			}
		}
	}
	return nil
}

// windowRecordColumns returns the window labels copied onto the records:
// those req's matrices fold (vectors.MatrixComputedMembers produced by a
// window), then those its regressions read (vectors.RegressionWindowReads,
// when the regressions slot computes) not already listed, in first-read
// order; nil when none. Both read vectors.ComputedColumns, the one
// record-column model of a window label.
func (p *Processor) windowRecordColumns(req *types.Request) []windowRecordColumn {
	comp := vectors.ComputedColumns(req, p.schema)
	var out []windowRecordColumn
	seen := map[string]bool{}
	for _, m := range vectors.MatrixComputedMembers(req, p.schema) {
		if comp[m].Slot == "window" && !seen[m] {
			seen[m] = true
			out = append(out, windowRecordColumn{label: m, consumer: "matrix"})
		}
	}
	if p.compute.Regressions {
		for _, l := range vectors.RegressionWindowReads(req, p.schema) {
			if !seen[l] {
				seen[l] = true
				out = append(out, windowRecordColumn{label: l, consumer: "regression"})
			}
		}
	}
	return out
}
