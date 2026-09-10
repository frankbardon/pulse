package synth_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
)

// E5-S2 test pack: the residual-correlation half of the model-recovery
// section, and the numeric-target pair sections it sits beside.
//
// Fixtures reuse E5-S1's modelFidelitySpec / augmentForModelFidelity
// harness deliberately — the two halves read the same decoded partition
// and ride the same refits, so testing them against two different
// cohorts would hide a disagreement between them rather than expose
// one.

// residualPair finds one scored pair regardless of the order the two
// endpoints were declared in.
func residualPair(sec *synth.ModelResidualCorrelationFidelity, a, b string) *synth.ModelResidualPairFidelity {
	if sec == nil {
		return nil
	}
	for _, p := range sec.Pairs {
		if (p.A == a && p.B == b) || (p.A == b && p.B == a) {
			return p
		}
	}
	return nil
}

// syntheticValueCorrelation is the realized RAW-VALUE Pearson
// correlation of two numeric fields over the merged cohort's
// _synthetic=true partition.
//
// It exists so the "this is measured on residuals, not on values" claim
// can be asserted rather than asserted-about: a fixture whose two
// targets share a strong predictor has a large value correlation and a
// zero residual correlation at the same time, and only a test that
// computes both can tell that the section reported the second one.
func syntheticValueCorrelation(t *testing.T, schema *encoding.Schema, records []byte, a, b string) float64 {
	t.Helper()
	rr := encoding.NewRecordReader(bytes.NewReader(records), schema)
	values := make(map[string]float64, len(schema.Fields))
	nulls := make(map[string]bool, len(schema.Fields))
	var xs, ys []float64
	for {
		if err := rr.ReadRecordWithWide(values, nulls, nil); err != nil {
			break
		}
		if values["_synthetic"] == 0 || nulls[a] || nulls[b] {
			continue
		}
		xs = append(xs, values[a])
		ys = append(ys, values[b])
	}
	if len(xs) < 2 {
		t.Fatalf("synthetic partition carries %d co-present rows of %s x %s", len(xs), a, b)
	}
	var sx, sy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
	}
	mx, my := sx/float64(len(xs)), sy/float64(len(ys))
	var num, dx, dy float64
	for i := range xs {
		ax, ay := xs[i]-mx, ys[i]-my
		num += ax * ay
		dx += ax * ax
		dy += ay * ay
	}
	return num / math.Sqrt(dx*dy)
}

// TestBuildModelResidualFidelity_RecoversRequestedCorrelation is the
// primary accuracy assertion: a residual correlation generation was
// asked to impose comes back out of the generated partition.
//
// Both targets are `normal`, so the latent inversion is affine and the
// arithmetic stays checkable by hand while the code under test still
// takes the full Q/Phi round trip — the same fixture discipline E5-S1's
// coefficient tests use.
func TestBuildModelResidualFidelity_RecoversRequestedCorrelation(t *testing.T) {
	const wantRho = 0.7
	const recoveryBar = 0.05

	spec := modelFidelitySpec(20000,
		synth.FieldModelSpec{
			Field:       "spend",
			Intercept:   100,
			Predictors:  []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
			ResidualStd: 15,
		},
		synth.FieldModelSpec{
			Field:       "tenure",
			Intercept:   30,
			Predictors:  []synth.ModelPredictorSpec{catLevel("plan", "pro", 5)},
			ResidualStd: 4,
		},
	)
	spec.ResidualCorrelations = []synth.CorrelationSpec{{A: "spend", B: "tenure", Correlation: wantRho}}

	schema, records := augmentForModelFidelity(t, spec, 20000, 301)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	sec := report.ModelResidualCorrelations
	if sec == nil {
		t.Fatal("ModelResidualCorrelations is nil for a spec declaring a residual correlation")
	}
	// Component order is drawer order, which is SCHEMA field order —
	// spend precedes tenure in modelFidelitySpec. Pinning it here is
	// what keeps the reported participant list a function of the schema
	// rather than of the order the correlation array was written in.
	if got := strings.Join(sec.Fields, ","); got != "spend,tenure" {
		t.Errorf("Fields = %q, want %q (component order is schema field order)", got, "spend,tenure")
	}
	if sec.Compared != 1 {
		t.Fatalf("Compared = %d, want 1", sec.Compared)
	}
	if len(sec.Unmeasured) != 0 {
		t.Fatalf("Unmeasured = %+v, want none", sec.Unmeasured)
	}

	p := residualPair(sec, "spend", "tenure")
	if p == nil {
		t.Fatal("no scored pair for spend x tenure")
	}
	if p.CapturedRho != wantRho {
		t.Errorf("CapturedRho = %v, want %v (the figure generation was asked to hit)", p.CapturedRho, wantRho)
	}
	if math.Abs(p.RecoveredRho-wantRho) > recoveryBar {
		t.Errorf("RecoveredRho = %.4f, captured %.2f — gap %.4f exceeds the %.2f bar",
			p.RecoveredRho, wantRho, math.Abs(p.RecoveredRho-wantRho), recoveryBar)
	}
	if got := math.Abs(p.RecoveredRho - p.CapturedRho); math.Abs(p.Delta-got) > 1e-12 {
		t.Errorf("Delta = %v, want %v", p.Delta, got)
	}
	if p.Flagged {
		t.Errorf("Flagged on a %.4f gap, below the %.2f tolerance", p.Delta, synth.ResidualRecoveryTolerance)
	}
	// N is the pair's own co-occurrence count within the refit row cap,
	// not the generated row count: 20,000 rows were generated and the
	// refits consume at most 10,000 of them.
	if p.N != 10000 {
		t.Errorf("N = %d, want 10000 (the recovery row cap; no nulls in this fixture)", p.N)
	}
	if sec.MaxDelta != p.Delta || math.Abs(sec.MeanDelta-p.Delta) > 1e-12 {
		t.Errorf("MeanDelta/MaxDelta = %v/%v, want %v for a single compared pair",
			sec.MeanDelta, sec.MaxDelta, p.Delta)
	}
}

