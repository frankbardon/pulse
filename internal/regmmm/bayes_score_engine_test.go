package regmmm_test

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/types"
)

// bayes_score_engine_test.go holds REG_MMM_BAYES's save and scoring to
// their contract through the engine, on tiny fits (enough draws to run
// the path, not to read the posterior): a save, an engine holdout or a
// scoring reference without return_draws is refused identically by
// predict and the run; a saved file carries the thinned draws, the
// adstock record fixed at the posterior mean and the curve; scoring
// WIN_DISTRIBUTED_LAG columns built from that record equals the mean
// (and the credible bounds) of the model's prediction over the stored
// draws at that decay; the in-batch fit and its reloaded file score
// bit-identically; a `return` exclude keeps the draws off the wire while
// the file keeps them; the engine holdout scores through the scorer; a
// geo panel scores each row by its geo and an unseen geo from the
// population with a PULSE_MMM_GEO_UNSEEN warning.

// scoreParams is a tiny one-market fit that returns its draws, thinned.
const scoreParams = `{"channels":["tv"],"time":"week","chains":2,"warmup":30,"draws":12,"return_draws":true,"draws_thin":3}`

// fileDraws is a model file's outputs.draws, read back.
type fileDraws struct {
	Names         []string       `json:"names"`
	Draws         [][][]*float64 `json:"draws"`
	Thin          int            `json:"thin"`
	CredibleLevel float64        `json:"credible_level"`
}

// pooled is each coefficient's draws, chains in order then draws.
func (d fileDraws) pooled(name string) []float64 {
	j := slices.Index(d.Names, name)
	var out []float64
	for _, ch := range d.Draws {
		for _, v := range ch[j] {
			out = append(out, *v)
		}
	}
	return out
}

// quantile7 is the type-7 quantile of sorted at p.
func quantile7(sorted []float64, p float64) float64 {
	h := float64(float64(len(sorted)-1) * p)
	lo := int(math.Floor(h))
	if lo >= len(sorted)-1 {
		return sorted[len(sorted)-1]
	}
	return sorted[lo] + float64((h-float64(lo))*(sorted[lo+1]-sorted[lo]))
}

// summary is the mean and the credible bounds of preds at level.
func summary(preds []float64, level float64) (mean, lo, hi float64) {
	for _, v := range preds {
		mean += v
	}
	mean /= float64(len(preds))
	s := slices.Clone(preds)
	slices.Sort(s)
	return mean, quantile7(s, (1-level)/2), quantile7(s, (1+level)/2)
}

