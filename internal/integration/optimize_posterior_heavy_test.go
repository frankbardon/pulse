//go:build heavy

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// optimize_posterior_heavy_test.go runs a Compose optimize block's
// `posterior` over an in-batch REG_MMM_BAYES fit (mmm-bayes-depth
// E3-S3) — the carrier the fast tier cannot sample: one market and a
// two-market geo panel on the example fixture's weekly series, tiny
// single-chain fits.

// mmmWeeklyPulse imports the example fixture mmm_weekly.csv into a temp
// directory and returns an instance rooted there.
func mmmWeeklyPulse(t *testing.T) *pulse.Pulse {
	t.Helper()
	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	builder, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := loadFixtureSchema(filepath.Join("internal", "examples", "fixtures", "schemas", "mmm_weekly.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Import(context.Background(), &pio.ImportJob{
		Source: csv.NewReader(osfs, filepath.Join("internal", "examples", "fixtures", "mmm_weekly.csv")),
		Target: filepath.Join(tmp, "mmm_weekly.pulse"), Schema: schema, SampleRows: 50, FS: osfs,
	}); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{DataDir: tmp})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// posteriorBatch is a two-channel budget allocation over an in-batch
// REG_MMM_BAYES fit `mmm` of fitParams (one market: filtered to east),
// the objective objective, under posterior.
func posteriorBatch(t *testing.T, fitParams, filter, variables, objective, constraint, posterior string) *pulse.ComposedRequest {
	t.Helper()
	raw := `{"params": {"budget": 700},
	"fits": [{"name": "mmm", "request": {"cohort": {"filename": "mmm_weekly.pulse"}` + filter + `,
		"regressions": [{"type": "REG_MMM_BAYES", "target": "sales", "predictors": ["tv", "search"], "rng_seed": 1, "params": ` + fitParams + `}]}}],
	"requests": [{"label": "weeks", "cohort": {"filename": "mmm_weekly.pulse"}, "aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}]}],
	"optimize": [{"name": "allocation", "direction": "maximize", "variables": ` + variables + `,
		"objective": "` + objective + `", "constraints": [{"expr": "` + constraint + `", "type": "eq"}],
		"posterior": ` + posterior + `, "starts": 3, "rng_seed": 3}]}`
	var c pulse.ComposedRequest
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

// runPosterior runs c on both orchestrators, requires the same bytes
// and returns the single block's converged result.
func runPosterior(t *testing.T, p *pulse.Pulse, build func() *pulse.ComposedRequest) types.OptimizeResult {
	t.Helper()
	ctx := context.Background()
	env, err := p.PredictCompose(ctx, build())
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict: %v %s", err, mustJSON(t, env))
	}
	serial, err := p.Compose(ctx, build())
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := p.ComposeParallel(ctx, build(), pulse.ComposeOptions{MaxWorkers: 3})
	if err != nil {
		t.Fatal(err)
	}
	a, b := mustJSON(t, serial.Optimizations), mustJSON(t, parallel.Optimizations)
	if !bytes.Equal([]byte(a), []byte(b)) {
		t.Fatalf("serial and parallel differ:\n%s\n%s", a, b)
	}
	if len(serial.Optimizations) != 1 || serial.Optimizations[0].Error != nil || !serial.Optimizations[0].Converged {
		t.Fatalf("optimizations %s", a)
	}
	return serial.Optimizations[0]
}

