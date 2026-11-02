package cli

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
)

// TestAPIProcessCLI_LookupTablesDir: with PULSE_LOOKUP_TABLES_DIR set,
// `pulse api process --json` runs an ATTR_FORMULA calling
// lookup("rates", "a") against the directory-loaded table and returns
// the cell value; a malformed table file fails the leaf with its own
// PULSE_LOOKUP_* code and path in the envelope.
func TestAPIProcessCLI_LookupTablesDir(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	dir := withTempDataDir(t)
	tables := filepath.Join(dir, "lookups")
	if err := os.Mkdir(tables, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tables, "rates.json"),
		[]byte(`{"description": "rate grid", "rows": {"a": 4.25, "b": 9}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_LOOKUP_TABLES_DIR", tables)

	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}}}
	var cohort bytes.Buffer
	if err := encoding.WriteHeader(&cohort); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&cohort, schema); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteFieldValue(&cohort, encoding.FieldTypeF64, math.Float64bits(1.0)); err != nil {
		t.Fatal(err)
	}
	cohortPath := filepath.Join(dir, "c.pulse")
	if err := os.WriteFile(cohortPath, cohort.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	req, err := json.Marshal(map[string]any{
		"cohort": map[string]any{"filename": cohortPath},
		"attributes": []any{map[string]any{
			"type": "ATTR_FORMULA", "field": "score", "label": "rate",
			"expression": `lookup("rates", "a")`,
		}},
		"aggregations": []any{map[string]any{"type": "AGG_SUM", "field": "rate", "label": "s"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reqPath := filepath.Join(dir, "req.json")
	if err := os.WriteFile(reqPath, req, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "process", "--json", "-r", reqPath); err != nil {
		t.Fatalf("process: %v\n%s", err, buf.String())
	}
	var env struct {
		Data struct {
			Data []map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, buf.String())
	}
	if len(env.Data.Data) != 1 || env.Data.Data[0]["s"] != 4.25 {
		t.Fatalf("lookup cell not returned: %s", buf.String())
	}

	bad := filepath.Join(tables, "broken.json")
	if err := os.WriteFile(bad, []byte(`{"a": "x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	err = runAPICLI(t, &buf, "process", "--json", "-r", reqPath)
	wantCode(t, err, perrors.PULSE_LOOKUP_CELL_NOT_NUMERIC)
	if !strings.Contains(buf.String(), "PULSE_LOOKUP_CELL_NOT_NUMERIC") || !strings.Contains(buf.String(), "broken.json") {
		t.Fatalf("envelope must carry the code and the path:\n%s", buf.String())
	}
}
