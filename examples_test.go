package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
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
	cohorts := []string{"transactions", "customers", "orders", "training_data", "all_types", "experiment", "repeated_measures", "card_issuers", "tier_migrations", "survey"}
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
	for _, name := range []string{"customers", "training_data", "experiment"} {
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
	if len(matches) != 17 {
		t.Fatalf("expected exactly 17 regression examples, found %d", len(matches))
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
			}
		})
	}
}

func TestRegressionExamplesCount(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("internal", "examples", "regression", "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	const want = 17
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
