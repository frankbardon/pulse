package synth_test

import (
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// A packed_bool field holds ONE BIT, so every path that writes it has to
// reduce a drawn number to 0 or 1. Before the bernoulli reconstruction
// these tests pin, that reduction happened at the writer's threshold over
// a value drawn from a clamped normal, and the prevalence it produced was
// not approximately wrong — it was inverted. A 20%-prevalence boolean
// generated at 69.1%, a 50% one at 84.2%, an 80% one at 97.7%: the mass
// clamped to exactly 0 was the only mass the old `value != 0` threshold
// could call false, so P(false) collapsed to Phi(-p/sigma).
//
// The defect survived a full release because nothing measured a
// generated boolean's prevalence against its source. These tests are that
// measurement, on each of the three paths that can write the field: its
// own marginal sampler, a conditional pair, and a linear model.

// boolPrevalence counts the 1s in a 0/1 column.
func boolPrevalence(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	ones := 0
	for _, v := range values {
		if v != 0 {
			ones++
		}
	}
	return float64(ones) / float64(len(values))
}

// assertPrevalence checks a realized prevalence against its target with a
// tolerance stated in standard errors of the binomial proportion, so the
// bar scales with n instead of being a hand-tuned epsilon.
func assertPrevalence(t *testing.T, label string, values []float64, want float64, seMultiple float64) {
	t.Helper()
	got := boolPrevalence(values)
	se := math.Sqrt(want * (1 - want) / float64(len(values)))
	tol := seMultiple * se
	if math.Abs(got-want) > tol {
		t.Errorf("%s: prevalence %.4f, want %.4f +/- %.4f (%v SE over n=%d)",
			label, got, want, tol, seMultiple, len(values))
	}
}

// TestSpecFromProfile_BooleanReconstructsAsBernoulli pins the translation
// that fixes the marginal at its source. A packed_bool is summarized by
// the profiler's NUMERIC accumulator — it is neither date, categorical
// nor set — and the obvious reading of that summary, normal(mean, std)
// clamped to the observed [0, 1], is the shape that cannot round-trip
// through one bit.
func TestSpecFromProfile_BooleanReconstructsAsBernoulli(t *testing.T) {
	prof := &synth.Profile{
		RowCount: 40000,
		Fields: []synth.FieldProfile{{
			Name: "aware",
			Type: "packed_bool",
			Numeric: &synth.NumericProfile{
				Min: 0, Max: 1, Mean: 0.199025, Std: 0.39927,
			},
		}},
	}
	spec, warnings := synth.SpecFromProfile(prof, 40000)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(spec.Fields) != 1 {
		t.Fatalf("got %d fields, want 1", len(spec.Fields))
	}
	fs := spec.Fields[0]
	if fs.Distribution != synth.DistBernoulli {
		t.Fatalf("distribution %q, want %q — a clamped normal cannot reproduce a one-bit field's prevalence",
			fs.Distribution, synth.DistBernoulli)
	}
	p, ok := fs.Params["p"].(float64)
	if !ok {
		t.Fatalf("params %v carry no float64 p", fs.Params)
	}
	if math.Abs(p-0.199025) > 1e-12 {
		t.Errorf("p = %v, want the observed mean 0.199025 exactly", p)
	}
}

// TestSpecFromProfile_BooleanIgnoresCapturedShape is the ordering half of
// the same claim. fitNumericShape will happily prefer a two-component
// mixture on a 0/1 column — two well-separated near-zero-variance spikes
// beat a normal on BIC every time — and that mixture reproduces the
// prevalence no better than the normal did while costing a bisection per
// draw. The boolean arm must therefore sit AHEAD of the --fit-shape arm,
// which a reordering would silently undo.
func TestSpecFromProfile_BooleanIgnoresCapturedShape(t *testing.T) {
	prof := &synth.Profile{
		RowCount: 20000,
		Fields: []synth.FieldProfile{{
			Name: "aware",
			Type: "packed_bool",
			Numeric: &synth.NumericProfile{
				Min: 0, Max: 1, Mean: 0.4, Std: 0.49,
				Shape: &synth.ShapeProfile{
					Means:   []float64{0, 1},
					Stds:    []float64{0.01, 0.01},
					Weights: []float64{0.6, 0.4},
				},
			},
		}},
	}
	spec, _ := synth.SpecFromProfile(prof, 20000)
	if got := spec.Fields[0].Distribution; got != synth.DistBernoulli {
		t.Fatalf("distribution %q, want %q — a captured shape must not outrank the boolean arm",
			got, synth.DistBernoulli)
	}
}

// TestSynth_BooleanMarginalPrevalence covers the plain path: a boolean
// drawn from its own sampler, no conditioning of any kind. Three
// prevalences, because the old defect's error was a function of p and was
// at its most misleading on the rare end — a 20% boolean came out more
// likely than not.
func TestSynth_BooleanMarginalPrevalence(t *testing.T) {
	const rows = 40000
	for _, p := range []float64{0.2, 0.5, 0.8} {
		spec := &synth.Spec{
			RowCount: rows,
			Fields: []synth.FieldSpec{{
				Name: "flag", Type: "packed_bool",
				Distribution: synth.DistBernoulli,
				Params:       map[string]any{"p": p},
			}},
		}
		data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 900})
		if err != nil {
			t.Fatalf("p=%v: SynthBytes: %v", p, err)
		}
		assertPrevalence(t, "marginal", readF64Field(t, data, "flag"), p, 4)
	}
}

