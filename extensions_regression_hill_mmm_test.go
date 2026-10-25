package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// extensions_regression_hill_mmm_test.go pins custom-regressions E4-S2:
// the Harbor & Pine media-mix fixture (docs/roadmap/fixtures/
// mmm-harbor-pine) runs end to end as pure Pulse requests through the
// library facade plus ONE registration, REG_TESTMMM_HILL
// (extensions_testmmm_hill_test.go):
//
//  1. ImportFile the panel CSV;
//  2. Materialize the fixture's own req/04 (WIN_EWMA adstock grid), then
//     Materialize WIN_DISTRIBUTED_LAG geometric at the fixture's true decays
//     (TV 0.7, search 0.2, social 0.4, display 0.3) plus one geometric
//     twin per req/04 EWMA column — a regression cannot read a window
//     label of its own request, so adstock is materialized first;
//  3. fit REG_TESTMMM_HILL (Compose fits, saved);
//  4. ATTR_REG_RESIDUAL with params.fit beside ATTR_REG_PREDICT;
//  5. a Compose sweep ranked on residual_std_err.
//
// The fixture carries no Hill truth, so every assertion is an invariant
// over figures Pulse itself returned; no number the test computes is
// fed back into a request except a prior mean derived from a Pulse fit.

const mmmFixtureDir = "docs/roadmap/fixtures/mmm-harbor-pine/"

// mmmTrueDecays is the fixture's built-in geometric decay per channel
// (README, "Ground truth").
var mmmTrueDecays = []struct {
	channel string
	decay   float64
}{{"tv", 0.7}, {"search", 0.2}, {"social", 0.4}, {"display", 0.3}}

// mmmEWMAColumn matches req/04's WIN_EWMA adstock labels <channel>_ad<λ·100>.
var mmmEWMAColumn = regexp.MustCompile(`^(tv|search|social|display)_ad(\d+)$`)

// mmmMaxLag covers a whole region partition (104 weeks), so the
// geometric kernel truncates nothing.
const mmmMaxLag = 103

func mmmAdstock(field, label string, decay float64) *types.Window {
	return &types.Window{Type: types.WIN_DISTRIBUTED_LAG, Field: field, Label: label, PartitionBy: []string{"region"},
		OrderBy: []types.OrderKey{{Field: "week"}},
		Params: json.RawMessage(fmt.Sprintf(`{"kernel":"geometric","decay":%s,"max_lag":%d,"normalize":false}`,
			strconv.FormatFloat(decay, 'g', -1, 64), mmmMaxLag))}
}

// mmmControls are req/05's linear controls (FEAT_ONE_HOT rg_* included).
var mmmControls = []string{"email_ksends", "discount_pct", "holiday", "competitor_sov", "category_demand", "week_index",
	"rg_California", "rg_Mid-Atlantic", "rg_Mountain", "rg_Northeast", "rg_Pacific_NW", "rg_South_Central", "rg_Southeast"}

// mmmChannels are the true-decay WIN_DISTRIBUTED_LAG columns.
var mmmChannels = []string{"tv_adstock", "search_adstock", "social_adstock", "display_adstock"}

// mmmPipeline runs steps 1–2 on a fresh in-memory instance carrying the
// one registration and returns it with the req/04 materialize result.
func mmmPipeline(t testing.TB, mut func(*pulse.Options)) (*pulse.Pulse, afero.Fs, *pulse.MaterializeResult) {
	t.Helper()
	ctx := context.Background()
	csvBytes, err := os.ReadFile(mmmFixtureDir + "harbor_pine_mmm.csv")
	if err != nil {
		t.Fatal(err)
	}
	reqBytes, err := os.ReadFile(mmmFixtureDir + "req/04_adstock_grid.json")
	if err != nil {
		t.Fatal(err)
	}
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "harbor_pine_mmm.csv", csvBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	p := hillInstance(t, fsys, mut)
	imp, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "harbor_pine_mmm.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	var grid types.Request
	if err := json.Unmarshal(reqBytes, &grid); err != nil {
		t.Fatal(err)
	}
	grid.Cohort = &types.Cohort{Filename: imp.Path} // the managed import, in place of the CLI's data_dir file
	feat, err := p.Materialize(ctx, &grid, "harbor_pine_features.pulse", pulse.MaterializeOptions{})
	if err != nil {
		t.Fatalf("Materialize req/04: %v", err)
	}
	if feat.Records != 832 || len(feat.DerivedColumns) != 27 {
		t.Fatalf("req/04: %d records, %d derived columns; README says 832, 27", feat.Records, len(feat.DerivedColumns))
	}
	var wins []*types.Window
	for _, d := range mmmTrueDecays {
		wins = append(wins, mmmAdstock(d.channel+"_spend_k", d.channel+"_adstock", d.decay))
	}
	for _, col := range feat.DerivedColumns {
		if m := mmmEWMAColumn.FindStringSubmatch(col); m != nil {
			n, _ := strconv.Atoi(m[2])
			wins = append(wins, mmmAdstock(m[1]+"_spend_k", m[1]+"_gx"+m[2], float64(n)/100))
		}
	}
	if len(wins) != 4+19 {
		t.Fatalf("%d adstock windows, want 4 true-decay + 19 EWMA twins", len(wins))
	}
	if _, err := p.Materialize(ctx, &types.Request{Cohort: &types.Cohort{Filename: "harbor_pine_features.pulse"}, Windows: wins},
		"mmm_ad.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Fatalf("Materialize WIN_DISTRIBUTED_LAG: %v", err)
	}
	return p, fsys, feat
}

