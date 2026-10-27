package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unicode"
)

// assertSnakeData decodes a --json envelope and fails on any object key
// under `data` carrying an upper-case letter. Field NAMES taken from the
// user's source data are values, never keys, so the walk sees only the
// report's own vocabulary.
func assertSnakeData(t *testing.T, leaf, out string) map[string]any {
	t.Helper()
	var env struct {
		Data   map[string]any    `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%s: decode envelope: %v\n%s", leaf, err, out)
	}
	if len(env.Errors) > 0 || env.Data == nil {
		t.Fatalf("%s: unexpected error envelope:\n%s", leaf, out)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				for _, r := range k {
					if unicode.IsUpper(r) {
						t.Errorf("%s: data key %s.%s is not snake_case", leaf, path, k)
						break
					}
				}
				walk(path+"."+k, child)
			}
		case []any:
			for _, child := range x {
				walk(path+"[]", child)
			}
		}
	}
	walk("data", env.Data)
	return env.Data
}

// TestCliJSONCasing_ImportExportConvertShard: every import, export,
// convert and shard leaf's --json `data` speaks snake_case with field
// types as names (`"f64"`, never the type byte) — the managed-import
// result's casing, fixed in place under format_version "1.1".
func TestCliJSONCasing_ImportExportConvertShard(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	csvPath := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(csvPath, []byte("a,b,c\n1,x,2.5\n2,y,3.5\n3,x,\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cohort := filepath.Join(dir, "in.pulse")

	out, err := runApp(t, "import", "csv", "--input", csvPath, "--output", cohort, "--json")
	if err != nil {
		t.Fatalf("import csv: %v\n%s", err, out)
	}
	data := assertSnakeData(t, "import csv", out)
	if data["rows_imported"] != float64(3) {
		t.Errorf("import csv: rows_imported = %v, want 3\n%s", data["rows_imported"], out)
	}
	fields := data["schema"].(map[string]any)["fields"].([]any)
	wantTypes := map[string]string{"a": "u4", "b": "categorical_u8", "c": "f64"}
	for _, f := range fields {
		m := f.(map[string]any)
		if want := wantTypes[m["name"].(string)]; m["type"] != want {
			t.Errorf("import csv: field %v type = %#v, want %q", m["name"], m["type"], want)
		}
	}

	out, err = runApp(t, "import", "predict", "--input", csvPath, "--elide-constants", "--json")
	if err != nil {
		t.Fatalf("import predict: %v\n%s", err, out)
	}
	if data = assertSnakeData(t, "import predict", out); data["estimated_rows"] != float64(3) {
		t.Errorf("import predict: estimated_rows = %v, want 3\n%s", data["estimated_rows"], out)
	}

	out, err = runApp(t, "export", "csv", "--input", cohort, "--output", filepath.Join(dir, "out.csv"), "--json")
	if err != nil {
		t.Fatalf("export csv: %v\n%s", err, out)
	}
	if data = assertSnakeData(t, "export csv", out); data["rows_exported"] != float64(3) {
		t.Errorf("export csv: rows_exported = %v, want 3\n%s", data["rows_exported"], out)
	}

	out, err = runApp(t, "export", "predict", "--input", cohort, "--format", "csv", "--json")
	if err != nil {
		t.Fatalf("export predict: %v\n%s", err, out)
	}
	if data = assertSnakeData(t, "export predict", out); data["estimated_rows"] != float64(3) {
		t.Errorf("export predict: estimated_rows = %v, want 3\n%s", data["estimated_rows"], out)
	}

	out, err = runApp(t, "convert", "--json", csvPath, filepath.Join(dir, "out.ndjson"))
	if err != nil {
		t.Fatalf("convert: %v\n%s", err, out)
	}
	if data = assertSnakeData(t, "convert", out); data["rows_converted"] != float64(3) {
		t.Errorf("convert: rows_converted = %v, want 3\n%s", data["rows_converted"], out)
	}

	out, err = runApp(t, "convert", "predict", "--json", csvPath, filepath.Join(dir, "out2.ndjson"))
	if err != nil {
		t.Fatalf("convert predict: %v\n%s", err, out)
	}
	assertSnakeData(t, "convert predict", out)

	first := createShardPulseFile(t, dir, "shard_a", 6)
	second := createShardPulseFile(t, dir, "shard_b", 4)
	archive := filepath.Join(dir, "arch.pulse")
	out, err = runApp(t, "shard", "create", "-i", first, archive, "--json")
	if err != nil {
		t.Fatalf("shard create: %v\n%s", err, out)
	}
	assertSnakeData(t, "shard create", out)
	out, err = runApp(t, "shard", "add", archive, second, "--json")
	if err != nil {
		t.Fatalf("shard add: %v\n%s", err, out)
	}
	assertSnakeData(t, "shard add", out)
	out, err = runApp(t, "shard", "list", archive, "--json")
	if err != nil {
		t.Fatalf("shard list: %v\n%s", err, out)
	}
	data = assertSnakeData(t, "shard list", out)
	shards := data["shards"].([]any)
	if len(shards) != 2 || shards[0].(map[string]any)["record_count"] != float64(6) {
		t.Errorf("shard list: shards = %v, want two {filename, record_count} entries", shards)
	}
	out, err = runApp(t, "shard", "verify", archive, "--json")
	if err != nil {
		t.Fatalf("shard verify: %v\n%s", err, out)
	}
	assertSnakeData(t, "shard verify", out)
}
