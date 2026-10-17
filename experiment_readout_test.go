package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// abReadout is the whole A/B readout as one Compose batch with no
// aggregation anywhere: a tests slot — the sample-ratio check as a
// goodness-of-fit chi-square (kept out of the correction), the
// trial-start lift read against the control arm, the CUPED-adjusted
// pages lift — and a regression slot whose is_variant coefficient joins
// the same holm compose family. (One Process request carrying both tests
// and a regression is the buffered path: predict's "regression with
// tier-1 tests" reason; Compose keeps each slot on the streaming arm.)
const abReadout = `{"multiplicity":{"method":"holm","family":"compose"},"requests":[
	{"label":"readout","cohort":{"filename":"ab.pulse"},"tests":[
		{"type":"TEST_CHISQ","rows":"arm","label":"srm","params":{"expected":{"new_page":0.5,"old_page":0.5}},"multiplicity":{"method":"none"}},
		{"type":"TEST_PROP_Z","field":"trial_started","split_by":"arm","label":"trial_start","params":{"success":"yes","reference":"old_page"}},
		{"type":"TEST_WELCH","field":"pages_viewed","split_by":"arm","label":"pages_cuped","params":{"covariate":"prior_sessions","reference":"old_page"}}]},
	{"label":"regression_adjustment","cohort":{"filename":"ab.pulse"},"regressions":[
		{"type":"REG_OLS","name":"pages_adjusted","target":"pages_viewed","predictors":["is_variant","prior_sessions"],
		 "multiplicity":{"method":"holm","coefficients":["is_variant"]}}]}]}`

// holm is R p.adjust(p, "holm"): sort ascending, scale the i-th by
// m − i, carry the running maximum, cap at 1.
func holm(ps []float64) []float64 {
	idx := make([]int, len(ps))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return ps[idx[a]] < ps[idx[b]] })
	out := make([]float64, len(ps))
	run := 0.0
	for rank, i := range idx {
		v := math.Min(1, ps[i]*float64(len(ps)-rank))
		run = math.Max(run, v)
		out[i] = run
	}
	return out
}

