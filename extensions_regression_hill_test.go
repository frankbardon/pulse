package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// extensions_regression_hill_test.go pins custom-regressions E4-S1:
// the reference extension REG_TESTMMM_HILL (extensions_testmmm_hill_test.go),
// registered through the public contract alone, recovers a seeded
// Weibull-adstock + Hill media-mix truth after WIN_ADSTOCK; its prior
// and nonnegative params hold their invariants; its Scorer scores
// through ATTR_REG_PREDICT exactly as hill() writes the model, saves and
// reloads; a bootstrap refits it; the manifest describes it and a
// feature profile hides it. Like the extension, this file imports only
// public packages.

// hillTruth is the generator's ground truth.
type hillTruth struct {
	intercept float64
	tvBeta    float64
	tvK, tvS  float64
	srBeta    float64
	srK, srS  float64
	price     float64
	noise     float64
}

var hillDefaultTruth = hillTruth{
	intercept: 50, tvBeta: 12, tvK: 80, tvS: 2, srBeta: 6, srK: 30, srS: 1.6, price: -3, noise: 0.15,
}

// hillAdstockWindows adstocks tv (Weibull CDF) and search (Weibull PDF)
// per geo over week.
func hillAdstockWindows() []*types.Window {
	w := func(field, label, params string) *types.Window {
		return &types.Window{Type: types.WIN_ADSTOCK, Field: field, Label: label, PartitionBy: []string{"geo"},
			OrderBy: []types.OrderKey{{Field: "week"}}, Params: json.RawMessage(params)}
	}
	return []*types.Window{
		w("tv", "tv_ad", `{"kernel":"weibull_cdf","max_lag":8,"shape":2,"scale":3}`),
		w("search", "search_ad", `{"kernel":"weibull_pdf","max_lag":6,"shape":1.5,"scale":2}`),
	}
}

// hillKernel is the normalised lag weights the generator applies —
// written from op-win-adstock's formulas, independently of WIN_ADSTOCK.
func hillKernel(pdf bool, maxLag int, shape, scale float64) []float64 {
	w := make([]float64, maxLag+1)
	sum := 0.0
	for l := range w {
		if pdf {
			t := float64(l + 1)
			w[l] = shape / scale * math.Pow(t/scale, shape-1) * math.Exp(-math.Pow(t/scale, shape))
		} else {
			w[l] = math.Exp(-math.Pow(float64(l)/scale, shape))
		}
		sum += w[l]
	}
	for l := range w {
		w[l] /= sum
	}
	return w
}

func hillConvolve(x, w []float64) []float64 {
	out := make([]float64, len(x))
	for i := range x {
		for l, wl := range w {
			if i-l >= 0 {
				out[i] += float64(wl * x[i-l])
			}
		}
	}
	return out
}

var hillGeos = []string{"east", "north", "west"}

const hillWeeks = 104

func hillSchema() encoding.Schema {
	geo := encoding.NewDictionary()
	for _, g := range hillGeos {
		_, _ = geo.Add(g)
	}
	fields := []encoding.Field{
		{Name: "geo", Type: encoding.FieldTypeCategoricalU8, Dictionary: geo},
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "tv", Type: encoding.FieldTypeF64},
		{Name: "search", Type: encoding.FieldTypeF64},
		{Name: "price", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Media-mix test column " + fields[i].Name + "."
		off += fields[i].Type.ByteSize()
	}
	return encoding.Schema{Fields: fields}
}

// hillRow is one generated row with the generator's own adstock.
type hillRow struct {
	geo               string
	week              int
	tv, search, price float64
	tvAd, searchAd, y float64
}

