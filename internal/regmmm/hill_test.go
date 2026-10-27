package regmmm_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// hill_test.go pins REG_MMM_HILL (mmm-estimation E4-S1, built in since
// mmm-followups E2-S1): a stock instance runs it with no registration
// and self-describes it among the built-ins; the joint
// fit recovers a known truth after WIN_DISTRIBUTED_LAG with calibrated
// standard errors; a no-effect channel is exactly 0 with null standard
// errors; coef() reads the k:/s: curve coefficients; bootstrap is
// reproducible; holdout, ATTR_REG_PREDICT and a saved model score
// through the Scorer; data refusals fail the slot alone.

// TestMMMHill_DetmathGoldenBits: the public linalg/detmath Exp, Log and
// Pow the model computes with give exactly the bits pinned here (from
// detmath's own golden, TestDetmath_GoldenBits, which holds them equal
// on amd64 and arm64), so a fit is bit-identical across architectures
// and the curve matches hill().
func TestMMMHill_DetmathGoldenBits(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fn   string
		x, y float64
		want uint64
	}{
		{fn: "exp", x: 1, want: 0x4005bf0a8b145769},
		{fn: "exp", x: -1, want: 0x3fd78b56362cef38},
		{fn: "exp", x: 0.5, want: 0x3ffa61298e1e069c},
		{fn: "exp", x: 10, want: 0x40d5829dcf950560},
		{fn: "exp", x: -10, want: 0x3f07cd79b5647c9a},
		{fn: "exp", x: 4.502, want: 0x40568ca14feea20e},
		{fn: "exp", x: -4.447676987598294, want: 0x3f87f92c362c3112},
		{fn: "exp", x: 100.5, want: 0x48ffcc37a76f9e76},
		{fn: "exp", x: 709.78, want: 0x7fefe9ce5c4c52b4},
		{fn: "exp", x: -745.1, want: 0x0000000000000001},
		{fn: "log", x: 2, want: 0x3fe62e42fefa39ef},
		{fn: "log", x: 0.1, want: 0xc0026bb1bbb55515},
		{fn: "log", x: 1e-300, want: 0xc085963447f87fb5},
		{fn: "log", x: 12345.678, want: 0x4022d79559791e31},
		{fn: "log", x: 0.7071, want: 0xbfd62e6b3842a25e},
		{fn: "log", x: 3e+07, want: 0x4031377a2be97aa3},
		{fn: "pow", x: 2.1447721179624666, y: 2.1, want: 0x4013dbf089d512e8},
		{fn: "pow", x: 0.5, y: 1.6, want: 0x3fd51cb453b9536c},
		{fn: "pow", x: 3.7, y: 0.37, want: 0x3ff9f6862d7943a7},
		{fn: "pow", x: 0.001, y: 2.5, want: 0x3e60fa3389d6eb41},
		{fn: "pow", x: 12.5, y: -1.3, want: 0x3fa33309d03ff901},
		{fn: "pow", x: 2, y: 10, want: 0x4090000000000000},
		{fn: "pow", x: 0.9999, y: 123.4, want: 0x3fef9b86e2499fa6},
	}
	for _, c := range cases {
		var got float64
		switch c.fn {
		case "exp":
			got = detmath.Exp(c.x)
		case "log":
			got = detmath.Log(c.x)
		default:
			got = detmath.Pow(c.x, c.y)
		}
		if b := math.Float64bits(got); b != c.want {
			t.Errorf("%s(%v, %v) = %#016x, want %#016x", c.fn, c.x, c.y, b, c.want)
		}
	}
	if h := regmmm.HillCurve(0, 3, 2); h != 0 {
		t.Errorf("hill(0, 3, 2) = %v, want 0", h)
	}
}

