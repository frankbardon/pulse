package eval

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// writeRun writes one submission file per entry into a fresh run
// directory and returns it.
func writeRun(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	fs := afero.NewOsFs()
	for name, body := range files {
		if err := afero.WriteFile(fs, filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func referenceJSON(t *testing.T, id string) string {
	t.Helper()
	for _, task := range loadTasks(t) {
		if task.ID == id {
			b, err := json.Marshal(task.Reference)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
	}
	t.Fatalf("no task %s", id)
	return ""
}

// TestScoreRunDir_PassAndFail scores a run holding a passing read-only
// submission, a passing write-side one, a wrong answer, a malformed
// file and a transcript note: each task gets exactly one report, the
// unanswered ones fail as unanswered.
func TestScoreRunDir_PassAndFail(t *testing.T) {
	t.Parallel()
	run := writeRun(t, map[string]string{
		"describe_income_by_region.json": referenceJSON(t, "describe_income_by_region"),
		"prepare_import_intake.json":     referenceJSON(t, "prepare_import_intake"),
		// Averages revenue but never tests the gap.
		"compare_variant_revenue.json": `{"tool":"pulse_process","arguments":{
			"cohort":{"filename":"experiment.pulse","data_dir":".data"},
			"groups":[{"type":"GROUP_CATEGORY","field":"treatment"}],
			"aggregations":[{"type":"AGG_AVERAGE","field":"revenue"}]}}`,
		"flows_bronze_to_silver.json":  `{"arguments":{}}`,
		"describe_income_by_region.md": "transcript summary, ignored",
	})
	reports, err := ScoreRunDir(context.Background(), fixtureDir, t.TempDir(), run)
	if err != nil {
		t.Fatal(err)
	}
	tasks := loadTasks(t)
	if len(reports) != len(tasks) {
		t.Fatalf("%d reports for %d tasks", len(reports), len(tasks))
	}
	want := map[string]string{
		"describe_income_by_region": "",
		"prepare_import_intake":     "",
		"compare_variant_revenue":   "uses none of",
		"flows_bronze_to_silver":    "submission: no tool",
	}
	for _, r := range reports {
		frag, named := want[r.Task]
		switch {
		case named && frag == "":
			if !r.Pass {
				t.Errorf("%s: want pass, got %v", r.Task, r.Failures)
			}
		case named:
			if r.Pass || !strings.Contains(strings.Join(r.Failures, "\n"), frag) {
				t.Errorf("%s: want a failure naming %q, got pass=%v %v", r.Task, frag, r.Pass, r.Failures)
			}
		default:
			if r.Pass || len(r.Failures) != 1 || !strings.HasPrefix(r.Failures[0], "unanswered") {
				t.Errorf("%s: want unanswered, got pass=%v %v", r.Task, r.Pass, r.Failures)
			}
		}
	}

	stray := writeRun(t, map[string]string{"no_such_task.json": `{}`})
	if _, err := ScoreRunDir(context.Background(), fixtureDir, t.TempDir(), stray); err == nil {
		t.Fatal("a file naming no task must be an error")
	}
}
