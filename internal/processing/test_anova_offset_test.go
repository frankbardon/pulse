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

// exactMoments returns each group's mean minus ref and its sample
// variance, computed exactly on the given doubles and rounded once.
func exactMoments(groups [][]float64, ref float64) (meansMinusRef, variances []float64) {
	r := new(big.Rat).SetFloat64(ref)
	for _, g := range groups {
		var s big.Rat
		for _, v := range g {
			s.Add(&s, new(big.Rat).SetFloat64(v))
		}
		m := new(big.Rat).Quo(&s, new(big.Rat).SetInt64(int64(len(g))))
		var ss big.Rat
		for _, v := range g {
			e := new(big.Rat).Sub(new(big.Rat).SetFloat64(v), m)
			ss.Add(&ss, e.Mul(e, e))
		}
		vr, _ := new(big.Rat).Quo(&ss, new(big.Rat).SetInt64(int64(len(g)-1))).Float64()
		d, _ := new(big.Rat).Sub(m, r).Float64()
		meansMinusRef = append(meansMinusRef, d)
		variances = append(variances, vr)
	}
	return meansMinusRef, variances
}

// offsetGroups feeds devs (+ offset) to rt, split by g1, g2, ... and
// returns the doubles it fed.
func offsetGroups(t *testing.T, rt RowTest, offset float64, devs [][]float64) [][]float64 {
	t.Helper()
	schema := twoSampleFixtureSchema()
	groups := make([][]float64, len(devs))
	for i, ds := range devs {
		for _, d := range ds {
			v := offset + d
			groups[i] = append(groups[i], v)
			split := ""
			if len(devs) > 1 {
				split = []string{"g1", "g2", "g3"}[i]
			}
			var r *Record
			if split == "" {
				r = newTestRecord(t, schema, map[string]float64{"revenue": v}, "", "")
			} else {
				r = newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", split)
			}
			if err := rt.UpdateRow(r); err != nil {
				t.Fatal(err)
			}
		}
	}
	return groups
}

func offsetClose(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol*math.Max(math.Abs(want), 1e-300) {
		t.Errorf("%s = %.17g, exact on the input %.17g (rel %.2g)", what, got, want, math.Abs(got-want)/math.Abs(want))
	}
}

// TestMeanTests_LargeOffsetMatchesExact: the one-sample t statistic and
// the Welch ANOVA F hold their precision under a large common offset
// (#636), against exact moments of the input doubles. The two-sample t
// and z keep byte parity with their overlays instead (#637).
func TestMeanTests_LargeOffsetMatchesExact(t *testing.T) {
	t.Parallel()
	one := [][]float64{{0.3, 0.5, 0.4, 0.6, 0.2, 0.45}}
	three := [][]float64{{0.3, 0.5, 0.4, 0.6, 0.2}, {0.4, 0.6, 0.5, 0.75, 0.3}, {0.5, 0.7, 0.65, 0.8, 0.4}}
	schema := twoSampleFixtureSchema()
	for _, offset := range []float64{0, 1e6, 1e12} {
		// One-sample t against mu = offset + 0.45.
		mu := offset + 0.45
		rt, err := newTTestRow(&types.Test{Type: types.TEST_T, Field: "revenue", Alpha: 0.05, Params: []byte(`{"mu":` + big.NewFloat(mu).Text('g', 17) + `}`)}, schema)
		if err != nil {
			t.Fatal(err)
		}
		groups := offsetGroups(t, rt, offset, one)
		res, err := rt.Finalize()
		if err != nil {
			t.Fatalf("one-sample offset %g: %v", offset, err)
		}
		m, v := exactMoments(groups, mu)
		offsetClose(t, "one-sample statistic", res.Statistic, m[0]/math.Sqrt(v[0]/float64(len(groups[0]))), 1e-9)

		// Welch ANOVA F from exact moments.
		rw, err := newAnovaWelchRow(&types.Test{Type: types.TEST_ANOVA_WELCH, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}, schema)
		if err != nil {
			t.Fatal(err)
		}
		groups = offsetGroups(t, rw, offset, three)
		res, err = rw.Finalize()
		if err != nil {
			t.Fatalf("welch anova offset %g: %v", offset, err)
		}
		m, v = exactMoments(groups, offset)
		k := float64(len(groups))
		var W, wm float64
		w := make([]float64, len(groups))
		for i := range groups {
			w[i] = float64(len(groups[i])) / v[i]
			W += w[i]
			wm += w[i] * m[i]
		}
		wm /= W
		var num, tail float64
		for i := range groups {
			d := m[i] - wm
			num += w[i] * d * d
			sh := 1 - w[i]/W
			tail += sh * sh / float64(len(groups[i])-1)
		}
		num /= k - 1
		offsetClose(t, "welch anova F", res.Statistic, num/(1+2*(k-2)/(k*k-1)*tail), 1e-9)
		offsetClose(t, "welch anova weighted_mean", res.Details["weighted_mean"].(float64), offset+wm, 1e-15)
	}
}
