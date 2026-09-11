package synth_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// FU-19 test pack: model recovery for a STAIRCASE target — a `discrete`
// small integer or a `bernoulli` boolean.
//
// Both are generated through a step/staircase Q, so a generated value
// pins the latent to an INTERVAL and latentFor has no point inverse to
// offer. WP-C's refusal was sound and it was also a measured gap: on the
// motivating survey cohort it took FidelityReport.Models from 14
// comparable targets of 55 down to 2, on a cohort where 90 of 105
// modelled targets are packed_bool and the rest small integers — the
// instrument stopped covering the fields the feature applies to.
//
// The restoration is a CALIBRATED interval-midpoint probit score. The
// raw score is attenuated, exactly as WP-C said; what makes it honest is
// that the SAME score is computed on both sides — once from the
// generated values, once as the conditional mean the CAPTURED model
// implies for each row — and OLS is linear in its response, so the
// attenuation divides out exactly rather than approximately. See
// synth/fidelity_score.go.
//
// Every test below asserts BOTH halves where they exist, because either
// alone is trivially satisfiable: an estimator that echoes the captured
// coefficient passes "recovers a faithful model" and fails "flags a
// broken one", and one that always returns zero does the reverse.

// modelFidelityStaircaseSpec is the staircase fixture: the same three
// predictor fields modelFidelitySpec carries, plus a 7-level `score`
// (u4, `discrete`) and a `flag` (packed_bool, `bernoulli`) — the two
// shapes a real survey cohort is made of.
func modelFidelityStaircaseSpec(rows int, models ...synth.FieldModelSpec) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"east", "west", "north"},
					"weights": []any{1.0, 1.0, 1.0},
				},
			},
			{
				Name: "plan", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"basic", "pro"},
					"weights": []any{1.0, 1.0},
				},
			},
			{
				Name: "features", Type: "set_u8",
				Distribution: synth.DistSetBernoulli,
				Params: map[string]any{
					"options":     []any{"premium", "trial"},
					"frequencies": []any{0.5, 0.5},
				},
			},
			{
				Name: "score", Type: "u4",
				Distribution: "discrete",
				Params: map[string]any{
					"values":  []any{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0},
					"weights": []any{0.09, 0.10, 0.12, 0.24, 0.18, 0.13, 0.14},
				},
			},
			{
				Name: "flag", Type: "packed_bool",
				Distribution: synth.DistBernoulli,
				Params:       map[string]any{"p": 0.6},
			},
		},
		Models: models,
	}
}