// TestMMMHill_RegistersAndSelfDescribes: a stock instance (no
// Extensions) lists REG_MMM_HILL among the BUILT-IN regressions — never
// under extensions — as a buffered model with its params and k / s
// outputs; predict accepts a fit as valid and non-streamable, with vcov.
func TestMMMHill_RegistersAndSelfDescribes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := instance(t, afero.NewMemMapFs(), nil)
	cohort(t, p, defaultTruth, 7)

	var meta *descriptor.RegressionMeta
	man := p.Manifest(ctx)
	for i, m := range man.Regressions {
		if m.Name == string(regmmm.Name) {
			meta = &man.Regressions[i]
		}
	}
	if meta == nil {
		t.Fatalf("manifest regressions lacks %s", regmmm.Name)
	}
	for _, m := range man.Extensions.Regressions {
		if m.Name == string(regmmm.Name) {
			t.Errorf("manifest lists the built-in %s under extensions", regmmm.Name)
		}
	}
	if meta.Streamable {
		t.Error("manifest: a buffered model listed as streamable")
	}
	var params, outputs []string
	for _, prm := range meta.Params {
		params = append(params, prm.Name)
	}
	for _, o := range meta.OutputSchema {
		outputs = append(outputs, o.Key)
	}
	for _, want := range []string{"channels", "nonnegative", "prior"} {
		if !slices.Contains(params, want) {
			t.Errorf("manifest params %v lack %s", params, want)
		}
	}
	if !slices.Equal(outputs, []string{"k", "s"}) {
		t.Errorf("manifest output_schema keys = %v, want [k s]", outputs)
	}
	s := spec(twoChannels, paramsTwo)
	s.Vcov = true
	pr, err := p.Predict(ctx, &types.Request{Cohort: &types.Cohort{Filename: "mmm_ad.pulse"}, Regressions: []*types.RegressionSpec{s}})
	if err != nil {
		t.Fatal(err)
	}
	if !pr.Valid || pr.Streamable {
		t.Errorf("predict valid %v streamable %v, want valid and buffered (errors %+v)", pr.Valid, pr.Streamable, pr.Errors)
	}
}

// Recovery tolerances, relative to the truth. Over generator seeds
// 1..64 (312 rows, noise σ = 0.15 against contributions up to 12) the
// worst relative errors were 0.20% (intercept), 5.9% (a channel β),
// 5.1% (k), 7.3% (s) and 1.1% (the price control) — sampling error, the
// solver converging to 1e-13 relative in cost. Each tolerance carries
// about twice that; every one is far inside what a perturbed truth
// moves (TestMMMHill_RecoveryFalsified).
const (
	tolIntercept = 0.005
	tolBeta      = 0.12
	tolK         = 0.10
	tolS         = 0.15
	tolControl   = 0.025
)

// truthCoefficients is the coefficient table the truth implies, with
// each figure's tolerance.
func truthCoefficients(tr truth) map[string][2]float64 {
	return map[string][2]float64{
		"(intercept)": {tr.intercept, tolIntercept},
		"tv_ad":       {tr.tvBeta, tolBeta}, "search_ad": {tr.srBeta, tolBeta},
		"price":   {tr.price, tolControl},
		"k:tv_ad": {tr.tvK, tolK}, "k:search_ad": {tr.srK, tolK},
		"s:tv_ad": {tr.tvS, tolS}, "s:search_ad": {tr.srS, tolS},
	}
}

// recoveryViolations lists every coefficient outside its tolerance of
// truth (empty: recovered).
func recoveryViolations(r *types.RegressionResult, tr truth) []string {
	var out []string
	for name, w := range truthCoefficients(tr) {
		if got := r.Coefficients[name]; !relClose(got, w[0], w[1]) {
			out = append(out, fmt.Sprintf("%s = %.6g, want %.6g ± %.1f%%", name, got, w[0], 100*w[1]))
		}
	}
	slices.Sort(out)
	return out
}

