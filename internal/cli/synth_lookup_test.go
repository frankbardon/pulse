package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSynthFromSchemaCLI_HonoursLookupTablesDirEnv: `pulse synth
// from-schema` builds its instance through pulse.New, so a rule calling
// lookup() reads PULSE_LOOKUP_TABLES_DIR; without it the same spec is
// refused as an unknown table.
func TestSynthFromSchemaCLI_HonoursLookupTablesDirEnv(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	dir := t.TempDir()
	tables := filepath.Join(dir, "lookups")
	if err := os.MkdirAll(tables, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tables, "rates.json"), []byte(`{"east": 0.25, "west": 0.75}`), 0o644); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(dir, "spec.json")
	spec := `{"row_count": 20, "fields": [
		{"name": "region", "type": "categorical_u8", "distribution": "weighted_categorical", "params": {"values": ["east", "west"]}},
		{"name": "rate", "type": "f64", "distribution": "uniform", "params": {"min": 0, "max": 1}}
	], "rules": [{"set_expr": {"rate": "lookup(\"rates\", region)"}}]}`
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pulse")

	t.Setenv("PULSE_LOOKUP_TABLES_DIR", "")
	var buf bytes.Buffer
	if err := runSynthCLI(t, &buf, "from-schema", "--spec", specPath, "--output", out, "--json"); err == nil || !bytes.Contains(buf.Bytes(), []byte("PULSE_LOOKUP_TABLE_UNKNOWN")) {
		t.Fatalf("without the env var: err = %v, out = %s; want PULSE_LOOKUP_TABLE_UNKNOWN", err, buf.String())
	}

	t.Setenv("PULSE_LOOKUP_TABLES_DIR", tables)
	buf.Reset()
	if err := runSynthCLI(t, &buf, "from-schema", "--spec", specPath, "--output", out, "--json"); err != nil {
		t.Fatalf("with PULSE_LOOKUP_TABLES_DIR: %v (%s)", err, buf.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("no output written: %v", err)
	}
}
