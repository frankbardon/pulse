package window

import "github.com/frankbardon/pulse/types"

type runningSumComputer struct {
	field string
	frame *types.FrameSpec
}

func init() {
	register(types.WIN_RUNNING_SUM, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		return &runningSumComputer{field: w.Field, frame: w.Frame}, nil
	})
}

func (c *runningSumComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for i, rowIdx := range part {
			lo, hi := resolveFrame(i, len(part), c.frame)
			var sum float64
			seen := false
			for k := lo; k <= hi; k++ {
				v, ok := cellFloat(rows[part[k]], c.field)
				if !ok {
					continue
				}
				sum += v
				seen = true
			}
			if !seen {
				rows[rowIdx][label] = nil
			} else {
				rows[rowIdx][label] = sum
			}
		}
	}
	return nil
}