// TestMMMHill_RecoversTruth: after WIN_DISTRIBUTED_LAG (materialized;
// equal to the generator's kernels) REG_MMM_HILL recovers the intercept,
// each channel's β, k and s and the control's γ within tolerance; the
// coefficient table is the documented order, k:/s: equal the k / s
// outputs, every standard error is finite and positive and the vcov is
// symmetric with the squared standard errors on its diagonal; the fit is
// bit-identical across DecodeWorkers.
func TestMMMHill_RecoversTruth(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	p := instance(t, fsys, nil)
	rows := cohort(t, p, defaultTruth, 7)
	s := spec(twoChannels, paramsTwo)
	s.Vcov = true
	res := fit(t, p, s)
	if v := recoveryViolations(res, defaultTruth); len(v) != 0 {
		t.Fatalf("truth not recovered:\n%v", v)
	}
	if res.NObs != len(rows) || res.ConvergedIters < 1 {
		t.Errorf("n_obs %d (want %d), converged_iters %d", res.NObs, len(rows), res.ConvergedIters)
	}
	order := []string{"(intercept)", "tv_ad", "search_ad", "price", "k:tv_ad", "k:search_ad", "s:tv_ad", "s:search_ad"}
	if res.Vcov == nil || !slices.Equal(res.Vcov.RowKeys, order) {
		t.Fatalf("vcov keys = %v, want %v", res.Vcov, order)
	}
	for i, name := range order {
		se := res.StdErrors[name]
		if !(se > 0) || math.IsInf(se, 0) {
			t.Errorf("std_errors.%s = %v, want finite and positive", name, se)
		}
		if d := res.Vcov.Values[i][i]; !relClose(d, se*se, 1e-12) {
			t.Errorf("vcov[%s] = %v, want se² %v", name, d, se*se)
		}
		for j := range order {
			if res.Vcov.Values[i][j] != res.Vcov.Values[j][i] {
				t.Errorf("vcov not symmetric at %s,%s", name, order[j])
			}
		}
	}
	for _, ch := range []string{"tv_ad", "search_ad"} {
		for _, key := range []string{"k", "s"} {
			if got := res.Outputs[key].(map[string]any)[ch].(float64); got != res.Coefficients[key+":"+ch] {
				t.Errorf("outputs.%s.%s = %v, coefficient %v", key, ch, got, res.Coefficients[key+":"+ch])
			}
		}
	}

	want := mustJSON(t, res)
	for _, dw := range []int{1, 8} {
		q := instance(t, fsys, func(o *pulse.Options) { o.DecodeWorkers = dw })
		if got := mustJSON(t, fit(t, q, s)); got != want {
			t.Fatalf("DecodeWorkers %d fit differs:\n%s\n%s", dw, got, want)
		}
	}
}

// TestMMMHill_RecoversAcrossSeeds: recovery is not one lucky seed —
// every one of 64 generator seeds recovers the truth within the same
// tolerances — and the standard errors are calibrated: per coefficient
// the z-scores (estimate − truth) / SE over the seeds have a standard
// deviation near 1 (measured 0.81–1.08; the band [0.6, 1.4] is about 4
// sampling standard errors of a 64-draw SD wide).
func TestMMMHill_RecoversAcrossSeeds(t *testing.T) {
	t.Parallel()

	z := map[string][]float64{}
	for seed := uint64(1); seed <= 64; seed++ {
		p := instance(t, afero.NewMemMapFs(), nil)
		cohort(t, p, defaultTruth, seed)
		r := fit(t, p, spec(twoChannels, paramsTwo))
		if v := recoveryViolations(r, defaultTruth); len(v) != 0 {
			t.Errorf("seed %d: truth not recovered: %v", seed, v)
		}
		for name, w := range truthCoefficients(defaultTruth) {
			z[name] = append(z[name], (r.Coefficients[name]-w[0])/r.StdErrors[name])
		}
	}
	for name, zs := range z {
		var m, v float64
		for _, x := range zs {
			m += x
		}
		m /= float64(len(zs))
		for _, x := range zs {
			v += (x - m) * (x - m)
		}
		if sd := math.Sqrt(v / float64(len(zs)-1)); !(sd >= 0.6 && sd <= 1.4) {
			t.Errorf("%s: z-score SD %.3f over 64 seeds, want within [0.6, 1.4] (standard errors miscalibrated)", name, sd)
		}
	}
}

