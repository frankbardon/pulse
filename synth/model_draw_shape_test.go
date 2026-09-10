package synth_test

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// E4-S1 test pack: a `--fit-shape` numeric accepting conditioning.
//
// The fixture below is chosen so the two ways this story can fail are
// separable in the DATA rather than only in the code, because the whole
// point of the story is that both must hold at once:
//
//   - "shape destroyed" — the conditioning is applied in VALUE space
//     (draw from the mixture, then add the linear prediction). The
//     conditioning half then passes convincingly while the fitted
//     marginal is smeared across the level offsets.
//   - "no conditioning" — the field draws from its own mixture sampler
//     and the model is dropped, which is the pre-E4-S1 behaviour. The
//     shape half then passes perfectly and the relationship the capture
//     measured is silently absent.
//
// shapeCondSpec's two modes sit 80 units apart with std 1.5, and the
// east coefficient is 30 — a third of the mode gap. Under the correct
// latent-scale construction every drawn value lands inside one of the
// two fitted modes for either level, and east simply puts MORE of its
// mass in the upper one. Under the value-space shortcut east's values
// land at 40 and 120, which is nowhere near a fitted mode. So
// assertShapeSurvives (all mass inside the fitted modes) and
// assertConditioningApplied (the level means genuinely differ) fail on
// opposite failures, and neither can be satisfied by faking the other.

const (
	shapeModeLow  = 10.0
	shapeModeHigh = 90.0
	shapeModeStd  = 1.5

	// shapeEastCoefficient shifts the LATENT for rows with region ==
	// "east". Its data-scale effect is deliberately not asserted: the
	// map from latent to value is non-linear for a mixture Q, which is
	// this story's documented cost. See synth/mixture_quantile.go.
	shapeEastCoefficient = 30.0
)

// shapeCondSpec is a two-level `region` categorical plus a `spend`
// numeric whose marginal is a well-separated two-component mixture —
// the reconstruction `profile create --fit-shape` produces — optionally
// carrying a linear model that conditions spend on region.
//
// Intercept is the mixture's own analytic mean, so the reference level
// ("west", the level the model carries no coefficient for) predicts a
// latent of exactly 0 and therefore reproduces the fitted marginal
// untouched. ResidualStd is the mixture's own analytic std, so the
// latent residual is a standard normal and Phi maps it to a uniform —
// again reproducing the fitted marginal exactly for the reference
// level. Both are computed here rather than hard-coded so the fixture
// keeps its meaning if the modes move.
func shapeCondSpec(rows int, withModel bool) *synth.Spec {
	mean, std := shapeMixtureMoments()
	s := &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"east", "west"},
					"weights": []any{1.0, 1.0},
				},
			},
			{
				Name: "spend", Type: "f64",
				Distribution: synth.DistMixture,
				Params: map[string]any{
					"means":   []any{shapeModeLow, shapeModeHigh},
					"stds":    []any{shapeModeStd, shapeModeStd},
					"weights": []any{0.5, 0.5},
				},
			},
		},
	}
	if withModel {
		s.Models = []synth.FieldModelSpec{{
			Field:       "spend",
			Intercept:   mean,
			ResidualStd: std,
			Predictors:  []synth.ModelPredictorSpec{catLevel("region", "east", shapeEastCoefficient)},
		}}
	}
	return s
}

// shapeMixtureMoments is the fixture's mixture mean/std by the same
// law-of-total-variance arithmetic mixtureComponents.moments uses,
// recomputed independently here so the fixture is not defined in terms
// of the code under test.
func shapeMixtureMoments() (mean, std float64) {
	mean = 0.5*shapeModeLow + 0.5*shapeModeHigh
	m2 := 0.5*(shapeModeStd*shapeModeStd+shapeModeLow*shapeModeLow) +
		0.5*(shapeModeStd*shapeModeStd+shapeModeHigh*shapeModeHigh)
	return mean, math.Sqrt(m2 - mean*mean)
}

// assertShapeSurvives is the marginal half of the acceptance bar. It
// asserts both the coarse property (two peaks, separated, with a real
// valley between them — the same assertBimodal the shape-fit stories
// use) and the sharp one the value-space shortcut fails: essentially
// every value must sit inside one of the two FITTED components, at
// their fitted locations. A construction that adds a level offset in
// value space puts a level's whole mass a coefficient away from both,
// so offMode catches it even though such a sample is still perfectly
// bimodal.
func assertShapeSurvives(t *testing.T, values []float64) {
	t.Helper()
	assertBimodal(t, values, shapeModeLow, shapeModeStd, shapeModeHigh, shapeModeStd)

	const modeRadius = 4 * shapeModeStd // ~99.99% of a component's own mass
	offMode := 0
	for _, v := range values {
		if math.Abs(v-shapeModeLow) > modeRadius && math.Abs(v-shapeModeHigh) > modeRadius {
			offMode++
		}
	}
	// The tolerance is for the genuine valley crossings — a latent that
	// lands near Phi^-1(0.5) maps into the flat region between the modes
	// — not for a systematic offset. The mixture has almost no density
	// there, so the real figure is a handful of rows in thousands.
	if frac := float64(offMode) / float64(len(values)); frac > 0.01 {
		t.Errorf("%.2f%% of values sit outside both fitted modes (%v +/- %v, %v +/- %v) — the fitted marginal did not survive; a value-space effect smears mass a coefficient away from the modes",
			frac*100, shapeModeLow, modeRadius, shapeModeHigh, modeRadius)
	}
}

