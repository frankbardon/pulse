package window

import (
	"github.com/frankbardon/pulse/internal/distlagparams"
	"github.com/frankbardon/pulse/types"
)

type distributedLagComputer struct {
	field   string
	weights []float64
}

func init() {
	register(types.WIN_DISTRIBUTED_LAG, func(w *types.Window, _ WindowOptions) (WindowComputer, error) {
		// The params reading is shared with predict (internal/distlagparams
		// through internal/windowparams), so both refuse the same request
		// with the same message.
		spec, ref := distlagparams.Resolve(w.Params)
		if ref != nil {
			return nil, ref.Coded()
		}
		return &distributedLagComputer{field: w.Field, weights: spec.Weights}, nil
	})
}

// Compute writes, per row i of each ordered partition, the finite
// convolution y_i = Σ_{l=0..max_lag} w_l · x_{i−l}. A lag that reaches
// before the partition's first row contributes nothing (the carry-over
// starts from zero), and so does a null value at a lagged row: the
// weights are never renormalised over the values present. A row whose
// own value is null emits null — the WIN_EWMA convention, so rows
// preceding the partition's first value are null too.
//
// The sum runs lag 0 first; each product is an explicit float64 so the
// kernel never fuses into an FMA and reads the same on every arch.
func (c *distributedLagComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	var xs []float64
	var present []bool
	for _, part := range partitions {
		xs = append(xs[:0], make([]float64, len(part))...)
		present = append(present[:0], make([]bool, len(part))...)
		for i, rowIdx := range part {
			xs[i], present[i] = cellFloat(rows[rowIdx], c.field)
		}
		for i, rowIdx := range part {
			if !present[i] {
				rows[rowIdx][label] = nil
				continue
			}
			acc := 0.0
			for l := 0; l < len(c.weights) && l <= i; l++ {
				if present[i-l] {
					acc += float64(c.weights[l] * xs[i-l])
				}
			}
			rows[rowIdx][label] = acc
		}
	}
	return nil
}
