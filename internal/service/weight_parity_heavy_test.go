//go:build heavy

package service

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestWeightUnityParity: an all-1.0 weight answers byte-identically to
// no weight, for every weight-aware aggregator × execution arm × weight
// source. The weighted side sheds only what a weight adds — the floor
// keys (pinned to their unity values first: sum_weights = n_eff = n,
// n_weight_invalid = 0), the manifest's optional operator keys and the
// row's declared weight-only keys — and must then marshal to the same
// bytes, so every float matches bit for bit.
func TestWeightUnityParity(t *testing.T) {
	t.Parallel()

	t.Run("coverage", assertParityCoverage)
	aware := manifestAware()
	store := newParityStore(t, "unity", func(n int) []parityRow { return parityRows(n, unityWeight) })
	t.Run("tests", func(t *testing.T) { testUnityParity(t, store) })
	t.Run("regressions", func(t *testing.T) { regUnityParity(t, store) })
	t.Run("groupers", func(t *testing.T) { groupUnityParity(t, store) })
	for _, mode := range parityModes() {
		for _, row := range parityOps {
			t.Run(mode.name+"/"+string(row.op), func(t *testing.T) {
				path := store.paths[mode.cohort]
				base := runArm(t, store, mode, path, row, row.baselineOp(), nil)
				if base == nil {
					t.Logf("%s does not run on the %s arm unweighted; not applicable", row.baselineOp(), mode.name)
					return
				}
				stripSteer(base)
				dropOperatorKeys(base, row.weightNullKeys)
				want := mustMarshal(t, base)
				for _, src := range paritySources() {
					t.Run(src.name, func(t *testing.T) {
						got := runArm(t, store, mode, path, row, row.op, &src)
						stripSteer(got)
						shedUnity(t, got, row, aware[string(row.op)], src.kind)
						if g := mustMarshal(t, got); !bytes.Equal(g, want) {
							t.Errorf("unity weight changed the answer:\n weighted   %s\n unweighted %s", g, want)
						}
					})
				}
			})
		}
	}
}

// TestWeightFrequencyExpansionParity: integer weights (0..3) answer
// like the cohort whose rows are physically duplicated w times, run
// unweighted — every figure within 1e-12 relative, counts and sums of
// integer data exactly, and each weighted slot's sum_weights exactly
// the expanded slot's n. Both kinds: a frequency weight is the
// replication count by definition, and the descriptive figures of an
// integer probability weight are the same numbers — except median /
// percentile (scaleNormalized), whose probability weights are rescaled
// to sum to n, so only their frequency arm expands.
func TestWeightFrequencyExpansionParity(t *testing.T) {
	t.Parallel()

	t.Run("coverage", assertParityCoverage)
	weighted := newParityStore(t, "weighted", func(n int) []parityRow { return parityRows(n, freqWeight) })
	expanded := newParityStore(t, "expanded", func(n int) []parityRow { return expandRows(parityRows(n, freqWeight)) })
	scaled := newParityStore(t, "scaled", func(n int) []parityRow {
		return parityRows(n, func(i int) float64 { return freqWeight(i) * testScaleC })
	})
	t.Run("tests", func(t *testing.T) { testExpansionParity(t, weighted, expanded, scaled) })
	t.Run("regressions", func(t *testing.T) { regExpansionParity(t, weighted, expanded, scaled) })
	t.Run("groupers", func(t *testing.T) { groupExpansionParity(t, weighted, expanded, scaled) })
	kinds := []paritySource{paritySources()[0], paritySources()[1]}
	for _, mode := range parityModes() {
		for _, row := range parityOps {
			t.Run(mode.name+"/"+string(row.op), func(t *testing.T) {
				base := runArm(t, expanded, mode, expanded.paths[mode.cohort], row, row.baselineOp(), nil)
				if base == nil {
					t.Logf("%s does not run on the %s arm unweighted; not applicable", row.baselineOp(), mode.name)
					return
				}
				stripSteer(base)
				for _, src := range kinds {
					t.Run(src.name, func(t *testing.T) {
						if row.scaleNormalized && src.kind == types.WeightKindProbability {
							t.Skip("probability weights are scale-normalized for this operator; see TestWeightProbabilityQuantileScaleInvariance")
						}
						if row.kishScaled && src.kind == types.WeightKindProbability {
							want := runArm(t, weighted, mode, weighted.paths[mode.cohort], row, row.op, &src)
							stripSteer(want)
							got := runArm(t, scaled, mode, scaled.paths[mode.cohort], row, row.op, &src)
							stripSteer(got)
							assertKishScaled(t, got, want, row)
							return
						}
						got := runArm(t, weighted, mode, weighted.paths[mode.cohort], row, row.op, &src)
						stripSteer(got)
						assertExpanded(t, got, base, row)
					})
				}
			})
		}
	}
}
