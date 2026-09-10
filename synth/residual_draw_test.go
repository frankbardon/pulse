package synth_test

import (
	"bytes"
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// E3-S2 test pack: a modelled numeric drawing through the CORRELATED
// residual.
//
// The construction under test composes two structures that used to be
// mutually exclusive, so every assertion here has to be able to fail on
// the loss of either one independently:
//
//   - "correlated but not conditioned" — the copula reaches the field
//     and overwrites the model's output. The realized correlation then
//     looks perfect while the predictor's effect is gone.
//   - "conditioned but not correlated" — the drawer keeps taking its own
//     independent z (the pre-E3-S2 behaviour). The conditioning looks
//     perfect while the residual correlation is ~0.
//
// residualCondSpec's fixture separates them in the DATA: the level
// offsets are large relative to the residual scale, so a lost
// conditioning collapses the between-level gap to zero, while the
// residual correlation is measured on the residuals themselves
// (value - prediction) and is therefore blind to the conditioning.
// TestSynthResidual_ConditionedAndCorrelatedAtOnce asserts both on one
// cohort, which is the story's headline criterion.

// residualTolerance matches TestSynth_CorrelationReconstructionWithinTolerance's
// 0.03. For a `normal` target the residual correlation is EXACT in
// expectation — the latent-to-value map is affine, so the realized
// figure is a sampling estimate of rho and nothing else — so the
// tolerance here is pure sampling slack at 20k rows, not an allowance
// for construction error.
const residualTolerance = 0.03

const (
	residualEastSpend  = 40.0
	residualEastVisits = 3.0
)

// residualCondSpec is two modelled numerics over one two-level
// categorical, optionally sharing a residual correlation.
//
// Both models are `normal` targets whose intercept is the field's own
// marginal mean and whose ResidualStd is a real (non-zero) scale, so the
// composed draw collapses to prediction + ResidualStd*z (see
// synth/model_draw.go) and the residual is recoverable per row as
// value - prediction. That is what lets the correlation be measured on
// the quantity the spec actually names rather than inferred from raw
// values.
func residualCondSpec(rows int, rho float64) *synth.Spec {
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
				Distribution: synth.DistNormal,
				Params:       map[string]any{"mean": 100.0, "std": 25.0},
			},
			{
				Name: "visits", Type: "f64",
				Distribution: synth.DistNormal,
				Params:       map[string]any{"mean": 12.0, "std": 6.0},
			},
		},
		Models: []synth.FieldModelSpec{
			{
				Field: "spend", Intercept: 100, ResidualStd: 20,
				Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", residualEastSpend)},
			},
			{
				Field: "visits", Intercept: 12, ResidualStd: 4,
				Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", residualEastVisits)},
			},
		},
	}
	if rho != 0 {
		s.ResidualCorrelations = []synth.CorrelationSpec{{A: "spend", B: "visits", Correlation: rho}}
	}
	return s
}

// residualsOf recovers each row's model residual for a two-level
// conditioned field: value minus the prediction its drawn level implies.
func residualsOf(values []float64, levels []string, intercept, eastCoef float64) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		pred := intercept
		if levels[i] == "east" {
			pred += eastCoef
		}
		out[i] = v - pred
	}
	return out
}

// TestSynthResidual_ConditionedAndCorrelatedAtOnce is the story's
// headline: ONE field must be demonstrably both conditioned on a
// predictor and correlated with a sibling numeric, in one cohort.
//
// The two halves are asserted on different quantities so neither can
// stand in for the other — the conditioning on the between-level means
// of the raw values, the correlation on the residuals with the
// conditioning divided out. Before E3-S2 the second assertion failed at
// ~0 with the first passing exactly as it does now, which is the
// regression this test exists to catch.
func TestSynthResidual_ConditionedAndCorrelatedAtOnce(t *testing.T) {
	const targetRho = 0.75
	data, res, err := synth.SynthBytes(residualCondSpec(20000, targetRho), synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("a fully supplied two-field residual matrix warned: %v", res.Warnings)
	}
	regions := readCategoricalField(t, data, "region")
	spend := readField(t, data, "spend")
	visits := readField(t, data, "visits")

	// (a) CONDITIONED: the level means must sit a coefficient apart.
	gotGap := levelMeanGap(t, spend, regions)
	if math.Abs(gotGap-residualEastSpend) > 1.5 {
		t.Errorf("spend east-west mean gap = %.3f, want ~%.1f — the conditioning was lost", gotGap, residualEastSpend)
	}
	if gap := levelMeanGap(t, visits, regions); math.Abs(gap-residualEastVisits) > 0.4 {
		t.Errorf("visits east-west mean gap = %.3f, want ~%.1f — the conditioning was lost", gap, residualEastVisits)
	}

	// (b) CORRELATED: with the conditioning divided out, what remains
	// must carry the requested structure.
	rs := residualsOf(spend, regions, 100, residualEastSpend)
	rv := residualsOf(visits, regions, 12, residualEastVisits)
	if rho := pearsonOf(rs, rv); math.Abs(rho-targetRho) > residualTolerance {
		t.Errorf("residual rho = %.4f, want within %.2f of target %.2f — the residual draw is still independent",
			rho, residualTolerance, targetRho)
	}
}

