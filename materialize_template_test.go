package pulse

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
)

// reshapeRecipe is the stored reshape request the docs teach: the cohort
// is a template variable, everything else (columns, rename, bucket,
// container) is fixed once. Rendered per cohort, it materializes through
// the same validated path a hand-written request takes.
const reshapeRecipe = `{
  "description": "Hand-off reshape of a sales cohort",
  "target": "request",
  "variables": [
    {"name": "cohort", "type": "string", "required": true}
  ],
  "body": {
    "cohort": {"filename": {"$var": "cohort"}},
    "output": {
      "columns": [
        {"from": "id", "as": "key"},
        {"from": "region"},
        {"from": "spend", "as": "spend_band", "bucket": {"ranges": [
          {"label": "low", "end": 15},
          {"label": "high", "start": 15}
        ]}}
      ],
      "elide_constants": true
    }
  }
}`

// TestMaterializeTemplate_RendersAndMaterializesPerCohort: one stored
// reshape recipe, rendered with the cohort as a variable, predicts and
// materializes over each of two source cohorts and writes the same
// layout both times.
func TestMaterializeTemplate_RendersAndMaterializesPerCohort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTmpl(t, dir, "reshape/handoff.json", reshapeRecipe)
	p, _ := matPulse(t, Options{TemplateDirs: []string{dir}})
	buildMatSource(t, p, "wave1.pulse", CohortBuilderOptions{})
	buildMatSource(t, p, "wave2.pulse", CohortBuilderOptions{})
	ctx := context.Background()

	for _, cohort := range []string{"wave1.pulse", "wave2.pulse"} {
		req, err := p.RenderTemplateRequest("reshape/handoff", map[string]any{"cohort": cohort})
		if err != nil {
			t.Fatalf("%s: render: %v", cohort, err)
		}
		if req.Cohort == nil || req.Cohort.Filename != cohort || req.Output == nil {
			t.Fatalf("%s: rendered request = %+v, want the cohort bound and the output block kept", cohort, req)
		}
		env, err := p.PredictMaterialize(ctx, req)
		if err != nil || len(env.Errors) != 0 {
			t.Fatalf("%s: PredictMaterialize: %v %+v", cohort, err, env.Errors)
		}
		out := "reshaped_" + cohort
		res, err := p.Materialize(ctx, req, out, MaterializeOptions{})
		if err != nil {
			t.Fatalf("%s: Materialize: %v", cohort, err)
		}
		if res.Records != 12 {
			t.Errorf("%s: Records = %d, want 12", cohort, res.Records)
		}
		schema, rows := readAll(t, p, out)
		if want := []string{"key", "region", "spend_band"}; !slices.Equal(fieldNames(schema), want) {
			t.Fatalf("%s: fields = %v, want %v", cohort, fieldNames(schema), want)
		}
		if got, want := rows[0][2], "low"; got != want {
			t.Errorf("%s: spend 0.5 banded %v, want %v", cohort, got, want)
		}
		if got, want := rows[11][2], "high"; got != want {
			t.Errorf("%s: spend 33.5 banded %v, want %v", cohort, got, want)
		}
	}
}

// TestMaterializeTemplate_OutputIsMaterializeOnly: the same rendered
// request is refused by Process, which is why the template docs send it
// to Materialize.
func TestMaterializeTemplate_OutputIsMaterializeOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTmpl(t, dir, "reshape/handoff.json", reshapeRecipe)
	p, _ := matPulse(t, Options{TemplateDirs: []string{dir}})
	buildMatSource(t, p, "wave1.pulse", CohortBuilderOptions{})
	req, err := p.RenderTemplateRequest("reshape/handoff", map[string]any{"cohort": "wave1.pulse"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Process(context.Background(), req)
	assertCode(t, err, errors.PULSE_MATERIALIZE_OUTPUT_ONLY)
}

// TestMaterializeExample_IsAMaterializeRequest: the shipped example
// decodes as a request whose only job is the output slot (it names no
// derived column) and declares the capability the slot rides.
func TestMaterializeExample_IsAMaterializeRequest(t *testing.T) {
	t.Parallel()

	ex, ok := examples.Get("materialize_reshape_for_handoff")
	if !ok {
		t.Fatal("materialize_reshape_for_handoff example missing")
	}
	var req Request
	if err := json.Unmarshal(ex.Body, &req); err != nil {
		t.Fatalf("example does not decode: %v", err)
	}
	if req.Output == nil || len(req.Output.Columns) == 0 || !req.Output.ElideConstants {
		t.Fatalf("example output = %+v", req.Output)
	}
	if got := examples.Capabilities()["materialize_reshape_for_handoff"]; !slices.Equal(got, []string{"capability:materialize_output"}) {
		t.Errorf("capabilities = %v", got)
	}
}