// TestBuildModelResidualFidelity_MeasuresResidualsNotValues is the
// acceptance criterion that the comparison is on RESIDUALS.
//
// Both targets are driven hard by the SAME predictor and declared
// residually INDEPENDENT. Their raw values therefore correlate strongly
// — the shared predictor is in both — while their residuals do not, and
// the two figures are far enough apart that a section accidentally
// measuring values could not pass. This is the exact confusion
// synth/residual_corr.go's header warns about: imposing a raw-value
// correlation on a residual applies every shared predictor twice.
func TestBuildModelResidualFidelity_MeasuresResidualsNotValues(t *testing.T) {
	spec := modelFidelitySpec(20000,
		synth.FieldModelSpec{
			Field:     "spend",
			Intercept: 100,
			Predictors: []synth.ModelPredictorSpec{
				catLevel("region", "east", 60), catLevel("region", "north", -60),
			},
			ResidualStd: 8,
		},
		synth.FieldModelSpec{
			Field:     "tenure",
			Intercept: 30,
			Predictors: []synth.ModelPredictorSpec{
				catLevel("region", "east", 14), catLevel("region", "north", -14),
			},
			ResidualStd: 2,
		},
	)
	spec.ResidualCorrelations = []synth.CorrelationSpec{{A: "spend", B: "tenure", Correlation: 0}}

	schema, records := augmentForModelFidelity(t, spec, 20000, 311)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	p := residualPair(report.ModelResidualCorrelations, "spend", "tenure")
	if p == nil {
		t.Fatal("no scored pair for spend x tenure")
	}

	valueRho := syntheticValueCorrelation(t, schema, records, "spend", "tenure")
	if valueRho < 0.5 {
		t.Fatalf("fixture is not discriminating: raw-value correlation is %.4f, "+
			"too small to distinguish a value measurement from a residual one", valueRho)
	}
	if math.Abs(p.RecoveredRho) > 0.05 {
		t.Errorf("RecoveredRho = %.4f, want ~0 (residuals are independent here); "+
			"the raw-value correlation over the same rows is %.4f, so this reads as a VALUE measurement",
			p.RecoveredRho, valueRho)
	}
}