// mmmHillSpec is the media-mix slot over channels (each a predictor)
// plus the controls; extra is spliced into params after channels.
func mmmHillSpec(channels []string, extra string) *types.RegressionSpec {
	ch, _ := json.Marshal(channels)
	return &types.RegressionSpec{Type: regTestMMMHill, Name: "mmm", Target: "revenue_k",
		Predictors: append(slices.Clone(channels), mmmControls...),
		Params:     json.RawMessage(`{"channels":` + string(ch) + `,"nonnegative":true` + extra + `}`)}
}

// mmmFitRequest reads the adstock cohort with req/05's region one-hots.
func mmmFitRequest(spec *types.RegressionSpec) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "mmm_ad.pulse"},
		Features:    []*types.Feature{{Type: types.FEAT_ONE_HOT, Field: "region", Label: "rg"}},
		Regressions: []*types.RegressionSpec{spec}}
}

func mmmFit(t *testing.T, p *pulse.Pulse, spec *types.RegressionSpec) *types.RegressionResult {
	t.Helper()
	resp, err := p.Process(context.Background(), mmmFitRequest(spec))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		t.Fatalf("regressions = %s", mustJSON(t, resp.Regressions))
	}
	return resp.Regressions[0]
}

// mmmColumns reads every row of cohort as name -> value.
func mmmColumns(t testing.TB, p *pulse.Pulse, cohort string) []map[string]any {
	t.Helper()
	c, err := p.Open(context.Background(), cohort)
	if err != nil {
		t.Fatal(err)
	}
	rd, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rd.Close() }()
	fields := c.Schema().Fields
	var out []map[string]any
	for i := int64(0); ; i++ {
		rec, err := rd.RecordAt(i)
		if err != nil {
			break
		}
		row := map[string]any{}
		for j, f := range fields {
			row[f.Name] = rec[j]
		}
		out = append(out, row)
	}
	if len(out) != 832 {
		t.Fatalf("%s: read %d rows, want 832", cohort, len(out))
	}
	return out
}

func mmmNum(t testing.TB, row map[string]any, name string) float64 {
	t.Helper()
	switch v := row[name].(type) {
	case float64:
		return v
	case uint64:
		return float64(v)
	}
	t.Fatalf("column %s = %#v, want a number", name, row[name])
	return 0
}

