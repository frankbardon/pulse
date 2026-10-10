package window

import (
	"github.com/frankbardon/pulse/internal/ewmaparams"
	"github.com/frankbardon/pulse/types"
)

type ewmaComputer struct {
	field string
	alpha float64
}

func init() {
	register(types.WIN_EWMA, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		// The params reading is shared with predict (internal/ewmaparams), so
		// both refuse the same request with the same message.
		alpha, ref := ewmaparams.Resolve(w.Params)
		if ref != nil {
			return nil, ref.Coded()
		}
		return &ewmaComputer{field: w.Field, alpha: alpha}, nil
	})
}

// Compute applies the recurrence s_i = α·x_i + (1-α)·s_{i-1}, with s_0
// seeded from the first non-null value in the partition. Output is nil
// for rows preceding the first non-null and for nulls themselves
// (carrying the most recent state through to the next non-null row).
func (c *ewmaComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		var s float64
		seeded := false
		for i, rowIdx := range part {
			v, ok := cellFloat(rows[part[i]], c.field)
			_ = v
			_ = i
			if !ok {
				if !seeded {
					rows[rowIdx][label] = nil
					continue
				}
				rows[rowIdx][label] = nil
				continue
			}
			if !seeded {
				s = v
				seeded = true
			} else {
				s = c.alpha*v + (1-c.alpha)*s
			}
			rows[rowIdx][label] = s
		}
	}
	return nil
}
