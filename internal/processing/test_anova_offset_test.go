package processing

import (
	"math"
	"math/big"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// exactANOVASS is the one-way SSB and SSW of the given doubles computed
// in exact rational arithmetic: the figure an error-free algorithm
// returns for this input, independent of how the input was rounded.
func exactANOVASS(groups [][]float64) (ssb, ssw float64) {
	var total big.Rat
	n := 0
	means := make([]*big.Rat, len(groups))
	for i, g := range groups {
		var s big.Rat
		for _, v := range g {
			s.Add(&s, new(big.Rat).SetFloat64(v))
		}
		total.Add(&total, &s)
		n += len(g)
		means[i] = new(big.Rat).Quo(&s, new(big.Rat).SetInt64(int64(len(g))))
	}
	grand := new(big.Rat).Quo(&total, new(big.Rat).SetInt64(int64(n)))
	var b, w big.Rat
	for i, g := range groups {
		d := new(big.Rat).Sub(means[i], grand)
		d.Mul(d, d)
		b.Add(&b, d.Mul(d, new(big.Rat).SetInt64(int64(len(g)))))
		for _, v := range g {
			e := new(big.Rat).Sub(new(big.Rat).SetFloat64(v), means[i])
			w.Add(&w, e.Mul(e, e))
		}
	}
	ssb, _ = b.Float64()
	ssw, _ = w.Float64()
	return ssb, ssw
}

// TestAnovaF_LargeOffsetMatchesExact: a large common offset (NIST
// SmLs07-09's 1e12 + small deviations) must not cost the sums of
// squares their precision. The input doubles are what they are; the
// test holds the arithmetic to the exact result on those doubles.
func TestAnovaF_LargeOffsetMatchesExact(t *testing.T) {
	t.Parallel()

	for _, offset := range []float64{0, 1e6, 1e12} {
		schema := twoSampleFixtureSchema()
		rt, err := newAnovaRow(&types.Test{Type: types.TEST_ANOVA_F, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}, schema)
		if err != nil {
			t.Fatal(err)
		}
		labels := []string{"g1", "g2", "g3"}
		devs := [][]float64{{0.3, 0.5, 0.4, 0.6, 0.2}, {0.4, 0.6, 0.5, 0.7, 0.3}, {0.5, 0.7, 0.6, 0.8, 0.4}}
		groups := make([][]float64, len(devs))
		for i, ds := range devs {
			for _, d := range ds {
				v := offset + d
				groups[i] = append(groups[i], v)
				if err := rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", labels[i])); err != nil {
					t.Fatal(err)
				}
			}
		}
		res, err := rt.Finalize()
		if err != nil {
			t.Fatalf("offset %g: %v", offset, err)
		}
		wantB, wantW := exactANOVASS(groups)
		for _, c := range []struct {
			name      string
			got, want float64
		}{{"ss_between", res.Details["ss_between"].(float64), wantB}, {"ss_within", res.Details["ss_within"].(float64), wantW}} {
			if rel := math.Abs(c.got-c.want) / c.want; rel > 1e-9 {
				t.Errorf("offset %g: %s = %.17g, exact on the input %.17g (rel %.2g)", offset, c.name, c.got, c.want, rel)
			}
		}
		means := res.Details["group_means"].([]float64)
		for i, g := range groups {
			var s float64
			for _, v := range g {
				s += v - offset
			}
			if want := offset + s/float64(len(g)); math.Abs(means[i]-want) > 1e-15*math.Max(1, math.Abs(want)) {
				t.Errorf("offset %g: group_means[%d] = %.17g, want %.17g", offset, i, means[i], want)
			}
		}
	}
}