// TestExtensions_RegressionHillMMMAdstockMatchesEWMA: WIN_DISTRIBUTED_LAG
// geometric, unnormalised and scaled by (1 − λ), agrees with every one
// of req/04's 19 WIN_EWMA adstock columns. The two differ only by the
// EWMA's seed: EWMA_i = Σ_{l<i} (1−λ)λ^l·x_{i−l} + λ^i·x_0 (it seeds on
// the partition's first value), while (1−λ)·A_i = Σ_{l≤i} (1−λ)λ^l·x_{i−l}
// with the kernel covering the whole partition (max_lag 103), so
// EWMA_i − (1−λ)·A_i = λ^{i+1}·x_0 exactly in real arithmetic. The test
// asserts that identity within 1e-10 relative (the two sums of ≤104
// positive terms associate differently), and that the seed term itself
// is what separates them — it vanishes once λ^{i+1} is negligible.
func TestExtensions_RegressionHillMMMAdstockMatchesEWMA(t *testing.T) {
	p, _, feat := mmmPipeline(t, nil)
	rows := mmmColumns(t, p, "mmm_ad.pulse")
	var compared int
	for _, col := range feat.DerivedColumns {
		m := mmmEWMAColumn.FindStringSubmatch(col)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		lambda := float64(n) / 100
		spend, twin := m[1]+"_spend_k", m[1]+"_gx"+m[2]
		var region string
		var pos int
		var x0 float64
		for r, row := range rows {
			if g, _ := row["region"].(string); g != region || r == 0 {
				region, pos, x0 = g, 0, mmmNum(t, row, spend)
			}
			if wi := mmmNum(t, row, "week_index"); int(wi) != pos {
				t.Fatalf("row %d: week_index %v at partition position %d; the cohort is not sorted by region, week", r, wi, pos)
			}
			ewma, ad := mmmNum(t, row, col), mmmNum(t, row, twin)
			seed := math.Pow(lambda, float64(pos+1)) * x0
			if diff := ewma - (1-lambda)*ad; math.Abs(diff-seed) > 1e-10*math.Max(1, math.Abs(ewma)) {
				t.Fatalf("%s vs (1−%v)·%s, %s week %d: EWMA %v, scaled adstock %v, gap %v, seed term λ^(i+1)·x0 %v",
					col, lambda, twin, region, pos, ewma, (1-lambda)*ad, diff, seed)
			}
			pos++
		}
		compared++
	}
	if compared != 19 {
		t.Fatalf("compared %d EWMA columns, want req/04's 19", compared)
	}
}

