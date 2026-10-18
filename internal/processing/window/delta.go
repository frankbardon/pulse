package window

import (
	"github.com/frankbardon/pulse/internal/windowparams"
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

func init() {
	register(types.WIN_DELTA, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		// The params reading is shared with predict (internal/windowparams);
		// a frame is refused by the registry's slot check, as predict does.
		periods, ref := windowparams.Periods(w)
		if ref != nil {
			return nil, ref.Coded()
		}
		return &deltaComputer{field: w.Field, periods: periods}, nil
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
