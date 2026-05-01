package window

import (
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

type ewmaComputer struct {
	field string
	alpha float64
}

type ewmaParams struct {
	Alpha *float64 `json:"alpha"`
}

func init() {
	register(types.WIN_EWMA, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		var p ewmaParams
		if len(w.Params) > 0 {
			if err := json.Unmarshal(w.Params, &p); err != nil {
				return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "ewma params")
			}
		}
		if p.Alpha == nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "ewma requires params.alpha")
		}
		if *p.Alpha <= 0 || *p.Alpha > 1 {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "ewma alpha must be in (0, 1]")
		}
		return &ewmaComputer{field: w.Field, alpha: *p.Alpha}, nil
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
