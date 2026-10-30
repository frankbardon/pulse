package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	iio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// buildExampleFixtures imports every shared example fixture cohort into
// tmp as <name>.pulse and builds the transactions_sharded.pulse archive
// the anchor-syntax example reads.
func buildExampleFixtures(t *testing.T, p *pulse.Pulse, fs afero.Fs, tmp string) {
	t.Helper()
	cohorts := []string{"transactions", "customers", "orders", "training_data", "all_types", "experiment", "repeated_measures", "card_issuers", "tier_migrations", "survey", "subscribers", "warehouse_orders", "plan_history", "plan_history_wide", "battery", "intake", "mmm_weekly", "brand_survey"}
	for _, name := range cohorts {
		csvPath := filepath.Join("internal", "examples", "fixtures", name+".csv")
		schemaPath := filepath.Join("internal", "examples", "fixtures", "schemas", name+".json")
		outPath := filepath.Join(tmp, name+".pulse")

		schema, err := loadFixtureSchema(schemaPath)
		if err != nil {
			t.Fatalf("loadFixtureSchema(%s): %v", schemaPath, err)
		}

		job := &pio.ImportJob{
			Source:     csv.NewReader(fs, csvPath),
			Target:     outPath,
			Schema:     schema,
			SampleRows: 50,
			FS:         fs,
		}
		report, err := p.Import(context.Background(), job)
		if err != nil {
			t.Fatalf("import %s: %v", name, err)
		}
		if report.RowsImported == 0 {
			t.Fatalf("import %s: zero rows imported", name)
		}
	}

	// Build a shard archive from transactions.pulse so anchor-syntax
	// examples (cohort.filename = `transactions_sharded.pulse#wave1.pulse`)
	// resolve at runtime. The two shards split records evenly; each is a
	// complete standalone single-file .pulse so the anchor view reads
	// like any other cohort.
	if err := buildShardedTransactionsFixture(p, fs, tmp); err != nil {
		t.Fatalf("build sharded transactions fixture: %v", err)
	}

	// The point-lookup index a {record} comparison reference reads
	// (attributes/20_record_reference.json) — fixtures/build.sh builds
	// the same one.
	if _, err := p.BuildIndex(context.Background(), filepath.Join(tmp, "experiment.pulse"), []string{"id"}); err != nil {
		t.Fatalf("index experiment.pulse on id: %v", err)
	}

	// The saved population compose/06_population_score.json loads:
	// run compose/05_vector_fit_save.json with its cohorts and its
	// `save` path redirected into tmp (p's filesystem root is the OS
	// root, so a relative save would land in the working directory).
	seedExamplePopulation(t, p, tmp)
}

// examplePopulationSave is compose/05's `save` path (relative to the
// data root), the file compose/06 loads.
const examplePopulationSave = "populations/panel_a.pulse-population.json"

// seedExamplePopulation writes tmp/<examplePopulationSave> by running
// compose/05_vector_fit_save.json through p with every cohort data_dir
// and the fit's save path rebased under tmp.
func seedExamplePopulation(t *testing.T, p *pulse.Pulse, tmp string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("internal", "examples", "compose", "05_vector_fit_save.json"))
	if err != nil {
		t.Fatal(err)
	}
	var req pulse.ComposedRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("parse compose/05: %v", err)
	}
	for i := range req.Fits { // Fits holds values: write through the index
		f := &req.Fits[i]
		f.Request.Cohort.DataDir = tmp
		if f.Save != examplePopulationSave {
			t.Fatalf("compose/05 saves %q, want %q", f.Save, examplePopulationSave)
		}
		f.Save = filepath.Join(tmp, f.Save)
	}
	for _, r := range req.Requests {
		r.Cohort.DataDir = tmp
	}
	if _, err := p.Compose(context.Background(), &req); err != nil {
		t.Fatalf("seed example population (compose/05): %v", err)
	}
}

