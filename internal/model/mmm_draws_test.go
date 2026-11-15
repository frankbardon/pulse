package model

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// drawsInline is an inline REG_MMM_BAYES model carrying outputs.draws:
// two chains of two draws over its coefficient table, at level 0.9.
const drawsInline = `{"model_format_version": 1, "type": "REG_MMM_BAYES", "target": "sales",
	"predictors": ["tv"], "terms": ["tv"], "features": ["adstock", "outputs"],
	"adstock": {"tv": {"kernel": "geometric", "decay": 0.5, "max_lag": 4, "normalize": false}},
	"coefficients": {"(intercept)": 10, "tv": 3, "decay:tv": 0.5, "k:tv": 50, "s:tv": 1.5, "sigma": 2},
	"outputs": {"draws": {"names": ["(intercept)", "tv", "decay:tv", "k:tv", "s:tv", "sigma"],
		"draws": [[[9, 11], [2, 4], [0.4, 0.6], [40, 60], [1.4, 1.6], [2, 2]],
		          [[10, 10], [2.5, 3.5], [0.3, 0.7], [45, 55], [1.5, 1.5], [2, 2]]],
		"thin": 1, "credible_level": 0.9}}}`

// drawsSet binds drawsInline as "m".
func drawsSet(t *testing.T) *Set {
	t.Helper()
	rs := Refs{Models: []types.ModelRef{inlineRef("m", drawsInline)}}
	if cerr := CheckRefs(rs, nil); cerr != nil {
		t.Fatal(cerr)
	}
	loaded, cerr := LoadFiles(rs, nil, stubInline)
	if cerr != nil {
		t.Fatal(cerr)
	}
	set, err := (*Set)(nil).WithLoaded(rs, loaded)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// TestWithMMMDraw_BindsOneDraw: draw k of a loaded file's draws (chains
// pooled in order) is what coef() and response() read on the view —
// the coefficient, the curve and the adstock decay all the draw's —
// while the posterior-mean set is left as it was.
func TestWithMMMDraw_BindsOneDraw(t *testing.T) {
	t.Parallel()

	set := drawsSet(t)
	d, err := set.MMMDrawsOf("m")
	if err != nil {
		t.Fatal(err)
	}
	if d.Len() != 4 || d.Level != 0.9 {
		t.Fatalf("draws %d at %v", d.Len(), d.Level)
	}
	// Pooled draw 2 is chain 1's first: tv 2.5, decay 0.3, k 45, s 1.5.
	view, err := set.WithMMMDraw("m", d, 2)
	if err != nil {
		t.Fatal(err)
	}
	if v, cerr := Coef(view, "m", "tv", StatPoint); cerr != nil || v != 2.5 {
		t.Errorf("coef tv on draw 2: %v %v", v, cerr)
	}
	if v, cerr := Coef(view, "m", InterceptKey, StatPoint); cerr != nil || v != 10 {
		t.Errorf("intercept on draw 2: %v %v", v, cerr)
	}
	m := (1 - math.Pow(0.3, 5)) / (1 - 0.3)
	want := CurveResponseAt("", 2.5, []float64{45, 1.5}, m, 40)
	if got, cerr := Response(view, "m", "tv", 40); cerr != nil || !near(got, want, 1e-12) {
		t.Errorf("response on draw 2: %v (%v), want %v", got, cerr, want)
	}
	// The mean set is untouched.
	if v, _ := Coef(set, "m", "tv", StatPoint); v != 3 {
		t.Errorf("mean coef tv %v after a draw view", v)
	}
	m = (1 - math.Pow(0.5, 5)) / (1 - 0.5)
	if got, _ := Response(set, "m", "tv", 40); !near(got, CurveResponseAt("", 3, []float64{50, 1.5}, m, 40), 1e-12) {
		t.Errorf("mean response %v changed", got)
	}
	if _, err := set.WithMMMDraw("m", d, 4); err == nil {
		t.Error("draw 4 of 4 bound")
	}
	if _, err := set.MMMDrawsOf("nope"); err == nil {
		t.Error("an unbound fit had draws")
	}
}

// TestQuantile7 pins the type-7 interpolation.
func TestQuantile7(t *testing.T) {
	t.Parallel()

	s := []float64{1, 2, 4, 8}
	for _, tc := range []struct{ p, want float64 }{{0, 1}, {1, 8}, {0.5, 3}, {0.25, 1.75}, {0.9, 6.8}} {
		if got := Quantile7(s, tc.p); !near(got, tc.want, 1e-12) {
			t.Errorf("q(%v) = %v, want %v", tc.p, got, tc.want)
		}
	}
	if got := Quantile7([]float64{5}, 0.3); got != 5 {
		t.Errorf("single %v", got)
	}
}