// TestExperimentReadout_EndToEnd (U48 acceptance oracle): on the A/B
// showcase cohort (seed 64 = the casebook's ab_test.pulse) one Compose
// batch carries the whole readout and every figure equals the
// showcase's — SRM χ² 0.757 / df 1 / p 0.384; trial-start lift
// new_page − old_page +1.02 pp (CI +0.41 to +1.62 pp, p 0.00105); CUPED
// pages lift 0.0862 (CI 0.0374–0.1349, p 0.000535); the adjusted
// is_variant coefficient 0.0862 (p 0.00054) — the two lifts and the
// coefficient corrected together as one holm family of three, the SRM
// check raw. Predict-compose says every slot streams with no AGG_COUNT
// and sizes the same family; the run takes the streaming arm.
func TestExperimentReadout_EndToEnd(t *testing.T) {
	fs := afero.NewMemMapFs()
	var mu sync.Mutex
	arms := map[observe.OperationKind]observe.Arm{}
	p, err := pulse.New(pulse.Options{FS: fs, Hooks: &observe.Hooks{
		OnOperationEnd: func(_ context.Context, info observe.OperationInfo, res observe.OperationResult) {
			mu.Lock()
			arms[info.Kind] = res.Arm
			mu.Unlock()
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	showcaseABCohort(t, p)
	batch := func() *types.ComposedRequest {
		var c types.ComposedRequest
		if err := json.Unmarshal([]byte(abReadout), &c); err != nil {
			t.Fatal(err)
		}
		return &c
	}

	env, err := p.PredictCompose(ctx, batch())
	if err != nil {
		t.Fatal(err)
	}
	pred := env.Data.(*pulse.ComposePredictResult)
	if !pred.Valid || len(env.Errors) != 0 || len(pred.Slots) != 2 {
		t.Fatalf("predict-compose valid %v slots %d errors %+v", pred.Valid, len(pred.Slots), env.Errors)
	}
	for i, s := range pred.Slots {
		if !s.Streamable || len(s.StreamableReasons) != 0 {
			t.Errorf("slot %d streamable %v %v", i, s.Streamable, s.StreamableReasons)
		}
		if f := s.Families; len(f) != 1 || f[0].ID != "compose" {
			t.Errorf("slot %d families %+v, want the compose family", i, f)
		}
	}
	if pv := pred.Slots[0].PValues; pv == nil || pv.Total != 3 || pv.Uncorrected != 1 {
		t.Errorf("tests slot p_values %+v, want total 3, uncorrected 1 (srm)", pv)
	}
	if pv := pred.Slots[1].PValues; pv == nil || pv.Total != 2 || pv.Uncorrected != 1 {
		t.Errorf("regression slot p_values %+v, want total 2, uncorrected 1 (prior_sessions)", pv)
	}

	out, err := p.Compose(ctx, batch())
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotArm := arms[observe.OpCompose]
	mu.Unlock()
	if gotArm != observe.ArmStreaming {
		t.Fatalf("compose ran on the %q arm, want streaming", gotArm)
	}
	tests, regs := out.Responses[0], out.Responses[1]
	for _, r := range out.Responses {
		if r.Data != nil || r.Metadata.TotalRows != 40000 {
			t.Fatalf("slot %s data %v total rows %d", r.Label, r.Data, r.Metadata.TotalRows)
		}
	}
	srm, trial, cuped, reg := tests.Tests[0], tests.Tests[1], tests.Tests[2], regs.Regressions[0]

	near := func(name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.6g, want %.6g ± %g", name, got, want, tol)
		}
	}
	// Sample-ratio check: goodness of fit against the 50/50 design.
	if srm.Variant != "goodness_of_fit" || srm.DF != 1 {
		t.Errorf("srm variant %q df %v", srm.Variant, srm.DF)
	}
	near("srm chi2", srm.Statistic, 0.757, 5e-4)
	near("srm p", srm.PValue, 0.384, 5e-4)
	if srm.PAdjusted != nil || srm.Multiplicity != nil {
		t.Errorf("srm joined the family: %v %+v", srm.PAdjusted, srm.Multiplicity)
	}

	// Trial-start lift against the control arm.
	td := trial.Details
	if g := td["groups"].([]string); len(g) != 2 || g[0] != "new_page" || g[1] != "old_page" || td["reference"] != "old_page" {
		t.Errorf("trial groups %v reference %v", td["groups"], td["reference"])
	}
	if n, s := td["n"].([]int64), td["successes"].([]int64); n[0] != 20087 || n[1] != 19913 || s[0] != 2267 || s[1] != 2045 {
		t.Errorf("trial n %v successes %v, want 20087/19913 and 2267/2045", n, s)
	}
	near("trial diff", td["diff"].(float64), 0.0102, 5e-5)
	near("trial ci_low", td["ci_low"].(float64), 0.0041, 5e-5)
	near("trial ci_high", td["ci_high"].(float64), 0.0162, 5e-5)
	near("trial p", trial.PValue, 0.00105, 5e-6)

	// CUPED-adjusted pages lift.
	cd := cuped.Details
	if cd["reference"] != "old_page" {
		t.Errorf("cuped reference %v", cd["reference"])
	}
	near("cuped diff", cd["diff"].(float64), 0.0862, 5e-5)
	near("cuped ci_low", cd["ci_low"].(float64), 0.0374, 5e-5)
	near("cuped ci_high", cd["ci_high"].(float64), 0.1349, 5e-5)
	near("cuped p", cuped.PValue, 0.000535, 5e-7)

	// Regression adjustment: the same lift as a coefficient.
	near("is_variant coefficient", reg.Coefficients["is_variant"], 0.0862, 5e-5)
	near("is_variant p", reg.PValues["is_variant"], 0.00054, 5e-6)
	if _, ok := reg.PAdjusted["prior_sessions"]; ok || len(reg.PAdjusted) != 1 {
		t.Errorf("regression members %v, want is_variant alone", reg.PAdjusted)
	}

	// One holm family of three: the two lifts and the coefficient.
	want := holm([]float64{trial.PValue, cuped.PValue, reg.PValues["is_variant"]})
	got := []float64{*trial.PAdjusted, *cuped.PAdjusted, reg.PAdjusted["is_variant"]}
	for i, name := range []string{"trial_start", "pages_cuped", "is_variant"} {
		if math.Abs(got[i]-want[i]) > 1e-12 {
			t.Errorf("%s p_adjusted %.12g, holm over the family %.12g", name, got[i], want[i])
		}
	}
	for name, m := range map[string]*types.AppliedMultiplicity{"trial_start": trial.Multiplicity, "pages_cuped": cuped.Multiplicity, "pages_adjusted": reg.Multiplicity} {
		if m == nil || m.Method != types.MultiplicityMethodHolm || m.Family != types.MultiplicityFamilyCompose || m.M != 3 {
			t.Errorf("%s multiplicity %+v, want holm compose m 3", name, m)
		}
	}
	if !*trial.SignificantAdjusted || !*cuped.SignificantAdjusted || !reg.SignificantAdjusted["is_variant"] {
		t.Errorf("a lift lost significance after holm: %v %v %v", *trial.SignificantAdjusted, *cuped.SignificantAdjusted, reg.SignificantAdjusted)
	}
}