// generateHill draws the deterministic media-mix data: per geo, 104
// weeks of bursty TV flights and always-on search, a price control,
// adstock by the generator's kernels and y from truth plus Gaussian
// noise. Every product feeding an addition is wrapped in float64(),
// so arm64's FMA fusion cannot change the inputs.
func generateHill(truth hillTruth, seed uint64) []hillRow {
	rng := rand.New(rand.NewPCG(seed, 0x4d4d4d))
	tvW := hillKernel(false, 8, 2, 3)
	srW := hillKernel(true, 6, 1.5, 2)
	var rows []hillRow
	for _, g := range hillGeos {
		tv, sr, pr := make([]float64, hillWeeks), make([]float64, hillWeeks), make([]float64, hillWeeks)
		for w := range tv {
			if rng.Float64() < 0.5 {
				tv[w] = float64(rng.Float64() * 320)
			}
			if rng.Float64() < 0.6 {
				sr[w] = float64(rng.Float64() * 120)
			}
			pr[w] = float64(rng.Float64()*2) - 1
		}
		tvAd, srAd := hillConvolve(tv, tvW), hillConvolve(sr, srW)
		for w := range tv {
			y := truth.intercept + float64(truth.tvBeta*hillCurve(tvAd[w], truth.tvK, truth.tvS)) +
				float64(truth.srBeta*hillCurve(srAd[w], truth.srK, truth.srS)) + float64(truth.price*pr[w]) +
				float64(truth.noise*rng.NormFloat64())
			rows = append(rows, hillRow{geo: g, week: w, tv: tv[w], search: sr[w], price: pr[w], tvAd: tvAd[w], searchAd: srAd[w], y: y})
		}
	}
	return rows
}

func writeHillCohort(t testing.TB, p *pulse.Pulse, path string, truth hillTruth, seed uint64) []hillRow {
	t.Helper()
	rows := generateHill(truth, seed)
	b, err := p.NewCohortBuilder(context.Background(), path, hillSchema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if err := b.Append(pulse.CohortRow{r.geo, uint64(r.week), r.tv, r.search, r.price, r.y}); err != nil {
			t.Fatalf("Append row %d: %v", i, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// hillInstance opens an instance over fsys with REG_TESTMMM_HILL
// registered; mut adjusts the options.
func hillInstance(t testing.TB, fsys afero.Fs, mut func(*pulse.Options)) *pulse.Pulse {
	t.Helper()
	opts := pulse.Options{FS: fsys, Extensions: pulse.Extensions{Regressions: []pulse.RegressionRegistration{testMMMHillRegistration()}}}
	if mut != nil {
		mut(&opts)
	}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

// hillCohort writes the generated source cohort mmm.pulse and
// materializes its WIN_ADSTOCK columns into mmm_ad.pulse — the cohort
// every fit reads (a regression cannot name a window label of its own
// request, so the adstock is materialized first, as an embedder would).
func hillCohort(t testing.TB, p *pulse.Pulse, truth hillTruth, seed uint64) []hillRow {
	t.Helper()
	rows := writeHillCohort(t, p, "mmm.pulse", truth, seed)
	if _, err := p.Materialize(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "mmm.pulse"}, Windows: hillAdstockWindows()},
		"mmm_ad.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	return rows
}

// hillSpec is the media-mix slot: tv and search through the Hill curve,
// price a linear control.
func hillSpec(params string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: regTestMMMHill, Name: "mmm", Target: "y",
		Predictors: []string{"tv_ad", "search_ad", "price"}, Params: json.RawMessage(params)}
}

const hillParamsNonneg = `{"channels":["tv_ad","search_ad"],"nonnegative":true}`

func fitHill(t testing.TB, p *pulse.Pulse, cohort string, spec *types.RegressionSpec) *types.RegressionResult {
	t.Helper()
	resp, err := p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{spec}})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		b, _ := json.Marshal(resp.Regressions)
		t.Fatalf("regressions = %s", b)
	}
	return resp.Regressions[0]
}

// hillOutput is outputs.<key>.<channel> of a fit.
func hillOutput(t testing.TB, r *types.RegressionResult, key, channel string) float64 {
	t.Helper()
	m, ok := r.Outputs[key].(map[string]any)
	if !ok {
		t.Fatalf("outputs.%s = %#v, want an object", key, r.Outputs[key])
	}
	v, ok := m[channel].(float64)
	if !ok {
		t.Fatalf("outputs.%s.%s = %#v, want a number", key, channel, m[channel])
	}
	return v
}

