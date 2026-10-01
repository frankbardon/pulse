package window

import "github.com/frankbardon/pulse/types"

type rankComputer struct {
	orderBy []types.OrderKey
	dense   bool
}

func init() {
	register(types.WIN_RANK, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		return &rankComputer{orderBy: w.OrderBy, dense: false}, nil
	})
	register(types.WIN_DENSE_RANK, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		return &rankComputer{orderBy: w.OrderBy, dense: true}, nil
	})
}

func (c *rankComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		var rank int64 = 1
		var same int64 = 1 // count of rows with same rank (for gap accounting)
		for i, rowIdx := range part {
			if i == 0 {
				rows[rowIdx][label] = rank
				continue
			}
			prev := rows[part[i-1]]
			cur := rows[rowIdx]
			if equalsOnKeys(cur, prev, c.orderBy) {
				// Tie: same rank as previous.
				same++
				rows[rowIdx][label] = rank
				continue
			}
			if c.dense {
				rank++
			} else {
				rank += same
				same = 1
			}
			rows[rowIdx][label] = rank
			if c.dense {
				same = 1
			}
		}
	}
	return nil
}
