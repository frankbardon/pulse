package synth_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// E2-S3 (FR-16) test pack: the utility section compares the twin with
// the SOURCE partition, which the models section — a comparison with the
// CAPTURED model — cannot.

// churnByContractSpec is a `contract` categorical and a `churned`
// packed_bool driven by it.
func churnByContractSpec(rows int, monthly, twoYear float64) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{
				Name: "contract", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"monthly", "one_year", "two_year"},
					"weights": []any{1.0, 1.0, 1.0},
				},
			},
			{
				Name: "churned", Type: "packed_bool",
				Distribution: synth.DistBernoulli,
				Params:       map[string]any{"p": 0.5},
			},
		},
		Models: []synth.FieldModelSpec{{
			Field:     "churned",
			Intercept: 0.5,
			Predictors: []synth.ModelPredictorSpec{
				catLevel("contract", "monthly", monthly),
				catLevel("contract", "two_year", twoYear),
			},
			ResidualStd: 0.45,
		}},
	}
}

// mergedTwin generates a source partition from sourceSpec and tops it up
// with twinSpec's rows, returning the merged schema and records.
func mergedTwin(t *testing.T, sourceSpec, twinSpec *synth.Spec, seed int64) (*encoding.Schema, []byte) {
	t.Helper()
	src, _, err := synth.SynthBytes(sourceSpec, synth.Options{Seed: seed})
	if err != nil {
		t.Fatalf("source SynthBytes: %v", err)
	}
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", src, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := synth.AugmentFromProfile(fs, twinSpec, "/source.pulse", "/merged.pulse", synth.Options{Seed: seed + 1})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	return schema, data[len(data)-r.Len():]
}

func findTargetRate(report *synth.FidelityReport, target, by string) *synth.TargetRateFidelity {
	if report.Utility == nil {
		return nil
	}
	for _, e := range report.Utility.TargetRates {
		if e.Target == target && e.By == by {
			return e
		}
	}
	return nil
}

// TestBuildTargetRates_CompressedTwinTheModelsSectionCallsRecovered is
// the acceptance case: the twin's captured model carries a much weaker
// contract effect than the source shows, and generation applies that
// model faithfully. The models section — which compares against the
// captured coefficients — therefore reports it recovered, with no flag.
// The utility section compares against the source rows and reports the
// churn-by-contract contrast compressed.
func TestBuildTargetRates_CompressedTwinTheModelsSectionCallsRecovered(t *testing.T) {
	t.Parallel()

	source := churnByContractSpec(3000, 0.30, -0.30)
	twin := churnByContractSpec(3000, 0.06, -0.06)
	schema, records := mergedTwin(t, source, twin, 501)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, twin)
	synth.BuildTargetRates(report, schema, records, twin)

	m := findModelFidelity(report, "churned")
	if m == nil || m.Error != "" {
		t.Fatalf("models entry for churned = %+v", m)
	}
	if m.Flagged {
		t.Fatalf("models flagged churned (max delta %.3f) — the fixture must be a FAITHFUL generation of the twin's own model", m.MaxDelta)
	}

	e := findTargetRate(report, "churned", "contract")
	if e == nil {
		t.Fatalf("no target_rates entry for churned by contract; utility=%+v", report.Utility)
	}
	if e.Error != "" {
		t.Fatalf("entry error: %s", e.Error)
	}
	t.Logf("churned by contract: source spread %.3f synthetic %.3f ratio %.2f, ε² %.3f → %.3f, verdict %s",
		e.SourceSpread, e.SyntheticSpread, e.SpreadRatio, e.SourceEpsilonSquared, e.SyntheticEpsilonSquared, e.Verdict)
	if e.Verdict != synth.UtilityVerdictCompressed {
		t.Errorf("verdict = %q, want %q", e.Verdict, synth.UtilityVerdictCompressed)
	}
	if e.SpreadRatio <= 0 || e.SpreadRatio > 0.5 {
		t.Errorf("spread ratio = %.3f, want a strongly compressed (0, 0.5]", e.SpreadRatio)
	}
	if !(e.SyntheticEpsilonSquared < e.SourceEpsilonSquared) {
		t.Errorf("synthetic ε² %.4f not below source %.4f", e.SyntheticEpsilonSquared, e.SourceEpsilonSquared)
	}
	byLevel := map[string]*synth.TargetRateLevel{}
	for _, l := range e.Levels {
		byLevel[l.Level] = l
	}
	monthly, twoYear := byLevel["monthly"], byLevel["two_year"]
	if len(e.Levels) != 3 || monthly == nil || twoYear == nil {
		t.Fatalf("levels = %d entries, want the three contracts", len(e.Levels))
	}
	if monthly.SourceN == 0 || monthly.SyntheticN == 0 {
		t.Errorf("level counts missing: %+v", *monthly)
	}
	// The source's monthly rate is far above its two-year rate.
	if d := monthly.SourceMean - twoYear.SourceMean; d < 0.4 {
		t.Errorf("source monthly − two_year rate = %.3f, want the planted ≥ 0.4 contrast", d)
	}
}

