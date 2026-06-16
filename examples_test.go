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
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
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

	cohorts := []string{"transactions", "customers", "orders", "training_data", "all_types", "experiment", "repeated_measures", "card_issuers"}
	for _, name := range cohorts {
		csvPath := filepath.Join("examples", "fixtures", name+".csv")
		schemaPath := filepath.Join("examples", "fixtures", "schemas", name+".json")
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

	categories := []struct {
		name    string
		dir     string
		minJSON int
	}{
		{"features", "features", 10},
		{"attributes", "attributes", 6},
		{"filterers", "filterers", 4},
		{"groupers", "groupers", 5},
		{"windows", "windows", 10},
		{"aggregations", "aggregations", 4},
		{"tests", "tests", 27},
		{"regression", "regression", 16},
		{"crosstab", "crosstab", 13},
	}

	for _, cat := range categories {
		t.Run(cat.name, func(t *testing.T) {
			matches, err := filepath.Glob(filepath.Join("examples", cat.dir, "*.json"))
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
	if resp.Data == nil && len(resp.Tests) == 0 && len(resp.PostTests) == 0 && len(resp.Regressions) == 0 && resp.Crosstab == nil {
		t.Fatalf("%s: nil data and no test/regression/crosstab results", examplePath)
	}
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
	for _, name := range []string{"customers", "training_data"} {
		csvPath := filepath.Join("examples", "fixtures", name+".csv")
		schemaPath := filepath.Join("examples", "fixtures", "schemas", name+".json")
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

	matches, err := filepath.Glob(filepath.Join("examples", "regression", "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 16 {
		t.Fatalf("expected exactly 16 regression examples, found %d", len(matches))
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
	matches, err := filepath.Glob(filepath.Join("examples", "regression", "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	const want = 16
	if len(matches) != want {
		t.Errorf("want %d regression examples under examples/regression/, found %d: %v", want, len(matches), matches)
	}
}

// loadFixtureSchema mirrors the loadSchemaFromFile helper in
// internal/cli/import.go. Re-implemented here because that helper is
// unexported and we want the test to live with the examples.
func loadFixtureSchema(path string) (*encoding.Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	type fieldDef struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
		Precision   uint8  `json:"precision,omitempty"`
		Scale       uint8  `json:"scale,omitempty"`
	}
	var fields []fieldDef
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	schema := &encoding.Schema{Fields: make([]encoding.Field, len(fields))}
	offset := 0
	for i, f := range fields {
		ft := parseFixtureFieldType(f.Type)
		field := encoding.Field{
			Name:         f.Name,
			Type:         ft,
			ByteOffset:   offset,
			CsvColumnIdx: i,
			Description:  f.Description,
		}
		if ft.IsDecimal() {
			field.Precision = f.Precision
			field.Scale = f.Scale
		}
		schema.Fields[i] = field
		offset += ft.ByteSize()
	}
	return schema, nil
}

// parseFixtureFieldType maps the schema-template strings to FieldType.
// Delegates to encoding.ParseFieldType so set_* and any future type
// strings work without a per-test allowlist; the explicit switch is
// retained as a fallback for legacy strings the canonical parser may
// not know about.
func parseFixtureFieldType(s string) encoding.FieldType {
	if ft, ok := encoding.ParseFieldType(s); ok {
		return ft
	}
	switch s {
	case "u8":
		return encoding.FieldTypeU8
	case "u16":
		return encoding.FieldTypeU16
	case "u32":
		return encoding.FieldTypeU32
	case "u64":
		return encoding.FieldTypeU64
	case "f32":
		return encoding.FieldTypeF32
	case "f64":
		return encoding.FieldTypeF64
	case "u4":
		return encoding.FieldTypeU4
	case "date":
		return encoding.FieldTypeDate
	case "packed_bool":
		return encoding.FieldTypePackedBool
	case "categorical_u8":
		return encoding.FieldTypeCategoricalU8
	case "categorical_u16":
		return encoding.FieldTypeCategoricalU16
	case "categorical_u32":
		return encoding.FieldTypeCategoricalU32
	case "decimal128":
		return encoding.FieldTypeDecimal128
	}
	return encoding.FieldTypeU8
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
	if err := encoding.ReadHeader(r); err != nil {
		return err
	}
	schema, err := encoding.ReadSchema(r)
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
	return p.CreateShardArchive(context.Background(), archivePath,
		[]string{wave1Path, wave2Path})
}