// TestMMMHill_RecoveryFalsified: the recovery check bites — data
// generated from a perturbed truth, fitted the same way, fails recovery
// against the default truth and recovers its own.
func TestMMMHill_RecoveryFalsified(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		mut  func(*truth)
	}{
		{"tv half-saturation ×1.5", func(h *truth) { h.tvK *= 1.5 }},
		{"search shape ×1.5", func(h *truth) { h.srS *= 1.5 }},
		{"tv β ×1.3", func(h *truth) { h.tvBeta *= 1.3 }},
		{"search half-saturation ×0.7", func(h *truth) { h.srK *= 0.7 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tr := defaultTruth
			tc.mut(&tr)
			p := instance(t, afero.NewMemMapFs(), nil)
			cohort(t, p, tr, 7)
			res := fit(t, p, spec(twoChannels, paramsTwo))
			if v := recoveryViolations(res, defaultTruth); len(v) == 0 {
				t.Fatalf("a fit of perturbed data passes recovery against the default truth: %v", res.Coefficients)
			}
			if v := recoveryViolations(res, tr); len(v) != 0 {
				t.Fatalf("the perturbed truth itself is not recovered: %v", v)
			}
		})
	}
}

// TestMMMHill_ZeroChannelExactlyZero: display has no effect (true β 0).
// Over seeds 1..16 the bound binds on many (measured: 7); on each such
// seed display's β is exactly 0 with a null standard error, and so are
// its k and s (the data cannot inform a curve with no effect), its vcov
// row and column are null, and the channels with an effect keep finite
// standard errors. On every seed the other channels still recover.
// Under nonnegative: false the same data fits a negative β on the first
// binding seed — the bound, not the data, made it 0.
func TestMMMHill_ZeroChannelExactlyZero(t *testing.T) {
	t.Parallel()

	bound := 0
	var firstBound uint64
	for seed := uint64(1); seed <= 16; seed++ {
		p := instance(t, afero.NewMemMapFs(), nil)
		cohort(t, p, defaultTruth, seed)
		s := spec(threeChannels, paramsThree)
		s.Vcov = true
		r := fit(t, p, s)
		if v := recoveryViolations(r, defaultTruth); len(v) != 0 {
			t.Errorf("seed %d: the other channels not recovered beside display: %v", seed, v)
		}
		if b := r.Coefficients["display_ad"]; b != 0 {
			if !(b > 0) {
				t.Errorf("seed %d: display β %v under nonnegative", seed, b)
			}
			continue
		}
		bound++
		if firstBound == 0 {
			firstBound = seed
		}
		var doc struct {
			StdErrors map[string]any `json:"std_errors"`
		}
		if err := json.Unmarshal([]byte(mustJSON(t, r)), &doc); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"display_ad", "k:display_ad", "s:display_ad"} {
			if v, ok := doc.StdErrors[name]; !ok || v != nil {
				t.Errorf("seed %d: std_errors.%s = %v (present %v), want null", seed, name, v, ok)
			}
		}
		for _, name := range []string{"tv_ad", "search_ad", "price", "k:tv_ad", "s:search_ad"} {
			if se := r.StdErrors[name]; !(se > 0) || math.IsInf(se, 0) {
				t.Errorf("seed %d: std_errors.%s = %v, want finite", seed, name, se)
			}
		}
		i := slices.Index(r.Vcov.RowKeys, "display_ad")
		for j := range r.Vcov.RowKeys {
			if !math.IsNaN(r.Vcov.Values[i][j]) || !math.IsNaN(r.Vcov.Values[j][i]) {
				t.Errorf("seed %d: vcov[display_ad][%s] = %v / %v, want NaN", seed, r.Vcov.RowKeys[j], r.Vcov.Values[i][j], r.Vcov.Values[j][i])
			}
		}
	}
	if bound == 0 {
		t.Fatal("the nonnegative bound bound on no seed: the exact-zero path is untested")
	}
	p := instance(t, afero.NewMemMapFs(), nil)
	cohort(t, p, defaultTruth, firstBound)
	free := fit(t, p, spec(threeChannels, `{"channels":["tv_ad","search_ad","display_ad"],"nonnegative":false}`))
	if b := free.Coefficients["display_ad"]; !(b < 0) {
		t.Errorf("seed %d unconstrained: display β %v, want the negative fit the bound clips", firstBound, b)
	}
}

