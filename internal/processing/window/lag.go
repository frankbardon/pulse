package window

import (
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
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

type lagLeadParams struct {
	Offset  *int            `json:"offset"`
	Default json.RawMessage `json:"default"`
}

func newLag(w *types.Window, _ WindowOptions) (WindowComputer, error) {
	c, err := newLagLead(w, false)
	return c, err
}

func newLead(w *types.Window, _ WindowOptions) (WindowComputer, error) {
	c, err := newLagLead(w, true)
	return c, err
}

func newLagLead(w *types.Window, forward bool) (*lagComputer, error) {
	c := &lagComputer{field: w.Field, offset: 1, forward: forward}
	if len(w.Params) > 0 {
		var p lagLeadParams
		if err := json.Unmarshal(w.Params, &p); err != nil {
			return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "lag/lead params")
		}
		if p.Offset != nil {
			c.offset = *p.Offset
		}
		if len(p.Default) > 0 && string(p.Default) != "null" {
			var v any
			if err := json.Unmarshal(p.Default, &v); err != nil {
				return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "lag/lead default")
			}
			c.hasDef = true
			c.defVal = v
		}
	}
	return c, nil
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