// TestSynth_BooleanDegeneratePrevalence pins the two exact ends. p == 0
// and p == 1 are legitimate observed prevalences for a constant column,
// and quantileFor's threshold handles them by arithmetic rather than by a
// special case — which is worth a test precisely because a special case
// is what a future reader will be tempted to add.
func TestSynth_BooleanDegeneratePrevalence(t *testing.T) {
	for _, tc := range []struct{ p, want float64 }{{0, 0}, {1, 1}} {
		spec := &synth.Spec{
			RowCount: 500,
			Fields: []synth.FieldSpec{{
				Name: "flag", Type: "packed_bool",
				Distribution: synth.DistBernoulli,
				Params:       map[string]any{"p": tc.p},
			}},
		}
		data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 901})
		if err != nil {
			t.Fatalf("p=%v: SynthBytes: %v", tc.p, err)
		}
		if got := boolPrevalence(readF64Field(t, data, "flag")); got != tc.want {
			t.Errorf("p=%v: prevalence %v, want exactly %v", tc.p, got, tc.want)
		}
	}
}

// boolPairSpec is a four-region cohort whose boolean target is written by
// a conditional pair rather than by its own sampler.
//
// The target's MARGINAL is what now decides the cell draw — the pair's
// `bernoulli` wire flag is retired and read by nothing — so the
// distribution is the knob these tests turn. `flag` exists only so the
// retired slot can be exercised on a target that contradicts it.
func boolPairSpec(rows int, dist string, params map[string]any, flag bool) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"north", "south", "east", "west"},
					"weights": []any{0.25, 0.25, 0.25, 0.25},
				},
			},
			{
				Name: "aware", Type: "packed_bool",
				Distribution: dist,
				Params:       params,
			},
		},
		CategoricalNumericPairs: []synth.CategoricalNumericPairSpec{{
			A: "region", B: "aware", Bernoulli: flag,
			Min: 0, Max: 1, HasClamp: true,
			Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "north", Mean: 0.60, Std: 0.49},
				{Category: "south", Mean: 0.30, Std: 0.46},
				{Category: "east", Mean: 0.15, Std: 0.36},
				{Category: "west", Mean: 0.05, Std: 0.22},
			},
		}},
	}
}

// boolPairBernoulliSpec is the ordinary shape: the target reconstructs as
// a bernoulli step and the pair says nothing about it. It is what
// SpecFromProfile now emits, and what the retired flag used to be
// required for.
func boolPairBernoulliSpec(rows int) *synth.Spec {
	return boolPairSpec(rows, synth.DistBernoulli, map[string]any{"p": 0.275}, false)
}

// TestSynth_BooleanConditionalPairPrevalence covers the second writer. A
// captured categorical-numeric pair overwrites the target outright from
// the cell's own moments, so without the Bernoulli flag it reproduces the
// continuous defect once PER CELL — and the per-cell error is worst
// exactly where the signal is (a 5% cell generated at 41%, destroying the
// contrast the pair exists to carry).
func TestSynth_BooleanConditionalPairPrevalence(t *testing.T) {
	const rows = 60000
	data, _, err := synth.SynthBytes(boolPairBernoulliSpec(rows), synth.Options{Seed: 910})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	flags := readF64Field(t, data, "aware")
	regions := readCategoricalField(t, data, "region")
	if len(flags) != len(regions) {
		t.Fatalf("column length mismatch: %d flags, %d regions", len(flags), len(regions))
	}

	byRegion := map[string][]float64{}
	for i := range flags {
		byRegion[regions[i]] = append(byRegion[regions[i]], flags[i])
	}
	for region, want := range map[string]float64{
		"north": 0.60, "south": 0.30, "east": 0.15, "west": 0.05,
	} {
		got, ok := byRegion[region]
		if !ok {
			t.Fatalf("region %q absent from generated output", region)
		}
		assertPrevalence(t, "cell "+region, got, want, 4)
	}
}

