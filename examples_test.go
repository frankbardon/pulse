package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	iio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestExamples_RunEndToEnd builds the shared fixture cohorts into a
// temp directory, then runs every example JSON across all categories
// (attributes, filterers, groupers, windows, features) through
// pulse.Process. Asserts no errors come back. Catches both example
// bitrot (schema drift) and operator regressions.
func TestExamples_RunEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	fs := afero.NewOsFs()

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	buildExampleFixtures(t, p, fs, tmp)

	categories := []struct {
		name    string
		dir     string
		minJSON int
		glob    string // "" = every *.json in dir
	}{
		{"features", "features", 10, ""},
		{"attributes", "attributes", 6, ""},
		{"filterers", "filterers", 4, ""},
		{"groupers", "groupers", 5, ""},
		{"windows", "windows", 10, ""},
		{"aggregations", "aggregations", 4, ""},
		{"tests", "tests", 27, ""},
		{"regression", "regression", 17, ""},
		{"matrices", "matrices", 2, ""},
		{"crosstab", "crosstab", 13, ""},
		// The overlays directory mixes Request-, Compose- and Facet-host
		// examples, and only Request-host ones run through pulse.Process,
		// so this arm is scoped to the MATRIX-host axis-pairing family.
		{"overlays-crosstab-pairwise", "overlays", 4, "*_crosstab_pairwise_*.json"},
		// The flows overlays (U30 E7) on a from x to crosstab.
		{"overlays-crosstab-symmetry", "overlays", 1, "*_crosstab_symmetry.json"},
		{"overlays-crosstab-retention", "overlays", 1, "*_crosstab_retention.json"},
		// The three flows overlays straight from a long panel (U55).
		{"overlays-crosstab-previous-state", "overlays", 1, "*_crosstab_previous_state_*.json"},
	}

	for _, cat := range categories {
		t.Run(cat.name, func(t *testing.T) {
			pattern := cat.glob
			if pattern == "" {
				pattern = "*.json"
			}
			matches, err := filepath.Glob(filepath.Join("internal", "examples", cat.dir, pattern))
			if err != nil {
				t.Fatalf("glob %s: %v", cat.dir, err)
			}
			if len(matches) < cat.minJSON {
				t.Fatalf("category %s: expected >= %d examples, found %d", cat.name, cat.minJSON, len(matches))
			}
			for _, ex := range matches {
				t.Run(filepath.Base(ex), func(t *testing.T) {
					runExample(t, p, ex, tmp)
				})
			}
		})
	}
}

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
// non-nil response with non-nil data.
func runExample(t *testing.T, p *pulse.Pulse, examplePath, dataDir string) {
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

// TestRegressionExamplesEndToEnd is a tighter contract over the
// regression-only example category: it asserts every regression example
// emits a non-empty Regressions slice with no envelope errors, catching
// drift between the example wire shape and the regression engine
// surface.
func TestRegressionExamplesEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	fs := afero.NewOsFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	for _, name := range []string{"customers", "training_data", "experiment", "subscribers", "mmm_weekly"} {
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
		if _, err := p.Import(context.Background(), job); err != nil {
			t.Fatalf("import %s: %v", name, err)
		}
	}

	matches, err := filepath.Glob(filepath.Join("internal", "examples", "regression", "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 19 {
		t.Fatalf("expected exactly 19 regression examples, found %d", len(matches))
	}
	for _, ex := range matches {
		t.Run(filepath.Base(ex), func(t *testing.T) {
			body, err := os.ReadFile(ex)
			if err != nil {
				t.Fatalf("read %s: %v", ex, err)
			}
			var req pulse.Request
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("parse %s: %v", ex, err)
			}
			if req.Cohort == nil {
				t.Fatalf("%s: missing cohort", ex)
			}
			req.Cohort.DataDir = tmp
			resp, err := p.Process(context.Background(), &req)
			if err != nil {
				t.Fatalf("Process %s: %v", ex, err)
			}
			if resp == nil {
				t.Fatalf("%s: nil response", ex)
			}
			if len(resp.Regressions) == 0 {
				t.Fatalf("%s: expected Regressions to be populated", ex)
			}
			for i, r := range resp.Regressions {
				if r == nil || r.Type == "" {
					t.Errorf("%s: regression result %d is empty (%+v)", ex, i, r)
				}
				if r != nil && r.NObs == 0 {
					t.Errorf("%s: regression result %d has zero n_obs", ex, i)
				}
				if r != nil && r.Error != nil {
					t.Errorf("%s: regression result %d failed: %s %s", ex, i, r.Error.Code, r.Error.Message)
				}
			}
		})
	}
}