// assertConditioningApplied is the conditioning half: the field's mean
// must genuinely differ across the predictor's levels, in the captured
// DIRECTION (positive coefficient => higher mean). The magnitude is not
// asserted against the coefficient, because a mixture Q makes the
// latent-to-value map non-linear; what is asserted is that the
// difference is large enough that no amount of sampling noise on this
// many rows could produce it.
func assertConditioningApplied(t *testing.T, values []float64, levels []string) {
	t.Helper()
	sum := map[string]float64{}
	n := map[string]int{}
	for i, v := range values {
		sum[levels[i]] += v
		n[levels[i]]++
	}
	if n["east"] == 0 || n["west"] == 0 {
		t.Fatalf("fixture drew only one level: east=%d west=%d", n["east"], n["west"])
	}
	east := sum["east"] / float64(n["east"])
	west := sum["west"] / float64(n["west"])

	// west is the reference level and reproduces the fitted marginal, so
	// its mean is the mixture's own mean (50 here); east puts ~77% of
	// its mass in the upper mode and lands near 72. A floor of 15 is
	// comfortably above sampling noise (the per-level standard error on
	// a 40-std field over thousands of rows is under 1) and comfortably
	// below the real effect.
	const minSeparation = 15.0
	if east-west < minSeparation {
		t.Errorf("mean(spend | east) = %.2f, mean(spend | west) = %.2f, difference %.2f — want at least %.2f in the captured (positive) direction; the model's conditioning was not applied",
			east, west, east-west, minSeparation)
	}
}

// TestSynthModel_ShapeFittedTargetKeepsShapeAndGainsConditioning is the
// E4-S1 acceptance gate, and it is deliberately ONE test rather than
// two: "shape preserved" and "conditioning applied" are each trivially
// satisfiable by abandoning the other, and this story exists precisely
// because the shipped behaviour satisfied the first by abandoning the
// second. Splitting them would let a regression pass half the suite and
// read as a partial failure rather than as the whole feature being
// gone.
func TestSynthModel_ShapeFittedTargetKeepsShapeAndGainsConditioning(t *testing.T) {
	const rows = 8000
	spec := shapeCondSpec(rows, true)

	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 601})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	// Composition, not arbitration: nothing was dropped, so nothing is
	// reported. A warning here means the pre-claim ordering regressed.
	if len(res.Warnings) != 0 {
		t.Fatalf("shape and model must compose without a conflict; warnings = %v", res.Warnings)
	}

	values := readF64Field(t, data, "spend")
	levels := readCategoricalField(t, data, "region")

	assertShapeSurvives(t, values)
	assertConditioningApplied(t, values, levels)
}

// TestSynthModel_ShapeFittedWithoutModelIsUnchanged is the other half of
// the bar: retiring the exclusivity must not change what an UNMODELLED
// `--fit-shape` field does. It still draws its own mixture untouched,
// and it is still pre-claimed by resolveConflicts under "captured shape
// (--fit-shape)", so a conditional pair naming it is still excluded with
// that exact wording.
func TestSynthModel_ShapeFittedWithoutModelIsUnchanged(t *testing.T) {
	const rows = 8000
	spec := shapeCondSpec(rows, false)
	spec.CategoricalNumericPairs = []synth.CategoricalNumericPairSpec{{
		A: "region", B: "spend",
		Categories: []synth.CategoricalNumericCategorySpec{
			{Category: "east", Mean: 500, Std: 1},
			{Category: "west", Mean: 500, Std: 1},
		},
	}}

	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 602})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if !warningContainsAll(res.Warnings, `field "spend"`, "captured shape (--fit-shape)") {
		t.Fatalf("an unmodelled shape-fit field must still hold its pre-claim; warnings = %v", res.Warnings)
	}

	values := readF64Field(t, data, "spend")
	assertShapeSurvives(t, values)

	// No model and no surviving pair means the pure fitted marginal:
	// the two components at their declared 50/50 weights, so the sample
	// mean sits on the mixture's own mean rather than being pulled
	// toward either mode.
	mean, _ := shapeMixtureMoments()
	var sum float64
	for _, v := range values {
		sum += v
	}
	if got := sum / float64(len(values)); math.Abs(got-mean) > 2 {
		t.Errorf("mean = %.2f, want ~%.2f — an unmodelled shape-fit field must draw its own marginal", got, mean)
	}
}