// Recovery tolerances, relative to the truth. The generator has 312
// rows and noise σ = 0.15 against a Hill contribution of up to 12, so a
// fit's sampling error — not the optimiser, which converges to 1e-15
// relative in cost — sets the bounds: over seeds 1..64 the worst
// relative errors were 0.27% (intercept), 5.2% (a channel β), 5.5% (k),
// 6.6% (s) and 1.3% (the price control); each tolerance carries about
// twice that, which also absorbs any amd64 / arm64 last-bit difference
// in the generator's math.Pow / math.Exp tails, and every one is far
// inside what a perturbed truth moves
// (TestExtensions_RegressionHillRecoveryFalsified).
const (
	hillTolIntercept = 0.005
	hillTolBeta      = 0.10
	hillTolK         = 0.10
	hillTolS         = 0.12
	hillTolControl   = 0.025
)

// hillRecoveryViolations lists every recovered figure outside its
// tolerance of truth (empty: recovered).
func hillRecoveryViolations(t testing.TB, r *types.RegressionResult, truth hillTruth) []string {
	t.Helper()
	var out []string
	check := func(what string, got, want, tol float64) {
		if !(math.Abs(got-want) <= tol*math.Abs(want)) {
			out = append(out, fmt.Sprintf("%s = %.6g, want %.6g ± %.0f%%", what, got, want, 100*tol))
		}
	}
	check("(intercept)", r.Coefficients["(intercept)"], truth.intercept, hillTolIntercept)
	check("β tv_ad", r.Coefficients["tv_ad"], truth.tvBeta, hillTolBeta)
	check("β search_ad", r.Coefficients["search_ad"], truth.srBeta, hillTolBeta)
	check("γ price", r.Coefficients["price"], truth.price, hillTolControl)
	check("k tv_ad", hillOutput(t, r, "k", "tv_ad"), truth.tvK, hillTolK)
	check("k search_ad", hillOutput(t, r, "k", "search_ad"), truth.srK, hillTolK)
	check("s tv_ad", hillOutput(t, r, "s", "tv_ad"), truth.tvS, hillTolS)
	check("s search_ad", hillOutput(t, r, "s", "search_ad"), truth.srS, hillTolS)
	return out
}