// TestBuildModelResidualFidelity_AbsentWithoutResidualCorrelations
// pins the old-format guarantee. A spec that declares no residual
// correlations — every spec predating `profile create
// --residual-correlations` — must leave the key off the wire entirely,
// so a report produced for such a profile is byte-identical to the
// pre-E5-S2 document.
func TestBuildModelResidualFidelity_AbsentWithoutResidualCorrelations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []synth.FieldModelSpec
	}{
		{
			name: "modelled but no residual correlations",
			models: []synth.FieldModelSpec{{
				Field:       "spend",
				Intercept:   100,
				Predictors:  []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
				ResidualStd: 12,
			}},
		},
		{
			name:   "no models at all",
			models: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := modelFidelitySpec(4000, tc.models...)
			schema, records := augmentForModelFidelity(t, spec, 4000, 321)

			report := &synth.FidelityReport{}
			synth.BuildModelFidelity(report, schema, records, spec)

			if report.ModelResidualCorrelations != nil {
				t.Fatalf("ModelResidualCorrelations = %+v, want nil", report.ModelResidualCorrelations)
			}
			out, err := json.Marshal(report)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if bytes.Contains(out, []byte("model_residual_correlations")) {
				t.Errorf("report carries a model_residual_correlations key: %s", out)
			}
		})
	}
}

// TestBuildModelResidualFidelity_UnmeasurableEndpointCarriesNoRho
// covers the no-fabricated-structure rule at the recovery boundary: a
// pair whose endpoint could not be refitted at all is recorded AS
// unmeasured, with a reason, and never as a recovered zero beside a
// captured 0.5.
//
// `tenure`'s model names a level the output cohort's dictionary does
// not carry, so every one of its design columns is dead and the refit
// has nothing to estimate from — the ModelFidelity entry reports that,
// and this section must not paper over it.
func TestBuildModelResidualFidelity_UnmeasurableEndpointCarriesNoRho(t *testing.T) {
	spec := modelFidelitySpec(6000,
		synth.FieldModelSpec{
			Field:       "spend",
			Intercept:   100,
			Predictors:  []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
			ResidualStd: 12,
		},
		synth.FieldModelSpec{
			Field:       "tenure",
			Intercept:   30,
			Predictors:  []synth.ModelPredictorSpec{catLevel("region", "atlantis", 9)},
			ResidualStd: 4,
		},
	)
	spec.ResidualCorrelations = []synth.CorrelationSpec{{A: "spend", B: "tenure", Correlation: 0.5}}

	schema, records := augmentForModelFidelity(t, spec, 6000, 331)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	sec := report.ModelResidualCorrelations
	if sec == nil {
		t.Fatal("ModelResidualCorrelations is nil")
	}
	if sec.Compared != 0 || len(sec.Pairs) != 0 {
		t.Fatalf("Compared = %d / len(Pairs) = %d, want 0/0", sec.Compared, len(sec.Pairs))
	}
	if len(sec.Unmeasured) != 1 {
		t.Fatalf("len(Unmeasured) = %d, want 1", len(sec.Unmeasured))
	}
	u := sec.Unmeasured[0]
	if u.Reason != synth.ResidualUnmeasuredNoModelFit {
		t.Errorf("Reason = %q, want %q", u.Reason, synth.ResidualUnmeasuredNoModelFit)
	}
	if u.CapturedRho != 0.5 {
		t.Errorf("CapturedRho = %v, want 0.5 — the reader needs to know how much went unverified", u.CapturedRho)
	}
	// The struct has no recovered-rho slot at all, so the JSON cannot
	// carry one. Asserted on the wire rather than on the struct because
	// that is where a future slot would do its damage.
	out, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(out, []byte("recovered_rho")) {
		t.Errorf("unmeasured entry carries a recovered_rho key: %s", out)
	}
}

// manyModelSpec builds a fixture with n modelled numerics over one
// categorical, plus the fully-declared C(n,2) residual submatrix — the
// shape the motivating cohort produces at 55 applied models and 1,485
// pairs, in miniature.
func manyModelSpec(n, rows int) *synth.Spec {
	s := &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{{
			Name: "region", Type: "categorical_u8",
			Distribution: synth.DistWeightedCategorical,
			Params: map[string]any{
				"values":  []any{"east", "west"},
				"weights": []any{1.0, 1.0},
			},
		}},
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("m%02d", i)
		s.Fields = append(s.Fields, synth.FieldSpec{
			Name: name, Type: "f64",
			Distribution: synth.DistNormal,
			Params:       map[string]any{"mean": 100.0, "std": 20.0},
		})
		s.Models = append(s.Models, synth.FieldModelSpec{
			Field:       name,
			Intercept:   100,
			Predictors:  []synth.ModelPredictorSpec{catLevel("region", "east", 10)},
			ResidualStd: 15,
		})
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			s.ResidualCorrelations = append(s.ResidualCorrelations, synth.CorrelationSpec{
				A: fmt.Sprintf("m%02d", i), B: fmt.Sprintf("m%02d", j), Correlation: 0.3,
			})
		}
	}
	return s
}

