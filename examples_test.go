package pulse_test

import (
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

	cohorts := []string{"transactions", "customers", "orders", "training_data", "all_types", "experiment", "repeated_measures"}
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

	categories := []struct {
		name    string
		dir     string
		minJSON int
	}{
		{"features", "features", 10},
		{"attributes", "attributes", 6},
		{"filterers", "filterers", 6},
		{"groupers", "groupers", 7},
		{"windows", "windows", 10},
		{"aggregations", "aggregations", 5},
		{"tests", "tests", 27},
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
	// long as at least one of Tests or PostTests is populated.
	if resp.Data == nil && len(resp.Tests) == 0 && len(resp.PostTests) == 0 {
		t.Fatalf("%s: nil data and no test results", examplePath)
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
		Name         string `json:"name"`
		Type         string `json:"type"`
		Description  string `json:"description"`
		Precision    uint8  `json:"precision,omitempty"`
		Scale        uint8  `json:"scale,omitempty"`
		H3Resolution *uint8 `json:"h3_resolution,omitempty"`
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
		if ft == encoding.FieldTypeH3Cell {
			if f.H3Resolution != nil {
				field.H3Resolution = *f.H3Resolution
			} else {
				field.H3Resolution = 0xFF
			}
		}
		schema.Fields[i] = field
		offset += ft.ByteSize()
	}
	return schema, nil
}

// parseFixtureFieldType maps the schema-template strings to FieldType.
// Covers only the types used by the fixture schemas.
func parseFixtureFieldType(s string) encoding.FieldType {
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
	case "nullable_bool":
		return encoding.FieldTypeNullableBool
	case "nullable_u4":
		return encoding.FieldTypeNullableU4
	case "nullable_u8":
		return encoding.FieldTypeNullableU8
	case "nullable_u16":
		return encoding.FieldTypeNullableU16
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
	case "nullable_decimal128":
		return encoding.FieldTypeNullableDecimal128
	case "point_f64":
		return encoding.FieldTypePointF64
	case "h3_cell":
		return encoding.FieldTypeH3Cell
	}
	return encoding.FieldTypeU8
}