// runExample loads an example, rewrites its cohort.data_dir to the
// temp .pulse dir, and dispatches the request through both Predict and
// Process. Asserts predict reports Valid=true (warnings are allowed —
// the leakage example is expected to warn) and that process returns a
// non-nil response with non-nil data; returns the response.
func runExample(t *testing.T, p *pulse.Pulse, examplePath, dataDir string) *pulse.Response {
	t.Helper()
	body, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read %s: %v", examplePath, err)
	}
	var req pulse.Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("parse %s: %v", examplePath, err)
	}
	if req.Cohort == nil {
		t.Fatalf("%s: missing cohort", examplePath)
	}
	req.Cohort.DataDir = dataDir

	predictResult, err := p.Predict(context.Background(), &req)
	if err != nil {
		t.Fatalf("Predict %s: %v", examplePath, err)
	}
	if predictResult == nil || !predictResult.Valid {
		t.Fatalf("%s: predict did not report Valid=true (result=%+v)", examplePath, predictResult)
	}

	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process %s: %v", examplePath, err)
	}
	if resp == nil {
		t.Fatalf("%s: nil response", examplePath)
	}
	// Test-only requests legitimately have nil Data — the rows slot is
	// empty when the request carries no aggregations. Accept that as
	// long as at least one of Tests, PostTests, Regressions, or
	// Crosstab is populated. Regression-only requests (e.g., simple
	// OLS) emit no rows; matrix-shape crosstabs populate Crosstab
	// instead of Data.
	if resp.Data == nil && len(resp.Tests) == 0 && len(resp.PostTests) == 0 && len(resp.Regressions) == 0 && len(resp.Matrices) == 0 && resp.Crosstab == nil {
		t.Fatalf("%s: nil data and no test/regression/crosstab results", examplePath)
	}
	// Every requested overlay must come back as a layer — a dropped
	// spec would otherwise pass as a clean run.
	if len(resp.Overlays) != len(req.Overlays) {
		t.Fatalf("%s: %d overlay specs but %d layers", examplePath, len(req.Overlays), len(resp.Overlays))
	}
	// Every layer must carry at least one figure — an example whose
	// params silently skip every cell (a p_source over raw counts, say)
	// returns a well-formed but empty layer that a shape check misses.
	for i := range resp.Overlays {
		if !overlayLayerHasFigure(&resp.Overlays[i]) {
			t.Fatalf("%s: overlay layer %q (%s) carries no figure; warnings: %+v",
				examplePath, resp.Overlays[i].Name, resp.Overlays[i].Kind, resp.Overlays[i].Warnings)
		}
	}
	return resp
}

// overlayLayerHasFigure reports whether an overlay layer's payload
// carries at least one present value on whichever shape it declares.
func overlayLayerHasFigure(l *types.OverlayLayer) bool {
	pl := l.Payload
	if pl.Scalar != nil {
		return true
	}
	if pl.Series != nil && len(pl.Series.Entries) > 0 {
		return true
	}
	if pl.Matrix != nil {
		for _, row := range pl.Matrix.Cells {
			for _, c := range row {
				if c.Present {
					return true
				}
			}
		}
	}
	return false
}

// heavyExamples are the library's two multi-second examples, keyed by
// "<category>/<_meta.name>" with their file under internal/examples.
// TestExamples_RunEndToEnd is the ONE example pass that executes them
// (and holds their figures, exampleChecks); every other pass over the
// library skips or cheapens them by name and says what still covers
// them there. TestHeavyExamplesExist keeps the names live.
var heavyExamples = map[string]string{
	"regression/mmm_bayes":              "regression/20_mmm_bayes.json",
	"matrices/sem-defined-contribution": "matrices/17_sem_contribution.json",
}

// isHeavyExample reports whether the example at category/name is one
// of heavyExamples.
func isHeavyExample(category, name string) bool {
	_, ok := heavyExamples[category+"/"+name]
	return ok
}

// isHeavyExampleFile reports whether path (any prefix, then
// internal/examples/<category>/<file>) is one of heavyExamples' files.
func isHeavyExampleFile(path string) bool {
	rel := filepath.ToSlash(path)
	for _, f := range heavyExamples {
		if rel == f || strings.HasSuffix(rel, "/"+f) {
			return true
		}
	}
	return false
}

