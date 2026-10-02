package window

import (
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// deltaComputer implements WIN_DELTA: within each partition, in OrderBy order,
// the point difference between the current row's field value and the value
// `periods` rows earlier.
//
// It is the subtraction counterpart of WIN_PCT_CHANGE and shares its parameter
// shape, frame contract (no frame), null semantics and streamability. The one
// behavioural divergence is deliberate: WIN_PCT_CHANGE nulls a row whose prior
// value is zero because the division is undefined. Subtraction has no such
// case, so a zero prior is a LEGITIMATE delta here and must never be nulled.
// Getting that wrong is silent — the column still renders, with null holes
// exactly where real values belong.
type deltaComputer struct {
	field   string
	periods int
}

type deltaParams struct {
	Periods *int `json:"periods"`
}

func init() {
	register(types.WIN_DELTA, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		if w.Frame != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"delta does not accept a frame")
		}
		c := &deltaComputer{field: w.Field, periods: 1}
		if len(w.Params) > 0 {
			var p deltaParams
			if err := json.Unmarshal(w.Params, &p); err != nil {
				return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "delta params")
			}
			if p.Periods != nil {
				c.periods = *p.Periods
			}
		}
		if c.periods <= 0 {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "delta periods must be >= 1")
		}
		return c, nil
	})
}

func (c *deltaComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for i, rowIdx := range part {
			if i < c.periods {
				rows[rowIdx][label] = nil
				continue
			}
			cur, curOk := cellFloat(rows[part[i]], c.field)
			prev, prevOk := cellFloat(rows[part[i-c.periods]], c.field)
			// NO `|| prev == 0` clause here — see the type doc comment.
			if !curOk || !prevOk {
				rows[rowIdx][label] = nil
				continue
			}
			rows[rowIdx][label] = cur - prev
		}
	}
	return nil
}
