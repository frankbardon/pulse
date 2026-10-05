package weighting

import (
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// unweightedWelford is the reference unweighted recurrence (the shape
// of internal/processing's welfordBucket) the weighted bucket must
// reproduce bit for bit at w ≡ 1.
type unweightedWelford struct {
	n        int64
	mean, m2 float64
}

func (b *unweightedWelford) add(v float64) {
	b.n++
	delta := v - b.mean
	b.mean += delta / float64(b.n)
	b.m2 += delta * (v - b.mean)
}

// TestWelford_UnityBitIdentical: w ≡ 1 reproduces the unweighted
// recurrence — mean, M2 and the variance — bit for bit, under the
// unweighted and frequency bases (and probability, whose n_eff is then
// n exactly).
func TestWelford_UnityBitIdentical(t *testing.T) {
	var u unweightedWelford
	var w Welford
	x := 0.1
	for i := range 400 {
		x = math.Mod(x*7.31+float64(i%13)*0.173, 97.3) - 11.7
		u.add(x)
		w.Add(x, 1)
	}
	if w.N != u.n || w.SumW != float64(u.n) {
		t.Fatalf("n %d/%v vs %d", w.N, w.SumW, u.n)
	}
	if math.Float64bits(w.Mean) != math.Float64bits(u.mean) || math.Float64bits(w.M2) != math.Float64bits(u.m2) {
		t.Fatalf("moments differ: mean %v/%v m2 %v/%v", w.Mean, u.mean, w.M2, u.m2)
	}
	want := u.m2 / float64(u.n-1)
	for _, b := range []Basis{Unweighted, Frequency, Probability} {
		if got := w.Variance(b); math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("basis %v: variance %v, want %v", b, got, want)
		}
		if got := w.NStar(b); got != float64(u.n) {
			t.Errorf("basis %v: N* %v, want %d", b, got, u.n)
		}
	}
}

// TestWelford_FrequencyIsExpansion: integer weights equal physically
// repeated rows (N* = Σw, variance m2/(Σw−1)).
func TestWelford_FrequencyIsExpansion(t *testing.T) {
	xs := []float64{2, 5, 9, 4}
	ws := []float64{3, 1, 2, 4}
	var w, e Welford
	for i, x := range xs {
		w.Add(x, ws[i])
		for range int(ws[i]) {
			e.Add(x, 1)
		}
	}
	if w.NStar(Frequency) != 10 || e.NStar(Unweighted) != 10 {
		t.Fatalf("N* %v / %v", w.NStar(Frequency), e.NStar(Unweighted))
	}
	if !approxEq(w.Mean, e.Mean) || !approxEq(w.Variance(Frequency), e.Variance(Unweighted)) {
		t.Fatalf("mean %v/%v var %v/%v", w.Mean, e.Mean, w.Variance(Frequency), e.Variance(Unweighted))
	}
}

// TestWelford_ProbabilityKish: hand-computed Kish case. x = {1, 3, 8},
// w = {1, 2, 1}: Σw = 4, Σw² = 6, n_eff = 16/6 = 8/3, mean = 15/4,
// M2 = 1·(2.75)² + 2·(0.75)² + 1·(4.25)² = 26.75, c = n_eff/Σw = 2/3,
// s² = c·M2/(n_eff−1) = (53.5/3)/(5/3) = 10.7.
func TestWelford_ProbabilityKish(t *testing.T) {
	var w Welford
	for i, x := range []float64{1, 3, 8} {
		w.Add(x, []float64{1, 2, 1}[i])
	}
	if !approxEq(w.NEff(), 8.0/3) || !approxEq(w.NStar(Probability), 8.0/3) || w.NStar(Frequency) != 4 {
		t.Fatalf("n_eff %v N*f %v", w.NEff(), w.NStar(Frequency))
	}
	if !approxEq(w.Mean, 3.75) || !approxEq(w.M2, 26.75) {
		t.Fatalf("mean %v m2 %v", w.Mean, w.M2)
	}
	if got := w.Variance(Probability); !approxEq(got, 10.7) {
		t.Fatalf("probability variance %v, want 10.7", got)
	}
	if got := w.Variance(Frequency); !approxEq(got, 26.75/3) {
		t.Fatalf("frequency variance %v, want %v", got, 26.75/3)
	}
	if got := Probability.Scale(w.SumW, w.SumWSq); !approxEq(got, 2.0/3) {
		t.Fatalf("scale %v", got)
	}
	if Frequency.Scale(4, 6) != 1 || Unweighted.Scale(4, 6) != 1 || Probability.Scale(0, 0) != 0 {
		t.Fatal("scale identities")
	}
	var one Welford
	one.Add(5, 2)
	if one.Variance(Probability) != 0 || one.Variance(Frequency) != 0 {
		t.Fatal("a single-row bucket must report variance 0 under probability (n_eff 1)")
	}
}