// TestBuildTargetRates_FaithfulTwinIsPreserved is the other half: a twin
// generated from the source's own model keeps its contrast, so the
// verdict cannot be a constant.
func TestBuildTargetRates_FaithfulTwinIsPreserved(t *testing.T) {
	t.Parallel()

	spec := churnByContractSpec(3000, 0.30, -0.30)
	schema, records := mergedTwin(t, spec, spec, 503)

	report := &synth.FidelityReport{}
	synth.BuildTargetRates(report, schema, records, spec)
	e := findTargetRate(report, "churned", "contract")
	if e == nil || e.Error != "" {
		t.Fatalf("entry = %+v", e)
	}
	if e.Verdict != synth.UtilityVerdictPreserved {
		t.Errorf("verdict = %q (ratio %.3f), want %q", e.Verdict, e.SpreadRatio, synth.UtilityVerdictPreserved)
	}
	if e.SpreadRatio < 0.8 || e.SpreadRatio > 1.2 {
		t.Errorf("spread ratio = %.3f, want within 0.8–1.2", e.SpreadRatio)
	}
}

// TestBuildTargetRates_InflatedTwin: a twin whose contrast exceeds the
// source's is reported as such, not as preserved.
func TestBuildTargetRates_InflatedTwin(t *testing.T) {
	t.Parallel()

	schema, records := mergedTwin(t, churnByContractSpec(3000, 0.06, -0.06), churnByContractSpec(3000, 0.30, -0.30), 505)
	report := &synth.FidelityReport{}
	twin := churnByContractSpec(3000, 0.30, -0.30)
	synth.BuildTargetRates(report, schema, records, twin)
	e := findTargetRate(report, "churned", "contract")
	if e == nil || e.Verdict != synth.UtilityVerdictInflated {
		t.Fatalf("entry = %+v, want verdict %q", e, synth.UtilityVerdictInflated)
	}
}

// TestBuildTargetRates_NoModelNoSection: a spec with no applied model
// leaves Utility nil, so its report JSON carries no `utility` key — the
// byte-identity half for every report predating `--fit-models`.
func TestBuildTargetRates_NoModelNoSection(t *testing.T) {
	t.Parallel()

	spec := churnByContractSpec(500, 0.3, -0.3)
	spec.Models = nil
	schema, records := mergedTwin(t, spec, spec, 507)
	report := &synth.FidelityReport{}
	synth.BuildTargetRates(report, schema, records, spec)
	if report.Utility != nil {
		t.Fatalf("Utility = %+v, want nil for a models-free spec", report.Utility)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "utility") {
		t.Fatalf("report JSON carries a utility key: %s", raw)
	}
}

// TestCompareModelFits_FoldsBothSides pins the fold: the fit statistic
// follows the regression type, the ratio is synthetic ÷ source, terms
// are sorted, a term one side lacks is named, and a side's failure is
// carried without hiding the other.
func TestCompareModelFits_FoldsBothSides(t *testing.T) {
	t.Parallel()

	req := &types.Request{Regressions: []*types.RegressionSpec{
		{Type: types.REG_GLM, Name: "churn", Target: "churned"},
		{Type: types.REG_OLS, Name: "spend", Target: "spend"},
	}}
	src := &types.Response{Regressions: []*types.RegressionResult{
		{Type: types.REG_GLM, Name: "churn", Family: "binomial", PseudoR2: 0.20, NObs: 1000,
			Coefficients: map[string]float64{"b": -0.5, "a": 0.4, "_intercept": 0.1},
			StdErrors:    map[string]float64{"b": 0.05, "a": 0.04}},
		{Type: types.REG_OLS, Name: "spend", R2: 0.5, NObs: 1000, Coefficients: map[string]float64{"x": 2}},
	}}
	syn := &types.Response{Regressions: []*types.RegressionResult{
		{Type: types.REG_GLM, Name: "churn", Family: "binomial", PseudoR2: 0.17, NObs: 900,
			Coefficients: map[string]float64{"b": -0.4, "a": 0.2}},
		{Type: types.REG_OLS, Name: "spend", Error: &types.SlotError{Code: "PULSE_REGRESSION_SINGULAR", Message: "singular design"}},
	}}
	report := &synth.FidelityReport{}
	synth.CompareModelFits(report, req, src, syn, nil, nil)
	if report.Utility == nil || len(report.Utility.ModelFits) != 2 {
		t.Fatalf("model fits = %+v", report.Utility)
	}
	churn := report.Utility.ModelFits[0]
	if churn.Statistic != synth.UtilityFitPseudoR2 || churn.SourceFit != 0.20 || churn.SyntheticFit != 0.17 {
		t.Errorf("churn fit = %+v", churn)
	}
	if want := 0.17 / 0.20; churn.FitRatio != want {
		t.Errorf("fit ratio = %v, want %v", churn.FitRatio, want)
	}
	if churn.Target != "churned" || churn.SourceNObs != 1000 || churn.SyntheticNObs != 900 {
		t.Errorf("identity / n = %+v", churn)
	}
	var terms []string
	for _, c := range churn.Coefficients {
		terms = append(terms, c.Term)
	}
	if strings.Join(terms, ",") != "_intercept,a,b" {
		t.Errorf("terms = %v, want sorted _intercept,a,b", terms)
	}
	if c := churn.Coefficients[0]; c.Missing != "synthetic" {
		t.Errorf("_intercept missing = %q, want synthetic", c.Missing)
	}
	if c := churn.Coefficients[1]; c.Ratio != 0.5 || c.SourceStdError != 0.04 {
		t.Errorf("a = %+v, want ratio 0.5 and the source SE", c)
	}

	spend := report.Utility.ModelFits[1]
	if spend.Statistic != synth.UtilityFitR2 || spend.SourceFit != 0.5 {
		t.Errorf("spend source side = %+v", spend)
	}
	if spend.SyntheticError != "singular design" || spend.FitRatio != 0 {
		t.Errorf("spend synthetic side = %+v, want the slot error and no ratio", spend)
	}
}