// TestSynthResidual_ReconstructionWithinTolerance is the residual-scale
// twin of TestSynth_CorrelationReconstructionWithinTolerance: across the
// sign and magnitude range, the realized residual correlation must land
// within the established tolerance of the captured target.
//
// rho = 0 is included deliberately. It is the one case where "the
// feature does nothing" and "the feature works" produce the same number,
// so it proves nothing on its own — but a construction that overshoots
// or leaks structure would fail it, and it is the measured-zero case the
// capture side (synth/residual_corr.go) went to some length to keep
// distinguishable from a gap.
func TestSynthResidual_ReconstructionWithinTolerance(t *testing.T) {
	for _, targetRho := range []float64{-0.85, -0.4, 0, 0.4, 0.85} {
		data, _, err := synth.SynthBytes(residualCondSpec(20000, targetRho), synth.Options{Seed: 19})
		if err != nil {
			t.Fatalf("rho %.2f: SynthBytes: %v", targetRho, err)
		}
		regions := readCategoricalField(t, data, "region")
		rs := residualsOf(readField(t, data, "spend"), regions, 100, residualEastSpend)
		rv := residualsOf(readField(t, data, "visits"), regions, 12, residualEastVisits)
		if got := pearsonOf(rs, rv); math.Abs(got-targetRho) > residualTolerance {
			t.Errorf("target rho %.2f: realized %.4f, want within %.2f", targetRho, got, residualTolerance)
		}
	}
}

// TestSynthResidual_NonParticipantsDrawIndependently pins the criterion
// that a modelled field the submatrix does not name is untouched: it
// still takes its own fresh z, so its residual must be uncorrelated with
// the participants' even though it is drawn in the same stage from the
// same generator.
//
// This is the failure mode a shared vector invites — an off-by-one in
// the component assignment would hand a non-participant somebody else's
// correlated draw, and every marginal would still look perfect.
func TestSynthResidual_NonParticipantsDrawIndependently(t *testing.T) {
	const targetRho = 0.8
	spec := residualCondSpec(20000, targetRho)
	spec.Fields = append(spec.Fields, synth.FieldSpec{
		Name: "tenure", Type: "f64",
		Distribution: synth.DistNormal,
		Params:       map[string]any{"mean": 30.0, "std": 8.0},
	})
	spec.Models = append(spec.Models, synth.FieldModelSpec{
		Field: "tenure", Intercept: 30, ResidualStd: 8,
	})

	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 23})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	regions := readCategoricalField(t, data, "region")
	rs := residualsOf(readField(t, data, "spend"), regions, 100, residualEastSpend)
	rv := readField(t, data, "visits")
	rt := residualsOf(readField(t, data, "tenure"), regions, 30, 0)

	if got := pearsonOf(rs, residualsOf(rv, regions, 12, residualEastVisits)); math.Abs(got-targetRho) > residualTolerance {
		t.Fatalf("participants realized rho %.4f, want within %.2f of %.2f", got, residualTolerance, targetRho)
	}
	if got := pearsonOf(rs, rt); math.Abs(got) > 0.05 {
		t.Errorf("a non-participant correlated with a participant at %.4f; its z is not independent", got)
	}
}