// TestExtensions_RegressionHillRecoversTruth: the generated cohort's
// spend goes through WIN_ADSTOCK (materialized; it equals the
// generator's own kernels), then REG_TESTMMM_HILL recovers the
// intercept, every channel's β, k and s and the control's γ within the
// stated tolerances. The coefficients carry no standard error, test
// statistic or p-value (null on the wire, keys kept), the declared k / s
// outputs are objects keyed by channel, and the fit is bit-identical
// across DecodeWorkers and across a shard archive at any ShardWorkers.
func TestExtensions_RegressionHillRecoversTruth(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p := hillInstance(t, fsys, nil)
	rows := hillCohort(t, p, hillDefaultTruth, 7)

	// WIN_ADSTOCK equals the generator's kernels (relative 1e-12: the
	// same sums in a possibly different association).
	c, err := p.Open(ctx, "mmm_ad.pulse")
	if err != nil {
		t.Fatal(err)
	}
	rd, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rd.Close() }()
	idx := map[string]int{}
	for i, f := range c.Schema().Fields {
		idx[f.Name] = i
	}
	for i, r := range rows {
		got, err := rd.RecordAt(int64(i))
		if err != nil {
			t.Fatal(err)
		}
		for _, col := range []struct {
			name string
			want float64
		}{{"tv_ad", r.tvAd}, {"search_ad", r.searchAd}} {
			v, _ := got[idx[col.name]].(float64)
			if math.Abs(v-col.want) > 1e-12*math.Max(1, math.Abs(col.want)) {
				t.Fatalf("row %d %s = %v, generator %v", i, col.name, v, col.want)
			}
		}
	}

	res := fitHill(t, p, "mmm_ad.pulse", hillSpec(hillParamsNonneg))
	if v := hillRecoveryViolations(t, res, hillDefaultTruth); len(v) != 0 {
		t.Fatalf("truth not recovered:\n%v", v)
	}
	if res.NObs != len(rows) || res.ConvergedIters < 1 {
		t.Errorf("n_obs %d (want %d), converged_iters %d", res.NObs, len(rows), res.ConvergedIters)
	}

	wire, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(wire, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"std_errors", "statistics", "p_values"} {
		m, _ := doc[key].(map[string]any)
		if len(m) != 4 {
			t.Fatalf("%s = %v, want the four coefficients", key, doc[key])
		}
		for name, v := range m {
			if v != nil {
				t.Errorf("%s.%s = %v, want null", key, name, v)
			}
		}
	}
	outs, _ := doc["outputs"].(map[string]any)
	if len(outs) != 2 {
		t.Fatalf("outputs = %v, want k and s", doc["outputs"])
	}

	// Determinism: decode workers, and a shard archive (split by geo,
	// so each shard's adstock is its partition's) at any shard workers.
	want := mustJSON(t, res)
	for _, dw := range []int{1, 8} {
		q := hillInstance(t, fsys, func(o *pulse.Options) { o.DecodeWorkers = dw })
		if got := mustJSON(t, fitHill(t, q, "mmm_ad.pulse", hillSpec(hillParamsNonneg))); got != want {
			t.Fatalf("DecodeWorkers %d fit differs:\n%s\n%s", dw, got, want)
		}
	}
	for i, geos := range [][]string{{"east", "north"}, {"west"}} {
		b, err := p.NewCohortBuilder(ctx, fmt.Sprintf("shard%d.pulse", i), hillSchema(), pulse.CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.geo == geos[0] || (len(geos) > 1 && r.geo == geos[1]) {
				if err := b.Append(pulse.CohortRow{r.geo, uint64(r.week), r.tv, r.search, r.price, r.y}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Materialize(ctx, &types.Request{Cohort: &types.Cohort{Filename: fmt.Sprintf("shard%d.pulse", i)}, Windows: hillAdstockWindows()},
			fmt.Sprintf("shard%d_ad.pulse", i), pulse.MaterializeOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.CreateShardArchive(ctx, "mmm_archive.pulse", []string{"shard0_ad.pulse", "shard1_ad.pulse"}); err != nil {
		t.Fatal(err)
	}
	for _, sw := range []int{1, 4} {
		q := hillInstance(t, fsys, func(o *pulse.Options) { o.ShardWorkers = sw })
		if got := mustJSON(t, fitHill(t, q, "mmm_archive.pulse", hillSpec(hillParamsNonneg))); got != want {
			t.Fatalf("archive at ShardWorkers %d fit differs from the single file:\n%s\n%s", sw, got, want)
		}
	}
}

// TestExtensions_RegressionHillRecoversAcrossSeeds: recovery is not one
// lucky seed — every one of 64 generator seeds recovers the truth
// within the same tolerances.
func TestExtensions_RegressionHillRecoversAcrossSeeds(t *testing.T) {
	for seed := uint64(1); seed <= 64; seed++ {
		p := hillInstance(t, afero.NewMemMapFs(), nil)
		hillCohort(t, p, hillDefaultTruth, seed)
		if v := hillRecoveryViolations(t, fitHill(t, p, "mmm_ad.pulse", hillSpec(hillParamsNonneg)), hillDefaultTruth); len(v) != 0 {
			t.Errorf("seed %d: truth not recovered: %v", seed, v)
		}
	}
}

// TestExtensions_RegressionHillRecoveryFalsified: the recovery check
// bites — data generated from a perturbed truth, fitted the same way,
// fails recovery against the default truth (and recovers its own).
func TestExtensions_RegressionHillRecoveryFalsified(t *testing.T) {
	perturb := []struct {
		name string
		mut  func(*hillTruth)
	}{
		{"tv half-saturation ×1.5", func(h *hillTruth) { h.tvK *= 1.5 }},
		{"search shape ×1.5", func(h *hillTruth) { h.srS *= 1.5 }},
		{"tv β ×1.3", func(h *hillTruth) { h.tvBeta *= 1.3 }},
		{"search half-saturation ×0.7", func(h *hillTruth) { h.srK *= 0.7 }},
	}
	for _, tc := range perturb {
		t.Run(tc.name, func(t *testing.T) {
			truth := hillDefaultTruth
			tc.mut(&truth)
			p := hillInstance(t, afero.NewMemMapFs(), nil)
			hillCohort(t, p, truth, 7)
			res := fitHill(t, p, "mmm_ad.pulse", hillSpec(hillParamsNonneg))
			if v := hillRecoveryViolations(t, res, hillDefaultTruth); len(v) == 0 {
				t.Fatalf("a fit of perturbed data passes recovery against the default truth: %v", res.Coefficients)
			}
			if v := hillRecoveryViolations(t, res, truth); len(v) != 0 {
				t.Fatalf("the perturbed truth itself is not recovered: %v", v)
			}
		})
	}
}

// TestExtensions_RegressionHillInvariants: a large prior precision pins
// its channel's β to the prior mean (and a zero precision leaves the
// fit as without a prior); under nonnegative every channel β is ≥ 0 on
// data whose search effect is negative — which the unconstrained fit
// does report as negative, so the constraint binds.
func TestExtensions_RegressionHillInvariants(t *testing.T) {
	fsys := afero.NewMemMapFs()
	p := hillInstance(t, fsys, nil)
	hillCohort(t, p, hillDefaultTruth, 7)

	pinned := fitHill(t, p, "mmm_ad.pulse", hillSpec(`{"channels":["tv_ad","search_ad"],"nonnegative":true,
		"prior":{"channel":"tv_ad","mean":5,"precision":1e9}}`))
	if b := pinned.Coefficients["tv_ad"]; math.Abs(b-5) > 1e-4 {
		t.Errorf("precision 1e9: β tv_ad = %v, want pinned to the prior mean 5", b)
	}
	inert := fitHill(t, p, "mmm_ad.pulse", hillSpec(`{"channels":["tv_ad","search_ad"],"nonnegative":true,
		"prior":{"channel":"tv_ad","mean":5,"precision":0}}`))
	free := fitHill(t, p, "mmm_ad.pulse", hillSpec(hillParamsNonneg))
	if a, b := inert.Coefficients["tv_ad"], free.Coefficients["tv_ad"]; math.Abs(a-b) > 1e-6*math.Abs(b) {
		t.Errorf("precision 0: β tv_ad = %v, want the prior-free %v", a, b)
	}

	negative := hillDefaultTruth
	negative.srBeta = -4
	q := hillInstance(t, afero.NewMemMapFs(), nil)
	hillCohort(t, q, negative, 7)
	constrained := fitHill(t, q, "mmm_ad.pulse", hillSpec(hillParamsNonneg))
	for _, ch := range []string{"tv_ad", "search_ad"} {
		if b := constrained.Coefficients[ch]; !(b >= 0) {
			t.Errorf("nonnegative: β %s = %v, want ≥ 0", ch, b)
		}
	}
	unconstrained := fitHill(t, q, "mmm_ad.pulse", hillSpec(`{"channels":["tv_ad","search_ad"]}`))
	if b := unconstrained.Coefficients["search_ad"]; !(b < 0) {
		t.Errorf("unconstrained β search_ad = %v, want the negative effect the data carries", b)
	}
}

// hillScoreSlot scores fit name on every week of mmm_ad.pulse — p by
// ATTR_REG_PREDICT through the registration's Scorer, f by an
// ATTR_FORMULA writing the same model with the hill() expression
// function over the fit's reported figures — summed per week.
func hillScoreSlot(fit *types.RegressionResult, name string, models ...types.ModelRef) *types.Request {
	k := func(ch string) float64 { return fit.Outputs["k"].(map[string]any)[ch].(float64) }
	s := func(ch string) float64 { return fit.Outputs["s"].(map[string]any)[ch].(float64) }
	formula := fmt.Sprintf("%.17g + %.17g*hill(tv_ad, %.17g, %.17g) + %.17g*hill(search_ad, %.17g, %.17g) + %.17g*price",
		fit.Coefficients["(intercept)"],
		fit.Coefficients["tv_ad"], k("tv_ad"), s("tv_ad"),
		fit.Coefficients["search_ad"], k("search_ad"), s("search_ad"),
		fit.Coefficients["price"])
	return &types.Request{
		Label:  "score",
		Cohort: &types.Cohort{Filename: "mmm_ad.pulse"},
		Models: models,
		Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "week", Interval: 1}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"` + name + `"}`)},
			{Type: types.ATTR_FORMULA, Label: "f", Expression: formula},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "p", Label: "p"},
			{Type: types.AGG_SUM, Field: "f", Label: "f"},
		},
	}
}

func hillFitRequest(spec *types.RegressionSpec) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "mmm_ad.pulse"}, Regressions: []*types.RegressionSpec{spec}}
}

// TestExtensions_RegressionHillScorerSavesReloads: ATTR_REG_PREDICT
// scores a Compose fit of the model through its Scorer exactly as the
// model written with hill() (relative 1e-12: hill() computes
// x^s/(x^s+k^s), the scorer 1/(1+(k/x)^s)); the fit saves as a model
// file carrying its k / s outputs, and a fresh instance reloads it and
// scores bit-identically, predict accepting the load.
func TestExtensions_RegressionHillScorerSavesReloads(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p := hillInstance(t, fsys, nil)
	hillCohort(t, p, hillDefaultTruth, 7)
	const save = "models/mmm.pulse-model.json"
	direct := fitHill(t, p, "mmm_ad.pulse", hillSpec(hillParamsNonneg))
	out, err := p.Compose(ctx, &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "mmm", Request: hillFitRequest(hillSpec(hillParamsNonneg)), Save: save}},
		Requests: []*types.Request{hillScoreSlot(direct, "mmm")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mustJSON(t, out.Fits["mmm"].RegressionResult), mustJSON(t, direct); got != want {
		t.Fatalf("the Compose fit differs from Process':\n%s\n%s", got, want)
	}
	data := out.Responses[0].Data
	if len(data) != hillWeeks {
		t.Fatalf("%d score rows, want %d", len(data), hillWeeks)
	}
	for _, row := range data {
		pv, fv := row["p"].(float64), row["f"].(float64)
		if math.Abs(pv-fv) > 1e-12*math.Abs(fv) {
			t.Fatalf("week %v: scorer %v, hill() formula %v", row["week"], pv, fv)
		}
	}

	raw, err := afero.ReadFile(fsys, save)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	outs, _ := doc["outputs"].(map[string]any)
	if doc["type"] != string(regTestMMMHill) || outs["k"] == nil || outs["s"] == nil {
		t.Fatalf("saved model: type %v outputs %v", doc["type"], doc["outputs"])
	}

	fresh := hillInstance(t, fsys, nil)
	loadReq := hillScoreSlot(direct, "mmm", types.ModelRef{Name: "mmm", Path: save})
	loaded, err := fresh.Process(ctx, loadReq)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := mustJSON(t, loaded.Data), mustJSON(t, data); a != b {
		t.Fatalf("the reloaded model scores differently:\n%s\n%s", a, b)
	}
	cohort, err := afero.ReadFile(fsys, "mmm_ad.pulse")
	if err != nil {
		t.Fatal(err)
	}
	env, err := fresh.PredictBytes(ctx, cohort, loadReq)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict on the load: %v %+v", err, env.Errors)
	}
}

// TestExtensions_RegressionHillBootstrap: resample: bootstrap refits the
// model on every replicate — each coefficient gains a finite, positive
// replicate standard error and a p-value where the plain fit has none,
// the estimates stay the full fit's, and the run is reproducible.
func TestExtensions_RegressionHillBootstrap(t *testing.T) {
	p := hillInstance(t, afero.NewMemMapFs(), nil)
	hillCohort(t, p, hillDefaultTruth, 7)
	spec := func() *types.RegressionSpec {
		s := hillSpec(hillParamsNonneg)
		s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 12, 11
		return s
	}
	boot := fitHill(t, p, "mmm_ad.pulse", spec())
	plain := fitHill(t, p, "mmm_ad.pulse", hillSpec(hillParamsNonneg))
	if boot.BootstrapRequested != 12 || boot.BootstrapSucceeded != 12 {
		t.Errorf("bootstrap requested %d succeeded %d, want 12 / 12", boot.BootstrapRequested, boot.BootstrapSucceeded)
	}
	for name, est := range plain.Coefficients {
		if boot.Coefficients[name] != est {
			t.Errorf("%s: bootstrap estimate %v, full fit %v", name, boot.Coefficients[name], est)
		}
		se, pv := boot.StdErrors[name], boot.PValues[name]
		if !(se > 0) || math.IsInf(se, 0) || math.IsNaN(pv) {
			t.Errorf("%s: replicate se %v p %v, want finite positive se and a p-value", name, se, pv)
		}
	}
	if a, b := mustJSON(t, boot), mustJSON(t, fitHill(t, p, "mmm_ad.pulse", spec())); a != b {
		t.Fatalf("bootstrap not reproducible:\n%s\n%s", a, b)
	}
}

// TestExtensions_RegressionHillSelfDescribed: the manifest lists
// REG_TESTMMM_HILL as a buffered model with its declared params and
// k / s outputs, and predict accepts a fit as non-streamable; a feature
// profile that leaves it out hides it from the manifest and refuses it
// at Process and Predict exactly as a never-registered model.
func TestExtensions_RegressionHillSelfDescribed(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p := hillInstance(t, fsys, nil)
	hillCohort(t, p, hillDefaultTruth, 7)

	var meta *descriptor.RegressionMeta
	for i, m := range p.Manifest(ctx).Extensions.Regressions {
		if m.Name == string(regTestMMMHill) {
			meta = &p.Manifest(ctx).Extensions.Regressions[i]
		}
	}
	if meta == nil {
		t.Fatalf("manifest extensions.regressions lacks %s", regTestMMMHill)
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
	req := hillFitRequest(hillSpec(hillParamsNonneg))
	pr, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !pr.Valid || pr.Streamable {
		t.Errorf("predict valid %v streamable %v, want valid and buffered (errors %+v)", pr.Valid, pr.Streamable, pr.Errors)
	}

	hidden := hillInstance(t, fsys, func(o *pulse.Options) {
		o.FeatureProfile = &pulse.FeatureProfile{Features: []string{"capability:process", string(types.REG_OLS)}}
	})
	never, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range hidden.Manifest(ctx).Extensions.Regressions {
		if m.Name == string(regTestMMMHill) {
			t.Error("profile-hidden model still in the manifest")
		}
	}
	_, hiddenErr := hidden.Process(ctx, req)
	_, neverErr := never.Process(ctx, req)
	if hiddenErr == nil || neverErr == nil || hiddenErr.Error() != neverErr.Error() || mustJSON(t, hiddenErr) != mustJSON(t, neverErr) {
		t.Errorf("hidden refusal %v, never-registered %v; want the same refusal", hiddenErr, neverErr)
	}
	hp, err := hidden.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	np, err := never.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if hp.Valid || mustJSON(t, hp.Errors) != mustJSON(t, np.Errors) {
		t.Errorf("hidden predict errors %+v, never-registered %+v", hp.Errors, np.Errors)
	}
}