// TestBuildModelResidualFidelity_ListingIsBoundedAtScale is the
// readability criterion. The section is quadratic in participants by
// construction, so the per-pair listing is capped worst-first while the
// aggregate counters still describe every compared pair — nothing is
// hidden, and a pair that did not make the listing recovered at least
// as well as the last one that did.
func TestBuildModelResidualFidelity_ListingIsBoundedAtScale(t *testing.T) {
	const participants = 9 // C(9,2) = 36 pairs, comfortably over the cap
	const wantPairs = participants * (participants - 1) / 2

	spec := manyModelSpec(participants, 4000)
	schema, records := augmentForModelFidelity(t, spec, 4000, 341)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	sec := report.ModelResidualCorrelations
	if sec == nil {
		t.Fatal("ModelResidualCorrelations is nil")
	}
	if sec.Compared+len(sec.Unmeasured)+sec.UnmeasuredOmitted != wantPairs {
		t.Fatalf("Compared %d + unmeasured %d/%d != %d declared pairs",
			sec.Compared, len(sec.Unmeasured), sec.UnmeasuredOmitted, wantPairs)
	}
	if len(sec.Pairs) > 20 {
		t.Errorf("len(Pairs) = %d, want at most the 20-entry cap", len(sec.Pairs))
	}
	if sec.Compared-len(sec.Pairs) != sec.Omitted {
		t.Errorf("Omitted = %d, want %d (Compared %d less the %d listed) — the truncated tail must be counted, not dropped",
			sec.Omitted, sec.Compared-len(sec.Pairs), sec.Compared, len(sec.Pairs))
	}
	// Worst-first: the listing must be sorted by descending Delta, and
	// its largest entry must be the aggregate MaxDelta. A cap that kept
	// an arbitrary 20 would fail both.
	for i := 1; i < len(sec.Pairs); i++ {
		if sec.Pairs[i-1].Delta < sec.Pairs[i].Delta {
			t.Fatalf("Pairs not sorted worst-first at %d: %.6f then %.6f",
				i, sec.Pairs[i-1].Delta, sec.Pairs[i].Delta)
		}
	}
	if len(sec.Pairs) > 0 && sec.Pairs[0].Delta != sec.MaxDelta {
		t.Errorf("Pairs[0].Delta = %v but MaxDelta = %v", sec.Pairs[0].Delta, sec.MaxDelta)
	}
	if sec.MeanDelta > sec.MaxDelta {
		t.Errorf("MeanDelta %v exceeds MaxDelta %v", sec.MeanDelta, sec.MaxDelta)
	}
}

// TestBuildModelResidualFidelity_IsByteReproducible pins the report's
// own determinism. The section folds a mean over every compared pair
// and sorts a listing; both must be functions of the spec alone, never
// of Go map iteration order (E2-S4 — float addition is not associative,
// and a fidelity number that moves when nothing moved is unusable for
// the regression-gating this report exists to support).
func TestBuildModelResidualFidelity_IsByteReproducible(t *testing.T) {
	spec := manyModelSpec(6, 3000)
	schema, records := augmentForModelFidelity(t, spec, 3000, 351)

	var first []byte
	for i := 0; i < 4; i++ {
		report := &synth.FidelityReport{}
		synth.BuildModelFidelity(report, schema, records, spec)
		out, err := json.Marshal(report.ModelResidualCorrelations)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if i == 0 {
			first = out
			continue
		}
		if !bytes.Equal(first, out) {
			t.Fatalf("run %d differs:\nfirst: %s\nthis:  %s", i, first, out)
		}
	}
}