func TestRegressionExamplesCount(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("internal", "examples", "regression", "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	const want = 19
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

// TestExamples_MMMDecaySweepRanksTrueDecayFirst runs
// compose/08_mmm_decay_sweep.json over the mmm_weekly fixture cohort
// (built from the same rows the acceptance suites use): predict is
// clean, ten decay slots come back ranked ascending by the fit's own
// holdout RMSE, the data's true decay (0.5) ranks first, and the
// return excludes data.
func TestExamples_MMMDecaySweepRanksTrueDecayFirst(t *testing.T) {
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
	body, err := os.ReadFile(filepath.Join("internal", "examples", "compose", "08_mmm_decay_sweep.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The instance is rooted at tmp, so the sweep body's data_dir goes.
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
	if len(out.Responses) != 10 || len(out.Ranking) != 10 {
		t.Fatalf("%d slots, %d ranked; want 10 each", len(out.Responses), len(out.Ranking))
	}
	for _, r := range out.Responses {
		if len(r.Data) != 0 {
			t.Fatalf("slot %s carries data; its return excludes it", r.Label)
		}
		if len(r.Regressions) != 1 || r.Regressions[0].Error != nil || r.Regressions[0].Holdout == nil {
			t.Fatalf("slot %s: regressions %+v, want one fit with a holdout", r.Label, r.Regressions)
		}
	}
	if out.Ranking[0].Label != "decay_0.5" {
		t.Fatalf("best slot %q, want decay_0.5; ranking %+v", out.Ranking[0].Label, out.Ranking)
	}
	for i := 1; i < len(out.Ranking); i++ {
		if !(out.Ranking[i-1].Value < out.Ranking[i].Value) {
			t.Fatalf("ranking not ascending at %d: %+v", i, out.Ranking)
		}
	}
}

// TestExamples_MMMDecayLHSSweepRanksNearTrueDecay runs
// compose/11_mmm_decay_lhs_sweep.json over the mmm_weekly fixture
// cohort: predict is clean and counts the 20 samples with the seed the
// run echoes, every slot carries its fit and holdout but no data, and
// the best-ranked slot's drawn decay lies within 0.1 of the data's true
// decay (0.5).
func TestExamples_MMMDecayLHSSweepRanksNearTrueDecay(t *testing.T) {
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
	body, err := os.ReadFile(filepath.Join("internal", "examples", "compose", "11_mmm_decay_lhs_sweep.json"))
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
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose: %v %v", err, env.Errors)
	}
	out, err := p.Compose(context.Background(), &req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	cv, ok := env.Data.(*descx.ComposeValidationResult)
	if !ok || cv.Sweep == nil || cv.Sweep.ExpandedCount != 20 || cv.Sweep.RNGSeed == nil || out.SweepSeed == nil || *cv.Sweep.RNGSeed != *out.SweepSeed {
		t.Fatalf("predict sweep %+v, run seed %v; want 20 samples and one seed", env.Data, out.SweepSeed)
	}
	if len(out.Responses) != 20 || len(out.Ranking) != 20 {
		t.Fatalf("%d slots, %d ranked; want 20 each", len(out.Responses), len(out.Ranking))
	}
	for _, r := range out.Responses {
		if len(r.Data) != 0 || len(r.Regressions) != 1 || r.Regressions[0].Error != nil || r.Regressions[0].Holdout == nil {
			t.Fatalf("slot %s: %d data rows, regressions %+v; want a fit with a holdout and no data", r.Label, len(r.Data), r.Regressions)
		}
	}
	_, v, _ := strings.Cut(out.Ranking[0].Label, "_decay=")
	if d, err := strconv.ParseFloat(v, 64); err != nil || math.Abs(d-0.5) > 0.1 {
		t.Fatalf("best slot %q (%v): want a decay within 0.1 of 0.5", out.Ranking[0].Label, err)
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

// TestExamples_SEMContributionRecipe runs
// matrices/17_sem_contribution.json over the brand_survey fixture: the
// := rows close the table (vectors.group null, vectors.label the name)
// with a delta-method SE, p and Wald interval; the planted wave-2 shift
// makes value's contribution negative and service's positive, each
// distinguishable from zero (p < 0.05, the interval excluding 0); the
// slot's Holm block adjusts every row with a p, := rows included; and
// the bootstrap slot keeps the estimates, echoes 200 replicates and
// replaces the SEs and intervals.
func TestExamples_SEMContributionRecipe(t *testing.T) {
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
	body, err := os.ReadFile(filepath.Join("internal", "examples", "matrices", "17_sem_contribution.json"))
	if err != nil {
		t.Fatal(err)
	}
	var req pulse.Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	req.Cohort.DataDir = ""
	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matrices) != 2 || resp.Matrices[0].Error != nil || resp.Matrices[1].Error != nil {
		t.Fatalf("matrices %+v", resp.Matrices)
	}
	delta, boot := resp.Matrices[0], resp.Matrices[1]
	est := delta.Auxiliary["estimates"]
	row := func(name string) (int, []float64) {
		for i, k := range est.RowKeys {
			if strings.HasPrefix(k, name+" := ") {
				return i, est.Values[i]
			}
		}
		t.Fatalf("no %s := row in %v", name, est.RowKeys)
		return 0, nil
	}
	for name, sign := range map[string]float64{"dv": -1, "ds": 1} {
		i, r := row(name)
		v, p, lo, hi := r[0], r[4], r[5], r[6]
		if !(v*sign > 0) || !(p < 0.05) || !(lo*sign > 0 && hi*sign > 0) {
			t.Errorf("%s = %.4f, p %.3g, interval [%.4f, %.4f]: want a %+g-signed contribution distinguishable from zero", name, v, p, lo, hi, sign)
		}
		if g := delta.Vectors["group"].([]any)[i]; g != nil {
			t.Errorf("%s vectors.group %v, want null", name, g)
		}
		if l := delta.Vectors["label"].([]string)[i]; l != name {
			t.Errorf("%s vectors.label %q", name, l)
		}
	}
	adj := delta.Auxiliary["p_adjusted"]
	if adj == nil {
		t.Fatal("no p_adjusted under the slot's Holm block")
	}
	var family []float64
	for _, r := range est.Values {
		if !math.IsNaN(r[4]) {
			family = append(family, r[4])
		}
	}
	di, _ := row("dv")
	if math.IsNaN(adj.Values[di][0]) || !(adj.Values[di][0] >= est.Values[di][4]) || len(family) < 40 {
		t.Errorf("dv p_adjusted %v vs raw %v over a family of %d", adj.Values[di][0], est.Values[di][4], len(family))
	}
	if boot.Scalars["bootstrap_requested"] != 200 || !(boot.Scalars["bootstrap_succeeded"] >= 190) {
		t.Errorf("bootstrap echoes %v / %v", boot.Scalars["bootstrap_requested"], boot.Scalars["bootstrap_succeeded"])
	}
	bv := boot.Auxiliary["estimates"].Values
	if bv[di][0] != est.Values[di][0] || bv[di][2] == est.Values[di][2] || bv[di][5] == est.Values[di][5] {
		t.Errorf("bootstrap dv row %v vs delta %v: want the estimate kept, the SE and interval replaced", bv[di], est.Values[di])
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