// TestSynthResidual_PreservesLognormalMarginal is the marginal-shape
// criterion on the residual scale, mirroring
// TestSynth_CopulaPreservesLognormalMarginal.
//
// The two models are zero-predictor and calibrated to their fields' own
// marginals (intercept = mean, ResidualStd = std), so the latent is
// exactly the correlated standard normal and the drawn value is exactly
// Q(Phi(z)) — the field's own marginal, reproduced through the model
// path. A construction that leaked the linear predictor's raw scale into
// the latent, or that bypassed Q, would smear the lognormal toward
// Gaussian while the correlation still landed.
//
// Pearson attenuation under a non-linear Q is the same well-understood
// lognormal effect the value-scale test documents, so sigma is kept at
// 0.25 for the same reason and the rank correlation is checked as the
// quantity the copula actually targets.
func TestSynthResidual_PreservesLognormalMarginal(t *testing.T) {
	const targetRho = 0.8
	const mu, sigma = 0.0, 0.25
	lnMean := math.Exp(mu + sigma*sigma/2)
	lnStd := math.Sqrt((math.Exp(sigma*sigma) - 1) * math.Exp(2*mu+sigma*sigma))

	spec := &synth.Spec{
		RowCount: 20000,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistLogNormal,
				Params: map[string]any{"mu": mu, "sigma": sigma}},
		},
		Models: []synth.FieldModelSpec{
			{Field: "a", Intercept: 50, ResidualStd: 10},
			{Field: "b", Intercept: lnMean, ResidualStd: lnStd},
		},
		ResidualCorrelations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: targetRho}},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 29})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	a := readF64Field(t, data, "a")
	b := readF64Field(t, data, "b")

	if rho := pearsonOf(a, b); math.Abs(rho-targetRho) > residualTolerance {
		t.Errorf("realized rho = %.4f, want within %.2f of %.2f", rho, residualTolerance, targetRho)
	}

	wantSkew := (math.Exp(sigma*sigma) + 2) * math.Sqrt(math.Exp(sigma*sigma)-1)
	if gotSkew := sampleSkewness(b); math.Abs(gotSkew-wantSkew) > 0.3 {
		t.Errorf("lognormal skewness = %.4f, want within 0.3 of theoretical %.4f — the marginal was pulled toward Gaussian",
			gotSkew, wantSkew)
	}
	sorted := append([]float64(nil), b...)
	sort.Float64s(sorted)
	for _, p := range []float64{0.1, 0.25, 0.5, 0.75, 0.9} {
		want := math.Exp(mu + sigma*normInv(p))
		got := empiricalQuantile(sorted, p)
		if math.Abs(got-want) > 0.03*want {
			t.Errorf("lognormal quantile p=%.2f: got %.4f, want within 3%% of %.4f", p, got, want)
		}
	}
}

// TestSynthResidual_PreservesMixtureMarginal is the same criterion for
// the `--fit-shape` case E4-S1 landed: a captured two-component mixture
// must survive being drawn through a CORRELATED residual.
//
// Shape is asserted the way model_draw_shape_test.go asserts it — every
// drawn value inside one of the two fitted modes — because that is the
// assertion the value-space shortcut fails: smearing a correlated draw
// across the mixture would put mass in the empty gap between the modes,
// where the fitted marginal has essentially none. The correlation is
// asserted on RANK, which is the quantity a Gaussian copula targets
// exactly regardless of how non-linear Q is; a Pearson figure over a
// bimodal marginal is dominated by which mode each row landed in and
// says less than it looks like it does.
func TestSynthResidual_PreservesMixtureMarginal(t *testing.T) {
	const targetRho = 0.8
	mean, std := shapeMixtureMoments()

	mixField := func(name string) synth.FieldSpec {
		return synth.FieldSpec{
			Name: name, Type: "f64",
			Distribution: synth.DistMixture,
			Params: map[string]any{
				"means":   []any{shapeModeLow, shapeModeHigh},
				"stds":    []any{shapeModeStd, shapeModeStd},
				"weights": []any{0.5, 0.5},
			},
		}
	}
	spec := &synth.Spec{
		RowCount: 20000,
		Fields:   []synth.FieldSpec{mixField("a"), mixField("b")},
		Models: []synth.FieldModelSpec{
			{Field: "a", Intercept: mean, ResidualStd: std},
			{Field: "b", Intercept: mean, ResidualStd: std},
		},
		ResidualCorrelations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: targetRho}},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 31})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	a := readF64Field(t, data, "a")
	b := readF64Field(t, data, "b")

	for _, col := range []struct {
		name string
		vals []float64
	}{{"a", a}, {"b", b}} {
		for i, v := range col.vals {
			nearLow := math.Abs(v-shapeModeLow) <= 6*shapeModeStd
			nearHigh := math.Abs(v-shapeModeHigh) <= 6*shapeModeStd
			if !nearLow && !nearHigh {
				t.Fatalf("%s row %d = %.4f is in neither fitted mode; the mixture marginal was destroyed by the correlated draw",
					col.name, i, v)
			}
		}
	}
	if got := spearmanOf(a, b); math.Abs(got-targetRho) > 0.05 {
		t.Errorf("mixture rank correlation = %.4f, want within 0.05 of %.2f", got, targetRho)
	}
}

