package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// showcaseABCohort synthesises the A/B readout showcase cohort (40,000
// users, pages_viewed correlated 0.78 with the pre-period
// prior_sessions, a planted +0.07 page lift on new_page) at its fixed
// seed into fs as ab.pulse.
func showcaseABCohort(t *testing.T, p *pulse.Pulse) {
	t.Helper()
	raw, err := os.ReadFile("testdata/experiment/ab_test.synth.json")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := synth.ParseSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Synth(context.Background(), spec, "ab.pulse", pulse.SynthOptions{Seed: 64}); err != nil {
		t.Fatal(err)
	}
}

func decodeRequest(t *testing.T, body string) *types.Request {
	t.Helper()
	var req types.Request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

// cupedFormula is the single-call total() recipe: y − θ(x − x̄) with the
// pooled θ = Cov(x, y)/Var(x) and x̄ as ratios of cohort totals.
const cupedFormula = `pages_viewed - ((total(1)*total(pages_viewed*prior_sessions) - total(pages_viewed)*total(prior_sessions)) / (total(1)*total(prior_sessions*prior_sessions) - total(prior_sessions)*total(prior_sessions))) * (prior_sessions - total(prior_sessions)/total(1))`

// TestExperimentCUPED_OracleEqualsTwoStep: on the showcase cohort,
// params.covariate on TEST_WELCH reads the adjusted lift new_page −
// old_page directly — diff 0.0862, 95% CI 0.0374–0.1349, p 0.000535 —
// and equals the two-step it replaces (the total() formula's adjusted
// column under a plain TEST_WELCH), with theta 0.7601 and covariate mean
// 10.0076 (the showcase's REG_OLS slope and AGG_AVERAGE). TEST_T and
// TEST_Z_TWO_SAMPLE take the same adjustment.
func TestExperimentCUPED_OracleEqualsTwoStep(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	showcaseABCohort(t, p)

	one := decodeRequest(t, `{"cohort":{"filename":"ab.pulse"},"tests":[
		{"type":"TEST_WELCH","field":"pages_viewed","split_by":"arm","label":"welch","params":{"covariate":"prior_sessions","reference":"old_page"}},
		{"type":"TEST_T","field":"pages_viewed","split_by":"arm","label":"t","params":{"covariate":"prior_sessions","reference":"old_page"}},
		{"type":"TEST_Z_TWO_SAMPLE","field":"pages_viewed","split_by":"arm","label":"z","params":{"covariate":"prior_sessions","reference":"old_page"}},
		{"type":"TEST_WELCH","field":"pages_viewed","split_by":"arm","label":"raw","params":{"reference":"old_page"}}]}`)
	if env := predictEnvelope(t, p, fs, "ab.pulse", one); len(env.Errors) != 0 {
		t.Fatalf("predict: %+v", env.Errors)
	}
	resp, err := p.Process(ctx, one)
	if err != nil {
		t.Fatal(err)
	}
	welch, tt, z, raw := resp.Tests[0], resp.Tests[1], resp.Tests[2], resp.Tests[3]
	d := welch.Details
	for _, c := range []struct {
		name      string
		got, want float64
		tol       float64
	}{
		{"diff", d["diff"].(float64), 0.0862, 5e-5},
		{"ci_low", d["ci_low"].(float64), 0.0374, 5e-5},
		{"ci_high", d["ci_high"].(float64), 0.1349, 5e-5},
		{"p_value", welch.PValue, 0.000535, 5e-7},
		{"theta", d["theta"].(float64), 0.7601, 5e-5},
		{"covariate_mean", d["covariate_mean"].(float64), 10.0076, 5e-5},
		{"variance_reduction", d["variance_reduction"].(float64), 0.60, 5e-3},
		{"diff_unadjusted", d["diff_unadjusted"].(float64), raw.Details["diff"].(float64), 1e-12},
	} {
		if math.Abs(c.got-c.want) > c.tol {
			t.Errorf("%s = %.6g, want %.6g", c.name, c.got, c.want)
		}
	}
	if d["n_covariate_dropped"] != int64(0) {
		t.Errorf("n_covariate_dropped %v", d["n_covariate_dropped"])
	}
	// TEST_T two-sample is the Welch test; the z-test shares its SE.
	if tt.Statistic != welch.Statistic || tt.PValue != welch.PValue || z.Statistic != welch.Statistic ||
		z.Details["diff"] != d["diff"] || z.Details["theta"] != d["theta"] {
		t.Errorf("TEST_T / TEST_Z_TWO_SAMPLE adjustment differs from TEST_WELCH: %v / %v", tt.Details, z.Details)
	}

	// The two-step: the total() formula's adjusted column, plain Welch.
	two := decodeRequest(t, `{"cohort":{"filename":"ab.pulse"},
		"attributes":[{"type":"ATTR_FORMULA","field":"pages_viewed","label":"pages_cuped","expression":`+jsonString(cupedFormula)+`}],
		"tests":[{"type":"TEST_WELCH","field":"pages_cuped","split_by":"arm","params":{"reference":"old_page"}}]}`)
	tresp, err := p.Process(ctx, two)
	if err != nil {
		t.Fatal(err)
	}
	ref := tresp.Tests[0]
	for _, c := range [][3]any{
		{"statistic", welch.Statistic, ref.Statistic}, {"df", welch.DF, ref.DF}, {"p_value", welch.PValue, ref.PValue},
		{"diff", d["diff"], ref.Details["diff"]}, {"ci_low", d["ci_low"], ref.Details["ci_low"]}, {"ci_high", d["ci_high"], ref.Details["ci_high"]},
	} {
		got, want := c[1].(float64), c[2].(float64)
		if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
			t.Errorf("%s: covariate %.15g, two-step %.15g", c[0], got, want)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestExperimentCUPED_StreamsAndShardsAgree: a covariate-adjusted test
// is one pass — predict streams it (the engine's streaming arm equals
// its buffered arm: internal/processing TestCuped_StreamingEqualsBuffered)
// — and over a shard archive of the same rows it reports what the
// single file does.
func TestExperimentCUPED_StreamsAndShardsAgree(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	showcaseABCohort(t, p)
	if _, err := p.FilterToFileBySetAndExpr(ctx, "ab.pulse", "s0.pulse", "", nil, "user_id < 120001"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.FilterToFileBySetAndExpr(ctx, "ab.pulse", "s1.pulse", "", nil, "user_id >= 120001"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(ctx, "archive.pulse", []string{"s0.pulse", "s1.pulse"}); err != nil {
		t.Fatal(err)
	}
	body := func(cohort string) *types.Request {
		return decodeRequest(t, `{"cohort":{"filename":"`+cohort+`"},
			"aggregations":[{"type":"AGG_COUNT","field":"user_id"}],
			"tests":[{"type":"TEST_WELCH","field":"pages_viewed","split_by":"arm","params":{"covariate":"prior_sessions","reference":"old_page"}}]}`)
	}
	env := predictEnvelope(t, p, fs, "ab.pulse", body("ab.pulse"))
	if res := env.Data.(*descriptor.PredictResult); len(env.Errors) != 0 || !res.Streamable {
		t.Fatalf("predict errors %+v streamable %v (%v)", env.Errors, res.Streamable, res.StreamableReasons)
	}
	single, err := p.Process(ctx, body("ab.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	sharded, err := p.Process(ctx, body("archive.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(single.Tests)
	b, _ := json.Marshal(sharded.Tests)
	if string(a) != string(b) {
		t.Fatalf("single file and shard archive differ:\n%s\n%s", a, b)
	}
	if single.Tests[0].Details["n"].([]int64)[0] == 0 {
		t.Fatal("no rows")
	}
}

// TestExperimentCUPED_CovariateFieldTypeParity walks every field type in
// the covariate slot of a mean test and of TEST_PROP_Z: predict accepts
// exactly what the runtime accepts, and both refuse the rest with one
// code — a non-numeric column PULSE_TEST_COVARIATE_INVALID
// (not_numeric), a vector PULSE_VECTOR_FIELD_NOT_SCALAR.
func TestExperimentCUPED_CovariateFieldTypeParity(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for _, tmpl := range []string{
		`{"type":"TEST_WELCH","field":"x","split_by":"g","params":{"covariate":"$C"}}`,
		`{"type":"TEST_PROP_Z","field":"subj","split_by":"g","params":{"success":"s1","covariate":"$C"}}`,
	} {
		covariateFieldTypeParity(ctx, t, p, fs, cohort, tmpl)
	}
}

func covariateFieldTypeParity(ctx context.Context, t *testing.T, p *pulse.Pulse, fs afero.Fs, cohort, tmpl string) {
	t.Helper()
	accepted := 0
	for _, ft := range acceptanceFieldTypes() {
		req := decodeRequest(t, `{"tests":[`+strings.Replace(tmpl, "$C", "t_"+ft.String(), 1)+`]}`)
		req.Cohort = &types.Cohort{Filename: cohort}
		_, rerr := p.Process(ctx, req)
		var ce *errors.CodedError
		runtimeOK := rerr == nil || (stderrors.As(rerr, &ce) && acceptanceDataOutcomes[ce.Code])
		env := predictEnvelope(t, p, fs, cohort, req)
		switch {
		case runtimeOK && len(env.Errors) == 0:
			accepted++
			if ft.IsCategorical() || ft.IsSet() || ft.IsVector() {
				t.Errorf("%s: accepted as a covariate", ft)
			}
		case runtimeOK || len(env.Errors) != 1 || ce == nil || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message:
			t.Errorf("%s %s: runtime %v, predict %+v", tmpl, ft, rerr, env.Errors)
		case ft.IsVector() && ce.Code != errors.PULSE_VECTOR_FIELD_NOT_SCALAR,
			!ft.IsVector() && ce.Code != errors.PULSE_TEST_COVARIATE_INVALID:
			t.Errorf("%s %s: refused with %s", tmpl, ft, ce.Code)
		}
	}
	if accepted == 0 {
		t.Fatalf("%s: no covariate type accepted", tmpl)
	}
}

// TestExperimentCUPED_PropZOnShowcase: TEST_PROP_Z adjusts the
// retention rate by the pre-period prior_sessions (retention rises
// gently with it in the showcase, so θ > 0 and the reduction is small
// but positive): one pass (predict streams it), the raw difference
// is the plain test's, and the adjusted interval is narrower than the
// unadjusted Wald one by exactly the reported variance reduction.
func TestExperimentCUPED_PropZOnShowcase(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	showcaseABCohort(t, p)
	req := decodeRequest(t, `{"cohort":{"filename":"ab.pulse"},
		"aggregations":[{"type":"AGG_COUNT","field":"user_id"}],
		"tests":[
		{"type":"TEST_PROP_Z","field":"retained_30d","split_by":"arm","label":"adj","params":{"success":"yes","reference":"old_page","covariate":"prior_sessions"}},
		{"type":"TEST_PROP_Z","field":"retained_30d","split_by":"arm","label":"raw","params":{"success":"yes","reference":"old_page"}}]}`)
	env := predictEnvelope(t, p, fs, "ab.pulse", req)
	if res := env.Data.(*descriptor.PredictResult); len(env.Errors) != 0 || !res.Streamable {
		t.Fatalf("predict errors %+v streamable %v (%v)", env.Errors, res.Streamable, res.StreamableReasons)
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	adj, raw := resp.Tests[0], resp.Tests[1]
	d := adj.Details
	if adj.Variant != "two_proportion_adjusted" || raw.Variant != "two_proportion_pooled" {
		t.Fatalf("variants %s / %s", adj.Variant, raw.Variant)
	}
	if got, want := d["diff_unadjusted"].(float64), raw.Details["diff"].(float64); math.Abs(got-want) > 1e-12 {
		t.Errorf("diff_unadjusted %v, plain diff %v", got, want)
	}
	vr := d["variance_reduction"].(float64)
	if !(vr > 0) || !(d["theta"].(float64) > 0) {
		t.Fatalf("variance_reduction %v theta %v: prior_sessions predicts retention", vr, d["theta"])
	}
	width := func(r *types.TestResult) float64 {
		return r.Details["ci_high"].(float64) - r.Details["ci_low"].(float64)
	}
	if got, want := width(adj)/width(raw), math.Sqrt(1-vr); math.Abs(got-want) > 1e-9 {
		t.Errorf("interval width ratio %v, want √(1 − variance_reduction) %v", got, want)
	}
}

// TestExperimentCUPED_GroupedThetaPerPartition: under groups every
// partition pools its own θ and x̄ — each group's adjusted test (mean
// and proportion) is exactly the ungrouped adjusted test filtered to
// that group, and the per-partition slopes differ.
func TestExperimentCUPED_GroupedThetaPerPartition(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	showcaseABCohort(t, p)
	tests := `[{"type":"TEST_WELCH","field":"pages_viewed","split_by":"arm","params":{"covariate":"prior_sessions","reference":"old_page"}},
		{"type":"TEST_PROP_Z","field":"retained_30d","split_by":"arm","params":{"success":"yes","covariate":"prior_sessions","reference":"old_page"}}]`
	req := decodeRequest(t, `{"cohort":{"filename":"ab.pulse"},"groups":[{"type":"GROUP_CATEGORY","field":"device"}],
		"aggregations":[{"type":"AGG_COUNT","field":"user_id"}],"tests":`+tests+`}`)
	if env := predictEnvelope(t, p, fs, "ab.pulse", req); len(env.Errors) != 0 {
		t.Fatalf("predict: %+v", env.Errors)
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Tests) != 6 {
		t.Fatalf("%d results, want 2 tests × 3 devices", len(resp.Tests))
	}
	thetas := map[float64]bool{}
	for k, r := range resp.Tests {
		single := decodeRequest(t, `{"cohort":{"filename":"ab.pulse"},"aggregations":[{"type":"AGG_COUNT","field":"user_id"}],"tests":`+tests+`}`)
		single.Filterers = []*types.Filterer{gtKeyFilter(req.Groups[0], r.GroupKey[0])}
		sresp, err := p.Process(ctx, single)
		if err != nil {
			t.Fatal(err)
		}
		want := sresp.Tests[k/3]
		for _, c := range [][3]any{
			{"statistic", r.Statistic, want.Statistic}, {"p_value", r.PValue, want.PValue},
			{"diff", r.Details["diff"], want.Details["diff"]}, {"theta", r.Details["theta"], want.Details["theta"]},
			{"covariate_mean", r.Details["covariate_mean"], want.Details["covariate_mean"]},
			{"variance_reduction", r.Details["variance_reduction"], want.Details["variance_reduction"]},
		} {
			if c[1].(float64) != c[2].(float64) {
				t.Errorf("tests[%d] group %v %s: grouped %v, filtered %v", k, r.GroupKey, c[0], c[1], c[2])
			}
		}
		thetas[r.Details["theta"].(float64)] = true
	}
	if len(thetas) != 6 {
		t.Errorf("per-partition thetas %v: want one per (test, group)", thetas)
	}
}
