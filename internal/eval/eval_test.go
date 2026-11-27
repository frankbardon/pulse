package eval

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	idesc "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// fixtureDir is the example fixture directory, relative to this package.
var fixtureDir = filepath.Join("..", "examples", "fixtures")

func loadTasks(t *testing.T) []*Task {
	t.Helper()
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 {
		t.Fatal("no eval tasks")
	}
	return tasks
}

// TestEvalReferences runs every task's reference submission on a fresh
// fixture data directory and requires it to pass its own assertions:
// the answer key is itself a correct answer.
//
// Read-only references share one data directory; a write-side one gets
// its own.
func TestEvalReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tasks := loadTasks(t)
	var readers []*Task
	for _, task := range tasks {
		if !task.Reference.Writes() {
			readers = append(readers, task)
		}
	}
	shared, err := NewEnv(ctx, fixtureDir, t.TempDir(), readers...)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		t.Run(task.ID, func(t *testing.T) {
			var r Report
			if task.Reference.Writes() {
				r = Score(ctx, fixtureDir, t.TempDir(), task, task.Reference)
			} else {
				r = ScoreIn(ctx, shared, task, task.Reference)
			}
			if !r.Pass {
				t.Fatalf("reference fails its own assertions:\n%s", strings.Join(r.Failures, "\n"))
			}
		})
	}
}

// TestEvalIntentCoverage requires at least one task per intent in the
// taxonomy (descriptor.IntentIDs), so a new intent needs a task.
func TestEvalIntentCoverage(t *testing.T) {
	t.Parallel()
	have := map[string]int{}
	for _, task := range loadTasks(t) {
		have[task.Intent]++
	}
	for _, id := range idesc.IntentIDs() {
		if have[id] == 0 {
			t.Errorf("intent %q has no eval task under internal/eval/tasks/", id)
		}
	}
}

// TestEvalTasksWellFormed holds every task to real fixtures and real
// operator names, so a typo cannot make an assertion vacuous.
func TestEvalTasksWellFormed(t *testing.T) {
	t.Parallel()
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(p.Manifest(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	osfs := afero.NewOsFs()
	for _, task := range loadTasks(t) {
		for _, c := range task.Cohorts {
			for _, f := range []string{c + ".csv", filepath.Join("schemas", c+".json")} {
				if ok, _ := afero.Exists(osfs, filepath.Join(fixtureDir, f)); !ok {
					t.Errorf("%s: cohort %s has no fixture %s", task.ID, c, f)
				}
			}
		}
		var ops func([]Assertion)
		ops = func(as []Assertion) {
			for _, a := range as {
				names := append(append([]string(nil), a.AnyOf...), a.Types...)
				if a.Metric != nil {
					names = append(names, a.Metric.Types...)
				}
				for _, op := range names {
					if !strings.Contains(string(manifest), `"`+op+`"`) {
						t.Errorf("%s: operator %s is not in the manifest", task.ID, op)
					}
				}
				ops(a.Assertions)
			}
		}
		ops(task.Assertions)
	}
}

// operatorWord matches an operator-shaped token (AGG_AVERAGE, TEST_T).
var operatorWord = regexp.MustCompile(`\b[A-Z]+_[A-Z0-9_]+\b`)

// TestEvalNoAnswerKeyLeak keeps the answer key out of everything an
// agent can read: no task question appears in the served example
// library or the skill pack, no reference request equals a served
// example body, no question names an operator, and no package outside
// internal/eval but the contributor tools imports it (so it is never
// compiled into a shipped binary).
func TestEvalNoAnswerKeyLeak(t *testing.T) {
	t.Parallel()
	var served []string
	bodies := map[string]string{}
	for _, ex := range examples.All() {
		served = append(served, ex.Question, ex.Description, ex.Interpretation, string(ex.Body))
		bodies[canonicalRequest(t, ex.Body)] = ex.Name
	}
	for _, name := range skills.Names() {
		body, _ := skills.Get(name)
		served = append(served, body)
	}
	corpus := normalize(strings.Join(served, "\n"))
	for _, task := range loadTasks(t) {
		if q := normalize(task.Question); strings.Contains(corpus, q) {
			t.Errorf("%s: question appears in a served example or skill", task.ID)
		}
		if w := operatorWord.FindString(task.Question); w != "" {
			t.Errorf("%s: question names %s; ask it the way a non-statistician would", task.ID, w)
		}
		if name, ok := bodies[canonicalRequest(t, task.Reference.Arguments)]; ok {
			t.Errorf("%s: reference request equals served example %s", task.ID, name)
		}
	}

	const self = "github.com/frankbardon/pulse/internal/eval"
	osfs := afero.NewOsFs()
	root := filepath.Join("..", "..")
	err := afero.Walk(osfs, root, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "book", "eval":
				return filepath.SkipDir
			}
			return nil
		}
		// Contributor tools (internal/tools/, e.g. evalscore) are never
		// shipped, so they may import it.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.Contains(filepath.ToSlash(path), "/internal/tools/") {
			return nil
		}
		src, err := afero.ReadFile(osfs, path)
		if err != nil {
			return err
		}
		if strings.Contains(string(src), `"`+self+`"`) {
			t.Errorf("%s imports %s: the answer key must stay out of every served binary", path, self)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// normalize lowercases s and collapses whitespace.
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// canonicalRequest is a request body with the keys that never change
// its meaning dropped (data_dir, label, return), re-encoded with sorted
// keys.
func canonicalRequest(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	for _, k := range []string{"data_dir", "label", "return"} {
		v = dropKey(v, k)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestEvalCompositionAnswers pins the composition task's scoring both
// ways: a count-by-region request and a crosstab share answer pass, a
// request answering another question (average income by region) fails,
// and so do shares attached to the wrong regions.
func TestEvalCompositionAnswers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var task *Task
	for _, tk := range loadTasks(t) {
		if tk.ID == "composition_region_share" {
			task = tk
		}
	}
	if task == nil {
		t.Fatal("no composition_region_share task")
	}
	env, err := NewEnv(ctx, fixtureDir, t.TempDir(), task)
	if err != nil {
		t.Fatal(err)
	}
	const byRegion = `"cohort":{"filename":"customers.pulse"},"groups":[{"type":"GROUP_CATEGORY","field":"region"}]`
	for _, c := range []struct {
		name, args string
		pass       bool
	}{
		{"count by region", `{` + byRegion + `,"aggregations":[{"type":"AGG_COUNT","field":"id"}]}`, true},
		{"crosstab share", `{"cohort":{"filename":"customers.pulse"},"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"region"}],"cell":{"type":"AGG_COUNT","field":"id"},"normalize":"total"}}`, true},
		{"average income by region", `{` + byRegion + `,"aggregations":[{"type":"AGG_AVERAGE","field":"income"}]}`, false},
	} {
		r := ScoreIn(ctx, env, task, Submission{Tool: "pulse_process", Arguments: json.RawMessage(c.args)})
		if r.Pass != c.pass {
			t.Errorf("%s: pass=%v, want %v (%v)", c.name, r.Pass, c.pass, r.Failures)
		}
	}
	// The right share values on the wrong regions are not an answer.
	wrong := outcome(t, `{}`, `{"data":[{"region":"south","share":0.26},{"region":"north","share":0.28},
		{"region":"east","share":0.205},{"region":"west","share":0.255}]}`)
	if fails := Check(task, wrong); len(fails) == 0 {
		t.Error("shares on the wrong regions pass the composition task")
	}
}