// writeRows writes rows under fields to name.
func writeRows(t testing.TB, p *pulse.Pulse, name string, fields []encoding.Field, rows []pulse.CohortRow) {
	t.Helper()
	s := encoding.Schema{Fields: fields}
	off := 0
	for i := range s.Fields {
		s.Fields[i].CsvColumnIdx = i
		s.Fields[i].ByteOffset = off
		s.Fields[i].Description = "Bayesian media-mix scoring column " + s.Fields[i].Name + "."
		off += s.FieldWidth(i)
	}
	b, err := p.NewCohortBuilder(context.Background(), name, s, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if err := b.Append(r); err != nil {
			t.Fatalf("Append row %d: %v", i, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// scoreReq scores fit per week (and geo when byGeo): the point score,
// the lower and the upper credible bound.
func scoreReq(cohort string, byGeo bool, models ...types.ModelRef) *types.Request {
	groups := []*types.Group{{Type: types.GROUP_RANGE, Field: "week", Interval: 1}}
	if byGeo {
		groups = append([]*types.Group{{Type: types.GROUP_CATEGORY, Field: "geo"}}, groups...)
	}
	attr := func(label, bound string) *types.Attribute {
		params := `{"fit":"m"}`
		if bound != "" {
			params = `{"fit":"m","bound":"` + bound + `"}`
		}
		return &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: label, Params: json.RawMessage(params)}
	}
	return &types.Request{
		Cohort:     &types.Cohort{Filename: cohort},
		Models:     models,
		Groups:     groups,
		Attributes: []*types.Attribute{attr("p", ""), attr("lo", "lower"), attr("hi", "upper")},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "p", Label: "p"},
			{Type: types.AGG_SUM, Field: "lo", Label: "lo"},
			{Type: types.AGG_SUM, Field: "hi", Label: "hi"},
		},
	}
}

// refusedLikePredict runs req and predicts it: both must refuse with
// the same code, message and details, carrying want as details.reason.
func refusedLikePredict(t *testing.T, p *pulse.Pulse, req *types.Request, code perr.Code, reason string) {
	t.Helper()
	ctx := context.Background()
	_, err := p.Process(ctx, req)
	var ce *perr.CodedError
	if !errorsAs(err, &ce) || ce.Code != code || ce.Details["reason"] != reason {
		t.Fatalf("runtime: %v, want %s reason %s", err, code, reason)
	}
	pr, perrv := p.Predict(ctx, req)
	if perrv != nil || len(pr.Errors) == 0 {
		t.Fatalf("predict accepted: %v", perrv)
	}
	got := pr.Errors[0]
	if got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
		t.Fatalf("predict first error %+v, runtime %s %q %v", got, ce.Code, ce.Message, ce.Details)
	}
}

// TestMMMBayes_ScoringNeedsDrawsLikePredict: a save, an engine holdout,
// a Compose fit's save and a scoring reference over a loaded file
// without draws are each refused draws_required — PROCESSING_CONFIG for
// the save and the holdout, PULSE_MODEL_SCORE_INVALID for the score —
// with the same code, message and details by predict and the run,
// before any sampling.
func TestMMMBayes_ScoringNeedsDrawsLikePredict(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	c := &countingSampler{}
	withSampler(t, c)
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p := instance(t, fsys, nil)
	writeBayesCohort(t, p, regmmm.BayesCohort, encoding.FieldTypeU16, regmmm.BayesRows(11))

	save := regmmm.BayesSpec(regmmm.FastParams)
	save.Save = "m.pulse-model.json"
	refusedLikePredict(t, p, bayesRequest(regmmm.BayesCohort, save), perr.PROCESSING_CONFIG, model.ReasonDrawsRequired)

	hold := regmmm.BayesSpec(regmmm.FastParams)
	hold.Holdout = "week > 50"
	refusedLikePredict(t, p, bayesRequest(regmmm.BayesCohort, hold), perr.PROCESSING_CONFIG, model.ReasonDrawsRequired)

	batch := &pulse.ComposedRequest{
		Fits: []types.FitSpec{{Name: "m", Save: "m.pulse-model.json",
			Request: bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(regmmm.FastParams))}},
		Requests: []*types.Request{bayesRequest(regmmm.BayesCohort)},
	}
	_, err := p.Compose(ctx, batch)
	var ce *perr.CodedError
	if !errorsAs(err, &ce) || ce.Code != perr.PROCESSING_CONFIG || ce.Details["reason"] != model.ReasonDrawsRequired || ce.Details["fit"] != 0 {
		t.Fatalf("compose fit save: %v, want PROCESSING_CONFIG draws_required on fits[0]", err)
	}
	env, err := p.PredictCompose(ctx, batch)
	if err != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message ||
		mustJSON(t, env.Errors[0].Details) != mustJSON(t, ce.Details) {
		t.Fatalf("predict compose %v %s, runtime %s %q %s", err, mustJSON(t, env), ce.Code, ce.Message, mustJSON(t, ce.Details))
	}

	// A file saved without draws (an earlier build could not save one;
	// a hand-written one may lack them) cannot score.
	doc := `{"model_format_version":1,"features":["outputs"],"name":"m","type":"REG_MMM_BAYES","target":"sales",
		"predictors":["tv","price"],"terms":["tv","price"],
		"coefficients":{"(intercept)":200,"tv":40,"price":-5,"decay:tv":0.5,"k:tv":120,"s:tv":1.5,"sigma":1},
		"outputs":{"seed":1},"n_obs":60,"provenance":{}}`
	if err := afero.WriteFile(fsys, "nodraws.pulse-model.json", []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	score := scoreReq(regmmm.BayesCohort, false, types.ModelRef{Name: "m", Path: "nodraws.pulse-model.json"})
	refusedLikePredict(t, p, score, perr.PULSE_MODEL_SCORE_INVALID, model.ReasonDrawsRequired)
	if n := c.runs.Load(); n != 0 {
		t.Errorf("a draws-less save, holdout or score sampled %d times", n)
	}
}