// TestSynth_BooleanConditionalPairOnAContinuousMarginalIsBiased is the
// deliberate negative. The continuous arm is RETAINED for the one shape
// that still reaches it — a HAND-AUTHORED spec putting a continuous
// distribution on a packed_bool field — and it is biased rather than
// correct. The test states that plainly so nobody reads the retained arm
// as an equally valid choice, and so a future attempt to "unify" the two
// arms has to confront the number.
//
// It used to reach that arm by omitting the pair's `bernoulli` flag over
// a bernoulli-reconstructed target. That shape is gone: the flag is
// retired and the draw follows the target's own marginal, so an unflagged
// pair over a bernoulli field is now CORRECT — which is the bug fix, and
// is asserted by TestSynth_BooleanConditionalPairDerivesFromTheMarginal.
// The premise here survives intact because it never depended on the flag;
// it depended on the cell draw being continuous.
func TestSynth_BooleanConditionalPairOnAContinuousMarginalIsBiased(t *testing.T) {
	const rows = 60000
	spec := boolPairSpec(rows, synth.DistNormal,
		map[string]any{"mean": 0.275, "std": 0.45, "min": 0.0, "max": 1.0}, false)
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 911})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	flags := readF64Field(t, data, "aware")
	regions := readCategoricalField(t, data, "region")
	var west []float64
	for i := range flags {
		if regions[i] == "west" {
			west = append(west, flags[i])
		}
	}
	if len(west) == 0 {
		t.Fatal("no west rows generated")
	}
	got := boolPrevalence(west)
	// The cell's captured prevalence is 0.05. A normal(0.05, 0.22)
	// rounded at 0.5 overstates it; the point of the test is only that
	// it does, and that the flagged arm above does not.
	if math.Abs(got-0.05) <= 0.01 {
		t.Errorf("continuous arm produced %.4f for a 0.05 cell — if it is now accurate, "+
			"the arm has changed and this test's premise needs revisiting", got)
	}
}

