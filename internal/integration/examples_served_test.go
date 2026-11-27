package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// servedExamplesEngine imports the experiment fixture alone into a temp
// data root and returns an instance rooted there — a server whose data
// directory holds the cohort, as a deployed one does.
func servedExamplesEngine(t *testing.T) *pulse.Pulse {
	t.Helper()
	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	builder, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := loadFixtureSchema(filepath.Join("internal", "examples", "fixtures", "schemas", "experiment.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Import(context.Background(), &pio.ImportJob{
		Source:     csv.NewReader(osfs, filepath.Join("internal", "examples", "fixtures", "experiment.csv")),
		Target:     filepath.Join(tmp, "experiment.pulse"),
		Schema:     schema,
		SampleRows: 50,
		FS:         osfs,
	}); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{DataDir: tmp})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestExamples_ServedBodyRunsAgainstTheDataRoot: the body ExampleGet
// serves (pulse_examples_get, pulse examples show) runs as is against a
// server rooted at its data directory — the repo-relative fixture
// data_dir the checked-in file carries does not, and the facet
// population path loses its fixture prefix too.
func TestExamples_ServedBodyRunsAgainstTheDataRoot(t *testing.T) {
	t.Parallel()

	p := servedExamplesEngine(t)
	ctx := context.Background()

	served, ok := p.ExampleGet("t_test_one_sample")
	if !ok {
		t.Fatal("t_test_one_sample not served")
	}
	var req pulse.Request
	if err := json.Unmarshal(served.Body, &req); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Process(ctx, &req); err != nil {
		t.Fatalf("served body does not run against the data root: %v", err)
	}

	// The checked-in body names the repo-relative fixture directory, so
	// against the same server it is the SERVICE_RESOURCE the fix removes.
	raw, _ := examples.Get("t_test_one_sample")
	var rawReq pulse.Request
	if err := json.Unmarshal(raw.Body, &rawReq); err != nil {
		t.Fatal(err)
	}
	var ce *perrors.CodedError
	if _, err := p.Process(ctx, &rawReq); !stderrors.As(err, &ce) || ce.Code != perrors.SERVICE_RESOURCE {
		t.Fatalf("raw body err = %v, want SERVICE_RESOURCE (the fixture data_dir does not resolve)", err)
	}

	facet, ok := p.ExampleGet("facet-index-vs-pop")
	if !ok {
		t.Fatal("facet-index-vs-pop not served")
	}
	if strings.Contains(string(facet.Body), examples.FixtureDataDir) {
		t.Fatalf("served facet body still names %s: %s", examples.FixtureDataDir, facet.Body)
	}
	var freq types.FacetRequest
	if err := json.Unmarshal(facet.Body, &freq); err != nil {
		t.Fatal(err)
	}
	if _, err := p.FacetSchema(ctx, &freq); err != nil {
		t.Fatalf("served facet body does not run against the data root: %v", err)
	}
}
