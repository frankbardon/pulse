package multiplicity

import (
	"math"
	"strings"
	"testing"
)

// padjustRefCase is one R p.adjust reference row (generated into
// reference_values_test.go by testdata/padjust_reference).
type padjustRefCase struct {
	name string
	p    []float64
	want map[Method][]float64
}

// refTol is the relative tolerance against R. Bonferroni / Holm / BH
// agree bit-for-bit; BY's harmonic sum is accumulated in long double by
// R, so its scale can differ in the last ulps.
const refTol = 1e-13

func closeTo(got, want float64) bool {
	if math.IsNaN(want) || math.IsNaN(got) {
		return math.IsNaN(want) && math.IsNaN(got)
	}
	if got == want {
		return true
	}
	return math.Abs(got-want) <= refTol*math.Max(math.Abs(got), math.Abs(want))
}

func TestMultiplicityReferenceValues(t *testing.T) {
	if !strings.Contains(padjustRefProvenance, "R 4.6.1") {
		t.Fatalf("fixture provenance %q, want R 4.6.1", padjustRefProvenance)
	}
	if len(padjustRefCases) == 0 {
		t.Fatal("no reference cases")
	}
	for _, tc := range padjustRefCases {
		for _, method := range Methods() {
			want, ok := tc.want[method]
			if !ok {
				t.Fatalf("%s: fixture lacks method %q", tc.name, method)
			}
			t.Run(tc.name+"/"+string(method), func(t *testing.T) {
				got, err := Adjust(method, tc.p)
				if err != nil {
					t.Fatalf("Adjust: %v", err)
				}
				if len(got) != len(want) {
					t.Fatalf("len %d, want %d", len(got), len(want))
				}
				for i := range want {
					if !closeTo(got[i], want[i]) {
						t.Errorf("[%d] p=%v: got %.17g, want %.17g", i, tc.p[i], got[i], want[i])
					}
				}
			})
		}
	}
}

func TestMultiplicityFixtureCoverage(t *testing.T) {
	// The generator must keep the edge shapes the story pins.
	need := []string{"empty", "single", "all_nan", "ties", "nan_interspersed", "zero_and_one", "large_m"}
	have := map[string]bool{}
	for _, tc := range padjustRefCases {
		have[tc.name] = true
	}
	for _, n := range need {
		if !have[n] {
			t.Errorf("reference fixture lacks case %q", n)
		}
	}
}

func TestAdjustDoesNotMutateInput(t *testing.T) {
	in := []float64{0.04, math.NaN(), 0.01, 0.03, 0.01, 0.9}
	for _, method := range Methods() {
		t.Run(string(method), func(t *testing.T) {
			p := append([]float64(nil), in...)
			got, err := Adjust(method, p)
			if err != nil {
				t.Fatal(err)
			}
			for i := range in {
				if !closeTo(p[i], in[i]) || math.Float64bits(p[i]) != math.Float64bits(in[i]) {
					t.Errorf("input[%d] mutated: %v -> %v", i, in[i], p[i])
				}
			}
			if len(got) > 0 && &got[0] == &p[0] {
				t.Error("result aliases the input slice")
			}
		})
	}
}

func TestAdjustNoneIsExactCopy(t *testing.T) {
	in := []float64{0.3, math.NaN(), 0, 1, 1e-300, 0.1 + 0.2}
	got := None(in)
	for i := range in {
		if math.Float64bits(got[i]) != math.Float64bits(in[i]) {
			t.Errorf("[%d] got %v, want bit-identical %v", i, got[i], in[i])
		}
	}
	via, err := Adjust(MethodNone, in)
	if err != nil {
		t.Fatal(err)
	}
	for i := range in {
		if math.Float64bits(via[i]) != math.Float64bits(in[i]) {
			t.Errorf("Adjust(none)[%d] not bit-identical", i)
		}
	}
}

func TestAdjustProperties(t *testing.T) {
	cases := []struct {
		name string
		p    []float64
	}{
		{"ties", []float64{0.02, 0.01, 0.02, 0.03, 0.01, 0.02}},
		{"nan", []float64{0.01, math.NaN(), 0.04, 0.03, math.NaN(), 0.005}},
		{"cap", []float64{0.3, 0.6, 0.9, 0.45, 0.8}},
		{"all_nan", []float64{math.NaN(), math.NaN()}},
		{"empty", []float64{}},
		{"nil", nil},
	}
	for _, tc := range cases {
		for _, method := range Methods() {
			t.Run(tc.name+"/"+string(method), func(t *testing.T) {
				got, err := Adjust(method, tc.p)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(tc.p) {
					t.Fatalf("len %d, want %d", len(got), len(tc.p))
				}
				for i, v := range tc.p {
					if math.IsNaN(v) {
						if !math.IsNaN(got[i]) {
							t.Errorf("[%d] NaN input adjusted to %v", i, got[i])
						}
						continue
					}
					if got[i] > 1 {
						t.Errorf("[%d] %v exceeds cap", i, got[i])
					}
					if got[i] < v {
						t.Errorf("[%d] adjusted %v below raw %v", i, got[i], v)
					}
					// Equal raw p-values adjust equally (ties by position
					// never change the figure) and order is preserved.
					for j, w := range tc.p {
						if math.IsNaN(w) {
							continue
						}
						if v == w && got[i] != got[j] {
							t.Errorf("tie [%d]=[%d] adjusted %v vs %v", i, j, got[i], got[j])
						}
						if v < w && got[i] > got[j] {
							t.Errorf("monotonicity: p[%d]<p[%d] but %v > %v", i, j, got[i], got[j])
						}
					}
				}
			})
		}
	}
}

func TestFamilySizeExcludesNaN(t *testing.T) {
	cases := []struct {
		p    []float64
		want int
	}{
		{nil, 0},
		{[]float64{math.NaN()}, 0},
		{[]float64{0.1, math.NaN(), 0.2}, 2},
		{[]float64{0, 1, 0.5}, 3},
	}
	for _, tc := range cases {
		if got := FamilySize(tc.p); got != tc.want {
			t.Errorf("FamilySize(%v) = %d, want %d", tc.p, got, tc.want)
		}
	}
	// NaN shrinks m: one finite p among NaNs is a family of one.
	got := Bonferroni([]float64{math.NaN(), 0.03, math.NaN()})
	if got[1] != 0.03 {
		t.Errorf("Bonferroni with m=1 got %v, want 0.03", got[1])
	}
}

func TestMethodValidity(t *testing.T) {
	for _, m := range Methods() {
		if !m.Valid() {
			t.Errorf("%q not Valid", m)
		}
	}
	for _, bad := range []Method{"", "BH", "fdr", "hochberg", "Bonferroni"} {
		if bad.Valid() {
			t.Errorf("%q reported Valid", bad)
		}
		if _, err := Adjust(bad, []float64{0.1}); err == nil {
			t.Errorf("Adjust(%q) returned no error", bad)
		}
	}
}
