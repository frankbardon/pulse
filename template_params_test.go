package pulse

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
	"github.com/spf13/afero"
)

// buildWaveCohort writes a 300-row cohort: `wave` 1..300 (u16, one row
// per wave) and `amount` constant 2.
func buildWaveCohort(t *testing.T, memFs afero.Fs, path string) {
	t.Helper()
	spec := &synth.Spec{
		RowCount: 300,
		Fields: []synth.FieldSpec{
			{Name: "wave", Type: "u16", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
			{Name: "amount", Type: "u32", Distribution: synth.DistConstant,
				Params: map[string]any{"value": 2.0}},
		},
	}
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Synth(context.Background(), spec, path, SynthOptions{Seed: 20261024}); err != nil {
		t.Fatalf("Synth: %v", err)
	}
}

// TestTemplateExprSplice_ParamsAcceptancePath is the templates'
// acceptance path for request params: the shipped example template
// (internal/template/testdata/wave-filter.json) fills `params.from_wave`
// with a type-preserving marker and filters `wave == params.from_wave`;
// rendered with a caller's number it decodes to a typed request whose
// param stays a number, and Process keeps exactly that wave's row.
func TestTemplateExprSplice_ParamsAcceptancePath(t *testing.T) {
	t.Parallel()

	memFs := afero.NewMemMapFs()
	buildWaveCohort(t, memFs, "panel.pulse")

	example, err := os.ReadFile(filepath.Join("internal", "template", "testdata", "wave-filter.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTmpl(t, dir, "panel/wave-filter.json", string(example))
	p, err := New(Options{FS: memFs, TemplateDirs: []string{dir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, wave := range []any{7, json.Number("120")} {
		req, err := p.RenderTemplateRequest("panel/wave-filter", map[string]any{"from_wave": wave})
		if err != nil {
			t.Fatalf("RenderTemplateRequest(%v) = %v", wave, err)
		}
		n, ok := req.Params["from_wave"].(json.Number)
		if !ok {
			t.Fatalf("Params[from_wave] = %#v (%T), want a json.Number, never a string", req.Params["from_wave"], req.Params["from_wave"])
		}
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process(rendered, from_wave %s) = %v", n, err)
		}
		if len(resp.Data) != 1 {
			t.Fatalf("Data = %v, want one ungrouped row", resp.Data)
		}
		count, _ := numericCell(resp.Data[0]["n"])
		total, _ := numericCell(resp.Data[0]["total"])
		if count != 1 || total != 2 {
			t.Errorf("from_wave %s: n = %v, total = %v; want the one row of that wave (n 1, total 2) out of 300", n, resp.Data[0]["n"], resp.Data[0]["total"])
		}
	}
}

// TestTemplateExprSplice_RefusedAtNew: a template splicing `{{token}}`
// into an expression is an author error, so the store refuses it at
// pulse.New with its own code.
func TestTemplateExprSplice_RefusedAtNew(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTmpl(t, dir, "panel/spliced.json", `{"target": "request",
  "variables": [{"name": "from_wave", "type": "number", "required": true}],
  "body": {"cohort": {"filename": "panel.pulse"},
    "filterers": [{"type": "FILTER_EXPRESSION", "expression": "wave == {{from_wave}}"}]}}`)
	_, err := New(Options{FS: afero.NewMemMapFs(), TemplateDirs: []string{dir}})
	ce := codedErr(t, err, perr.PULSE_TEMPLATE_EXPR_SPLICE)
	if ce.Details["slot"] != "expression" || ce.Details[perr.DetailVariable] != "from_wave" {
		t.Errorf("details %+v, want the expression slot and the spliced variable", ce.Details)
	}
}