// TestMMMBayes_SaveAndScore: a fit saved with return_draws and
// draws_thin writes the thinned draws (and their credible level), the
// adstock record at the posterior-mean decay stamped fixed_at
// posterior_mean, unnormalised over max_lag; a `return` exclude keeps
// the draws off the wire while the file keeps them. Scoring columns
// WIN_DISTRIBUTED_LAG builds from that record equals, per week, the
// mean over the stored draws of α + γ·price + β·hill(adstock, k, s) —
// the model's prediction at the posterior-mean decay, computed here with
// the fixture's own Hill and the record's weights — and lower / upper
// its credible bounds; the in-batch fit scores the same bytes as its
// reloaded file, and predict accepts the scoring request.
func TestMMMBayes_SaveAndScore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p := instance(t, fsys, nil)
	rows := regmmm.BayesRows(11)
	writeBayesCohort(t, p, regmmm.BayesCohort, encoding.FieldTypeU16, rows)

	const path = "models/m.pulse-model.json"
	s := regmmm.BayesSpec(scoreParams)
	// A pinned seed: the derived one hashes the spec, save included, and
	// the in-batch twin below does not save.
	s.Save, s.RNGSeed = path, 7
	req := bayesRequest(regmmm.BayesCohort, s)
	req.Return = &types.Return{Preset: types.ReturnPresetStandard, Exclude: []string{"regressions[*].outputs.draws"}}
	r := process(t, p, req).Regressions[0]
	if r.Error != nil || r.Outputs["draws"] != nil || r.Outputs["roi"] == nil {
		t.Fatalf("fit outputs %v (error %v): want roi without draws on the wire", keysOf(r.Outputs), r.Error)
	}
	raw, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := model.DecodeFile(path, raw)
	if err != nil {
		t.Fatal(err)
	}
	var d fileDraws
	if err := json.Unmarshal(f.Outputs["draws"], &d); err != nil || d.Thin != 3 || len(d.Draws) != 2 || len(d.Draws[0][0]) != 4 || d.CredibleLevel != 0.9 {
		t.Fatalf("file draws thin %d, %d chains × %v, level %v (%v); want 3, 2 × 4, 0.9", d.Thin, len(d.Draws), len(d.Draws[0][0]), d.CredibleLevel, err)
	}
	ad := f.Adstock["tv"]
	if ad.FixedAt != model.MMMAdstockFixedAtPosteriorMean || ad.Kernel != "geometric" || ad.Normalize || ad.MaxLag != regmmm.DefaultMaxLag ||
		ad.Decay == nil || *ad.Decay != float64(f.Coefficients["decay:tv"]) {
		t.Fatalf("adstock record %+v, want geometric at decay:tv %v, fixed_at posterior_mean", ad, f.Coefficients["decay:tv"])
	}

	// Pre-adstock the raw spend with WIN_DISTRIBUTED_LAG from the record.
	fields := []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "tv_raw", Type: encoding.FieldTypeF64},
		{Name: "price", Type: encoding.FieldTypeF64},
	}
	in := make([]pulse.CohortRow, len(rows))
	for i, r := range rows {
		in[i] = pulse.CohortRow{uint64(r.Week), r.TV, r.Price}
	}
	writeRows(t, p, "raw.pulse", fields, in)
	win := &types.Window{Type: types.WIN_DISTRIBUTED_LAG, Field: "tv_raw", Label: "tv", OrderBy: []types.OrderKey{{Field: "week"}}, Params: ad.Params()}
	if _, err := p.Materialize(ctx, &types.Request{Cohort: &types.Cohort{Filename: "raw.pulse"}, Windows: []*types.Window{win}},
		"score.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	load := scoreReq("score.pulse", false, types.ModelRef{Name: "m", Path: path})
	loaded := process(t, p, load)
	if len(loaded.Data) != len(rows) {
		t.Fatalf("%d score rows, want %d", len(loaded.Data), len(rows))
	}

	// The oracle: the record's weights over the raw spend, then each
	// stored draw's prediction.
	w, err := ad.Weights()
	if err != nil {
		t.Fatal(err)
	}
	alpha, beta, gamma := d.pooled("(intercept)"), d.pooled("tv"), d.pooled("price")
	k, sh := d.pooled("k:tv"), d.pooled("s:tv")
	for i, row := range loaded.Data {
		a := 0.0
		for l := 0; l < len(w) && l <= i; l++ {
			a += float64(w[l] * rows[i-l].TV)
		}
		preds := make([]float64, len(alpha))
		for j := range preds {
			preds[j] = alpha[j] + float64(gamma[j]*rows[i].Price) + float64(beta[j]*mmmfixture.JointHill(a, k[j], sh[j]))
		}
		mean, lo, hi := summary(preds, d.CredibleLevel)
		for _, c := range []struct {
			key  string
			want float64
		}{{"p", mean}, {"lo", lo}, {"hi", hi}} {
			if got := row[c.key].(float64); !(math.Abs(got-c.want) <= 1e-9*math.Max(1, math.Abs(c.want))) {
				t.Fatalf("week %v %s = %v, want %v (the draws' prediction at the posterior-mean decay)", row["week"], c.key, got, c.want)
			}
		}
		if !(row["lo"].(float64) <= row["p"].(float64) && row["p"].(float64) <= row["hi"].(float64)) {
			t.Fatalf("week %v: bounds %v ≤ %v ≤ %v out of order", row["week"], row["lo"], row["p"], row["hi"])
		}
	}

	// The in-batch fit scores the bytes its reloaded file does.
	twin := regmmm.BayesSpec(scoreParams)
	twin.RNGSeed = 7
	batch := &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "m", Request: bayesRequest(regmmm.BayesCohort, twin)}},
		Requests: []*types.Request{scoreReq("score.pulse", false)},
	}
	out, err := p.Compose(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := mustJSON(t, out.Responses[0].Data), mustJSON(t, loaded.Data); a != b {
		t.Fatalf("the in-batch fit scores differently from its file:\n%s\n%s", a, b)
	}
	data, err := afero.ReadFile(fsys, "score.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if env, err := p.PredictBytes(ctx, data, load); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict on the scoring request: %v %+v", err, env.Errors)
	}
}

