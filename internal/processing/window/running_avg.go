package window

import "github.com/frankbardon/pulse/types"

type runningAvgComputer struct {
	field string
	frame *types.FrameSpec
}

func init() {
	register(types.WIN_RUNNING_AVG, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		return &runningAvgComputer{field: w.Field, frame: w.Frame}, nil
	})
}

func (c *runningAvgComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
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
