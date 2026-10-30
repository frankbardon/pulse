package integration_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
)

// sem_bootstrap_test.go is MAT_SEM's params.resample "bootstrap"
// (lavaan's se = "bootstrap") end to end through the facade: the
// bootstrap SEs and percentile intervals of the brand-survey mediation
// and of the two-wave scalar-invariant fit against lavaan's
// (internal/sem/testdata brand_boot_*_lavaan.json; a sampling
// tolerance, the draws differ), the replicate echoes, the bits across
// runs, decode workers and a shard archive, predict's buffered verdict,
// the weight refusal, the too-few-successes slot failure, the config
// refusals (predict ≡ runtime) and the bootstrap p joining the slot's
// own multiplicity family.

// semBootLavaan is a fit_lavaan_boot.R reference.
type semBootLavaan struct {
	Lavaan     string `json:"lavaan"`
	Successful int    `json:"bootstrap_successful"`
	Params     []struct {
		LHS, Op, RHS, Group string
		Free                bool
		Est, SE             *float64
		CILower             *float64 `json:"ci_lower"`
		CIUpper             *float64 `json:"ci_upper"`
	} `json:"params"`
}

// The facade comparison is a wiring check, not the sampling parity:
// internal/sem (TestSEM_BootstrapMatchesLavaanSampling,
// TestSEM_BootstrapMultiGroupMatchesLavaan) holds the same two models'
// SEs and percentile bounds to these references at their 1000
// replicates (15% / 0.75 SE). Here semBootIters replicates run through
// the facade, so the tolerances are the B = 40 Monte-Carlo widths: a
// bootstrap SE's relative error is about 1/sqrt(2B) ≈ 11% (50% is
// ~4.4 SD; observed max 35%), a 2.5% / 97.5% percentile bound's about
// 0.42 SE under normality and more on a skewed := ratio (observed max
// 1.62 SE; 2.5 SE). checkBootReplaced separately proves the analytic
// SEs and intervals are gone.
const (
	semBootIters = 40
	semBootSETol = 0.5
	semBootCITol = 2.5
)