// TestBuildModelFidelity_DiscreteTargetRecoversItsCapturedCoefficients
// is the restoration: a 7-level `discrete` target whose model generation
// applied faithfully must come back with an estimate for every term, on
// the latent scale, within the flagging band — and NOT with the "step
// quantile" error WP-C's refusal produced.
//
// The coefficients are deliberately LARGE (retention 0.50 to 0.85 on
// this fixture), which is what makes the bar discriminating: measured
// over eight seeds, the mean gap on the raw uncalibrated midpoint score
// is -0.827 / +0.208 / -0.257 / -0.340 for the four terms — every one
// of them shrunk toward zero and every one far outside the 0.10 bar —
// while the calibrated gap averages +0.008 to +0.018 and never exceeds
// 0.073. Weaker coefficients would leave the raw score inside the bar
// too, and the test would pass with the calibration deleted.
func TestBuildModelFidelity_DiscreteTargetRecoversItsCapturedCoefficients(t *testing.T) {
	const recoveryBar = 0.10

	spec := modelFidelityStaircaseSpec(20000, synth.FieldModelSpec{
		Field:     "score",
		Intercept: 4.0,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 3.0),
			catLevel("region", "north", -2.4),
			catLevel("plan", "pro", 1.2),
			setOption("features", "premium", 1.6),
		},
		ResidualStd: 1.6,
	})
	schema, records := augmentForModelFidelity(t, spec, 20000, 211)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	m := findModelFidelity(report, "score")
	if m == nil {
		t.Fatal("no entry for score")
	}
	if m.Error != "" {
		t.Fatalf("Error = %q — a staircase target must be RECOVERABLE, not refused", m.Error)
	}
	if m.Scale != synth.RecoveryScaleProbitScore {
		t.Errorf("Scale = %q, want %q — a reader must be able to tell which comparison they hold",
			m.Scale, synth.RecoveryScaleProbitScore)
	}
	if m.NObs < 9000 {
		t.Errorf("NObs = %d — a staircase value is always scoreable, so admission must not drop rows", m.NObs)
	}
	if m.Flagged {
		t.Errorf("Flagged = true on a faithfully generated staircase model (MaxDelta %.4f)", m.MaxDelta)
	}
	// A staircase's cut points come from the captured MARGINAL, which
	// generation holds exactly by construction, so the score-scale
	// intercept is pinned by the marginal rather than by the model and
	// carries no independent evidence. Reporting a calibrated one would
	// put a number where there is no measurement.
	if m.RecoveredIntercept != 0 || m.InterceptDelta != 0 {
		t.Errorf("RecoveredIntercept = %v / InterceptDelta = %v — a score-scale entry reports no "+
			"intercept comparison", m.RecoveredIntercept, m.InterceptDelta)
	}

	std := m.LatentScale
	if std <= 0 {
		t.Fatalf("LatentScale = %v, want the discrete support's own std", std)
	}
	for _, w := range []struct {
		field, level string
		coef         float64
	}{
		{"region", "east", 3.0},
		{"region", "north", -2.4},
		{"plan", "pro", 1.2},
		{"features", "premium", 1.6},
	} {
		p := findPredictorFidelity(m, w.field, w.level)
		if p == nil {
			t.Fatalf("no predictor entry for %s=%s", w.field, w.level)
		}
		if p.Error != "" {
			t.Fatalf("%s=%s: Error %q", w.field, w.level, p.Error)
		}
		captured := w.coef / std
		if math.Abs(p.CapturedCoefficient-captured) > 1e-12 {
			t.Errorf("%s=%s: CapturedCoefficient = %v, want %v", w.field, w.level, p.CapturedCoefficient, captured)
		}
		if math.Abs(p.RecoveredCoefficient-captured) > recoveryBar {
			t.Errorf("%s=%s: RecoveredCoefficient = %.4f, captured %.4f — gap %.4f exceeds the %.2f bar",
				w.field, w.level, p.RecoveredCoefficient, captured,
				math.Abs(p.RecoveredCoefficient-captured), recoveryBar)
		}
		if p.Flagged {
			t.Errorf("%s=%s: Flagged on a %.4f gap", w.field, w.level, p.Delta)
		}
		// The retention factor is what the raw midpoint score loses to
		// the discretisation and the calibration divides back out. It
		// must be reported, and it must be a real fraction — a value of
		// 1 would mean the calibration is a no-op and the recovery above
		// is coincidence.
		if p.ScoreRetention <= 0 || p.ScoreRetention >= 1 {
			t.Errorf("%s=%s: ScoreRetention = %v, want a fraction in (0,1)", w.field, w.level, p.ScoreRetention)
		}
		if p.NFired < 1000 {
			t.Errorf("%s=%s: NFired = %d, too thin for the recovery to mean anything", w.field, w.level, p.NFired)
		}
		if p.StdError <= 0 {
			t.Errorf("%s=%s: StdError = %v, want a positive estimate", w.field, w.level, p.StdError)
		}
	}
}