func TestBasisOf(t *testing.T) {
	cases := []struct {
		spec *types.WeightSpec
		want Basis
	}{
		{nil, Unweighted},
		{&types.WeightSpec{Field: "w"}, Probability},
		{&types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}, Probability},
		{&types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}, Frequency},
	}
	for _, c := range cases {
		if got := BasisOf(c.spec); got != c.want {
			t.Errorf("BasisOf(%v) = %v, want %v", c.spec, got, c.want)
		}
	}
	if Unweighted.Weighted() || !Frequency.Weighted() || !Probability.Weighted() {
		t.Fatal("Weighted()")
	}
}

func approxEq(a, b float64) bool {
	return math.Abs(a-b) <= 1e-12*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// TestScaledVariance: the moment-holder's variance — what an overlay
// reads off a host cell's {m2, sum_weights, n_eff} — is the bucket's
// Variance under every basis, and c·M2/(N* − 1) in closed form.
func TestScaledVariance(t *testing.T) {
	var w Welford
	for i, x := range []float64{3, 7, 4, 9, 12} {
		w.Add(x, []float64{1, 3, 2, 1, 4}[i])
	}
	for _, b := range []Basis{Unweighted, Frequency, Probability} {
		if got, want := ScaledVariance(b, w.M2, w.SumW, w.NStar(b)), w.Variance(b); got != want {
			t.Fatalf("basis %v: %v, want %v bit for bit", b, got, want)
		}
	}
	nEff := w.NEff()
	if got, want := ScaledVariance(Probability, w.M2, w.SumW, nEff), (nEff/w.SumW)*w.M2/(nEff-1); !approxEq(got, want) {
		t.Fatalf("probability %v, want %v", got, want)
	}
	if ScaledVariance(Probability, 5, 2, 1) != 0 || ScaledVariance(Frequency, 5, 1, 1) != 0 {
		t.Fatal("N* ≤ 1 must report 0")
	}
}

// TestNSourceRefusal: the weighted-host n_source rule — unweighted
// counts (raw rows, n_within slabs, distinct keys; pairwise and panel
// spellings) refused under both kinds, the weight-sum sources under
// probability only, nothing on an unweighted host, for an omitted
// source or for the panel's payload-margin modes.
func TestNSourceRefusal(t *testing.T) {
	unweightedCounts := []string{"cell_n_unweighted", "row_margin_n", "column_margin_n", "n_within",
		"n_within_distinct", "row_margin_distinct", "column_margin_distinct", "row_margin_distinct_within"}
	refused := map[Basis][]string{
		Unweighted:  nil,
		Frequency:   unweightedCounts,
		Probability: append(append([]string(nil), unweightedCounts...), "cell_weight_sum", "cell_value_weighted"),
	}
	all := append(append([]string(nil), unweightedCounts...), "", "cell_weight_sum", "cell_value_weighted",
		"row_margin_value", "row_margin_value_within")
	for basis, want := range refused {
		for _, s := range all {
			isRefused := false
			for _, r := range want {
				isRefused = isRefused || r == s
			}
			if got := NSourceRefusal(s, basis) != ""; got != isRefused {
				t.Errorf("basis %v n_source %q: refused %v, want %v", basis, s, got, isRefused)
			}
		}
	}
}

// TestHiddenFloorRefusal: the floor-scaling kinds are exactly the χ²
// and Compose proportion kinds (the pairwise proportion kind already
// needs components to run at all; Fisher has no probability form; the
// mean kinds read their own legs), and the refusal names the overlay
// slot, the kind and the host.
func TestHiddenFloorRefusal(t *testing.T) {
	want := map[types.OverlayKind]bool{
		types.OverlayKindChiSqRow: true, types.OverlayKindChiSqCol: true, types.OverlayKindChiSqMatrix: true,
		types.OverlayKindChiSqVsRef: true, types.OverlayKindPropZCell: true, types.OverlayKindPropZPanel: true,
	}
	for _, k := range types.AllOverlayKinds() {
		if ScalesByHostFloor(k) != want[k] {
			t.Errorf("ScalesByHostFloor(%s) = %v, want %v", k, !want[k], want[k])
		}
	}
	msg, d := HiddenFloorRefusal("overlays[2]", types.OverlayKindChiSqVsRef, "requests[1]")
	for _, s := range []string{"overlays[2]", "OVERLAY_CHISQ_VS_REF", "requests[1]", "components disabled"} {
		if !strings.Contains(msg, s) {
			t.Errorf("message %q does not name %q", msg, s)
		}
	}
	if d["slot"] != "overlays[2]" || d["operator"] != "OVERLAY_CHISQ_VS_REF" || d["host"] != "requests[1]" ||
		d["kind"] != "probability" || d["reason"] != "components_disabled" {
		t.Errorf("details %v", d)
	}
}