// semBootParams are the bootstrap knobs every lavaan comparison runs.
func semBootParams(extra map[string]any) map[string]any {
	out := map[string]any{"resample": "bootstrap", "bootstrap_iters": semBootIters, "rng_seed": 20261024}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// checkBootProcess holds a bootstrapped result to a lavaan bootstrap
// reference: every free and := row's estimate (the full data's),
// bootstrap SE and percentile bounds, z = estimate / SE and the normal
// p; fixed rows null; the replicate echoes.
func checkBootProcess(t *testing.T, res types.MatrixResult, ref semBootLavaan, grouped bool) {
	t.Helper()
	if res.Error != nil || res.Scalars["converged"] != 1 {
		t.Fatalf("error %+v converged %v", res.Error, res.Scalars["converged"])
	}
	if res.Scalars["bootstrap_requested"] != semBootIters || res.Scalars["bootstrap_succeeded"] != semBootIters {
		t.Errorf("bootstrap_requested %v succeeded %v, want %d / %d", res.Scalars["bootstrap_requested"], res.Scalars["bootstrap_succeeded"], semBootIters, semBootIters)
	}
	est := res.Auxiliary["estimates"]
	if !reflect.DeepEqual(est.ColumnKeys, []string{"estimate", "std_estimate", "std_err", "z_value", "p_value", "ci_lower", "ci_upper"}) {
		t.Fatalf("columns %v", est.ColumnKeys)
	}
	rows := map[string][]float64{}
	for i, k := range est.RowKeys {
		rows[k] = est.Values[i]
	}
	checked := 0
	for _, r := range ref.Params {
		key := r.LHS + " " + r.Op + " " + r.RHS
		switch r.Op {
		case "=~":
			key = r.RHS + " ~ " + r.LHS
		case "~1":
			key = r.LHS + " ~1"
		}
		if grouped && r.Op != ":=" {
			key = "[" + r.Group + "] " + key
		}
		row, ok := rows[key]
		if !ok && r.Op == "~~" {
			alt := r.RHS + " ~~ " + r.LHS
			if grouped {
				alt = "[" + r.Group + "] " + alt
			}
			row, ok = rows[alt]
		}
		if !ok {
			continue // a fixed.x row lavaan lists and Pulse hides
		}
		if !r.Free && r.Op != ":=" {
			if !math.IsNaN(row[2]) || !math.IsNaN(row[5]) {
				t.Errorf("%s: a fixed row carries SE %g / interval %g", key, row[2], row[5])
			}
			continue
		}
		checked++
		if !(mgRel(row[0], *r.Est) <= 1e-4) {
			t.Errorf("%s estimate %.10g, lavaan %.10g: the point estimate is the full data's", key, row[0], *r.Est)
		}
		if d := math.Abs(row[2]-*r.SE) / *r.SE; !(d <= semBootSETol) {
			t.Errorf("%s bootstrap SE %g, lavaan %g (rel %.3f)", key, row[2], *r.SE, d)
		}
		for j, w := range map[int]*float64{5: r.CILower, 6: r.CIUpper} {
			if d := math.Abs(row[j]-*w) / *r.SE; !(d <= semBootCITol) {
				t.Errorf("%s %s %g, lavaan %g (%.2f SE)", key, est.ColumnKeys[j], row[j], *w, d)
			}
		}
		if z := row[0] / row[2]; row[3] != z || !(math.Abs(row[4]-math.Erfc(math.Abs(z)/math.Sqrt2)) <= 1e-15) {
			t.Errorf("%s z %g p %g: want the bootstrap SE's z %g and its normal p", key, row[3], row[4], z)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d bootstrapped rows matched lavaan's", checked)
	}
}

// checkBootReplaced: against the analytic fit plain, the bootstrap
// keeps every estimate and std_estimate bit for bit and replaces every
// analytic SE and interval (a bootstrap SE equal to the analytic one to
// the bit is no bootstrap) — the sampling tolerances alone cannot tell
// the two apart on near-normal data.
func checkBootReplaced(t *testing.T, plain, res types.MatrixResult) {
	t.Helper()
	est, base := res.Auxiliary["estimates"], plain.Auxiliary["estimates"]
	if !reflect.DeepEqual(est.RowKeys, base.RowKeys) {
		t.Fatal("the bootstrap changed the estimates rows")
	}
	replaced := 0
	for i, row := range est.Values {
		b := base.Values[i]
		if row[0] != b[0] || row[1] != b[1] {
			t.Fatalf("%s: estimate / std_estimate moved under the bootstrap", est.RowKeys[i])
		}
		if math.IsNaN(b[2]) {
			continue
		}
		if row[2] == b[2] || row[5] == b[5] || row[6] == b[6] {
			t.Errorf("%s: SE %g / interval [%g, %g] is the analytic one", est.RowKeys[i], row[2], row[5], row[6])
		}
		replaced++
	}
	if replaced < 20 || res.Scalars["chi2"] != plain.Scalars["chi2"] {
		t.Errorf("%d rows replaced; chi2 %v vs %v", replaced, res.Scalars["chi2"], plain.Scalars["chi2"])
	}
}

func loadSEMBoot(t *testing.T, name string) semBootLavaan {
	t.Helper()
	raw, err := os.ReadFile(semGroupTestdata + name + "_lavaan.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref semBootLavaan
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	if ref.Lavaan == "" || ref.Successful == 0 {
		t.Fatalf("%s: no lavaan release / replicates recorded", name)
	}
	return ref
}

// TestSEM_CILevelWald: params.ci_level is lavaan's level on the Wald
// interval too — without a bootstrap, a 0.90 level moves every free and
// := row's ci_lower / ci_upper to lavaan's (brand_def_l90_lavaan.json)
// and nothing else, an explicit 0.95 is the default's bits, and a
// semopy-convention fit (no interval) refuses it, predict ≡ runtime.
func TestSEM_CILevelWald(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	base := semGroupRun(t, p, semGroupRequest(t, "brand_def", map[string]any{"group": nil, "meanstructure": true}))
	at95 := semGroupRun(t, p, semGroupRequest(t, "brand_def", map[string]any{"group": nil, "meanstructure": true, "ci_level": 0.95}))
	a, _ := json.Marshal(base)
	b, _ := json.Marshal(at95)
	if string(a) != string(b) {
		t.Fatal("ci_level 0.95 moved the default result")
	}
	res := semGroupRun(t, p, semGroupRequest(t, "brand_def", map[string]any{"group": nil, "meanstructure": true, "ci_level": 0.9}))
	raw, err := os.ReadFile(semGroupTestdata + "brand_def_l90_lavaan.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref semBootLavaan
	if err := json.Unmarshal(raw, &ref); err != nil || ref.Lavaan == "" {
		t.Fatalf("brand_def_l90: %v (lavaan %q)", err, ref.Lavaan)
	}
	est := res.Auxiliary["estimates"]
	rows := map[string][]float64{}
	for i, k := range est.RowKeys {
		rows[k] = est.Values[i]
	}
	checked := 0
	for _, r := range ref.Params {
		if !r.Free && r.Op != ":=" {
			continue
		}
		key := r.LHS + " " + r.Op + " " + r.RHS
		switch r.Op {
		case "=~":
			key = r.RHS + " ~ " + r.LHS
		case "~1":
			key = r.LHS + " ~1"
		}
		row, ok := rows[key]
		if !ok && r.Op == "~~" {
			row, ok = rows[r.RHS+" ~~ "+r.LHS]
		}
		if !ok {
			t.Errorf("lavaan row %q has no estimate row", key)
			continue
		}
		checked++
		if !(mgRel(row[5], *r.CILower) <= 1e-4) || !(mgRel(row[6], *r.CIUpper) <= 1e-4) {
			t.Errorf("%s 90%% interval [%.8g, %.8g], lavaan [%.8g, %.8g]", key, row[5], row[6], *r.CILower, *r.CIUpper)
		}
	}
	if checked < 25 {
		t.Fatalf("only %d rows matched lavaan's", checked)
	}
	bv := base.Auxiliary["estimates"].Values
	for i, row := range est.Values {
		for j := 0; j < 5; j++ {
			if row[j] != bv[i][j] && !(math.IsNaN(row[j]) && math.IsNaN(bv[i][j])) {
				t.Errorf("%s %s moved under ci_level", est.RowKeys[i], est.ColumnKeys[j])
			}
		}
	}
	req := semGroupRequest(t, "brand_ms", map[string]any{"group": nil, "ci_level": 0.9})
	_, rerr := p.Process(ctx, req)
	var ce *errors.CodedError
	if !asCoded(rerr, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" || ce.Details["param"] != "ci_level" {
		t.Fatalf("semopy-convention ci_level: %v (%v), want SERVICE_VALIDATION bad_params on ci_level", rerr, semDetailsOf(ce))
	}
	sameEntry(t, predictEnvelope(t, p, fsys, "brand.pulse", req), rerr)
}

// TestSEM_BootstrapPredictBuffered: predict reports the bootstrap's
// buffered downgrade (request and spec level, the spec's own verdict),
// and the runtime result is what that request computes.
func TestSEM_BootstrapPredictBuffered(t *testing.T) {
	t.Parallel()

	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	for _, params := range []map[string]any{
		{"group": nil, "resample": "bootstrap", "bootstrap_iters": 12},
		{"resample": "bootstrap", "bootstrap_iters": 12},
	} {
		req := semGroupRequest(t, "brand_mgmis", params)
		env := predictEnvelope(t, p, fsys, "brand.pulse", req)
		if len(env.Errors) != 0 {
			t.Fatalf("predict errors %v", env.Errors)
		}
		pr := env.Data.(*descriptor.PredictResult)
		if pr.Streamable || !slices.Contains(pr.StreamableReasons, "matrix MAT_SEM requires the buffered path") {
			t.Errorf("predict streamable %v reasons %v: a bootstrap buffers", pr.Streamable, pr.StreamableReasons)
		}
		mp := pr.Matrices[0]
		if mp.Streamable || mp.Mergeable || req.Matrices[0].Streamable() || req.Matrices[0].Mergeable() {
			t.Errorf("predict spec streamable %v mergeable %v; spec %v / %v", mp.Streamable, mp.Mergeable, req.Matrices[0].Streamable(), req.Matrices[0].Mergeable())
		}
		res := semGroupRun(t, p, req)
		if res.Error != nil || res.Scalars["bootstrap_requested"] != 12 {
			t.Errorf("runtime error %+v requested %v", res.Error, res.Scalars["bootstrap_requested"])
		}
		checkSEMKeysDeclared(t, semDeclaredKeys(t, p), res)
	}
}

// TestSEM_BootstrapWeightRefused: a bootstrap under any weight — a
// frequency slot weight, a request weight — is PULSE_WEIGHT_UNSUPPORTED
// with the bootstrap's reason, predict ≡ runtime; the same weight
// without resample still fits.
func TestSEM_BootstrapWeightRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeCategoricalU8, weight: func(r int) int { return 1 + r%2 }}.build(t, pulse.Options{})
	w := types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	for name, set := range map[string]func(*types.Request){
		"slot":    func(r *types.Request) { r.Matrices[0].Weight = types.SlotWeightOf(w) },
		"request": func(r *types.Request) { r.Weight = &w },
	} {
		t.Run(name, func(t *testing.T) {
			req := func(params map[string]any) *types.Request {
				r := semGroupRequest(t, "brand_mgmis", params)
				set(r)
				return r
			}
			boot := map[string]any{"group": nil, "resample": "bootstrap", "bootstrap_iters": 12}
			_, rerr := p.Process(ctx, req(boot))
			var ce *errors.CodedError
			if !asCoded(rerr, &ce) || ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED || !strings.Contains(ce.Details["reason"].(string), "bootstrap") {
				t.Fatalf("runtime %v (%v), want PULSE_WEIGHT_UNSUPPORTED", rerr, semDetailsOf(ce))
			}
			sameEntry(t, predictEnvelope(t, p, fsys, "brand.pulse", req(boot)), rerr)
			if res := semGroupRun(t, p, req(map[string]any{"group": nil})); res.Error != nil {
				t.Errorf("the weighted fit without resample: %+v", res.Error)
			}
		})
	}
}

// TestSEM_BootstrapTooFewFailsSlot: replicates that never converge
// (max_iter 1) leave no success, so the slot fails alone —
// PULSE_MATRIX_NOT_CONVERGED, reason bootstrap_too_few, with the counts
// — beside a slot that fits, and PULSE_SLOT_FAILED warns; under Strict
// the run fails with the slot's own code.
func TestSEM_BootstrapTooFewFailsSlot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	req := semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil, "resample": "bootstrap", "bootstrap_iters": 200, "max_iter": 1})
	ok := semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil})
	ok.Matrices[0].Name = "plain"
	req.Matrices = append(req.Matrices, ok.Matrices[0])
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	failed, fine := resp.Matrices[0], resp.Matrices[1]
	if failed.Error == nil || failed.Error.Code != string(errors.PULSE_MATRIX_NOT_CONVERGED) || failed.Error.Details["reason"] != "bootstrap_too_few" ||
		failed.Error.Details["bootstrap_succeeded"] != 0 || failed.Error.Details["bootstrap_requested"] != 200 || failed.Auxiliary != nil {
		t.Fatalf("failed slot %+v", failed.Error)
	}
	if fine.Error != nil || fine.Scalars["converged"] != 1 {
		t.Errorf("the other slot %+v", fine.Error)
	}
	warned := false
	for _, w := range resp.Warnings {
		warned = warned || w.Code == string(errors.PULSE_SLOT_FAILED)
	}
	if !warned {
		t.Error("no PULSE_SLOT_FAILED warning")
	}
	strict, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{Strict: true})
	_, serr := strict.Process(ctx, req)
	var ce *errors.CodedError
	if !asCoded(serr, &ce) || ce.Code != errors.PULSE_MATRIX_NOT_CONVERGED {
		t.Errorf("strict: %v", serr)
	}
}

