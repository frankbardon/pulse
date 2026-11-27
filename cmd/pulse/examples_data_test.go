package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// TestExamplesData_WritesAndRefuses: `pulse examples data --out DIR
// --json` writes the fixture cohorts and lists them in the envelope; a
// second run into the same directory is the SERVICE_VALIDATION
// target_exists envelope; --overwrite replaces the files.
func TestExamplesData_WritesAndRefuses(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "data")
	out, err := runApp(t, "examples", "data", "--out", dir, "--json")
	if err != nil {
		t.Fatalf("examples data: %v\n%s", err, out)
	}
	var env struct {
		Data struct {
			Dir     string `json:"dir"`
			Cohorts []struct {
				File    string `json:"file"`
				Records int    `json:"records"`
				From    string `json:"from"`
			} `json:"cohorts"`
			Files []string `json:"files"`
		} `json:"data"`
		Errors   []*descriptor.EnvelopeEntry `json:"errors"`
		Warnings []*descriptor.EnvelopeEntry `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, out)
	}
	if env.Data.Dir != dir || len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Fatalf("envelope dir %q errors %v warnings %v", env.Data.Dir, env.Errors, env.Warnings)
	}
	from := map[string]string{}
	for _, c := range env.Data.Cohorts {
		from[c.File] = c.From
		if _, err := os.Stat(filepath.Join(dir, c.File)); err != nil || c.Records == 0 {
			t.Errorf("%s: %d records, stat %v", c.File, c.Records, err)
		}
	}
	for file, want := range map[string]string{"experiment.pulse": "csv", "transactions_sharded.pulse": "shards", "customers_twin.pulse": "synth"} {
		if from[file] != want {
			t.Errorf("%s: from %q, want %q", file, from[file], want)
		}
	}

	out, err = runApp(t, "examples", "data", "--out", dir, "--json")
	if err == nil {
		t.Fatalf("second run into a written directory succeeded:\n%s", out)
	}
	var refused descriptor.Envelope
	if jerr := json.Unmarshal([]byte(out), &refused); jerr != nil || len(refused.Errors) != 1 ||
		refused.Errors[0].Code != "SERVICE_VALIDATION" || refused.Errors[0].Details["reason"] != "target_exists" {
		t.Fatalf("refusal envelope (%v):\n%s", jerr, out)
	}
	if out, err := runApp(t, "examples", "data", "--out", dir, "--overwrite", "--json"); err != nil {
		t.Fatalf("--overwrite: %v\n%s", err, out)
	}
}
