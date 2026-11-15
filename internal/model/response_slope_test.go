package model

import (
	"math"
	"testing"
)

// fdSlope is the central difference of f at x with step 1e-6·max(1, |x|).
func fdSlope(f func(float64) float64, x float64) float64 {
	h := 1e-6 * math.Max(1, math.Abs(x))
	return (f(x+h) - f(x-h)) / (2 * h)
}

// TestCurveSlopeAt_AgreesWithFiniteDifferences: every saturation curve's
// slope in the spend agrees with a central difference of
// CurveResponseAt within 1e-6·max(1, |∂|), across multipliers and
// spends; at spend 0 the Hill slope is its limit (0 for s > 1, β·m/k at
// s = 1, +Inf below); outside the domain it is NaN.
func TestCurveSlopeAt_AgreesWithFiniteDifferences(t *testing.T) {
	t.Parallel()

	cases := []struct {
		curve  string
		params []float64
	}{
		{"", []float64{40, 1.5}},
		{MMMSaturationHill, []float64{12, 0.7}},
		{MMMSaturationHill, []float64{3, 2}},
		{MMMSaturationLogistic, []float64{0.02}},
		{MMMSaturationLogistic, []float64{1.3}},
		{MMMSaturationMichaelisMenten, []float64{30}},
	}
	for _, tc := range cases {
		for _, m := range []float64{1, 2.3} {
			for _, spend := range []float64{0.4, 7, 37.25, 400} {
				got := CurveSlopeAt(tc.curve, 2.5, tc.params, m, spend)
				want := fdSlope(func(x float64) float64 { return CurveResponseAt(tc.curve, 2.5, tc.params, m, x) }, spend)
				if !near(got, want, 1e-6) {
					t.Errorf("%q %v m=%v spend %v: slope %v, finite difference %v", tc.curve, tc.params, m, spend, got, want)
				}
			}
		}
	}
	for _, tc := range []struct {
		s, want float64
	}{{2, 0}, {1, 2.5 * 1.5 / 4}, {0.5, math.Inf(1)}} {
		if got := CurveSlopeAt("", 2.5, []float64{4, tc.s}, 1.5, 0); got != tc.want {
			t.Errorf("hill s=%v at 0: %v, want %v", tc.s, got, tc.want)
		}
	}
	if got := CurveSlopeAt(MMMSaturationMichaelisMenten, 2, []float64{5}, 1, 0); got != 2.0/5 {
		t.Errorf("michaelis_menten at 0: %v", got)
	}
	for _, spend := range []float64{-1, math.NaN()} {
		if got := CurveSlopeAt("", 2.5, []float64{4, 2}, 1, spend); !math.IsNaN(got) {
			t.Errorf("spend %v: %v, want NaN", spend, got)
		}
	}
}

// TestResponseSlope_AgreesWithFiniteDifferences: ResponseSlope (and the
// geo form, and ResponseSlopeBuiltin over both) is the spend derivative
// of Response over the very terms Response reads — every adstock kernel
// (geometric, normalised Weibull), every Bayes curve, a geo panel's own
// β and decay — and refuses as Response refuses.
func TestResponseSlope_AgreesWithFiniteDifferences(t *testing.T) {
	t.Parallel()

	s := responseSet(t)
	for _, ch := range []string{"tv_ad", "radio_ad"} {
		for _, spend := range []float64{0.5, 10, 37.25} {
			got, cerr := ResponseSlope(s, "mmm", ch, spend)
			want := fdSlope(func(x float64) float64 { v, _ := Response(s, "mmm", ch, x); return v }, spend)
			if cerr != nil || !near(got, want, 1e-6) {
				t.Errorf("%s at %v: slope %v (%v), finite difference %v", ch, spend, got, cerr, want)
			}
			b, err := ResponseSlopeBuiltin(s)("mmm", ch, spend)
			if err != nil || b != got {
				t.Errorf("%s builtin: %v %v, want %v", ch, b, err, got)
			}
		}
	}
	for _, sat := range []string{"", "logistic", "michaelis_menten"} {
		spec, res := bayesCurveFit(sat)
		b, err := Bind("bayes", spec, res)
		if err != nil {
			t.Fatal(err)
		}
		b.Adstocks = AdstockLineage(nil, spec, res)
		bs := &Set{}
		if err := bs.Add(b); err != nil {
			t.Fatal(err)
		}
		got, cerr := ResponseSlope(bs, "bayes", "tv", 20)
		want := fdSlope(func(x float64) float64 { v, _ := Response(bs, "bayes", "tv", x); return v }, 20)
		if cerr != nil || !near(got, want, 1e-6) {
			t.Errorf("bayes %q: slope %v (%v), finite difference %v", sat, got, cerr, want)
		}
	}
	panel := bayesGeoSet(t, true)
	for _, geo := range []string{"NE", "SW"} {
		got, cerr := ResponseGeoSlope(panel, "panel", "tv", geo, 20)
		want := fdSlope(func(x float64) float64 { v, _ := ResponseGeo(panel, "panel", "tv", geo, x); return v }, 20)
		if cerr != nil || !near(got, want, 1e-6) {
			t.Errorf("geo %s: slope %v (%v), finite difference %v", geo, got, cerr, want)
		}
		b, err := ResponseSlopeBuiltin(panel)("panel", "tv", 20, geo)
		if err != nil || b != got {
			t.Errorf("geo %s builtin: %v %v, want %v", geo, b, err, got)
		}
	}
	_, want := Response(s, "mmm", "nope", 10)
	if _, cerr := ResponseSlope(s, "mmm", "nope", 10); cerr == nil || want == nil || cerr.Code != want.Code || cerr.Message != want.Message {
		t.Fatalf("unknown channel: %v, Response refuses %v", cerr, want)
	}
	if _, err := ResponseSlopeBuiltin(s)("mmm", 3, 10); err == nil {
		t.Fatal("a numeric channel was read")
	}
}
