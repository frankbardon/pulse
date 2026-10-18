package window

import (
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

type lagComputer struct {
	field   string
	offset  int
	hasDef  bool
	defVal  any
	forward bool // false = lag (look back), true = lead (look forward)
}

func init() {
	register(types.WIN_LAG, newLag)
	register(types.WIN_LEAD, newLead)
}

func newLag(w *types.Window, _ WindowOptions) (WindowComputer, error) {
	return newLagLead(w, false)
}

func newLead(w *types.Window, _ WindowOptions) (WindowComputer, error) {
	return newLagLead(w, true)
}

// newLagLead reads the params through the reading predict shares
// (internal/windowparams): the same refusal on both sides.
func newLagLead(w *types.Window, forward bool) (*lagComputer, error) {
	spec, ref := windowparams.Lag(w)
	if ref != nil {
		return nil, ref.Coded()
	}
	return &lagComputer{field: w.Field, offset: spec.Offset, hasDef: spec.HasDefault, defVal: spec.Default, forward: forward}, nil
}

func (c *lagComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for i, rowIdx := range part {
			var srcIdx int
			if c.forward {
				srcIdx = i + c.offset
			} else {
				srcIdx = i - c.offset
			}
			var val any
			if srcIdx < 0 || srcIdx >= len(part) {
				if c.hasDef {
					val = c.defVal
				} else {
					val = nil
				}
			} else {
				val = rows[part[srcIdx]][c.field]
			}
			rows[rowIdx][label] = val
		}
	}
	return nil
}