// TestSEM_BootstrapRefusals: a bootstrap knob without resample (a
// ci_level on a semopy-convention fit, which reports no interval),
// another resample value, a non-positive bootstrap_iters, a ci_level
// outside (0, 1) and a params.fit binding are config refusals that
// predict reports identically.
func TestSEM_BootstrapRefusals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	for name, c := range map[string]struct {
		params        map[string]any
		reason, param string
	}{
		"iters_without_resample": {map[string]any{"bootstrap_iters": 10}, "bad_params", "bootstrap_iters"},
		"seed_without_resample":  {map[string]any{"rng_seed": 3}, "bad_params", "rng_seed"},
		"level_without_resample": {map[string]any{"ci_level": 0.9}, "bad_params", "ci_level"},
		"level_with_empty":       {map[string]any{"resample": "", "ci_level": 0.9}, "bad_params", "ci_level"},
		"jackknife":              {map[string]any{"resample": "jackknife"}, "bad_params", "resample"},
		"zero_iters":             {map[string]any{"resample": "bootstrap", "bootstrap_iters": 0}, "bad_params", "bootstrap_iters"},
		"level_one":              {map[string]any{"resample": "bootstrap", "ci_level": 1}, "bad_params", "ci_level"},
		"fit":                    {map[string]any{"resample": "bootstrap", "fit": "f"}, "fit_unsupported", "fit"},
	} {
		t.Run(name, func(t *testing.T) {
			params := map[string]any{"group": nil}
			for k, v := range c.params {
				params[k] = v
			}
			req := func() *types.Request { return semGroupRequest(t, "brand_mgmis", params) }
			_, rerr := p.Process(ctx, req())
			var ce *errors.CodedError
			if !asCoded(rerr, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != c.reason || ce.Details["param"] != c.param {
				t.Fatalf("runtime %v (%v), want SERVICE_VALIDATION %s on %s", rerr, semDetailsOf(ce), c.reason, c.param)
			}
			sameEntry(t, predictEnvelope(t, p, fsys, "brand.pulse", req()), rerr)
		})
	}
}

