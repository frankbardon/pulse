package window

import "github.com/frankbardon/pulse/types"

type movingAvgComputer struct {
	field string
	frame *types.FrameSpec
}

func init() {
	register(types.WIN_MOVING_AVG, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		return &movingAvgComputer{field: w.Field, frame: w.Frame}, nil
	})
}

// Compute is identical to RUNNING_AVG mechanically; predict guarantees the
// frame is bounded both sides for MOVING_AVG (the differentiator).
func (c *movingAvgComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for i, rowIdx := range part {
			lo, hi := resolveFrame(i, len(part), c.frame)
			var sum float64
			var n int
			for k := lo; k <= hi; k++ {
				v, ok := cellFloat(rows[part[k]], c.field)
				if !ok {
					continue
				}
				sum += v
				n++
			}
			if n == 0 {
				rows[rowIdx][label] = nil
			} else {
				rows[rowIdx][label] = sum / float64(n)
			}
		}
	}
	return nil
}
