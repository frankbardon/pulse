package synth

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
)

// withinSetStrengthFloor is the bias-corrected Cramér's V a within-set
// option pair must reach to be captured. For a 2x2 table V is |φ|, the
// Pearson correlation of the two option bits, so V ≥ 0.1 is φ² ≥ 0.01 —
// the same "explains at least 1% of the variance" floor model predictor
// selection applies to adjusted r² (profile_models_select.go). Below it
// the two options are independent for every practical purpose and the
// pair would only replace the option's marginal draw with a conditional
// that reproduces the same marginal.
const withinSetStrengthFloor = 0.1

// withinSetAcc accumulates one set field's option co-selection counts
// over its non-null rows: sel[i] rows selecting option i, co[i*k+j]
// (i < j) rows selecting both. The 2x2 table of any option pair follows
// from these and n, so the per-row cost is the square of the row's
// selected-option count rather than of the dictionary size.
type withinSetAcc struct {
	k   int
	n   int
	sel []int
	co  []int
}

func newWithinSetAcc(k int) *withinSetAcc {
	return &withinSetAcc{k: k, sel: make([]int, k), co: make([]int, k*k)}
}

func (a *withinSetAcc) add(mask encoding.SetMask) {
	a.n++
	var on []int
	for i := range mask.Bits() {
		if i >= a.k {
			break
		}
		on = append(on, i)
		a.sel[i]++
	}
	for x := 0; x < len(on); x++ {
		for y := x + 1; y < len(on); y++ {
			a.co[on[x]*a.k+on[y]]++
		}
	}
}

// table returns option i x option j's 2x2 counts keyed
// (option i state, option j state), omitting empty cells exactly as the
// online set-pair accumulators do — so a structural zero (tv never
// selected without broadband) is an ABSENT cell, which the pair sampler
// reads as probability zero.
func (a *withinSetAcc) table(i, j int) map[[2]string]int {
	both := a.co[i*a.k+j]
	counts := map[[2]string]int{}
	put := func(ai, bj string, c int) {
		if c > 0 {
			counts[[2]string{ai, bj}] = c
		}
	}
	put("selected", "selected", both)
	put("selected", "not_selected", a.sel[i]-both)
	put("not_selected", "selected", a.sel[j]-both)
	put("not_selected", "not_selected", a.n-a.sel[i]-a.sel[j]+both)
	return counts
}

// computeWithinSetPairs captures the dependencies between options of
// ONE set field. For every option j it keeps at most one pair, from the
// earlier-declared option i < j with the strongest association
// (bias-corrected Cramér's V, ties to the lowest i), and only when that
// strength reaches withinSetStrengthFloor.
//
// One source per target is what generation can use: a target option is
// drawn by exactly one pair (resolveConflicts), so capturing every i < j
// combination would cost O(k²) pairs — 32,640 on a 256-member set — for
// a single survivor each. The direction (i before j, dictionary order)
// makes every chain of within-set pairs run source-before-target in
// declaration order. The SetSetPairProfile entries returned carry
// SetA == SetB.
func computeWithinSetPairs(setFields []string, setOptionNames map[string][]string, accs map[string]*withinSetAcc, warnings *[]string) []SetSetPairProfile {
	var pairs []SetSetPairProfile
	for _, sf := range setFields {
		acc := accs[sf]
		if acc == nil || acc.n == 0 {
			continue
		}
		names := setOptionNames[sf]
		for j := 1; j < acc.k; j++ {
			if names[j] == "" {
				continue
			}
			best, bestV := -1, 0.0
			for i := 0; i < j; i++ {
				if names[i] == "" {
					continue
				}
				v := contingencyStrength(cellSpecs(collapseCells(acc.table(i, j), ContingencyCellCap)))
				if best < 0 || v > bestV {
					best, bestV = i, v
				}
			}
			if best < 0 || bestV < withinSetStrengthFloor {
				continue
			}
			cells := collapseCells(acc.table(best, j), ContingencyCellCap)
			for _, c := range cells {
				aLabel := fmt.Sprintf("%s[%s]=%s", sf, names[best], c.AValue)
				bLabel := fmt.Sprintf("%s[%s]=%s", sf, names[j], c.BValue)
				if w := thinPairWarning("set-set", aLabel, bLabel, c.Count, MinPairObservations); w != "" {
					*warnings = append(*warnings, w)
				}
			}
			pairs = append(pairs, SetSetPairProfile{
				SetA: sf, OptionA: names[best], SetB: sf, OptionB: names[j], Cells: cells, N: acc.n,
			})
		}
	}
	return pairs
}

// cellSpecs converts captured contingency cells to their spec shape.
func cellSpecs(cells []ContingencyCell) []CategoricalPairCellSpec {
	out := make([]CategoricalPairCellSpec, len(cells))
	for i, c := range cells {
		out[i] = CategoricalPairCellSpec(c)
	}
	return out
}
