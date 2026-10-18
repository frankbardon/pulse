package window

import (
	"github.com/frankbardon/pulse/internal/shareparams"
	"github.com/frankbardon/pulse/types"
)

// shareComputer implements WIN_SHARE and WIN_LIFT over one kernel.
//
// WIN_SHARE: the row's value as a share of its partition's total,
// x / Σx — or, cumulative, the running total from the partition's first
// row (in order_by order) to this one over the same total.
//
// WIN_LIFT: the share of field divided by the share of base,
// (x / Σx) / (b / Σb) — or, cumulative, the running share of field over
// the running share of base. It is exactly WIN_SHARE(field) divided by
// WIN_SHARE(base): each total is taken over its own column's present
// values.
//
// Missing values (null, absent, non-numeric, NaN) are skipped: they add
// nothing to a total or a running sum. A row whose own value is missing
// reads null on the non-cumulative form; on the cumulative form it
// carries the running figure, which is null until the first present
// value (as WIN_RUNNING_SUM). A zero total, and a zero base share, read
// null — an undefined figure, never 0.
type shareComputer struct {
	field string
	spec  shareparams.Spec
	lift  bool
}

func init() {
	register(types.WIN_SHARE, newShare)
	register(types.WIN_LIFT, newLift)
}

func newShare(w *types.Window, _ WindowOptions) (WindowComputer, error) {
	return newShareComputer(w, false)
}

func newLift(w *types.Window, _ WindowOptions) (WindowComputer, error) {
	return newShareComputer(w, true)
}

func newShareComputer(w *types.Window, lift bool) (WindowComputer, error) {
	// The slot reading is shared with predict (internal/shareparams), so
	// both refuse the same request with the same code and sentence.
	spec, ref := shareparams.Resolve(w)
	if ref != nil {
		return nil, ref.Coded()
	}
	return &shareComputer{field: w.Field, spec: spec, lift: lift}, nil
}

func (c *shareComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		fx := shares(rows, part, c.field, c.spec.Cumulative)
		if !c.lift {
			for i, rowIdx := range part {
				rows[rowIdx][label] = cell(fx[i])
			}
			continue
		}
		fb := shares(rows, part, c.spec.Base, c.spec.Cumulative)
		for i, rowIdx := range part {
			sx, sb := fx[i], fb[i]
			if !sx.ok || !sb.ok || sb.v == 0 {
				rows[rowIdx][label] = nil
				continue
			}
			rows[rowIdx][label] = sx.v / sb.v
		}
	}
	return nil
}

// share is one row's share, ok false when it is undefined (null).
type share struct {
	v  float64
	ok bool
}

func cell(s share) any {
	if !s.ok {
		return nil
	}
	return s.v
}

// shares returns, per position of part, the share of field: the row's
// own value over the partition total, or (cumulative) the running total
// up to and including the row over the partition total. The total and
// the running sum add the present values in partition order, so the
// last cumulative share is exactly 1 whenever the total is non-zero.
func shares(rows []map[string]any, part []int, field string, cumulative bool) []share {
	out := make([]share, len(part))
	var total float64
	present := false
	for _, rowIdx := range part {
		if v, ok := cellFloat(rows[rowIdx], field); ok {
			total += v
			present = true
		}
	}
	if !present || total == 0 {
		return out
	}
	var run float64
	seen := false
	for i, rowIdx := range part {
		v, ok := cellFloat(rows[rowIdx], field)
		if ok {
			run += v
			seen = true
		}
		switch {
		case cumulative && seen:
			out[i] = share{v: run / total, ok: true}
		case !cumulative && ok:
			out[i] = share{v: v / total, ok: true}
		}
	}
	return out
}
