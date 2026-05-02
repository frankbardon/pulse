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

	cohorts := []string{"transactions", "customers", "orders", "training_data"}
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
		{"filterers", "filterers", 4},
		{"groupers", "groupers", 5},
		{"windows", "windows", 10},
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
// temp .pulse dir, and dispatches via pulse.Process.
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

	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process %s: %v", examplePath, err)
	}
	if resp == nil {
		t.Fatalf("%s: nil response", examplePath)
	}
	if resp.Data == nil {
		t.Fatalf("%s: nil data", examplePath)
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
	}
	var fields []fieldDef
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	schema := &encoding.Schema{Fields: make([]encoding.Field, len(fields))}
	offset := 0
	for i, f := range fields {
		ft := parseFixtureFieldType(f.Type)
		schema.Fields[i] = encoding.Field{
			Name:         f.Name,
			Type:         ft,
			ByteOffset:   offset,
			CsvColumnIdx: i,
			Description:  f.Description,
		}
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
	case "date":
		return encoding.FieldTypeDate
	case "categorical_u8":
		return encoding.FieldTypeCategoricalU8
	case "categorical_u16":
		return encoding.FieldTypeCategoricalU16
	case "categorical_u32":
		return encoding.FieldTypeCategoricalU32
	}
	return encoding.FieldTypeU8
}