// TestSEM_BootstrapMultiplicity: the slot's own multiplicity block
// corrects the BOOTSTRAP p-values (every free and := row), the raw p
// never moving.
func TestSEM_BootstrapMultiplicity(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	req := semGroupRequest(t, "brand_def", map[string]any{"group": nil, "resample": "bootstrap", "bootstrap_iters": 40, "rng_seed": 5})
	base := semGroupRun(t, p, req)
	req.Matrices[0].Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	res := semGroupRun(t, p, req)
	est := res.Auxiliary["estimates"]
	a, _ := json.Marshal(est)
	b, _ := json.Marshal(base.Auxiliary["estimates"])
	if string(a) != string(b) {
		t.Fatal("the bootstrap estimates moved under a multiplicity block")
	}
	var family []float64
	var rows []int
	for i, row := range est.Values {
		if !math.IsNaN(row[4]) {
			family, rows = append(family, row[4]), append(rows, i)
		}
	}
	want, err := multiplicity.Adjust(multiplicity.MethodHolm, family)
	if err != nil {
		t.Fatal(err)
	}
	adj := res.Auxiliary["p_adjusted"]
	for k, i := range rows {
		if adj.Values[i][0] != want[k] {
			t.Errorf("%s p_adjusted %g, want Holm over the bootstrap p %g", est.RowKeys[i], adj.Values[i][0], want[k])
		}
	}
}
