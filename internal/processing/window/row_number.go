package window

import "github.com/frankbardon/pulse/types"

type rowNumberComputer struct{}

func init() {
	register(types.WIN_ROW_NUMBER, func(_ *types.Window, _ WindowOptions) (WindowComputer, error) {
		return &rowNumberComputer{}, nil
	})
}

func (c *rowNumberComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for i, rowIdx := range part {
			rows[rowIdx][label] = int64(i + 1)
		}
	}
	return nil
}