// TestBuildModelFidelity_DiscreteTargetFlagsBrokenGeneration is the
// other half, and the one that makes the half above evidence: a cohort
// generated with every coefficient set to zero, reported against a spec
// claiming real ones, must recover ~0 and flag.
//
// Without it, an implementation that simply echoed the captured
// coefficient into RecoveredCoefficient would pass the recovery test
// perfectly.
func TestBuildModelFidelity_DiscreteTargetFlagsBrokenGeneration(t *testing.T) {
	inert := modelFidelityStaircaseSpec(12000, synth.FieldModelSpec{
		Field:     "score",
		Intercept: 4.0,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 0),
			catLevel("plan", "pro", 0),
		},
		ResidualStd: 1.6,
	})
	schema, records := augmentForModelFidelity(t, inert, 12000, 223)

	claimed := modelFidelityStaircaseSpec(12000, synth.FieldModelSpec{
		Field:     "score",
		Intercept: 4.0,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 1.2),
			catLevel("plan", "pro", 0.6),
		},
		ResidualStd: 1.6,
	})

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, claimed)

	m := findModelFidelity(report, "score")
	if m == nil {
		t.Fatal("no entry for score")
	}
	if m.Error != "" {
		t.Fatalf("Error = %q — the refit must succeed; it is the RESULT that is bad", m.Error)
	}
	if !m.Flagged {
		t.Errorf("Flagged = false on a cohort carrying none of the claimed structure (MaxDelta %.4f)", m.MaxDelta)
	}
	east := findPredictorFidelity(m, "region", "east")
	if east == nil {
		t.Fatal("no entry for region=east")
	}
	if !east.Flagged {
		t.Errorf("region=east not flagged: captured %.4f recovered %.4f delta %.4f se %.4f",
			east.CapturedCoefficient, east.RecoveredCoefficient, east.Delta, east.StdError)
	}
	if math.Abs(east.RecoveredCoefficient) > 0.10 {
		t.Errorf("region=east: RecoveredCoefficient = %.4f, want ~0 for a relationship generation never applied",
			east.RecoveredCoefficient)
	}
	if east.NFired < 1000 {
		t.Fatalf("region=east: NFired = %d — the term must actually fire", east.NFired)
	}
}

// TestBuildModelFidelity_BooleanTargetRecoversAtKEqualsTwo is the K = 2
// case WP-C's refusal singled out.
//
// The interval-midpoint score at two levels IS the two-valued
// construction the bernoulli arm rejected, and the rejection was right
// about the RAW score: it retains the least of any K and would report a
// large attenuation for a generation path that is exactly correct. The
// calibration is what settles it, and settles it WITHOUT a K gate — the
// same expected-score projection divides the same factor out, so K = 2
// is not a degenerate case but the case with the smallest retention and
// therefore the widest standard error. That is reported, not hidden:
// ScoreRetention is asserted to be the smallest thing on the surface.
func TestBuildModelFidelity_BooleanTargetRecoversAtKEqualsTwo(t *testing.T) {
	const recoveryBar = 0.12

	spec := modelFidelityStaircaseSpec(20000, synth.FieldModelSpec{
		Field:     "flag",
		Intercept: 0.6,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 0.25),
			catLevel("plan", "pro", -0.18),
		},
		ResidualStd: 0.45,
	})
	schema, records := augmentForModelFidelity(t, spec, 20000, 233)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	m := findModelFidelity(report, "flag")
	if m == nil {
		t.Fatal("no entry for flag")
	}
	if m.Error != "" {
		t.Fatalf("Error = %q — the K=2 case must recover, not refuse", m.Error)
	}
	if m.Scale != synth.RecoveryScaleProbitScore {
		t.Errorf("Scale = %q, want %q", m.Scale, synth.RecoveryScaleProbitScore)
	}
	if m.Flagged {
		t.Errorf("Flagged = true on a faithfully generated boolean model (MaxDelta %.4f)", m.MaxDelta)
	}
	for _, w := range []struct {
		field, level string
		coef         float64
	}{
		{"region", "east", 0.25},
		{"plan", "pro", -0.18},
	} {
		p := findPredictorFidelity(m, w.field, w.level)
		if p == nil {
			t.Fatalf("no predictor entry for %s=%s", w.field, w.level)
		}
		if p.Error != "" {
			t.Fatalf("%s=%s: Error %q", w.field, w.level, p.Error)
		}
		captured := w.coef / m.LatentScale
		if math.Abs(p.RecoveredCoefficient-captured) > recoveryBar {
			t.Errorf("%s=%s: RecoveredCoefficient = %.4f, captured %.4f — gap %.4f exceeds %.2f",
				w.field, w.level, p.RecoveredCoefficient, captured,
				math.Abs(p.RecoveredCoefficient-captured), recoveryBar)
		}
		if p.ScoreRetention <= 0 || p.ScoreRetention > 0.75 {
			t.Errorf("%s=%s: ScoreRetention = %v — two levels retain the LEAST of any K; "+
				"a value near 1 means no calibration happened", w.field, w.level, p.ScoreRetention)
		}
	}
}

