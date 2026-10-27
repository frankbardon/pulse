package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/spf13/afero"
)

// TestExamples_StoryGoldens runs every story example (an example whose
// _meta declares both question and interpretation) against the shared
// fixture cohorts and compares its response, as json.MarshalIndent
// writes it, to the committed golden under
// internal/examples/testdata/stories/<name>.json — the figures the
// generated docs quote beside the story's interpretation. A missing,
// extra or differing golden fails; `go test . -run
// TestExamples_StoryGoldens -update` rewrites them. Story requests set
// return.precision (examples.StoryPrecision), so a golden is identical
// on every architecture.
func TestExamples_StoryGoldens(t *testing.T) {
	t.Parallel()

	stories := examples.Stories()
	if len(stories) == 0 {
		t.Fatal("no story examples")
	}

	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	builder, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	buildExampleFixtures(t, builder, osfs, tmp)
	// Rooted at the fixture directory, so a story's cohort resolves with
	// its data_dir cleared and no temp path reaches the golden.
	p, err := pulse.New(pulse.Options{DataDir: tmp})
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join("internal", "examples", filepath.FromSlash(examples.StoryGoldenDir))
	for _, name := range stories {
		t.Run(name, func(t *testing.T) {
			got := runStory(t, p, name)
			path := filepath.Join(dir, name+".json")
			if updateGoldens() {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing golden %s (run with -update): %v", path, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("story %s differs from its golden %s (run with -update)\n--- got\n%s\n--- want\n%s", name, path, got, want)
			}
		})
	}

	// Every committed golden belongs to a story.
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := filepath.Base(f[:len(f)-len(".json")])
		if !slices.Contains(stories, name) {
			if updateGoldens() {
				if err := os.Remove(f); err != nil {
					t.Fatal(err)
				}
				continue
			}
			t.Errorf("golden %s belongs to no story example (run with -update to remove it)", f)
		}
	}
}

// runStory executes one story example's request and returns its
// golden bytes: the response as json.MarshalIndent writes it (the
// request's return block shapes it), newline-terminated.
func runStory(t *testing.T, p *pulse.Pulse, name string) []byte {
	t.Helper()
	ex, ok := examples.Get(name)
	if !ok {
		t.Fatalf("story %s: no such example", name)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(ex.Body, &top); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var resp any
	switch {
	case top["requests"] != nil:
		var req pulse.ComposedRequest
		if err := json.Unmarshal(ex.Body, &req); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, r := range req.Requests {
			r.Cohort.DataDir = ""
		}
		for i := range req.Fits {
			req.Fits[i].Request.Cohort.DataDir = ""
		}
		r, err := p.Compose(ctx, &req)
		if err != nil {
			t.Fatalf("Compose %s: %v", name, err)
		}
		resp = r
	case top["fields"] != nil:
		var req pulse.FacetRequest
		if err := json.Unmarshal(ex.Body, &req); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if req.Cohort != nil {
			req.Cohort.DataDir = ""
		}
		r, err := p.FacetSchema(ctx, &req)
		if err != nil {
			t.Fatalf("Facet %s: %v", name, err)
		}
		resp = r
	default:
		var req pulse.Request
		if err := json.Unmarshal(ex.Body, &req); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if req.Cohort == nil {
			t.Fatalf("story %s: missing cohort", name)
		}
		req.Cohort.DataDir = ""
		r, err := p.Process(ctx, &req)
		if err != nil {
			t.Fatalf("Process %s: %v", name, err)
		}
		resp = r
	}
	out, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	return append(out, '\n')
}

// updateGoldens reads the root package's shared -update flag (declared
// by the internal-package docs_generated_current_test.go, which this
// external test package cannot name).
func updateGoldens() bool {
	f := flag.Lookup("update")
	if f == nil {
		return false
	}
	v, _ := f.Value.(flag.Getter).Get().(bool)
	return v
}
