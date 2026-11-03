package integration_test

import (
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Request templates over expression-capable extension params (U79
// E1-S4): the facade hands the instance's flagged keys to the template
// store, so a `{{token}}` inside one is PULSE_TEMPLATE_EXPR_SPLICE —
// slot extension_param — exactly as in a built-in expression slot, at
// pulse.New and on every rescan; an unflagged key keeps interpolating,
// and an extension a feature profile hides flags nothing.

// spliceTemplate is a request template declaring a number w and a
// string t over body.
func spliceTemplate(t *testing.T, body *types.Request) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return `{"target": "request", "variables": [
  {"name": "w", "type": "number", "default": 3},
  {"name": "t", "type": "string", "default": "x"}
], "body": ` + string(raw) + `}`
}

func writeTemplateFile(t *testing.T, dir, name, doc string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTemplatePulse(ext pulse.Extensions, dir string, fp *pulse.FeatureProfile) (*pulse.Pulse, error) {
	return pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext, TemplateDirs: []string{dir}, FeatureProfile: fp})
}

// TestTemplateExprSplice_ExtensionParamEveryCategory: per extension
// category, a template splicing a variable into the flagged `scale`
// fails pulse.New PULSE_TEMPLATE_EXPR_SPLICE with the built-in slots'
// details (template, variable, file path, slot = extension_param); the same
// token in the unflagged `tag` loads and renders.
func TestTemplateExprSplice_ExtensionParamEveryCategory(t *testing.T) {
	t.Parallel()

	ext := exprExtensions(&paramCapture{})
	for _, tc := range strictCases() {
		t.Run(tc.category, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeTemplateFile(t, dir, "bad.json", spliceTemplate(t, tc.request(nil, json.RawMessage(`{"scale":"params.k * {{w}}"}`))))
			_, err := newTemplatePulse(ext, dir, nil)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEMPLATE_EXPR_SPLICE {
				t.Fatalf("pulse.New = %v, want PULSE_TEMPLATE_EXPR_SPLICE", err)
			}
			// details.path is the file (the store's loadError); the body
			// path rides in the message.
			if !strings.Contains(ce.Message, "body."+tc.slot+".params.scale") {
				t.Errorf("message %q does not locate body.%s.params.scale", ce.Message, tc.slot)
			}
			want := map[string]any{"template": "bad", "variable": "w", "path": filepath.Join(dir, "bad.json"), "slot": "extension_param"}
			for k, v := range want {
				if ce.Details[k] != v {
					t.Errorf("details[%s] = %v, want %v (all: %v)", k, ce.Details[k], v, ce.Details)
				}
			}

			dir = t.TempDir()
			writeTemplateFile(t, dir, "ok.json", spliceTemplate(t, tc.request(nil, json.RawMessage(`{"scale":2,"tag":"t_{{t}}"}`))))
			p, err := newTemplatePulse(ext, dir, nil)
			if err != nil {
				t.Fatalf("pulse.New (unflagged key) = %v, want nil", err)
			}
			r, err := p.RenderTemplate("ok", map[string]any{"t": "y"})
			if err != nil {
				t.Fatalf("RenderTemplate = %v", err)
			}
			if !json.Valid(r.JSON) || !strings.Contains(string(r.JSON), `"tag":"t_y"`) {
				t.Fatalf("rendered %s, want tag t_y", r.JSON)
			}
		})
	}
}