// TestHeavyExamplesExist: every heavyExamples entry names an embedded
// example whose file is the one listed — a rename would otherwise drop
// the skip (a slow pass) or, worse, the figure checks that ride the one
// pass that runs it.
func TestHeavyExamplesExist(t *testing.T) {
	t.Parallel()

	for key, file := range heavyExamples {
		body, err := os.ReadFile(filepath.Join("internal", "examples", filepath.FromSlash(file)))
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		var doc struct {
			Meta struct {
				Name     string `json:"name"`
				Category string `json:"category"`
			} `json:"_meta"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if got := doc.Meta.Category + "/" + doc.Meta.Name; got != key {
			t.Errorf("%s is %s, heavyExamples keys it %s", file, got, key)
		}
	}
}

func TestRegressionExamplesCount(t *testing.T) {
	t.Parallel()

	matches, err := filepath.Glob(filepath.Join("internal", "examples", "regression", "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	const want = 20
	if len(matches) != want {
		t.Errorf("want %d regression examples under examples/regression/, found %d: %v", want, len(matches), matches)
	}
}

// loadFixtureSchema reads a fixture schema file through the shared
// schema-file decoder (internal/io.ParseSchemaJSON), so the fixtures are
// laid out exactly as `pulse import --schema` lays them out.
func loadFixtureSchema(path string) (*encoding.Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return iio.ParseSchemaJSON(data)
}

// buildShardedTransactionsFixture takes the imported transactions.pulse
// at dataDir/transactions.pulse, splits its records evenly into two
// standalone single-file shards, and wraps them in a shard archive at
// dataDir/transactions_sharded.pulse. The anchor-syntax aggregations
// example (06_sharded_anchor) reads through the archive's `wave1.pulse`
// shard.
func buildShardedTransactionsFixture(p *pulse.Pulse, fs afero.Fs, dataDir string) error {
	srcPath := filepath.Join(dataDir, "transactions.pulse")
	src, err := afero.ReadFile(fs, srcPath)
	if err != nil {
		return err
	}

	r := bytes.NewReader(src)
	pulseVersion, err := encoding.ReadHeader(r)
	if err != nil {
		return err
	}
	schema, err := encoding.ReadSchema(r, pulseVersion)
	if err != nil {
		return err
	}
	preludeLen := len(src) - r.Len()
	prelude := src[:preludeLen]
	records := src[preludeLen:]

	recordSize := 0
	for _, f := range schema.Fields {
		recordSize += f.Type.ByteSize()
	}
	if recordSize == 0 {
		return nil
	}
	splitAt := (len(records) / recordSize) / 2 * recordSize

	build := func(name string, recordTail []byte) (string, error) {
		path := filepath.Join(dataDir, name)
		buf := make([]byte, 0, len(prelude)+len(recordTail))
		buf = append(buf, prelude...)
		buf = append(buf, recordTail...)
		return path, afero.WriteFile(fs, path, buf, 0o644)
	}

	wave1Path, err := build("wave1.pulse", records[:splitAt])
	if err != nil {
		return err
	}
	wave2Path, err := build("wave2.pulse", records[splitAt:])
	if err != nil {
		return err
	}

	archivePath := filepath.Join(dataDir, "transactions_sharded.pulse")
	_, err = p.CreateShardArchive(context.Background(), archivePath,
		[]string{wave1Path, wave2Path})
	return err
}

// TestExamples_VectorFitPopulationRoundTrip runs the two population
// examples in order (compose/05 fits panel a and SAVES it as a
// population file; compose/06 loads that file through `populations`)
// on an instance rooted at the fixture directory, so the save path and
// the load path resolve against the same data root. Compose examples
// are not in TestExamples_RunEndToEnd (it runs single requests); this
// pins the pair end to end: no errors or warnings, the fit's vector
// summary, every slot answered, and the loaded population scoring
// panel b exactly as the in-batch fit did.
func TestExamples_VectorFitPopulationRoundTrip(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	builder, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	buildExampleFixtures(t, builder, osfs, tmp)
	p, err := pulse.New(pulse.Options{DataDir: tmp})
	if err != nil {
		t.Fatal(err)
	}

	run := func(name string) *pulse.ComposedResponse {
		t.Helper()
		body, err := os.ReadFile(filepath.Join("internal", "examples", "compose", name))
		if err != nil {
			t.Fatal(err)
		}
		var req pulse.ComposedRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		// The instance is rooted at tmp, so cohorts resolve from the root.
		for _, f := range req.Fits {
			f.Request.Cohort.DataDir = ""
		}
		for _, r := range req.Requests {
			r.Cohort.DataDir = ""
		}
		env, err := p.PredictCompose(context.Background(), &req)
		if err != nil {
			t.Fatalf("PredictCompose %s: %v", name, err)
		}
		if len(env.Errors) != 0 || len(env.Warnings) != 0 {
			t.Fatalf("PredictCompose %s: errors %v warnings %v", name, env.Errors, env.Warnings)
		}
		resp, err := p.Compose(context.Background(), &req)
		if err != nil {
			t.Fatalf("Compose %s: %v", name, err)
		}
		if len(resp.Responses) != len(req.Requests) {
			t.Fatalf("%s: %d slots, %d responses", name, len(req.Requests), len(resp.Responses))
		}
		for i, r := range resp.Responses {
			if r == nil || (r.Data == nil && len(r.Matrices) == 0) {
				t.Fatalf("%s: slot %d answered nothing", name, i)
			}
			if len(r.Warnings) != 0 {
				t.Fatalf("%s: slot %d warnings %v", name, i, r.Warnings)
			}
		}
		return resp
	}

	// buildExampleFixtures seeded the file; remove it so 05 must write it.
	if err := os.Remove(filepath.Join(tmp, examplePopulationSave)); err != nil {
		t.Fatal(err)
	}
	fit := run("05_vector_fit_save.json")
	if f := fit.Fits["panel_a"]; f == nil || f.Vector == nil || f.Vector.Operator != "MAT_COVARIANCE" {
		t.Fatalf("05: fits.panel_a = %+v, want a MAT_COVARIANCE vector fit", fit.Fits["panel_a"])
	}
	if _, err := os.Stat(filepath.Join(tmp, examplePopulationSave)); err != nil {
		t.Fatalf("05 did not save the population file: %v", err)
	}
	loaded := run("06_population_score.json")

	// Panel b's mean D² from the in-batch fit (05) and from the loaded
	// population (06, group b) must agree: the file round-trips the
	// fitted state at full precision.
	inBatch := fit.Responses[0].Data[0]["mean_d2"]
	var fromFile any
	for _, row := range loaded.Responses[0].Data {
		if row["panel"] == "b" {
			fromFile = row["mean_d2"]
		}
	}
	if inBatch == nil || inBatch != fromFile {
		t.Fatalf("panel b mean D²: in-batch fit %v, loaded population %v", inBatch, fromFile)
	}
	if len(loaded.Responses[1].Matrices) != 1 {
		t.Fatalf("06: panel_a_matrix returned %d matrices, want 1", len(loaded.Responses[1].Matrices))
	}
}

// TestExamples_SEMInvarianceLadderSweep runs
// compose/09_sem_invariance_sweep.json over the brand_survey fixture
// cohort: predict is clean, the zip sweep expands into the configural /
// metric / scalar slots in order, each a joint multi-group MAT_SEM fit
// with no slot error, dof rises step by step, only the scalar step
// frees wave2's latent means, and those means recover the fixture's
// planted shift (brand_spec.json: value −0.30 and service +0.70 factor
// SD, so −0.24 and +0.525 in marker units; quality 0) — each within two
// SEs of its truth, value and service distinguishable from zero.
func TestExamples_SEMInvarianceLadderSweep(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	builder, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	buildExampleFixtures(t, builder, osfs, tmp)
	p, err := pulse.New(pulse.Options{DataDir: tmp})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("internal", "examples", "compose", "09_sem_invariance_sweep.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	delete(doc["sweep"].(map[string]any)["request"].(map[string]any)["cohort"].(map[string]any), "data_dir")
	if body, err = json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
	var req pulse.ComposedRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("parse: %v", err)
	}

	env, err := p.PredictCompose(context.Background(), &req)
	if err != nil {
		t.Fatalf("PredictCompose: %v", err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("PredictCompose errors: %v", env.Errors)
	}
	out, err := p.Compose(context.Background(), &req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	steps := []string{"configural", "metric", "scalar"}
	if len(out.Responses) != len(steps) {
		t.Fatalf("%d slots, want %d", len(out.Responses), len(steps))
	}
	prevDof := -1.0
	for i, step := range steps {
		r := out.Responses[i]
		if r.Label != step || len(r.Matrices) != 1 || r.Matrices[0].Error != nil {
			t.Fatalf("slot %d: label %q, matrices %+v", i, r.Label, r.Matrices)
		}
		m := r.Matrices[0]
		dof := m.Scalars["dof"]
		if !(dof > prevDof) {
			t.Errorf("%s: dof %g does not rise past %g", step, dof, prevDof)
		}
		prevDof = dof
		est := m.Auxiliary["estimates"]
		if est == nil {
			t.Fatalf("%s: no estimates", step)
		}
		freeMean := false
		for k, key := range est.RowKeys {
			if key == "[wave2] service ~1" {
				freeMean = m.Vectors["free"].([]bool)[k]
			}
		}
		if want := step == "scalar"; freeMean != want {
			t.Errorf("%s: [wave2] service ~1 free = %v, want %v", step, freeMean, want)
		}
		if step != "scalar" {
			continue
		}
		for factor, truth := range map[string]float64{"quality": 0, "value": -0.24, "service": 0.525} {
			key := "[wave2] " + factor + " ~1"
			k := slices.Index(est.RowKeys, key)
			if k < 0 {
				t.Fatalf("scalar: no %s row", key)
			}
			v, se := est.Values[k][0], est.Values[k][2]
			if !(math.Abs(v-truth) <= 2*se) {
				t.Errorf("scalar: %s = %.4f (SE %.4f), planted %.3f", key, v, se, truth)
			}
			if truth != 0 && !(math.Abs(v/se) > 2) {
				t.Errorf("scalar: %s = %.4f (SE %.4f) is not distinguishable from zero", key, v, se)
			}
		}
	}
}

// TestExamples_SEMFitScoresFeedRegression runs
// compose/10_sem_fit_scores.json over the brand_survey fixture: predict
// is clean; the SEM fit binds (fits.brand is a SEM fit with no error);
// the by_wave slot's mean scores track the planted shift (wave1, the
// reference, near 0; value down and service up in wave2; quality flat);
// and the drivers slot's OLS of recommend on the three scores finds
// each a positive driver distinguishable from zero.
func TestExamples_SEMFitScoresFeedRegression(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	builder, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	buildExampleFixtures(t, builder, osfs, tmp)
	p, err := pulse.New(pulse.Options{DataDir: tmp})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("internal", "examples", "compose", "10_sem_fit_scores.json"))
	if err != nil {
		t.Fatal(err)
	}
	var req pulse.ComposedRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for i := range req.Fits {
		req.Fits[i].Request.Cohort.DataDir = ""
	}
	for _, r := range req.Requests {
		r.Cohort.DataDir = ""
	}
	env, err := p.PredictCompose(context.Background(), &req)
	if err != nil {
		t.Fatalf("PredictCompose: %v", err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("PredictCompose errors: %v", env.Errors)
	}
	out, err := p.Compose(context.Background(), &req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(out.Warnings) != 0 {
		t.Fatalf("compose warnings: %+v", out.Warnings)
	}
	fit, ok := out.Fits["brand"]
	if !ok || fit.SEM == nil || fit.SEM.Error != nil {
		t.Fatalf("fits.brand = %+v, want a SEM fit without error", fit)
	}
	if len(out.Responses) != 2 || out.Responses[0].Label != "by_wave" || out.Responses[1].Label != "drivers" {
		t.Fatalf("responses %+v", out.Responses)
	}

	means := map[string]map[string]float64{}
	for _, row := range out.Responses[0].Data {
		wave := fmt.Sprint(row["wave"])
		means[wave] = map[string]float64{}
		for _, lat := range []string{"quality", "value", "service"} {
			v, _ := row["mean_"+lat].(float64)
			means[wave][lat] = v
		}
	}
	w1, w2 := means["wave1"], means["wave2"]
	if w1 == nil || w2 == nil {
		t.Fatalf("by_wave rows %v", means)
	}
	for lat, planted := range map[string]float64{"quality": 0, "value": -0.24, "service": 0.525} {
		if math.Abs(w1[lat]) > 0.05 {
			t.Errorf("wave1 mean %s score %.4f, want near 0 (the reference)", lat, w1[lat])
		}
		if shift := w2[lat] - w1[lat]; math.Abs(shift-planted) > 0.15 {
			t.Errorf("mean %s score shift %.4f, planted %.3f", lat, shift, planted)
		}
	}

	regs := out.Responses[1].Regressions
	if len(regs) != 1 || regs[0].Error != nil {
		t.Fatalf("drivers regressions %+v", regs)
	}
	for _, lat := range []string{"quality", "value", "service"} {
		name := lat + "_score"
		b, found := regs[0].Coefficients[name]
		if !found || !(b > 0) || !(regs[0].PValues[name] < 0.05) {
			t.Errorf("recommend ~ %s: slope %v (found %v), p %v; want a positive driver distinguishable from zero", name, b, found, regs[0].PValues[name])
		}
	}
}

// TestExamples_ComposeRunClean runs every compose example over the
// shared fixture cohorts: predict reports no error and the batch
// compiles and runs to a response per slot with no slot failure. It is
// the compose twin of TestExamples_RunEndToEnd — example bitrot and
// operator regressions — and asserts nothing about any one domain's
// figures.
func TestExamples_ComposeRunClean(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	builder, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	buildExampleFixtures(t, builder, osfs, tmp)
	// Rooted at tmp: every cohort, save and population path resolves
	// from the data root, so each example drops its data_dir.
	p, err := pulse.New(pulse.Options{DataDir: tmp})
	if err != nil {
		t.Fatal(err)
	}
	var dropDataDir func(v any)
	dropDataDir = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if c, ok := x["cohort"].(map[string]any); ok {
				delete(c, "data_dir")
			}
			for _, e := range x {
				dropDataDir(e)
			}
		case []any:
			for _, e := range x {
				dropDataDir(e)
			}
		}
	}
	matches, err := filepath.Glob(filepath.Join("internal", "examples", "compose", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) < 13 {
		t.Fatalf("found %d compose examples, want at least 13", len(matches))
	}
	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(body, &doc); err != nil {
				t.Fatalf("parse: %v", err)
			}
			delete(doc, "_meta")
			dropDataDir(doc)
			if body, err = json.Marshal(doc); err != nil {
				t.Fatal(err)
			}
			var req pulse.ComposedRequest
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("decode: %v", err)
			}
			env, err := p.PredictCompose(context.Background(), &req)
			if err != nil {
				t.Fatalf("PredictCompose: %v", err)
			}
			if len(env.Errors) != 0 {
				t.Fatalf("PredictCompose errors: %s", mustJSON(t, env.Errors))
			}
			out, err := p.Compose(context.Background(), &req)
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if len(out.Responses) == 0 {
				t.Fatal("no responses")
			}
			for _, r := range out.Responses {
				for _, w := range r.Warnings {
					if w.Code == "PULSE_SLOT_FAILED" {
						t.Fatalf("slot %q: %s", r.Label, w.Message)
					}
				}
			}
		})
	}
}
