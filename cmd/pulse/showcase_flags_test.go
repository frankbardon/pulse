package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestShowcase_CLIStrictFlagTests: the committed packed_bool showcase
// examples (U45, TODO #317) run through `pulse api process --strict
// --json` against the committed subscribers fixture, imported by
// `pulse import csv`: exit 0, no error or warning, and the A/B z equals
// the hand-computed pooled two-proportion z (6/24 control vs 9/24
// variant conversions). The library suite (showcase_flags_money_test.go
// at the repo root) pins every figure of all four shapes.
func TestShowcase_CLIStrictFlagTests(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PULSE_DATA_DIR", dir)
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	fixtures := filepath.Join("..", "..", "internal", "examples", "fixtures")
	cohort := filepath.Join(dir, "subscribers.pulse")
	if out, err := runApp(t, "import", "csv", "--input", filepath.Join(fixtures, "subscribers.csv"),
		"--output", cohort, "--schema", filepath.Join(fixtures, "schemas", "subscribers.json"), "--strict"); err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}

	pool := 15.0 / 48
	wantZ := (6.0/24 - 9.0/24) / math.Sqrt(pool*(1-pool)*(1.0/24+1.0/24))

	for _, ex := range []string{"35_prop_z_on_flag.json", "36_chisq_on_flag.json"} {
		t.Run(ex, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", "internal", "examples", "tests", ex))
			if err != nil {
				t.Fatal(err)
			}
			var req map[string]any
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatal(err)
			}
			delete(req, "_meta")
			req["cohort"] = map[string]any{"filename": cohort}
			reqPath := filepath.Join(dir, ex)
			b, _ := json.Marshal(req)
			if err := os.WriteFile(reqPath, b, 0o644); err != nil {
				t.Fatal(err)
			}
			status, out, errOut := runExit(t, "api", "process", "--strict", "--json", "-r", reqPath)
			env := decodeExitEnvelope(t, ex, out)
			if status != 0 || env == nil || len(env.Errors) != 0 || len(env.Warnings) != 0 {
				t.Fatalf("exit %d, want a clean strict run\n%s\n%s", status, out, errOut)
			}
			var data struct {
				Tests []struct {
					Statistic float64 `json:"statistic"`
				} `json:"tests"`
			}
			raw, _ := json.Marshal(env.Data)
			if err := json.Unmarshal(raw, &data); err != nil || len(data.Tests) != 1 {
				t.Fatalf("data %s: %v", raw, err)
			}
			if ex == "35_prop_z_on_flag.json" && math.Abs(data.Tests[0].Statistic-wantZ) > 1e-12 {
				t.Errorf("z = %.15g, want %.15g", data.Tests[0].Statistic, wantZ)
			}
		})
	}
}
