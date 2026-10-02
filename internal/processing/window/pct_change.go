package window

import (
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

type pctChangeComputer struct {
	field   string
	periods int
}

type pctChangeParams struct {
	Periods *int `json:"periods"`
}

func init() {
	register(types.WIN_PCT_CHANGE, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		c := &pctChangeComputer{field: w.Field, periods: 1}
		if len(w.Params) > 0 {
			var p pctChangeParams
			if err := json.Unmarshal(w.Params, &p); err != nil {
				return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "pct_change params")
			}
			if p.Periods != nil {
				c.periods = *p.Periods
			}
		}
		if c.periods <= 0 {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "pct_change periods must be >= 1")
		}
		return c, nil
	})
}

func (c *pctChangeComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for i, rowIdx := range part {
			if i < c.periods {
				rows[rowIdx][label] = nil
				continue
			}
			cur, curOk := cellFloat(rows[part[i]], c.field)
			prev, prevOk := cellFloat(rows[part[i-c.periods]], c.field)
			if !curOk || !prevOk || prev == 0 {
				rows[rowIdx][label] = nil
				continue
			}
			rows[rowIdx][label] = (cur - prev) / prev
		}
	}
	return nil
}