// TestSynthResidual_DeterministicByteIdentical is the contract this
// story puts most at risk: a shared correlated vector makes every
// participant's value depend on the order components are assigned, so
// byte identity is asserted rather than a tolerance.
func TestSynthResidual_DeterministicByteIdentical(t *testing.T) {
	spec := residualCondSpec(500, 0.7)
	first, _, err := synth.SynthBytes(spec, synth.Options{Seed: 41})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	second, _, err := synth.SynthBytes(residualCondSpec(500, 0.7), synth.Options{Seed: 41})
	if err != nil {
		t.Fatalf("SynthBytes (second run): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same spec + same seed produced different bytes through the correlated residual draw")
	}
}

// TestSynthResidual_ComponentOrderIsSchemaNotListOrder pins WHICH order
// the shared vector's components are assigned in.
//
// The correlation list is a document artifact — SpecFromProfile emits it
// in the capture's sorted pair order, a hand-authored spec in whatever
// order it was typed — so binding component indices to it would make the
// generated cohort depend on how the request was serialized. They are
// bound to drawer order instead, which buildModelDrawers has already
// fixed to SCHEMA field order. Permuting the list (and reversing each
// pair's endpoints, which is the same matrix by symmetry) must therefore
// leave the output byte-identical.
func TestSynthResidual_ComponentOrderIsSchemaNotListOrder(t *testing.T) {
	build := func(pairs []synth.CorrelationSpec) *synth.Spec {
		s := residualCondSpec(400, 0)
		s.Fields = append(s.Fields, synth.FieldSpec{
			Name: "tenure", Type: "f64",
			Distribution: synth.DistNormal,
			Params:       map[string]any{"mean": 30.0, "std": 8.0},
		})
		s.Models = append(s.Models, synth.FieldModelSpec{
			Field: "tenure", Intercept: 30, ResidualStd: 8,
		})
		s.ResidualCorrelations = pairs
		return s
	}

	declared, _, err := synth.SynthBytes(build([]synth.CorrelationSpec{
		{A: "spend", B: "visits", Correlation: 0.6},
		{A: "spend", B: "tenure", Correlation: 0.3},
		{A: "visits", B: "tenure", Correlation: -0.2},
	}), synth.Options{Seed: 43})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	permuted, _, err := synth.SynthBytes(build([]synth.CorrelationSpec{
		{A: "tenure", B: "visits", Correlation: -0.2},
		{A: "visits", B: "spend", Correlation: 0.6},
		{A: "tenure", B: "spend", Correlation: 0.3},
	}), synth.Options{Seed: 43})
	if err != nil {
		t.Fatalf("SynthBytes (permuted): %v", err)
	}
	if !bytes.Equal(declared, permuted) {
		t.Fatal("component order followed the residual_correlations list rather than schema field order")
	}
}

