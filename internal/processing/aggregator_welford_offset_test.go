package processing

import (
	"math"
	"math/big"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// exactWeightedMoments is the weighted mean, the mean minus ref and
// Σw·(x − mean)² of the given doubles in exact rational arithmetic
// (w nil = unit weights), each rounded once.
func exactWeightedMoments(xs, ws []float64, ref float64) (mean, meanMinusRef, m2 float64) {
	var sw, swx big.Rat
	for i, x := range xs {
		w := new(big.Rat).SetInt64(1)
		if ws != nil {
			w.SetFloat64(ws[i])
		}
		sw.Add(&sw, w)
		swx.Add(&swx, new(big.Rat).Mul(w, new(big.Rat).SetFloat64(x)))
	}
	m := new(big.Rat).Quo(&swx, &sw)
	var ss big.Rat
	for i, x := range xs {
		w := new(big.Rat).SetInt64(1)
		if ws != nil {
			w.SetFloat64(ws[i])
		}
		e := new(big.Rat).Sub(new(big.Rat).SetFloat64(x), m)
		ss.Add(&ss, e.Mul(e, e).Mul(e, w))
	}
	mean, _ = m.Float64()
	meanMinusRef, _ = new(big.Rat).Sub(m, new(big.Rat).SetFloat64(ref)).Float64()
	m2, _ = ss.Float64()
	return mean, meanMinusRef, m2
}

// welfordOffsetRows is 60 rows at offset + d, d on a 2^-6 grid, with
// integer frequency weights 1..4: every value and weight exact, so the
// exact moments are those of the input doubles.
func welfordOffsetRows(offset float64) (xs, ws []float64) {
	for i := range 60 {
		d := float64((i*37)%23-11) / 64
		xs = append(xs, offset+d)
		ws = append(ws, float64(1+(i*13)%4))
	}
	return xs, ws
}

// TestWelford_LargeOffsetMergeMatchesExact: AGG_WELFORD accumulates
// about its first value (#637), and a merge re-expresses the other
// partial about the receiver's origin, so three partials with three
// different origins merge to the exact moments of the input under a
// 1e12 common offset — unweighted and frequency-weighted. Unshifted,
// the merged m2 was off by 1e-6 relative there and shifted_mean did
// not exist; the origin is the first partial's first value.
func TestWelford_LargeOffsetMergeMatchesExact(t *testing.T) {
	t.Parallel()

	for _, offset := range []float64{0, 1e8, 1e12} {
		xs, ws := welfordOffsetRows(offset)
		for _, weighted := range []bool{false, true} {
			spec := &types.Aggregation{Type: types.AGG_WELFORD, Field: "value", Label: "x"}
			var wantW []float64
			if weighted {
				spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: types.WeightKindFrequency})
				wantW = ws
			}
			parts := make([]MergeableAggregator, 3)
			for p := range parts {
				agg, err := aggregatorRegistry[types.AGG_WELFORD](spec, cohortSchema())
				if err != nil {
					t.Fatal(err)
				}
				parts[p] = agg.(MergeableAggregator)
			}
			for i, x := range xs {
				if err := parts[i*3/len(xs)].UpdateRow(cohortRec(x, ws[i], 0, 0, nil), "value"); err != nil {
					t.Fatal(err)
				}
			}
			for _, p := range parts[1:] {
				if err := parts[0].MergeOnline(p); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := parts[0].Finalize(); err != nil {
				t.Fatal(err)
			}
			comps, err := parts[0].(MetaAggregator).Components()
			if err != nil {
				t.Fatal(err)
			}
			wantMean, wantShifted, wantM2 := exactWeightedMoments(xs, wantW, xs[0])
			name := map[bool]string{false: "unweighted", true: "weighted"}[weighted]
			if got := comps["origin"].(float64); got != xs[0] {
				t.Errorf("offset %g %s: origin %v, want the first value %v", offset, name, got, xs[0])
			}
			if got := comps["m2"].(float64); math.Abs(got-wantM2) > 1e-13*wantM2 {
				t.Errorf("offset %g %s: m2 %.17g, exact %.17g (rel %.2g)", offset, name, got, wantM2, math.Abs(got-wantM2)/wantM2)
			}
			if got := comps["shifted_mean"].(float64); math.Abs(got-wantShifted) > 1e-15 {
				t.Errorf("offset %g %s: shifted_mean %.17g, exact %.17g", offset, name, got, wantShifted)
			}
			if got := comps["mean"].(float64); math.Abs(got-wantMean) > 1e-15*math.Max(1, math.Abs(wantMean)) {
				t.Errorf("offset %g %s: mean %.17g, exact %.17g", offset, name, got, wantMean)
			}
		}
	}
}