// TestSynth_BooleanConditionalPairDerivesFromTheMarginal is the gate on
// the retirement, and it asserts BOTH halves in one test because each is
// trivially satisfiable by abandoning the other:
//
//  1. A pair over a bernoulli-reconstructed target draws the step even
//     though the pair declares NOTHING. This is the bug: the SET-numeric
//     arm of SpecFromProfile never wrote the flag, so a boolean target
//     there took the clamped-normal draw once per cell.
//  2. A pair still DECLARING the retired flag over a target whose
//     marginal is not bernoulli does not get the step, and is WARNED
//     about rather than silently ignored.
func TestSynth_BooleanConditionalPairDerivesFromTheMarginal(t *testing.T) {
	const rows = 60000

	t.Run("unflagged bernoulli target draws the step", func(t *testing.T) {
		data, res, err := synth.SynthBytes(boolPairBernoulliSpec(rows), synth.Options{Seed: 912})
		if err != nil {
			t.Fatalf("SynthBytes: %v", err)
		}
		for _, w := range res.Warnings {
			if strings.Contains(w, "retired `bernoulli` flag") {
				t.Errorf("agreement must be silent, got %q", w)
			}
		}
		flags := readF64Field(t, data, "aware")
		regions := readCategoricalField(t, data, "region")
		var west []float64
		for i := range flags {
			if regions[i] == "west" {
				west = append(west, flags[i])
			}
		}
		if len(west) == 0 {
			t.Fatal("no west rows generated")
		}
		// The cell's captured prevalence is 0.05. The continuous arm this
		// replaces produced ~0.41 for it.
		assertPrevalence(t, "cell west", west, 0.05, 4)
	})

	t.Run("set-numeric pair over a bernoulli target draws the step", func(t *testing.T) {
		// The shipped bug, in its own arm. SpecFromProfile's SET-numeric
		// arm admitted a DistBernoulli target and never wrote the flag,
		// so this exact spec shape drew a clamped normal per cell. No
		// pair here can declare anything — the slot is retired.
		spec := &synth.Spec{
			RowCount: rows,
			Fields: []synth.FieldSpec{
				{
					Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
					Params: map[string]any{
						"options":     []any{"tv", "radio"},
						"frequencies": []any{0.5, 0.3},
					},
				},
				{
					Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
					Params: map[string]any{"p": 0.275},
				},
			},
			SetNumericPairs: []synth.SetNumericPairSpec{{
				Set: "channels", Option: "tv", Numeric: "aware",
				Min: 0, Max: 1, HasClamp: true,
				Categories: []synth.CategoricalNumericCategorySpec{
					{Category: "selected", Mean: 0.60, Std: 0.49},
					{Category: "not_selected", Mean: 0.05, Std: 0.22},
				},
			}},
		}
		data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 914})
		if err != nil {
			t.Fatalf("SynthBytes: %v", err)
		}
		flags := readF64Field(t, data, "aware")
		_, chanLabels, _ := readSetFieldRows(t, data, "channels")
		var sel, notSel []float64
		for i := range flags {
			if hasLabel(chanLabels[i], "tv") {
				sel = append(sel, flags[i])
			} else {
				notSel = append(notSel, flags[i])
			}
		}
		if len(sel) == 0 || len(notSel) == 0 {
			t.Fatalf("degenerate fixture: %d selected / %d not-selected", len(sel), len(notSel))
		}
		assertPrevalence(t, "selected", sel, 0.60, 4)
		assertPrevalence(t, "not_selected", notSel, 0.05, 4)
	})

	t.Run("retired flag over a non-bernoulli target is ignored and warned", func(t *testing.T) {
		spec := boolPairSpec(rows, synth.DistNormal,
			map[string]any{"mean": 0.275, "std": 0.45, "min": 0.0, "max": 1.0}, true)
		data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 913})
		if err != nil {
			t.Fatalf("SynthBytes: %v", err)
		}
		var warned bool
		for _, w := range res.Warnings {
			if strings.Contains(w, "retired `bernoulli` flag") && strings.Contains(w, `"aware"`) {
				warned = true
			}
		}
		if !warned {
			t.Errorf("a declared-but-ignored flag must be reported, got warnings %v", res.Warnings)
		}
		flags := readF64Field(t, data, "aware")
		regions := readCategoricalField(t, data, "region")
		var west []float64
		for i := range flags {
			if regions[i] == "west" {
				west = append(west, flags[i])
			}
		}
		if len(west) == 0 {
			t.Fatal("no west rows generated")
		}
		if got := boolPrevalence(west); math.Abs(got-0.05) <= 0.01 {
			t.Errorf("cell west prevalence %.4f — the retired flag must NOT have re-enabled the step draw", got)
		}
	})
}

// boolModelSpec is the same four-region cohort with the target written by
// a linear MODEL instead of a pair — the shape SpecFromProfile produces
// for a modelled boolean, and the path the motivating cohort's packed_bool
// targets actually take.
//
// The coefficients are a saturated linear probability model with `east`
// as the reference: OLS on a 0/1 target against a full set of level
// dummies recovers each cell's prevalence exactly, which is what
// `profile create --fit-models` captures for a cohort like this.
func boolModelSpec(rows int) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"north", "south", "east", "west"},
					"weights": []any{0.25, 0.25, 0.25, 0.25},
				},
			},
			{
				Name: "aware", Type: "packed_bool",
				Distribution: synth.DistBernoulli,
				Params:       map[string]any{"p": 0.275},
			},
		},
		Models: []synth.FieldModelSpec{{
			Field:     "aware",
			Intercept: 0.15,
			Predictors: []synth.ModelPredictorSpec{
				{Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "north", Coefficient: 0.45},
				{Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "south", Coefficient: 0.15},
				{Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "west", Coefficient: -0.10},
			},
			ResidualStd: 0.41,
			Min:         0,
			Max:         1,
			HasClamp:    true,
		}},
	}
}