// composeScore is a Compose batch: fit name over mmm_ad.pulse (s,
// saved to save when non-empty), then one request per week reading the
// fit through ATTR_REG_PREDICT (p) and through coef() + hill() in an
// ATTR_FORMULA (f), plus the curve coefficients as formulas.
func composeScore(s *types.RegressionSpec, save string) *pulse.ComposedRequest {
	return &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "mmm", Request: &types.Request{Cohort: &types.Cohort{Filename: "mmm_ad.pulse"}, Regressions: []*types.RegressionSpec{s}}, Save: save}},
		Requests: []*types.Request{scoreRequest()},
	}
}

// formulaModel is the fitted model written with coef() and hill().
const formulaModel = `coef("mmm", "(intercept)")` +
	` + coef("mmm", "tv_ad") * hill(tv_ad, coef("mmm", "k:tv_ad"), coef("mmm", "s:tv_ad"))` +
	` + coef("mmm", "search_ad") * hill(search_ad, coef("mmm", "k:search_ad"), coef("mmm", "s:search_ad"))` +
	` + coef("mmm", "price") * price`

func scoreRequest(models ...types.ModelRef) *types.Request {
	return &types.Request{
		Label:  "score",
		Cohort: &types.Cohort{Filename: "mmm_ad.pulse"},
		Models: models,
		Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "week", Interval: 1}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"mmm"}`)},
			{Type: types.ATTR_FORMULA, Label: "f", Expression: formulaModel},
			{Type: types.ATTR_FORMULA, Label: "k_tv", Expression: `coef("mmm", "k:tv_ad")`},
			{Type: types.ATTR_FORMULA, Label: "s_search", Expression: `coef("mmm", "s:search_ad")`},
			{Type: types.ATTR_FORMULA, Label: "k_tv_se", Expression: `coef("mmm", "k:tv_ad", "se")`},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "p", Label: "p"},
			{Type: types.AGG_SUM, Field: "f", Label: "f"},
			{Type: types.AGG_MAX, Field: "k_tv", Label: "k_tv"},
			{Type: types.AGG_MAX, Field: "s_search", Label: "s_search"},
			{Type: types.AGG_MAX, Field: "k_tv_se", Label: "k_tv_se"},
		},
	}
}

// TestMMMHill_CoefReadsCurveCoefficients: in an ATTR_FORMULA,
// coef("mmm", "k:tv_ad") / ("s:search_ad") / ("k:tv_ad", "se") read the
// fit's curve coefficients and standard error exactly; predict accepts
// the batch; an unknown curve name is PULSE_MODEL_TERM_UNKNOWN at run.
func TestMMMHill_CoefReadsCurveCoefficients(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := instance(t, afero.NewMemMapFs(), nil)
	cohort(t, p, defaultTruth, 7)
	s := spec(twoChannels, paramsTwo)
	out, err := p.Compose(ctx, composeScore(s, ""))
	if err != nil {
		t.Fatal(err)
	}
	f := out.Fits["mmm"].RegressionResult
	for _, row := range out.Responses[0].Data {
		if row["k_tv"] != f.Coefficients["k:tv_ad"] || row["s_search"] != f.Coefficients["s:search_ad"] || row["k_tv_se"] != f.StdErrors["k:tv_ad"] {
			t.Fatalf("week %v: coef() read k %v s %v se %v, fit %v %v %v", row["week"], row["k_tv"], row["s_search"], row["k_tv_se"],
				f.Coefficients["k:tv_ad"], f.Coefficients["s:search_ad"], f.StdErrors["k:tv_ad"])
		}
	}
	if env, err := p.PredictCompose(ctx, composeScore(s, "")); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict compose: %v %+v", err, env)
	}
	bad := composeScore(s, "")
	bad.Requests[0].Attributes[2].Expression = `coef("mmm", "k:radio")`
	_, err = p.Compose(ctx, bad)
	if !perr.HasCode(err, perr.PULSE_MODEL_TERM_UNKNOWN) {
		t.Fatalf("unknown curve coefficient: %v, want PULSE_MODEL_TERM_UNKNOWN", err)
	}
}

// TestMMMHill_ScorerHoldoutSaveReload: ATTR_REG_PREDICT scores through
// the Scorer exactly as the model written with coef() and hill() (both
// compute 1/(1+(k/x)^s) through the same deterministic power, so equal
// to the last bit); the fit saves as a model file and a fresh instance
// reloads it and scores bit-identically, predict accepting the load; a
// holdout predicate scores the held-out weeks (RMSE equal to the
// scorer's residuals on those rows).
func TestMMMHill_ScorerHoldoutSaveReload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p := instance(t, fsys, nil)
	rows := cohort(t, p, defaultTruth, 7)
	const save = "models/mmm.pulse-model.json"
	out, err := p.Compose(ctx, composeScore(spec(twoChannels, paramsTwo), save))
	if err != nil {
		t.Fatal(err)
	}
	data := out.Responses[0].Data
	if len(data) != weeks {
		t.Fatalf("%d score rows, want %d", len(data), weeks)
	}
	for _, row := range data {
		if row["p"] != row["f"] {
			t.Fatalf("week %v: scorer %v, coef()+hill() formula %v", row["week"], row["p"], row["f"])
		}
	}

	fresh := instance(t, fsys, nil)
	load := scoreRequest(types.ModelRef{Name: "mmm", Path: save})
	loaded, err := fresh.Process(ctx, load)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := mustJSON(t, loaded.Data), mustJSON(t, data); a != b {
		t.Fatalf("the reloaded model scores differently:\n%s\n%s", a, b)
	}
	raw, err := afero.ReadFile(fsys, "mmm_ad.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if env, err := fresh.PredictBytes(ctx, raw, load); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict on the load: %v %+v", err, env.Errors)
	}

	s := spec(twoChannels, paramsTwo)
	s.Holdout = "week >= 90"
	h := fit(t, p, s)
	if h.Holdout == nil || h.Holdout.N != 14*len(geos) || h.NObs != len(rows)-14*len(geos) {
		t.Fatalf("holdout = %+v, n_obs %d", h.Holdout, h.NObs)
	}
	sc, err := regmmm.Scorer(fittedOf(h, s))
	if err != nil {
		t.Fatal(err)
	}
	var sse float64
	for _, r := range rows {
		if r.week < 90 {
			continue
		}
		y, _ := sc.Score(scoreRow{"tv_ad": r.tvAd, "search_ad": r.searchAd, "price": r.price})
		sse += (r.y - y) * (r.y - y)
	}
	if want := math.Sqrt(sse / float64(h.Holdout.N)); !relClose(h.Holdout.RMSE, want, 1e-9) {
		t.Errorf("holdout rmse %v, want %v from the scorer on the held-out rows", h.Holdout.RMSE, want)
	}
	if !(h.Holdout.RMSE < 3*defaultTruth.noise) {
		t.Errorf("holdout rmse %v, want near the noise %v", h.Holdout.RMSE, defaultTruth.noise)
	}
}

// scoreRow is an extend.ScoreRow over a map.
type scoreRow map[string]float64

func (r scoreRow) NumericValue(name string) (float64, bool) { v, ok := r[name]; return v, ok }

// TestMMMHill_BootstrapReproducible: resample: bootstrap refits the
// model on every replicate — each coefficient (the curve ones included)
// gains a finite, positive replicate standard error and a p-value, the
// estimates stay the full fit's — and the same seed gives the same
// bytes.
func TestMMMHill_BootstrapReproducible(t *testing.T) {
	t.Parallel()

	p := instance(t, afero.NewMemMapFs(), nil)
	cohort(t, p, defaultTruth, 7)
	boot := func() *types.RegressionSpec {
		s := spec(twoChannels, paramsTwo)
		s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 12, 11
		return s
	}
	b := fit(t, p, boot())
	plain := fit(t, p, spec(twoChannels, paramsTwo))
	if b.BootstrapRequested != 12 || b.BootstrapSucceeded != 12 {
		t.Errorf("bootstrap requested %d succeeded %d, want 12 / 12", b.BootstrapRequested, b.BootstrapSucceeded)
	}
	if len(plain.Coefficients) != 8 {
		t.Fatalf("coefficients = %v", plain.Coefficients)
	}
	for name, est := range plain.Coefficients {
		if b.Coefficients[name] != est {
			t.Errorf("%s: bootstrap estimate %v, full fit %v", name, b.Coefficients[name], est)
		}
		se, pv := b.StdErrors[name], b.PValues[name]
		if !(se > 0) || math.IsInf(se, 0) || math.IsNaN(pv) {
			t.Errorf("%s: replicate se %v p %v, want a finite positive se and a p-value", name, se, pv)
		}
	}
	if x, y := mustJSON(t, b), mustJSON(t, fit(t, p, boot())); x != y {
		t.Fatalf("bootstrap not reproducible:\n%s\n%s", x, y)
	}
}

// TestMMMHill_SlotFailsAlone: too few rows for the model fails its slot
// alone — the result keeps its identity and carries
// PROCESSING_REGRESSION_INSUFFICIENT_DATA, PULSE_SLOT_FAILED warns, and
// the request's other slot fits; under Strict the run fails with the
// slot's code.
func TestMMMHill_SlotFailsAlone(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	p := instance(t, fsys, nil)
	cohort(t, p, defaultTruth, 7)
	req := func() *types.Request {
		return &types.Request{
			Cohort:    &types.Cohort{Filename: "mmm_ad.pulse"},
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "week < 2"}},
			Regressions: []*types.RegressionSpec{
				spec(twoChannels, paramsTwo),
				{Type: types.REG_OLS, Name: "ols", Target: "y", Predictors: []string{"tv_ad"}},
			},
		}
	}
	resp := process(t, p, req())
	if len(resp.Regressions) != 2 {
		t.Fatalf("regressions = %s", mustJSON(t, resp.Regressions))
	}
	m, ols := resp.Regressions[0], resp.Regressions[1]
	if m.Error == nil || m.Error.Code != string(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA) || m.Name != "mmm" || m.Type != regmmm.Name {
		t.Fatalf("mmm slot = %s, want its identity and INSUFFICIENT_DATA", mustJSON(t, m))
	}
	if ols.Error != nil || len(ols.Coefficients) != 2 {
		t.Fatalf("ols slot = %s, want a fit", mustJSON(t, ols))
	}
	warned := false
	for _, w := range resp.Warnings {
		warned = warned || w.Code == string(perr.PULSE_SLOT_FAILED)
	}
	if !warned {
		t.Errorf("warnings = %s, want PULSE_SLOT_FAILED", mustJSON(t, resp.Warnings))
	}
	strict := instance(t, fsys, func(o *pulse.Options) { o.Strict = true })
	if _, err := strict.Process(context.Background(), req()); !perr.HasCode(err, perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA) {
		t.Fatalf("strict: %v, want the slot's code", err)
	}
}

// TestMMMHill_ParamsRefused: a channel that is not a predictor (or is
// listed twice), an invalid prior (an unknown key included) and a
// negative channel value are
// PROCESSING_CONFIG at run — never a silent fit.
func TestMMMHill_ParamsRefused(t *testing.T) {
	t.Parallel()

	p := instance(t, afero.NewMemMapFs(), nil)
	cohort(t, p, defaultTruth, 7)
	for _, tc := range []struct {
		name       string
		predictors []string
		params     string
	}{
		{"channel not a predictor", twoChannels, `{"channels":["tv_ad","radio"]}`},
		{"channel twice", twoChannels, `{"channels":["tv_ad","tv_ad"]}`},
		{"prior on a control", twoChannels, `{"channels":["tv_ad"],"prior":{"channel":"price","mean":1,"precision":1}}`},
		{"prior key typo", twoChannels, `{"channels":["tv_ad"],"prior":{"channel":"tv_ad","mean":1,"precison":1}}`},
		{"negative precision", twoChannels, `{"channels":["tv_ad"],"prior":{"channel":"tv_ad","mean":1,"precision":-1}}`},
		{"negative channel", []string{"tv_ad", "price"}, `{"channels":["tv_ad","price"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "mmm_ad.pulse"},
				Regressions: []*types.RegressionSpec{spec(tc.predictors, tc.params)}})
			if !perr.HasCode(err, perr.PROCESSING_CONFIG) {
				t.Fatalf("err = %v, want PROCESSING_CONFIG", err)
			}
		})
	}
}