// retirementSpec is the half-2 fixture: two numerics, one of which
// carries a linear model, plus a contended relationship on every arm.
// `spend` is modelled; `tenure` is not, and keeps its pick-one pair.
func retirementSpec(rows int) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{1.0, 1.0}}},
			{Name: "brand", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"acme", "zeta"}, "weights": []any{1.0, 1.0}}},
			{Name: "features", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{"options": []any{"premium", "trial"}, "frequencies": []any{0.5, 0.5}}},
			{Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{"options": []any{"email", "sms"}, "frequencies": []any{0.5, 0.5}}},
			{Name: "spend", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 100.0, "std": 20.0}},
			{Name: "tenure", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 30.0, "std": 6.0}},
		},
		CategoricalPairs: []synth.CategoricalPairSpec{{
			A: "region", B: "brand", Cells: []synth.CategoricalPairCellSpec{
				{AValue: "east", BValue: "acme", Count: 90}, {AValue: "west", BValue: "zeta", Count: 90},
			}}},
		CategoricalNumericPairs: []synth.CategoricalNumericPairSpec{
			{A: "region", B: "spend", Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "east", Mean: 130, Std: 10}, {Category: "west", Mean: 70, Std: 10}}},
			{A: "region", B: "tenure", Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "east", Mean: 36, Std: 3}, {Category: "west", Mean: 24, Std: 3}}},
		},
		SetNumericPairs: []synth.SetNumericPairSpec{{
			Set: "channels", Option: "email", Numeric: "spend",
			Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "selected", Mean: 140, Std: 10}, {Category: "not_selected", Mean: 60, Std: 10}}}},
		SetCategoricalPairs: []synth.SetCategoricalPairSpec{{
			Set: "features", Option: "premium", Categorical: "region",
			Cells: []synth.CategoricalPairCellSpec{
				{AValue: "selected", BValue: "east", Count: 90},
				{AValue: "not_selected", BValue: "west", Count: 90}}}},
		SetSetPairs: []synth.SetSetPairSpec{{
			SetA: "features", OptionA: "premium", SetB: "channels", OptionB: "email",
			Cells: []synth.CategoricalPairCellSpec{
				{AValue: "selected", BValue: "selected", Count: 90},
				{AValue: "not_selected", BValue: "not_selected", Count: 90}}}},
	}
}

// buildAllPairwiseSections drives exactly the call sequence
// writeSynthFidelityReport uses, so a test here fails for the same
// reason production would.
func buildAllPairwiseSections(schema *encoding.Schema, records []byte, spec *synth.Spec) *synth.FidelityReport {
	report := &synth.FidelityReport{}
	catPairs, catNumPairs, setCatPairs, setNumPairs, setSetPairs, correlations := synth.ResolveConflicts(spec)
	synth.BuildPairwise(report, schema, records, correlations, nil)
	synth.BuildCategoricalPairwise(report, schema, records, catPairs)
	synth.BuildCategoricalNumericPairwise(report, schema, records, catNumPairs)
	synth.BuildSetCategoricalPairwise(report, schema, records, setCatPairs)
	synth.BuildSetNumericPairwise(report, schema, records, setNumPairs)
	synth.BuildSetSetPairwise(report, schema, records, setSetPairs)
	synth.BuildModelFidelity(report, schema, records, spec)
	return report
}