// TestTemplateExprSplice_ExtensionParamReload: a splicing template
// written after pulse.New is judged with the same instance facts on the
// rescan — ListTemplates marks it broken with the splice fault, and
// GetTemplate / RenderTemplate surface PULSE_TEMPLATE_EXPR_SPLICE.
func TestTemplateExprSplice_ExtensionParamReload(t *testing.T) {
	t.Parallel()

	agg := strictCases()[0]
	dir := t.TempDir()
	p, err := newTemplatePulse(onlyCategory(exprExtensions(&paramCapture{}), "aggregator"), dir, nil)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	writeTemplateFile(t, dir, "late.json", spliceTemplate(t, agg.request(nil, json.RawMessage(`{"scale":"{{w}}"}`))))
	if err := p.ReloadTemplates(); err != nil {
		t.Fatalf("ReloadTemplates = %v", err)
	}
	listed := false
	for _, s := range p.ListTemplates() {
		if s.Name == "late" {
			listed = s.Broken
		}
	}
	if !listed {
		t.Fatalf("ListTemplates = %+v, want late listed broken", p.ListTemplates())
	}
	if _, err := p.GetTemplate("late"); !perr.HasCode(err, perr.PULSE_TEMPLATE_EXPR_SPLICE) {
		t.Fatalf("GetTemplate = %v, want PULSE_TEMPLATE_EXPR_SPLICE", err)
	}
	if _, err := p.RenderTemplate("late", nil); !perr.HasCode(err, perr.PULSE_TEMPLATE_EXPR_SPLICE) {
		t.Fatalf("RenderTemplate = %v, want PULSE_TEMPLATE_EXPR_SPLICE", err)
	}
}

// TestTemplateExprSplice_ExtensionParamHidden: an extension a feature
// profile hides acts never-registered, so its flagged key is not an
// expression slot on that instance and the template loads.
func TestTemplateExprSplice_ExtensionParamHidden(t *testing.T) {
	t.Parallel()

	agg := strictCases()[0]
	dir := t.TempDir()
	writeTemplateFile(t, dir, "bad.json", spliceTemplate(t, agg.request(nil, json.RawMessage(`{"scale":"{{w}}"}`))))
	ext := onlyCategory(exprExtensions(&paramCapture{}), "aggregator")
	if _, err := newTemplatePulse(ext, dir, nil); !perr.HasCode(err, perr.PULSE_TEMPLATE_EXPR_SPLICE) {
		t.Fatalf("pulse.New (visible) = %v, want PULSE_TEMPLATE_EXPR_SPLICE", err)
	}
	fp := &pulse.FeatureProfile{Features: []string{"capability:process", "AGG_SUM"}}
	if _, err := newTemplatePulse(ext, dir, fp); err != nil {
		t.Fatalf("pulse.New (hidden) = %v, want nil", err)
	}
}

// TestTemplateExprSplice_ExtensionTemplatedType: a template whose
// operator `type` is itself a variable names no operator before render,
// so the ban judges the params key against every visible operator: a
// token in the flagged `scale` fails pulse.New PULSE_TEMPLATE_EXPR_SPLICE
// with details.type_templated, while the same token in the unflagged
// `tag` loads and renders.
func TestTemplateExprSplice_ExtensionTemplatedType(t *testing.T) {
	t.Parallel()

	ext := onlyCategory(exprExtensions(&paramCapture{}), "aggregator")
	doc := func(params string) string {
		return `{"target": "request", "variables": [
  {"name": "w", "type": "number", "default": 3},
  {"name": "op", "type": "string", "default": "AGG_ACME_STRICT"}
], "body": {"aggregations": [{"type": "{{op}}", "field": "score", "label": "v", "params": ` + params + `}]}}`
	}
	dir := t.TempDir()
	writeTemplateFile(t, dir, "bad.json", doc(`{"scale": "params.k * {{w}}"}`))
	_, err := newTemplatePulse(ext, dir, nil)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEMPLATE_EXPR_SPLICE {
		t.Fatalf("pulse.New = %v, want PULSE_TEMPLATE_EXPR_SPLICE", err)
	}
	if ce.Details["slot"] != "extension_param" || ce.Details["type_templated"] != true || ce.Details["variable"] != "w" {
		t.Fatalf("details %v, want slot extension_param, type_templated, variable w", ce.Details)
	}

	dir = t.TempDir()
	writeTemplateFile(t, dir, "ok.json", doc(`{"scale": 2, "tag": "t_{{w}}"}`))
	p, err := newTemplatePulse(ext, dir, nil)
	if err != nil {
		t.Fatalf("pulse.New (unflagged key) = %v, want nil", err)
	}
	if r, err := p.RenderTemplate("ok", nil); err != nil || !strings.Contains(string(r.JSON), `"tag":"t_3"`) {
		t.Fatalf("RenderTemplate = %v", err)
	}
}