// fittedOf is r (fitted from s) as the Scorer sees a fit.
func fittedOf(r *types.RegressionResult, s *types.RegressionSpec) extend.FittedRegression {
	return extend.FittedRegression{Type: r.Type, Target: s.Target, Predictors: s.Predictors, Coefficients: r.Coefficients, Outputs: r.Outputs}
}

// TestMMMHill_FactoryRefusesCurveNameCollision: a predictor named like a
// curve coefficient ("k:…" / "s:…") would collide in the coefficient
// table, so the factory refuses it with PROCESSING_CONFIG; a missing
// channels list is refused the same way.
func TestMMMHill_FactoryRefusesCurveNameCollision(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		predictors []string
		params     string
	}{
		{[]string{"tv", "k:tv"}, `{"channels":["tv"]}`},
		{[]string{"tv", "s:x"}, `{"channels":["tv"]}`},
		{[]string{"tv"}, `{}`},
	} {
		_, err := regmmm.Factory(spec(tc.predictors, tc.params), nil)
		if !perr.HasCode(err, perr.PROCESSING_CONFIG) {
			t.Errorf("predictors %v params %s: %v, want PROCESSING_CONFIG", tc.predictors, tc.params, err)
		}
	}
	if _, err := regmmm.Factory(spec([]string{"tv", "ks"}, `{"channels":["tv"]}`), nil); err != nil {
		t.Errorf("a predictor merely starting with k: %v", err)
	}
}