// TestSynthResidual_InertWhenNothingParticipates is the byte-identity
// property stated structurally, the residual-scale twin of
// TestSynthModel_StageInertWithoutDrawers: when fewer than two modelled
// fields survive to participate, the shared vector must not be drawn at
// all, so its presence is invisible in the output. That is the same
// reason a spec carrying no `residual_correlations` key reproduces its
// pre-E3-S2 bytes exactly.
//
// The zero-participant state is reached through buildModelDrawers'
// warn-and-skip for a target with no finite scale (a lognormal whose
// analytic std overflows), which is a legal spec — so the residual
// correlation names two modelled fields, validateSpec passes it, and one
// of them then compiles to nothing.
func TestSynthResidual_InertWhenNothingParticipates(t *testing.T) {
	build := func(withResidual bool) *synth.Spec {
		s := residualCondSpec(300, 0)
		for i := range s.Fields {
			if s.Fields[i].Name == "visits" {
				s.Fields[i].Distribution = synth.DistLogNormal
				s.Fields[i].Params = map[string]any{"mu": 0.0, "sigma": 30.0}
			}
		}
		if withResidual {
			s.ResidualCorrelations = []synth.CorrelationSpec{{A: "spend", B: "visits", Correlation: 0.7}}
		}
		return s
	}

	plain, _, err := synth.SynthBytes(build(false), synth.Options{Seed: 47})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	got, res, err := synth.SynthBytes(build(true), synth.Options{Seed: 47})
	if err != nil {
		t.Fatalf("SynthBytes (dropped participant): %v", err)
	}
	if !bytes.Equal(plain, got) {
		t.Fatal("a residual correlation that reached no participant still perturbed the RNG stream")
	}
	if !warningsContain(res.Warnings, "no surviving linear model") {
		t.Errorf("the dropped residual correlation was silent; warnings = %v", res.Warnings)
	}
}

// TestSynthResidual_UnsuppliedPairsAreCompletedAndReported checks that
// E3-S1's assume-and-record policy reaches the residual scale in the
// same words, without a second completion policy having grown beside it.
//
// Three participants, two pairs supplied: the third is completed as
// independent and the count says so. An unmeasured pair arrives here as
// an ABSENCE — SpecFromProfile deliberately translates
// residual_correlations.unmeasured into nothing — so this warning is the
// only place a reader learns the matrix was finished by assumption.
func TestSynthResidual_UnsuppliedPairsAreCompletedAndReported(t *testing.T) {
	s := residualCondSpec(300, 0)
	s.Fields = append(s.Fields, synth.FieldSpec{
		Name: "tenure", Type: "f64",
		Distribution: synth.DistNormal,
		Params:       map[string]any{"mean": 30.0, "std": 8.0},
	})
	s.Models = append(s.Models, synth.FieldModelSpec{
		Field: "tenure", Intercept: 30, ResidualStd: 8,
	})
	s.ResidualCorrelations = []synth.CorrelationSpec{
		{A: "spend", B: "visits", Correlation: 0.5},
		{A: "spend", B: "tenure", Correlation: 0.3},
	}

	_, res, err := synth.SynthBytes(s, synth.Options{Seed: 53})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if !warningsContain(res.Warnings, "residual correlation matrix completed by assumption: 1 of 3 pair(s) among 3 modelled field(s)") {
		t.Fatalf("the invented pair was not counted or not named as residual-scale; warnings = %v", res.Warnings)
	}
	if !warningsContain(res.Warnings, "an absent pair is unknown, not known to be uncorrelated") {
		t.Fatalf("the completion warning lost E3-S1's wording; warnings = %v", res.Warnings)
	}
}

// levelMeanGap is mean(east) - mean(west) for a two-level conditioned
// field.
func levelMeanGap(t *testing.T, values []float64, levels []string) float64 {
	t.Helper()
	var sumE, sumW float64
	var nE, nW int
	for i, v := range values {
		if levels[i] == "east" {
			sumE += v
			nE++
			continue
		}
		sumW += v
		nW++
	}
	if nE == 0 || nW == 0 {
		t.Fatalf("fixture drew only one level (east=%d west=%d)", nE, nW)
	}
	return sumE/float64(nE) - sumW/float64(nW)
}

// spearmanOf is Pearson over ranks — the quantity a Gaussian copula
// targets exactly, and the honest one to check for a marginal whose
// value scale is non-linear in the latent (a mixture, a lognormal).
// Ties are broken by index rather than averaged: the fields under test
// are continuous, so ties are measure-zero and an average-rank
// implementation would be untested code carrying no information.
func spearmanOf(a, b []float64) float64 {
	return pearsonOf(ranksOf(a), ranksOf(b))
}

func ranksOf(x []float64) []float64 {
	idx := make([]int, len(x))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return x[idx[i]] < x[idx[j]] })
	out := make([]float64, len(x))
	for r, i := range idx {
		out[i] = float64(r)
	}
	return out
}