// TestExtensions_RegressionHillMMMPipeline runs steps 3–5 and asserts
// the story's invariants:
//
//   - every media β is non-negative (params.nonnegative). Every true β
//     of the fixture is positive and so is its unconstrained Hill fit,
//     so here the constraint does not bind; that it binds is pinned on
//     generated data by TestExtensions_RegressionHillInvariants;
//   - the Hill fit's residual_std_err is no larger than REG_OLS's on the
//     same adstocked columns and controls (the Hill curve nests the
//     linear one as k → ∞, and the 8 extra parameters cost < 0.6% in
//     the df correction on 832 rows);
//   - ATTR_REG_RESIDUAL {params.fit} is exactly y − ATTR_REG_PREDICT
//     {fit} on every row (the same scorer value, one IEEE subtraction);
//   - the sweep's ranking is the slots' own residual_std_err sorted
//     ascending, its top label their argmin;
//   - the fit is bit-identical across DecodeWorkers.
func TestExtensions_RegressionHillMMMPipeline(t *testing.T) {
	ctx := context.Background()
	p, fsys, _ := mmmPipeline(t, nil)

	// Step 3: the Compose fit, saved; Process returns the same fit.
	const save = "models/mmm_hill.pulse-model.json"
	composed, err := p.Compose(ctx, &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "mmm", Request: mmmFitRequest(mmmHillSpec(mmmChannels, "")), Save: save}},
		Requests: []*types.Request{{Label: "noop", Cohort: &types.Cohort{Filename: "mmm_ad.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "revenue_k", Label: "n"}}}},
	})
	if err != nil {
		t.Fatalf("Compose fit: %v", err)
	}
	hill := composed.Fits["mmm"].RegressionResult
	if hill == nil || hill.Error != nil {
		t.Fatalf("fit = %s", mustJSON(t, composed.Fits["mmm"]))
	}
	if got, want := mustJSON(t, mmmFit(t, p, mmmHillSpec(mmmChannels, ""))), mustJSON(t, hill); got != want {
		t.Fatalf("Process fit differs from the Compose fit:\n%s\n%s", got, want)
	}
	if hill.NObs != 832 || hill.ConvergedIters < 1 {
		t.Fatalf("n_obs %d converged_iters %d", hill.NObs, hill.ConvergedIters)
	}

	// Non-negativity of the media β's.
	for _, ch := range mmmChannels {
		if b := hill.Coefficients[ch]; !(b >= 0) {
			t.Errorf("β %s = %v, want ≥ 0 under nonnegative", ch, b)
		}
	}

	// The reported RSE is the fit's own figure, not a default: it is
	// √(deviance / (n − dim)), dim = 1 + 3·4 channels + 13 controls.
	if dim := 1 + 3*len(mmmChannels) + len(mmmControls); !(hill.ResidualStdErr > 0) ||
		math.Abs(hill.ResidualStdErr-math.Sqrt(hill.Deviance/float64(832-dim))) > 1e-12*hill.ResidualStdErr {
		t.Fatalf("residual_std_err %v, deviance %v: want √(deviance/%d)", hill.ResidualStdErr, hill.Deviance, 832-dim)
	}

	// Hill RSE ≤ the linear REG_OLS RSE on the same columns.
	ols := mmmFit(t, p, &types.RegressionSpec{Type: types.REG_OLS, Name: "ols", Target: "revenue_k",
		Predictors: append(slices.Clone(mmmChannels), mmmControls...)})
	if !(hill.ResidualStdErr <= ols.ResidualStdErr) {
		t.Errorf("Hill residual_std_err %v > REG_OLS %v on the same adstocked columns", hill.ResidualStdErr, ols.ResidualStdErr)
	}

	// Step 4: residual with params.fit == y − the extension's scored value.
	scored, err := p.Materialize(ctx, &types.Request{
		Cohort:   &types.Cohort{Filename: "mmm_ad.pulse"},
		Models:   []types.ModelRef{{Name: "mmm", Path: save}},
		Features: []*types.Feature{{Type: types.FEAT_ONE_HOT, Field: "region", Label: "rg"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_REG_PREDICT, Label: "score", Params: json.RawMessage(`{"fit":"mmm"}`)},
			{Type: types.ATTR_REG_RESIDUAL, Label: "resid", Params: json.RawMessage(`{"fit":"mmm"}`)},
		},
	}, "mmm_scored.pulse", pulse.MaterializeOptions{})
	if err != nil {
		t.Fatalf("Materialize scores: %v", err)
	}
	if !slices.Contains(scored.DerivedColumns, "resid") || !slices.Contains(scored.DerivedColumns, "score") {
		t.Fatalf("derived columns %v lack score / resid", scored.DerivedColumns)
	}
	var ssr float64
	for r, row := range mmmColumns(t, p, "mmm_scored.pulse") {
		y, score, resid := mmmNum(t, row, "revenue_k"), mmmNum(t, row, "score"), mmmNum(t, row, "resid")
		if resid != y-score {
			t.Fatalf("row %d: residual %v, y − score = %v − %v = %v", r, resid, y, score, y-score)
		}
		ssr += resid * resid
	}
	// The scored residuals are the fit's own: their sum of squares is the
	// reported deviance (relative 1e-9: the fit sums in its own order).
	if math.Abs(ssr-hill.Deviance) > 1e-9*hill.Deviance {
		t.Errorf("Σ residual² %v, fit deviance %v", ssr, hill.Deviance)
	}

	// Step 5: a sweep over the TV decay, ranked on residual_std_err.
	tvDecays := []string{"10", "30", "50", "60", "70", "80", "90"}
	var axis []json.RawMessage
	for _, d := range tvDecays {
		axis = append(axis, json.RawMessage(strconv.Quote(d)))
	}
	body := mmmFitRequest(mmmHillSpec(append([]string{"tv_gx{{tv}}"}, mmmChannels[1:]...), ""))
	rawBody, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	axisJSON, _ := json.Marshal(axis)
	var sweepReq pulse.ComposedRequest
	if err := json.Unmarshal([]byte(`{"requests":[],"sweep":{"axes":[{"name":"tv","values":`+string(axisJSON)+`}],
		"label":"tv{{tv}}","request":`+string(rawBody)+`,
		"rank":{"by":"regressions.mmm.residual_std_err","order":"asc","top":3}}}`), &sweepReq); err != nil {
		t.Fatal(err)
	}
	swept, err := p.Compose(ctx, &sweepReq)
	if err != nil {
		t.Fatalf("Compose sweep: %v", err)
	}
	if len(swept.Responses) != len(tvDecays) {
		t.Fatalf("%d sweep responses, want %d", len(swept.Responses), len(tvDecays))
	}
	type slot struct {
		label string
		rse   float64
	}
	var slots []slot
	for _, r := range swept.Responses {
		if len(r.Regressions) != 1 || r.Regressions[0].Error != nil || !strings.HasPrefix(r.Label, "tv") {
			t.Fatalf("slot %q regressions = %s", r.Label, mustJSON(t, r.Regressions))
		}
		if r.Regressions[0].Type != regTestMMMHill {
			t.Fatalf("slot %q fitted %s", r.Label, r.Regressions[0].Type)
		}
		slots = append(slots, slot{r.Label, r.Regressions[0].ResidualStdErr})
	}
	sorted := slices.Clone(slots)
	slices.SortStableFunc(sorted, func(a, b slot) int {
		switch {
		case a.rse < b.rse:
			return -1
		case a.rse > b.rse:
			return 1
		}
		return 0
	})
	if len(swept.Ranking) != 3 {
		t.Fatalf("ranking = %s, want top 3", mustJSON(t, swept.Ranking))
	}
	for i, e := range swept.Ranking {
		if e.Label != sorted[i].label || e.Value != sorted[i].rse || e.Rank != i+1 {
			t.Errorf("ranking[%d] = %+v, want {%s %v %d} from the slots' own residual_std_err %v", i, e, sorted[i].label, sorted[i].rse, i+1, slots)
		}
	}
	// The expected top label: the slot at the fixture's true TV decay
	// (0.7). Its lead over the runner-up is ~0.2% of the RSE, far above
	// any cross-architecture rounding in the fit (≲1e-12 relative).
	if swept.Ranking[0].Label != "tv70" {
		t.Errorf("ranking top %q, want tv70 (the true TV decay)", swept.Ranking[0].Label)
	}
	// The tv70 slot is the main fit's twin (tv_gx70 is tv_adstock: the
	// same decay, kernel and lag), so its RSE is the main fit's.
	for _, s := range slots {
		if s.label == "tv70" && s.rse != hill.ResidualStdErr {
			t.Errorf("slot tv70 residual_std_err %v, the main fit %v", s.rse, hill.ResidualStdErr)
		}
	}
	t.Logf("sweep: %v; ranking %s", slots, mustJSON(t, swept.Ranking))

	// Determinism across decode workers.
	want := mustJSON(t, hill)
	for _, dw := range []int{1, 8} {
		q := hillInstance(t, fsys, func(o *pulse.Options) { o.DecodeWorkers = dw })
		if got := mustJSON(t, mmmFit(t, q, mmmHillSpec(mmmChannels, ""))); got != want {
			t.Fatalf("DecodeWorkers %d fit differs:\n%s\n%s", dw, got, want)
		}
	}
}