// TestFidelityReport_NumericTargetSectionsRetiredForModelledTargets is
// the second half of E5-S2: a numeric reached by a linear model is
// scored by `models`, never by the pick-one pair sections that describe
// a mechanism which did not run for it — while the three
// NON-numeric-target arms are untouched.
//
// Both reports are scored against the SAME generated records, so the
// three surviving sections must come back byte-identical: any
// difference between them would be the model arm reaching an arm it has
// no business touching. Only the numeric-target sections may move, and
// only by the modelled field's entry.
func TestFidelityReport_NumericTargetSectionsRetiredForModelledTargets(t *testing.T) {
	modelled := retirementSpec(6000)
	modelled.Models = []synth.FieldModelSpec{{
		Field:       "spend",
		Intercept:   100,
		Predictors:  []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
		ResidualStd: 12,
	}}
	schema, records := augmentForModelFidelity(t, modelled, 6000, 361)

	// The model-free control scores the identical records, which is what
	// makes the three surviving sections comparable byte for byte.
	control := *modelled
	control.Models = nil

	withModel := buildAllPairwiseSections(schema, records, modelled)
	without := buildAllPairwiseSections(schema, records, &control)

	// The retired arm: no entry for the modelled target...
	for _, e := range withModel.CategoricalNumericPairwise {
		if e.B == "spend" {
			t.Errorf("categorical_numeric_pairwise carries an entry for the modelled target %q (%s -> %s)", e.B, e.A, e.B)
		}
	}
	for _, e := range withModel.SetNumericPairwise {
		if e.Numeric == "spend" {
			t.Errorf("set_numeric_pairwise carries an entry for the modelled target %q", e.Numeric)
		}
	}
	if len(withModel.SetNumericPairwise) != 0 {
		t.Errorf("set_numeric_pairwise = %d entries, want none — its only pair targeted the modelled field",
			len(withModel.SetNumericPairwise))
	}
	// ...and the UNMODELLED numeric in the same spec keeps its pair.
	// The retirement is per TARGET, not per document: a numeric with no
	// model would otherwise be reconstructed from nothing at all.
	var sawTenure bool
	for _, e := range withModel.CategoricalNumericPairwise {
		if e.B == "tenure" {
			sawTenure = true
		}
	}
	if !sawTenure {
		t.Error("categorical_numeric_pairwise dropped the UNMODELLED target tenure: the retirement is per target, not per document")
	}
	// The control proves the retirement is what removed it, rather than
	// the fixture never having carried it.
	var controlSawSpend bool
	for _, e := range without.CategoricalNumericPairwise {
		if e.B == "spend" {
			controlSawSpend = true
		}
	}
	if !controlSawSpend {
		t.Fatal("fixture is not discriminating: the model-free control scores no spend pair either")
	}
	if findModelFidelity(withModel, "spend") == nil {
		t.Error("spend has no `models` entry: the relationship was retired from the pair arm and scored nowhere")
	}

	// The three non-numeric-target arms, byte for byte.
	for _, arm := range []struct {
		name string
		a, b any
	}{
		{"categorical_pairwise", withModel.CategoricalPairwise, without.CategoricalPairwise},
		{"set_categorical_pairwise", withModel.SetCategoricalPairwise, without.SetCategoricalPairwise},
		{"set_set_pairwise", withModel.SetSetPairwise, without.SetSetPairwise},
	} {
		gotJSON, err := json.Marshal(arm.a)
		if err != nil {
			t.Fatalf("%s: marshal: %v", arm.name, err)
		}
		wantJSON, err := json.Marshal(arm.b)
		if err != nil {
			t.Fatalf("%s: marshal: %v", arm.name, err)
		}
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Errorf("%s moved when a model was added\n with: %s\n without: %s", arm.name, gotJSON, wantJSON)
		}
		if bytes.Equal(gotJSON, []byte("null")) {
			t.Errorf("%s is empty in both runs — the fixture does not exercise it", arm.name)
		}
	}
}

// TestFidelityReport_OldFormatSpecCarriesNoNewKeys pins the
// old-format guarantee at the DOCUMENT level: a spec carrying neither
// `models` nor `residual_correlations` — every spec predating
// `profile create --fit-models` — produces a report whose top-level key
// set is exactly the pre-E5-S1 one, so its bytes are unchanged.
func TestFidelityReport_OldFormatSpecCarriesNoNewKeys(t *testing.T) {
	spec := retirementSpec(3000)
	schema, records := augmentForModelFidelity(t, spec, 3000, 371)

	report := buildAllPairwiseSections(schema, records, spec)
	out, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var keyed map[string]json.RawMessage
	if err := json.Unmarshal(out, &keyed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The pre-E5-S1 key set. A new key appearing here for a spec that
	// declares no model is a wire-shape regression for every profile
	// captured before this effort.
	allowed := map[string]bool{
		"source_rows": true, "synthetic_rows": true, "fields": true,
		"pairwise": true, "categorical_pairwise": true, "categorical_numeric_pairwise": true,
		"set_fields": true, "set_categorical_pairwise": true, "set_numeric_pairwise": true,
		"set_set_pairwise": true, "warnings": true,
	}
	for k := range keyed {
		if !allowed[k] {
			t.Errorf("old-format report carries the new key %q", k)
		}
	}
	if _, ok := keyed["categorical_numeric_pairwise"]; !ok {
		t.Error("fixture is not discriminating: the old-format report scores no categorical-numeric pair at all")
	}
}