// TestMMMHill_IterationCapIsNoConverge: a solve stopped by its iteration
// cap is PROCESSING_REGRESSION_NO_CONVERGE — the code that fails the slot
// alone — never a silently unconverged fit.
func TestMMMHill_IterationCapIsNoConverge(t *testing.T) {
	t.Parallel()

	y, a, z := generatedColumns(generate(defaultTruth, 7))
	if _, err := regmmm.SolveColumns(twoChannels[:2], twoChannels[2:], y, a, z, 2); !perr.HasCode(err, perr.PROCESSING_REGRESSION_NO_CONVERGE) {
		t.Fatalf("a 2-iteration cap: %v, want PROCESSING_REGRESSION_NO_CONVERGE", err)
	}
	if _, err := regmmm.SolveColumns(twoChannels[:2], twoChannels[2:], y, a, z, regmmm.MaxIterations); err != nil {
		t.Fatalf("the default cap: %v", err)
	}
}

// generatedColumns are the generator rows' columns: y, the tv / search
// adstock channels and the price control.
func generatedColumns(rows []row) (y []float64, a, z [][]float64) {
	a, z = make([][]float64, 2), make([][]float64, 1)
	for _, r := range rows {
		y = append(y, r.y)
		a[0], a[1] = append(a[0], r.tvAd), append(a[1], r.searchAd)
		z[0] = append(z[0], r.price)
	}
	return y, a, z
}