// TestOptimize_PosteriorInBatchFit: over a Compose REG_MMM_BAYES fit
// returning its draws, a seeded subsample of 12 of its 30 draws solves
// to the same bytes serial and parallel and replays them run after run;
// the echo names the fit and its draws, and every re-solve converges.
// A geo panel's per-geo response() reads each geo's own draws: the
// panel block allocates across the two markets and spends the budget.
func TestOptimize_PosteriorInBatchFit(t *testing.T) {
	t.Parallel()

	p := mmmWeeklyPulse(t)
	const one = `{"channels": ["tv", "search"], "time": "week", "max_lag": 6, "chains": 1, "warmup": 40, "draws": 30, "return_draws": true}`
	east := `, "filterers": [{"type": "FILTER_INCLUDE", "field": "market", "values": ["east"]}]`
	vars := `[{"name": "tv", "lower": 300, "upper": 650}, {"name": "search", "lower": 20, "upper": 300}]`
	obj := `response(\"mmm\", \"tv\", tv) + response(\"mmm\", \"search\", search)`
	build := func() *pulse.ComposedRequest {
		return posteriorBatch(t, one, east, vars, obj, "tv + search - params.budget", `{"draws": 12}`)
	}
	first := runPosterior(t, p, build)
	if again := runPosterior(t, p, build); mustJSON(t, again) != mustJSON(t, first) {
		t.Fatalf("a rerun differs:\n%s\n%s", mustJSON(t, again), mustJSON(t, first))
	}
	if pr := first.Posterior; pr == nil || pr.Fit != "mmm" || pr.Objective != types.OptimizePosteriorMean || pr.Draws != 12 || pr.DrawsAvailable != 30 || pr.DrawsResolved != 12 {
		t.Fatalf("posterior echo %+v", first.Posterior)
	}
	for _, v := range first.Variables {
		if v.Interval == nil || !(v.Interval.Lower <= v.Interval.Upper) {
			t.Errorf("%s interval %+v", v.Name, v.Interval)
		}
	}

	// The geo panel: tv in each market, read through its geo's own draws.
	const panel = `{"channels": ["tv", "search"], "time": "week", "geo": "market", "max_lag": 6, "chains": 1, "warmup": 40, "draws": 20, "return_draws": true}`
	geoVars := `[{"name": "east", "lower": 100, "upper": 600}, {"name": "west", "lower": 100, "upper": 600}]`
	geoObj := `response(\"mmm\", \"tv\", east, \"east\") + response(\"mmm\", \"tv\", west, \"west\")`
	geo := runPosterior(t, p, func() *pulse.ComposedRequest {
		return posteriorBatch(t, panel, "", geoVars, geoObj, "east + west - params.budget", `{"objective": "quantile", "q": 0.25}`)
	})
	if pr := geo.Posterior; pr == nil || pr.Draws != 20 || pr.Objective != types.OptimizePosteriorQuantile || pr.Q != 0.25 {
		t.Fatalf("geo posterior echo %+v", geo.Posterior)
	}
	if d := geo.Variables[0].Value + geo.Variables[1].Value - 700; d > 1e-6 || d < -1e-6 {
		t.Errorf("geo allocation %+v does not spend the budget", geo.Variables)
	}
}

// TestOptimize_PosteriorFailedFitFailsAlone: a posterior block over a
// Compose Bayes fit that failed on its data fails alone with the fit's
// own code (PULSE_SLOT_FAILED warning, slot_kind optimizations) — no
// draws to read — on both orchestrators.
func TestOptimize_PosteriorFailedFitFailsAlone(t *testing.T) {
	t.Parallel()

	p := mmmWeeklyPulse(t)
	const fit = `{"channels": ["tv", "search"], "time": "week", "chains": 1, "warmup": 10, "draws": 5, "return_draws": true}`
	// No week survives the filter: the fit fails on its data.
	none := `, "filterers": [{"type": "FILTER_EXPRESSION", "expression": "week > 1000"}]`
	build := func() *pulse.ComposedRequest {
		return posteriorBatch(t, fit, none, `[{"name": "tv", "lower": 300, "upper": 650}, {"name": "search", "lower": 20, "upper": 300}]`,
			`response(\"mmm\", \"tv\", tv) + response(\"mmm\", \"search\", search)`, "tv + search - params.budget", `{}`)
	}
	ctx := context.Background()
	for name, run := range map[string]func() (*pulse.ComposedResponse, error){
		"Compose": func() (*pulse.ComposedResponse, error) { return p.Compose(ctx, build()) },
		"ComposeParallel": func() (*pulse.ComposedResponse, error) {
			return p.ComposeParallel(ctx, build(), pulse.ComposeOptions{MaxWorkers: 2})
		},
	} {
		resp, err := run()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(resp.Optimizations) != 1 || resp.Optimizations[0].Error == nil {
			t.Fatalf("%s: optimizations %s", name, mustJSON(t, resp.Optimizations))
		}
		fe := resp.Fits["mmm"]
		if fe == nil || fe.Error == nil || resp.Optimizations[0].Error.Code != fe.Error.Code {
			t.Fatalf("%s: block error %+v, fit %s", name, resp.Optimizations[0].Error, mustJSON(t, fe))
		}
	}
}