// TestSynthModel_ShapeFittedDrawIsByteIdentical is the determinism
// contract applied to this story's one iterative component. The mixture
// quantile has no closed form and is inverted by bisection; a
// convergence-dependent inverse would still be deterministic within a
// single process and could still drift between builds, so the assertion
// is byte equality of two full generate runs rather than a numeric
// tolerance. Seed sensitivity is asserted alongside it, so a construction
// that became constant would not pass by accident.
func TestSynthModel_ShapeFittedDrawIsByteIdentical(t *testing.T) {
	first, _, err := synth.SynthBytes(shapeCondSpec(2000, true), synth.Options{Seed: 603})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	second, _, err := synth.SynthBytes(shapeCondSpec(2000, true), synth.Options{Seed: 603})
	if err != nil {
		t.Fatalf("SynthBytes (repeat): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two runs of the same spec and seed disagreed — the numeric quantile inverse is not deterministic")
	}
	other, _, err := synth.SynthBytes(shapeCondSpec(2000, true), synth.Options{Seed: 604})
	if err != nil {
		t.Fatalf("SynthBytes (other seed): %v", err)
	}
	if bytes.Equal(first, other) {
		t.Fatal("a different seed produced identical bytes — the draw is not reading the RNG")
	}
}

// TestSynthModel_ShapeFitRoundTripKeepsBothHalves is the same acceptance
// bar reached through the real pipeline rather than a hand-authored
// spec: a source cohort is generated, profiled with BOTH `--fit-shape`
// and `--fit-models`, translated by SpecFromProfile, and regenerated.
//
// The source's bimodality is NOT authored as a mixture — it is produced
// by a categorical-numeric conditional pair whose two levels sit 80
// units apart, so the pooled marginal is genuinely bimodal and the
// bimodality genuinely IS the conditioning. That makes it the sharpest
// available stand-in for the motivating cohort (whose `nps` is bimodal
// for exactly that reason, and which is not checked into this repo):
// capture must find both the shape and the model, and generation must
// reproduce both. Before E4-S1 this round trip dropped the model at
// translation time and the regenerated cohort carried no relationship
// to `region` at all.
func TestSynthModel_ShapeFitRoundTripKeepsBothHalves(t *testing.T) {
	source := &synth.Spec{
		RowCount: 6000,
		Fields: []synth.FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"east", "west"},
					"weights": []any{1.0, 1.0},
				},
			},
			{
				Name: "spend", Type: "f64",
				Distribution: synth.DistNormal,
				Params:       map[string]any{"mean": 50.0, "std": 40.0},
			},
		},
		CategoricalNumericPairs: []synth.CategoricalNumericPairSpec{{
			A: "region", B: "spend",
			Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "east", Mean: shapeModeHigh, Std: shapeModeStd},
				{Category: "west", Mean: shapeModeLow, Std: shapeModeStd},
			},
		}},
	}
	data, _, err := synth.SynthBytes(source, synth.Options{Seed: 701})
	if err != nil {
		t.Fatalf("source SynthBytes: %v", err)
	}

	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{
		IncludeStats:       true,
		IncludeConditional: true,
		FitShape:           true,
		FitModels:          true,
		Seed:               7,
	})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	var spendProfile *synth.NumericProfile
	for i := range prof.Fields {
		if prof.Fields[i].Name == "spend" {
			spendProfile = prof.Fields[i].Numeric
		}
	}
	if spendProfile == nil || spendProfile.Shape == nil {
		t.Fatal("capture failed upstream of this story: --fit-shape found no mixture on a clearly bimodal field")
	}
	if len(prof.Models) != 1 || prof.Models[0].Field != "spend" || len(prof.Models[0].Predictors) == 0 {
		t.Fatalf("capture failed upstream of this story: --fit-models produced %+v", prof.Models)
	}

	spec, warnings := synth.SpecFromProfile(prof, 8000)
	for _, w := range warnings {
		if strings.Contains(w, "not applied") || strings.Contains(w, "dropping linear model") {
			t.Fatalf("the round trip dropped a half: %q", w)
		}
	}
	var spendSpec *synth.FieldSpec
	for i := range spec.Fields {
		if spec.Fields[i].Name == "spend" {
			spendSpec = &spec.Fields[i]
		}
	}
	if spendSpec == nil || spendSpec.Distribution != synth.DistMixture {
		t.Fatalf("spend reconstructed as %+v, want a captured mixture", spendSpec)
	}
	if len(spec.Models) != 1 {
		t.Fatalf("spec models = %+v, want the captured model to survive translation", spec.Models)
	}

	out, res, err := synth.SynthBytes(spec, synth.Options{Seed: 702})
	if err != nil {
		t.Fatalf("regenerate SynthBytes: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("regeneration warned: %v", res.Warnings)
	}
	assertShapeSurvives(t, readF64Field(t, out, "spend"))
	assertConditioningApplied(t, readF64Field(t, out, "spend"), readCategoricalField(t, out, "region"))
}
