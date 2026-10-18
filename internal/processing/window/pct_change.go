package window

import (
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

type pctChangeComputer struct {
	field   string
	periods int
}

func init() {
	register(types.WIN_PCT_CHANGE, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		// The params reading is shared with predict (internal/windowparams);
		// a frame is refused by the registry's slot check, as predict does.
		periods, ref := windowparams.Periods(w)
		if ref != nil {
			return nil, ref.Coded()
		}
		return &pctChangeComputer{field: w.Field, periods: periods}, nil
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