// TestBuildModelFidelity_InvertibleTargetKeepsTheLatentScale pins the
// half that must NOT move: a distribution with a real point inverse is
// still compared on the latent scale, with no score, no calibration and
// no retention figure. The two arms are one mechanism with the identity
// calibration on one side, and this is the assertion that keeps the
// identity honest.
func TestBuildModelFidelity_InvertibleTargetKeepsTheLatentScale(t *testing.T) {
	spec := modelFidelitySpec(8000, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 30),
		},
		ResidualStd: 10,
	})
	schema, records := augmentForModelFidelity(t, spec, 8000, 241)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	m := findModelFidelity(report, "spend")
	if m == nil {
		t.Fatal("no entry for spend")
	}
	if m.Scale != "" {
		t.Errorf("Scale = %q, want \"\" — a latent-invertible target carries no score scale", m.Scale)
	}
	if m.RecoveredIntercept == 0 {
		t.Error("RecoveredIntercept = 0 — the intercept comparison stays live for an invertible target")
	}
	p := findPredictorFidelity(m, "region", "east")
	if p == nil {
		t.Fatal("no entry for region=east")
	}
	if p.ScoreRetention != 0 {
		t.Errorf("ScoreRetention = %v, want 0 (absent) on a latent-invertible target", p.ScoreRetention)
	}
}

// TestBuildModelFidelity_StaircaseTargetContributesNoResidualCorrelation
// pins the boundary of the restoration, which is deliberate rather than
// an oversight.
//
// A coefficient can be calibrated because OLS is LINEAR in its response,
// so projecting the same score twice divides the attenuation out exactly.
// A correlation cannot: the factor relating two score residuals'
// correlation to the correlation of the residuals that actually drove
// the draws depends on the PAIR's joint distribution, not on either
// marginal, so there is no projection that removes it. Feeding score
// residuals into the residual-correlation section would therefore report
// an attenuated rho as if it were a measured one — the exact class of
// wrong number the refusal exists to prevent.
//
// Those pairs stay in the unmeasured list under no_model_fit, counted
// rather than silent.
func TestBuildModelFidelity_StaircaseTargetContributesNoResidualCorrelation(t *testing.T) {
	spec := modelFidelityStaircaseSpec(12000,
		synth.FieldModelSpec{
			Field: "score", Intercept: 4.0,
			Predictors:  []synth.ModelPredictorSpec{catLevel("region", "east", 1.2)},
			ResidualStd: 1.6,
		},
		synth.FieldModelSpec{
			Field: "flag", Intercept: 0.6,
			Predictors:  []synth.ModelPredictorSpec{catLevel("region", "east", 0.2)},
			ResidualStd: 0.45,
		},
	)
	spec.ResidualCorrelations = []synth.CorrelationSpec{{A: "score", B: "flag", Correlation: 0.5}}
	schema, records := augmentForModelFidelity(t, spec, 12000, 251)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	// Both models must still RECOVER — the point is that recovery and
	// residual correlation are separable, not that a staircase target is
	// excluded from the section wholesale.
	for _, f := range []string{"score", "flag"} {
		m := findModelFidelity(report, f)
		if m == nil {
			t.Fatalf("no entry for %s", f)
		}
		if m.Error != "" {
			t.Errorf("%s: Error %q — the coefficient recovery must still run", f, m.Error)
		}
	}

	rc := report.ModelResidualCorrelations
	if rc == nil {
		t.Fatal("ModelResidualCorrelations is nil — the pair must be REPORTED as unmeasured, not dropped")
	}
	if rc.Compared != 0 {
		t.Errorf("Compared = %d, want 0 — a score residual's correlation is attenuated by an "+
			"amount no projection can remove, so it must not be reported as measured", rc.Compared)
	}
	if len(rc.Unmeasured) != 1 {
		t.Fatalf("len(Unmeasured) = %d, want 1", len(rc.Unmeasured))
	}
	u := rc.Unmeasured[0]
	if u.Reason != synth.ResidualUnmeasuredNoModelFit {
		t.Errorf("Unmeasured[0].Reason = %q, want %q", u.Reason, synth.ResidualUnmeasuredNoModelFit)
	}
	if u.CapturedRho != 0.5 {
		t.Errorf("Unmeasured[0].CapturedRho = %v, want 0.5 — the gap must carry the figure it could not check", u.CapturedRho)
	}
}
