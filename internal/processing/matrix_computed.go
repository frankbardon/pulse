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
// copy, so its fit reads the materialized column's value too, and so
// does a post-window attribute (vectors.PostWindowAttributes: a
// row-local attribute reading a window label), which then runs over the
// records (applyPostWindowAttributes), and — windows-first — so do the
// aggregations, groupers and crosstab reading a window label
// (vectors.WindowsFirstReads), which then aggregate the records.

// windowRecordColumn is one window label copied onto the records, and
// the consumer that reads it there ("matrix", "regression",
// "attribute" or "aggregation" — the windows-first output slots).
type windowRecordColumn struct {
	label    string
	consumer string
}

// copyWindowMembers writes, onto filtered[i], every window label a
// matrix of req reads as a computed member or a regression of req reads
// per record (windowRecordColumns), from rows[i] (the windowed record
// rows, still in record order). A request whose matrices and
// regressions read no window label copies nothing.
func (p *Processor) copyWindowMembers(req *types.Request, post vectors.PostWindow, filtered []*Record, rows []map[string]any) error {
	cols := p.windowRecordColumns(req, post)
	if len(cols) == 0 {
		return nil
	}
	for i, rec := range filtered {
		for _, c := range cols {
			l := c.label
			v, err := derivedFloat(l, rows[i][l])
			if err != nil {
				vt := fmt.Sprintf("%T", rows[i][l])
				switch c.consumer {
				case "regression":
					return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
						"regression input "+l+" holds a non-numeric window value, which a regression cannot read",
						map[string]any{"field": l, "value_type": vt})
				case "attribute":
					return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
						"post-window attribute input "+l+" holds a non-numeric window value, which an attribute cannot read",
						map[string]any{"field": l, "value_type": vt})
				case "aggregation":
					return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
						"windows-first input "+l+" holds a non-numeric window value, which an aggregation, grouper or crosstab over the records cannot read",
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
// order, then those its post-window attributes read (post.WindowReads),
// then — windows-first (post.WindowsFirst) — those its aggregations,
// groupers and crosstab read (vectors.WindowsFirstReads); nil when none. The first two read vectors.ComputedColumns, the one
// record-column model of a window label.
func (p *Processor) windowRecordColumns(req *types.Request, post vectors.PostWindow) []windowRecordColumn {
	cat := p.catalog()
	comp := vectors.ComputedColumns(req, p.schema, cat)
	var out []windowRecordColumn
	seen := map[string]bool{}
	for _, m := range vectors.MatrixComputedMembers(req, p.schema, cat) {
		if comp[m].Slot == "window" && !seen[m] {
			seen[m] = true
			out = append(out, windowRecordColumn{label: m, consumer: "matrix"})
		}
	}
	if p.compute.Regressions {
		for _, l := range vectors.RegressionWindowReads(req, p.schema, cat) {
			if !seen[l] {
				seen[l] = true
				out = append(out, windowRecordColumn{label: l, consumer: "regression"})
			}
		}
	}
	for _, l := range post.WindowReads {
		if !seen[l] {
			seen[l] = true
			out = append(out, windowRecordColumn{label: l, consumer: "attribute"})
		}
	}
	if post.WindowsFirst {
		for _, l := range vectors.WindowsFirstReads(req, p.schema) {
			if !seen[l] {
				seen[l] = true
				out = append(out, windowRecordColumn{label: l, consumer: "aggregation"})
			}
		}
	}
	return out
}