// TestExtensions_RegressionHillMMMPriorMonotone: raising params.prior's
// precision on the TV channel moves its β monotonically toward the
// prior mean, and the largest precision pins it there. The mean is half
// the prior-free fit's β (a Pulse figure, so far from the data's
// optimum). For a quadratic penalty the penalty term at the optimum is
// non-increasing in its weight, so |β − mean| is non-increasing in the
// precision; the slack 1e-9 relative absorbs the optimiser's stopping
// tolerance (1e-13 relative in cost).
func TestExtensions_RegressionHillMMMPriorMonotone(t *testing.T) {
	p, _, _ := mmmPipeline(t, nil)
	free := mmmFit(t, p, mmmHillSpec(mmmChannels, ""))
	mean := free.Coefficients["tv_adstock"] / 2
	prev := math.Inf(1)
	var last float64
	for _, prec := range []float64{0, 1e-2, 1, 1e2, 1e4, 1e6, 1e9} {
		fit := mmmFit(t, p, mmmHillSpec(mmmChannels, fmt.Sprintf(`,"prior":{"channel":"tv_adstock","mean":%s,"precision":%s}`,
			strconv.FormatFloat(mean, 'g', -1, 64), strconv.FormatFloat(prec, 'g', -1, 64))))
		gap := math.Abs(fit.Coefficients["tv_adstock"] - mean)
		if gap > prev*(1+1e-9) {
			t.Errorf("precision %g: |β tv − mean| = %v grew from %v", prec, gap, prev)
		}
		if prec == 0 && math.Abs(fit.Coefficients["tv_adstock"]-free.Coefficients["tv_adstock"]) > 1e-6*free.Coefficients["tv_adstock"] {
			t.Errorf("precision 0: β tv %v, the prior-free fit %v", fit.Coefficients["tv_adstock"], free.Coefficients["tv_adstock"])
		}
		t.Logf("precision %g: β tv %v (gap %v)", prec, fit.Coefficients["tv_adstock"], gap)
		prev, last = gap, gap
	}
	if first := math.Abs(free.Coefficients["tv_adstock"] - mean); !(last < 1e-3*first) {
		t.Errorf("precision 1e9 leaves |β tv − mean| = %v, want pinned (< 1e-3 × the free gap %v)", last, first)
	}
}