// TestMMMBayes_EngineHoldoutScores: the engine holdout fits the train
// rows and scores the held-out rows through the scorer on their columns
// as given (no carry-over from the history: params.holdout_from is the
// history-aware holdout) — its RMSE equals the train fit's scorer on
// those rows.
func TestMMMBayes_EngineHoldoutScores(t *testing.T) {
	t.Parallel()

	p := bayesInstance(t, nil)
	rows := regmmm.BayesRows(11)
	s := regmmm.BayesSpec(scoreParams)
	s.Holdout = "week > 50"
	r := process(t, p, bayesRequest(regmmm.BayesCohort, s)).Regressions[0]
	if r.Error != nil || r.Holdout == nil || r.Holdout.N != 10 || r.NObs != 50 {
		t.Fatalf("holdout fit = %s", mustJSON(t, r))
	}
	outs, err := model.NormalizeOutputs(r.Outputs)
	if err != nil {
		t.Fatal(err)
	}
	fit := fittedOf(r, s)
	fit.Outputs = outs
	sc, err := regmmm.BayesScorer(fit)
	if err != nil {
		t.Fatal(err)
	}
	var sse float64
	for _, row := range rows[50:] {
		y, ok := sc.Score(scoreRow{"tv": row.TV, "price": row.Price})
		if !ok {
			t.Fatalf("week %v scored null", row.Week)
		}
		sse += float64((row.Sales - y) * (row.Sales - y))
	}
	if want := math.Sqrt(sse / 10); !relClose(r.Holdout.RMSE, want, 1e-9) {
		t.Errorf("holdout rmse %v, want %v from the scorer on the held-out rows", r.Holdout.RMSE, want)
	}
}