// TestMMMHill_FitBitsPinned: a fit's figures are a function of its rows
// alone on every architecture — the solver, the curve (detmath's
// deterministic Exp / Log / Pow) and the covariance are serial and
// FMA-free, and linalg's Cholesky is Pulse's own kernel. The fit of the
// seed-7 generator rows (adstock from the generator, so no engine
// window in the path) is pinned to the SHA-256 of its estimates' and
// standard errors' bits, measured identical on darwin/arm64 and
// linux/amd64. (The engine's intervals and WIN_DISTRIBUTED_LAG's kernel
// weights are outside this pin.)
func TestMMMHill_FitBitsPinned(t *testing.T) {
	t.Parallel()

	const want = "bf80809268a6937f174e481096525d856dbaabcdf3d4a0d15ba165957faecf6f"
	y, a, z := generatedColumns(generate(defaultTruth, 7))
	out, err := regmmm.SolveColumns(twoChannels[:2], twoChannels[2:], y, a, z, regmmm.MaxIterations)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, c := range out.Coefficients {
		for _, v := range []float64{c.Estimate, *c.StdErr} {
			_ = binary.Write(h, binary.LittleEndian, math.Float64bits(v))
		}
	}
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != want {
		t.Fatalf("fit bits %s, want %s (a product lost its float64() barrier, or a stdlib transcendental crept in)", got, want)
	}
}
