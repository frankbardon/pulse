package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestAPIStrict_VectorMetricUnsuited verifies the CLI --strict plumbing
// the vector-metrics skill promises: an explicit metric that fits its
// vector's kind poorly is a warning from `pulse api predict --json` and
// in `pulse api process --json`'s data.warnings, and with --strict a
// predict error and a refused run whose envelope carries
// PULSE_VECTOR_METRIC_UNSUITED as its own code.
func TestAPIStrict_VectorMetricUnsuited(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	csv := "a,b,c\n"
	for i := 0; i < 30; i++ {
		csv += strconv.Itoa(1+i%5) + "," + strconv.Itoa(1+(i*3)%5) + "," + strconv.Itoa(i%3) + "\n"
	}
	cohort := seedCohortViaImport(t, dir, "k.csv", csv)
	b, err := json.Marshal(map[string]any{
		"cohort":  map[string]any{"filename": cohort},
		"vectors": []any{map[string]any{"name": "v", "fields": []string{"a", "b"}, "kind": "scale", "scale_min": 1, "scale_max": 5}},
		"matrices": []any{map[string]any{"name": "d", "type": "MAT_DISTANCE", "vector": "v",
			"params": map[string]any{"between": map[string]any{"type": "GROUP_RANGE", "field": "c", "interval": 1}, "metric": "cosine"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "req.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	type entry struct {
		Code string `json:"code"`
	}
	run := func(args ...string) (data struct {
		Warnings []entry `json:"warnings"`
	}, errs, warns []entry) {
		t.Helper()
		var buf bytes.Buffer
		root := APICommand()
		root.Writer = &buf
		_ = root.Run(context.Background(), append(args, "--json", "-r", path))
		var env struct {
			Data     json.RawMessage `json:"data"`
			Errors   []entry         `json:"errors"`
			Warnings []entry         `json:"warnings"`
		}
		if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
			t.Fatalf("%v: decode envelope: %v\n%s", args, err, buf.String())
		}
		if len(env.Data) > 0 && string(env.Data) != "null" {
			_ = json.Unmarshal(env.Data, &data)
		}
		return data, env.Errors, env.Warnings
	}
	has := func(es []entry) bool {
		for _, e := range es {
			if e.Code == "PULSE_VECTOR_METRIC_UNSUITED" {
				return true
			}
		}
		return false
	}

	if _, errs, warns := run("api", "predict"); has(errs) || !has(warns) {
		t.Errorf("predict: errors %v warnings %v, want the warning", errs, warns)
	}
	if _, errs, warns := run("api", "predict", "--strict"); !has(errs) || has(warns) {
		t.Errorf("predict --strict: errors %v warnings %v, want the error", errs, warns)
	}
	if data, errs, _ := run("api", "process"); len(errs) != 0 || !has(data.Warnings) {
		t.Errorf("process: errors %v data.warnings %v, want the warning", errs, data.Warnings)
	}
	if _, errs, _ := run("api", "process", "--strict"); len(errs) == 0 || errs[0].Code != "PULSE_VECTOR_METRIC_UNSUITED" {
		t.Errorf("process --strict: errors %v, want PULSE_VECTOR_METRIC_UNSUITED", errs)
	}
}