// geoScoreFields is a scoring panel's schema: a categorical geo whose
// dictionary holds a market the fit never saw (north).
func geoScoreFields(t testing.TB) []encoding.Field {
	geo := encoding.NewDictionary()
	for _, g := range []string{"east", "west", "north"} {
		if _, err := geo.Add(g); err != nil {
			t.Fatal(err)
		}
	}
	return []encoding.Field{
		{Name: "geo", Type: encoding.FieldTypeCategoricalU8, Dictionary: geo},
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "tv", Type: encoding.FieldTypeF64},
		{Name: "price", Type: encoding.FieldTypeF64},
	}
}

// TestMMMBayes_GeoScoreUnseenGeo: a geo panel saved with its draws
// scores each row with its own geo's intercept and β (the shared curve)
// and a geo the fit lacks from the population β and the per-draw mean of
// the geo intercepts, raising one PULSE_MMM_GEO_UNSEEN warning naming
// the geo, its row count and the fit's geos; a scoring cohort without
// the geo field is PULSE_MODEL_PREDICTOR_MISSING in predict and at run
// alike; a request that does not group by geo still decodes it.
func TestMMMBayes_GeoScoreUnseenGeo(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	p := instance(t, fsys, nil)
	writeBayesCohort(t, p, "panel.pulse", encoding.FieldTypeU16, panelRows())
	const path = "geo.pulse-model.json"
	s := regmmm.BayesSpec(`{"channels":["tv"],"time":"week","geo":"geo","chains":1,"warmup":20,"draws":8,"return_draws":true}`)
	s.Save = path
	if r := process(t, p, bayesRequest("panel.pulse", s)).Regressions[0]; r.Error != nil {
		t.Fatalf("panel fit = %s", mustJSON(t, r))
	}
	raw, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := model.DecodeFile(path, raw)
	if err != nil {
		t.Fatal(err)
	}
	var d fileDraws
	if err := json.Unmarshal(f.Outputs["draws"], &d); err != nil {
		t.Fatal(err)
	}

	// Pre-adstocked inputs: any non-negative column scores; three rows
	// per geo.
	var in []pulse.CohortRow
	for _, g := range []string{"east", "west", "north"} {
		for w := range 3 {
			in = append(in, pulse.CohortRow{g, uint64(w + 1), float64(40 * (w + 1)), 0.5 - float64(float64(w)*0.25)})
		}
	}
	writeRows(t, p, "score.pulse", geoScoreFields(t), in)
	resp := process(t, p, scoreReq("score.pulse", true, types.ModelRef{Name: "m", Path: path}))
	if len(resp.Data) != 9 {
		t.Fatalf("%d score rows, want 9", len(resp.Data))
	}
	intercept := func(g string) []float64 {
		if g != "north" {
			return d.pooled("(intercept)[" + g + "]")
		}
		e, w := d.pooled("(intercept)[east]"), d.pooled("(intercept)[west]")
		out := make([]float64, len(e))
		for j := range e {
			out[j] = (e[j] + w[j]) / 2
		}
		return out
	}
	betaOf := func(g string) []float64 {
		if g == "north" {
			return d.pooled("tv")
		}
		return d.pooled("tv[" + g + "]")
	}
	gamma, k, sh := d.pooled("price"), d.pooled("k:tv"), d.pooled("s:tv")
	for _, row := range resp.Data {
		g := row["geo"].(string)
		lo, _, _ := strings.Cut(row["week"].(string), "-")
		wk, err := strconv.Atoi(lo)
		if err != nil {
			t.Fatal(err)
		}
		x, price := float64(40*wk), 0.5-float64(float64(wk-1)*0.25)
		alpha, beta := intercept(g), betaOf(g)
		preds := make([]float64, len(alpha))
		for j := range preds {
			preds[j] = alpha[j] + float64(gamma[j]*price) + float64(beta[j]*mmmfixture.JointHill(x, k[j], sh[j]))
		}
		mean, _, _ := summary(preds, d.CredibleLevel)
		if got := row["p"].(float64); !(math.Abs(got-mean) <= 1e-9*math.Max(1, math.Abs(mean))) {
			t.Fatalf("%s week %d: score %v, want %v", g, wk, got, mean)
		}
	}
	// Grouped by week alone the slot still reads the geo (the projected
	// decode carries it): each week's sum is its three geos' scores.
	perWeek := map[string]float64{}
	for _, row := range resp.Data {
		perWeek[row["week"].(string)] += row["p"].(float64)
	}
	for _, row := range process(t, p, scoreReq("score.pulse", false, types.ModelRef{Name: "m", Path: path})).Data {
		want := perWeek[row["week"].(string)]
		if got, ok := row["p"].(float64); !ok || !(math.Abs(got-want) <= 1e-9*math.Max(1, math.Abs(want))) {
			t.Fatalf("week %v ungrouped by geo: %v, want %v (the geo field projected)", row["week"], row["p"], want)
		}
	}
	var unseen []*types.ResponseWarning
	for _, w := range resp.Warnings {
		if w.Code == string(perr.PULSE_MMM_GEO_UNSEEN) {
			unseen = append(unseen, w)
		}
	}
	if len(unseen) != 3 { // one per scoring slot (p, lo, hi)
		t.Fatalf("PULSE_MMM_GEO_UNSEEN warnings %s, want one per slot", mustJSON(t, resp.Warnings))
	}
	if got := mustJSON(t, unseen[0].Details); !strings.Contains(got, `"geos":["north"]`) || !strings.Contains(got, `"rows":[3]`) ||
		!strings.Contains(got, `"fit_geos":["east","west"]`) || !strings.Contains(got, `"field":"geo"`) || !strings.Contains(got, `"fit":"m"`) {
		t.Errorf("warning details %s", got)
	}

	// No geo field on the scoring frame: refused alike.
	noGeo := geoScoreFields(t)[1:]
	writeRows(t, p, "nogeo.pulse", noGeo, []pulse.CohortRow{{uint64(1), 10.0, 0.0}})
	req := scoreReq("nogeo.pulse", false, types.ModelRef{Name: "m", Path: path})
	ctx := context.Background()
	_, rerr := p.Process(ctx, req)
	var ce *perr.CodedError
	if !errorsAs(rerr, &ce) || ce.Code != perr.PULSE_MODEL_PREDICTOR_MISSING || ce.Details["field"] != "geo" {
		t.Fatalf("runtime without the geo field: %v", rerr)
	}
	pr, err := p.Predict(ctx, req)
	if err != nil || len(pr.Errors) == 0 || pr.Errors[0].Code != string(ce.Code) || pr.Errors[0].Message != ce.Message {
		t.Fatalf("predict without the geo field: %v %s, runtime %s", err, mustJSON(t, pr), ce.Message)
	}
}
