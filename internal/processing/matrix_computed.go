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
// fold then reads the same value a materialized column would hold.

// copyWindowMembers writes, onto filtered[i], every window label a
// matrix of req reads as a computed member, from rows[i] (the windowed
// record rows, still in record order). A request whose matrices read no
// window label copies nothing.
func (p *Processor) copyWindowMembers(req *types.Request, filtered []*Record, rows []map[string]any) error {
	labels := p.windowMatrixMembers(req)
	if len(labels) == 0 {
		return nil
	}
	for i, rec := range filtered {
		for _, l := range labels {
			v, err := derivedFloat(l, rows[i][l])
			if err != nil {
				return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					"matrix computed member "+l+" holds a non-numeric window value, which a matrix cannot read",
					map[string]any{"member": l, "value_type": fmt.Sprintf("%T", rows[i][l])})
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

// windowMatrixMembers returns the window labels req's matrices fold
// (vectors.MatrixComputedMembers produced by a window), in first-read
// order; nil when none.
func (p *Processor) windowMatrixMembers(req *types.Request) []string {
	comp := vectors.ComputedColumns(req, p.schema)
	var out []string
	for _, m := range vectors.MatrixComputedMembers(req, p.schema) {
		if comp[m].Slot == "window" {
			out = append(out, m)
		}
	}
	return out
}