// TestSynthModel_BooleanTargetKeepsPrevalenceAndOrdersByPredictor is the
// load-bearing test of the whole change, because a modelled boolean is
// the case the motivating cohort is almost entirely made of.
//
// It asserts BOTH halves in one test on purpose: each is trivially
// satisfiable by abandoning the other. Always emitting 0 keeps no
// prevalence; drawing from the marginal and ignoring the model keeps the
// prevalence perfectly and carries no signal. Only the composed draw
// value = Q(Phi(mu + sigma*z)) with a step Q does both, and it is a
// PROBIT: P(1 | row) = Phi((mu - Phi^-1(1-p)) / sigma).
//
// The bar on the conditional half is ORDERING, not magnitude. A
// coefficient on a boolean target shifts the latent, so it is not "this
// much probability" and must never be read as such — asserting recovered
// cell prevalences here would be asserting something the construction
// does not promise.
func TestSynthModel_BooleanTargetKeepsPrevalenceAndOrdersByPredictor(t *testing.T) {
	const rows = 60000
	data, res, err := synth.SynthBytes(boolModelSpec(rows), synth.Options{Seed: 920})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	// A bernoulli target must not be refused a model. A warning here
	// means the drawer declined to compile, which would leave the field
	// with no conditioning at all — the outcome this arm exists to avoid.
	for _, w := range res.Warnings {
		t.Errorf("unexpected warning (a bernoulli target must accept a model): %s", w)
	}

	flags := readF64Field(t, data, "aware")
	regions := readCategoricalField(t, data, "region")

	// Half one: the marginal is held at the field's captured prevalence,
	// because Phi(u) is uniform whenever u is standard normal and the
	// step Q maps a uniform to exactly that prevalence.
	assertPrevalence(t, "modelled marginal", flags, 0.275, 4)

	// Half two: the model discriminates, in the order its coefficients
	// declare. north (+0.45) > south (+0.15) > east (reference, 0) >
	// west (-0.10).
	byRegion := map[string][]float64{}
	for i := range flags {
		byRegion[regions[i]] = append(byRegion[regions[i]], flags[i])
	}
	order := []string{"north", "south", "east", "west"}
	prev := make([]float64, len(order))
	for i, r := range order {
		if len(byRegion[r]) == 0 {
			t.Fatalf("region %q absent from generated output", r)
		}
		prev[i] = boolPrevalence(byRegion[r])
	}
	for i := 1; i < len(order); i++ {
		if prev[i] >= prev[i-1] {
			t.Errorf("prevalence not strictly decreasing across %v: %s=%.4f then %s=%.4f",
				order, order[i-1], prev[i-1], order[i], prev[i])
		}
	}
	// And the discrimination is substantial, not a rounding artifact —
	// a 0.55 coefficient spread across a 0.41 residual sd separates the
	// extremes by a wide margin on the probit scale.
	if spread := prev[0] - prev[len(prev)-1]; spread < 0.30 {
		t.Errorf("extreme-cell spread %.4f is too small for the declared coefficients; "+
			"the model is barely reaching the target", spread)
	}
}

// TestSynthModel_BooleanTargetIsDeterministic pins the byte-identity
// contract across the new arm. The step Q consumes no randomness of its
// own and the drawer's single normal is drawn unconditionally, so the
// seeded stream is a function of the spec alone.
func TestSynthModel_BooleanTargetIsDeterministic(t *testing.T) {
	a, _, err := synth.SynthBytes(boolModelSpec(4000), synth.Options{Seed: 77})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	b, _, err := synth.SynthBytes(boolModelSpec(4000), synth.Options{Seed: 77})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("byte length differs across runs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same spec + same seed diverged at byte %d", i)
		}
	}
}

// TestSynthModel_ConstantBooleanTargetDropsItsModel covers the degenerate
// end of the model path. A boolean observed at p == 0 or p == 1 has zero
// variance, so there is no scale to standardise a prediction against.
// fieldMoments reports std = 0 rather than flooring it, and
// buildModelDrawers' own guard turns that into a dropped model WITH a
// warning — visible, rather than an infinite invStd producing NaNs.
func TestSynthModel_ConstantBooleanTargetDropsItsModel(t *testing.T) {
	spec := boolModelSpec(2000)
	spec.Fields[1].Params = map[string]any{"p": 1.0}

	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 930})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("a zero-variance boolean target must report its dropped model, not drop it silently")
	}
	// And the field still generates: the model is dropped, the marginal
	// is not.
	if got := boolPrevalence(readF64Field(t, data, "aware")); got != 1 {
		t.Errorf("prevalence %v, want exactly 1 — the marginal must survive the dropped model", got)
	}
}
