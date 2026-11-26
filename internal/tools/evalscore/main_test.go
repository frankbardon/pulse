package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/eval"
)

var fixtures = filepath.Join("..", "..", "examples", "fixtures")

func writeRun(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := afero.WriteFile(afero.NewOsFs(), filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestRun_ReportsPerTask: one PASS line for the right answer, a FAIL
// line with its reason for the wrong one and for every unanswered task,
// a total, and ok false; -json carries the same verdicts.
func TestRun_ReportsPerTask(t *testing.T) {
	t.Parallel()
	dir := writeRun(t, map[string]string{
		"lookup_experiment_unit.json": `{"tool":"pulse_lookup","arguments":{"cohort":{"filename":"experiment.pulse"},"field":"id","value":"42"}}`,
		"lookup_experiment_unit.md":   "summary",
		// The wrong unit.
		"compare_variant_revenue.json": `{"tool":"pulse_process","arguments":{"cohort":{"filename":"experiment.pulse"},"aggregations":[{"type":"AGG_AVERAGE","field":"revenue"}]}}`,
	})
	var out bytes.Buffer
	ok, err := run(context.Background(), &out, fixtures, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if ok {
		t.Fatalf("a run with failures reported ok:\n%s", text)
	}
	for _, want := range []string{"PASS lookup_experiment_unit\n", "FAIL compare_variant_revenue\n", "uses none of", "FAIL describe_income_by_region\n", "unanswered", "1/"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	out.Reset()
	if _, err := run(context.Background(), &out, fixtures, dir, true); err != nil {
		t.Fatal(err)
	}
	var reports []struct {
		Task string `json:"task"`
		Pass bool   `json:"pass"`
	}
	if err := json.Unmarshal(out.Bytes(), &reports); err != nil {
		t.Fatalf("-json output: %v\n%s", err, out.String())
	}
	for _, r := range reports {
		if r.Pass != (r.Task == "lookup_experiment_unit") {
			t.Errorf("%s: pass = %v", r.Task, r.Pass)
		}
	}
}

// TestRun_AllPass: a run answering every task with its reference
// reports every task PASS and ok; an empty run (all unanswered) does
// not.
func TestRun_AllPass(t *testing.T) {
	t.Parallel()
	tasks, err := eval.Tasks()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, task := range tasks {
		b, err := json.Marshal(task.Reference)
		if err != nil {
			t.Fatal(err)
		}
		files[task.ID+".json"] = string(b)
	}
	var out bytes.Buffer
	ok, err := run(context.Background(), &out, fixtures, writeRun(t, files), false)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || strings.Contains(out.String(), "FAIL") {
		t.Fatalf("every reference answered, yet not ok:\n%s", out.String())
	}

	out.Reset()
	if ok, err := run(context.Background(), &out, fixtures, writeRun(t, nil), false); err != nil || ok {
		t.Fatalf("an empty run reported ok=%v err=%v:\n%s", ok, err, out.String())
	}
}
