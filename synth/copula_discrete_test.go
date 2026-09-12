package synth_test

import (
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// TestSpecFromProfile_SmallIntegerPairsReachTheCorrelationMatrix is the
// gate at the level the defect actually bit: isNumericFieldType listed
// `nullable_u4` / `nullable_u8` / `nullable_u16` (spellings no Spec can
// declare) and omitted `u4` (the spelling encoding.FieldType.String()
// emits), so addCorrelation dropped every u4 pair on the floor. On the
// motivating survey cohort that was ALL 16 captured numeric pairs and 11
// of 14 integer columns, leaving Spec.Correlations empty with nothing
// anywhere saying so.
//
// packed_bool is asserted in the same table because it lands in the same
// numeric accumulator and carried one of that cohort's 16 pairs
// (nps x detractor); leaving it out would have reproduced the bug one
// type over.
func TestSpecFromProfile_SmallIntegerPairsReachTheCorrelationMatrix(t *testing.T) {
	num := func(name, typ string, mean, std, min, max float64) synth.FieldProfile {
		return synth.FieldProfile{
			Name: name, Type: typ,
			Numeric: &synth.NumericProfile{Min: min, Max: max, Mean: mean, Std: std},
		}
	}
	p := &synth.Profile{
		RowCount: 1000,
		Fields: []synth.FieldProfile{
			num("scale_u4", "u4", 4, 1.5, 1, 7),
			num("count_u8", "u8", 40, 12, 0, 200),
			num("flag", "packed_bool", 0.6, 0.49, 0, 1),
			num("amount", "f64", 10, 3, 0, 100),
			{Name: "region", Type: "categorical_u8", Categorical: &synth.CategoricalProfile{
				Cardinality: 2,
				Top: []synth.CategoryHit{
					{Value: "north", Weight: 0.5},
					{Value: "south", Weight: 0.5},
				},
			}},
		},
		Conditional: &synth.ConditionalProfile{
			NumericPairs: []synth.NumericPairProfile{
				{A: "scale_u4", B: "count_u8", Rho: 0.6, N: 1000},
				{A: "scale_u4", B: "flag", Rho: -0.4, N: 1000},
				{A: "amount", B: "count_u8", Rho: 0.3, N: 1000},
			},
		},
	}

	spec, _ := synth.SpecFromProfile(p, 1000)
	got := map[string]float64{}
	for _, c := range spec.Correlations {
		got[c.A+"|"+c.B] = c.Correlation
	}
	for _, want := range []struct {
		key string
		rho float64
	}{
		{"scale_u4|count_u8", 0.6},
		{"scale_u4|flag", -0.4},
		{"amount|count_u8", 0.3},
	} {
		rho, ok := got[want.key]
		if !ok {
			t.Errorf("captured pair %s never reached Spec.Correlations — "+
				"a numeric type the profiler measures must be a type the copula accepts", want.key)
			continue
		}
		if math.Abs(rho-want.rho) > 1e-12 {
			t.Errorf("pair %s: rho %v, want %v", want.key, rho, want.rho)
		}
	}
	if len(spec.Correlations) != 3 {
		t.Errorf("Spec.Correlations has %d entries, want 3: %+v", len(spec.Correlations), spec.Correlations)
	}
}

// TestSynthCorrelation_DiscreteMarginsHoldAndCorrelate asserts BOTH
// halves of what a `discrete` copula participant produces, in one test,
// because each is trivially satisfiable by abandoning the other: drawing
// each field independently keeps the marginals perfectly and correlates
// nothing, while overwriting one field with a rescaled copy of the other
// correlates perfectly and destroys the marginal.
//
// The attenuation is real and is the documented cost of a staircase Q:
// the requested rho lives on the LATENT, and discretising to seven
// levels loses some of it on the value scale. Measured on the motivating
// cohort's own regard x meaningfulness pair (captured rho +0.8400,
// 200,000 rows): Pearson +0.8072, Spearman +0.8051, marginals within
// 0.003. The bands below are wide enough to be seed-robust and narrow
// enough that "no correlation applied" — the pre-fix behaviour, a
// realised rho of ~0 — fails.
func TestSynthCorrelation_DiscreteMarginsHoldAndCorrelate(t *testing.T) {
	const rows = 20000
	const rho = 0.84
	levels := []float64{1, 2, 3, 4, 5, 6, 7}
	weightsA := []float64{0.089, 0.097, 0.120, 0.237, 0.184, 0.135, 0.138}
	weightsB := []float64{0.130, 0.109, 0.130, 0.227, 0.169, 0.120, 0.115}

	field := func(name string, w []float64) synth.FieldSpec {
		vals := make([]any, len(levels))
		ws := make([]any, len(w))
		for i := range levels {
			vals[i] = levels[i]
			ws[i] = w[i]
		}
		return synth.FieldSpec{
			Name: name, Type: "u4", Distribution: "discrete",
			Params: map[string]any{"values": vals, "weights": ws},
		}
	}
	spec := &synth.Spec{
		RowCount:     rows,
		Fields:       []synth.FieldSpec{field("a", weightsA), field("b", weightsB)},
		Correlations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: rho}},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	xs := readField(t, data, "a")
	ys := readField(t, data, "b")
	if len(xs) != rows {
		t.Fatalf("read %d rows, want %d", len(xs), rows)
	}

	// Half one: the dependence is induced.
	got := pearsonCorr(xs, ys)
	if got < 0.70 || got > rho+0.02 {
		t.Errorf("realised Pearson %.4f for a requested rho of %.2f — outside "+
			"[0.70, %.2f]; ~0 means the pair never reached the copula, and a "+
			"value at or above the request means something overwrote a marginal",
			got, rho, rho+0.02)
	}
	if s := spearmanCorr(xs, ys); s < 0.70 {
		t.Errorf("realised Spearman %.4f for a requested rho of %.2f — a staircase "+
			"Q attenuates, but the RANK ordering is the part that must survive", s, rho)
	}

	// Half two: each participant's own marginal is untouched by it.
	for name, want := range map[string][]float64{"a": weightsA, "b": weightsB} {
		col := xs
		if name == "b" {
			col = ys
		}
		hist := map[float64]int{}
		for _, v := range col {
			hist[v]++
		}
		for i, lv := range levels {
			share := float64(hist[lv]) / float64(rows)
			if math.Abs(share-want[i]) > 0.01 {
				t.Errorf("field %s level %g: generated share %.4f, declared %.4f — "+
					"the copula must preserve each participant's own marginal",
					name, lv, share, want[i])
			}
		}
	}
}

func pearsonCorr(x, y []float64) float64 {
	n := float64(len(x))
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= n
	my /= n
	var sxy, sxx, syy float64
	for i := range x {
		dx, dy := x[i]-mx, y[i]-my
		sxy += float64(dx * dy)
		sxx += float64(dx * dx)
		syy += float64(dy * dy)
	}
	return sxy / math.Sqrt(float64(sxx*syy))
}

func spearmanCorr(x, y []float64) float64 {
	return pearsonCorr(averageRanks(x), averageRanks(y))
}

func averageRanks(v []float64) []float64 {
	idx := make([]int, len(v))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return v[idx[a]] < v[idx[b]] })
	out := make([]float64, len(v))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && v[idx[j+1]] == v[idx[i]] {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			out[idx[k]] = avg
		}
		i = j + 1
	}
	return out
}
