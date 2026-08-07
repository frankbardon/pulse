package pulse

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/template"
	"github.com/spf13/afero"
)

// codedErr asserts err is a *errors.CodedError carrying want, and returns
// it so a caller can go on to inspect Details. Every template fault the
// facade surfaces is expected to arrive coded — the facade passes errors
// through from the store and the render walk rather than re-coding them,
// so this is the assertion that pass-through actually happened.
func codedErr(t *testing.T, err error, want perr.Code) *perr.CodedError {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want %s", want)
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error = %v (%T), want *errors.CodedError with code %s", err, err, want)
	}
	if ce.Code != want {
		t.Fatalf("error code = %s (%v), want %s", ce.Code, err, want)
	}
	return ce
}

// facadeDoc builds a well-formed template document with a caller-chosen
// target and body, so the target-dispatch tests can vary the one axis they
// are about without re-spelling the wrapper each time.
func facadeDoc(description, target, body string) string {
	return `{
  "description": "` + description + `",
  "target": "` + target + `",
  "variables": [{"name": "metric", "type": "field", "required": true}],
  "body": ` + body + `
}`
}

// requestBody is a minimal parameterised types.Request body: one cohort,
// one aggregation whose field comes from the "metric" variable.
const requestBody = `{"cohort": {"filename": "sales.pulse"},
  "aggregations": [{"type": "AGG_SUM", "field": {"$var": "metric"}}]}`

// facetBody is the same shape for types.FacetRequest — the non-request
// target used to prove RenderTemplateRequest refuses what it cannot return.
const facetBody = `{"cohort": {"filename": "sales.pulse"}, "fields": [{"$var": "metric"}]}`

// TestListTemplates covers the listing contract: deterministic name order,
// the projected summary fields, and shadowing visibility. The unconfigured
// case is the one that must not panic — an engine with no template
// directories is an ordinary deployment.
func TestListTemplates(t *testing.T) {
	t.Run("unconfigured engine lists nothing without panicking", func(t *testing.T) {
		t.Setenv(envTemplatesDir, "")

		p := newPulse(t, Options{})
		got := p.ListTemplates()
		if got == nil {
			t.Fatal("ListTemplates() = nil, want a non-nil empty slice for safe JSON marshaling")
		}
		if len(got) != 0 {
			t.Fatalf("ListTemplates() = %v, want empty", got)
		}
	})

	t.Run("configured but empty root lists nothing", func(t *testing.T) {
		p := newPulse(t, Options{TemplateDirs: []string{t.TempDir()}})
		if got := p.ListTemplates(); len(got) != 0 {
			t.Fatalf("ListTemplates() = %v, want empty", got)
		}
	})

	t.Run("sorted by name regardless of discovery order", func(t *testing.T) {
		dir := t.TempDir()
		writeTmpl(t, dir, "zulu.json", tmplDoc("z"))
		writeTmpl(t, dir, "alpha.json", tmplDoc("a"))
		writeTmpl(t, dir, "finance/revenue.json", tmplDoc("r"))

		p := newPulse(t, Options{TemplateDirs: []string{dir}})

		want := []string{"alpha", "finance/revenue", "zulu"}
		got := p.ListTemplates()
		if len(got) != len(want) {
			t.Fatalf("ListTemplates() returned %d entries, want %d (%v)", len(got), len(want), got)
		}
		for i, w := range want {
			if got[i].Name != w {
				t.Fatalf("ListTemplates()[%d].Name = %q, want %q (full: %v)", i, got[i].Name, w, got)
			}
		}
	})

	t.Run("summary projects the declaration a caller builds a form from", func(t *testing.T) {
		dir := t.TempDir()
		path := writeTmpl(t, dir, "finance/revenue.json", tmplDoc("Revenue by region"))

		p := newPulse(t, Options{TemplateDirs: []string{dir}})

		got := p.ListTemplates()
		if len(got) != 1 {
			t.Fatalf("ListTemplates() = %v, want exactly one entry", got)
		}
		s := got[0]
		if s.Name != "finance/revenue" {
			t.Errorf("Name = %q, want %q", s.Name, "finance/revenue")
		}
		if s.Description != "Revenue by region" {
			t.Errorf("Description = %q, want %q", s.Description, "Revenue by region")
		}
		if s.Target != template.TargetRequest {
			t.Errorf("Target = %q, want %q", s.Target, template.TargetRequest)
		}
		if len(s.Variables) != 1 || s.Variables[0] != "metric" {
			t.Errorf("Variables = %v, want [metric]", s.Variables)
		}
		if s.Path != path {
			t.Errorf("Path = %q, want %q", s.Path, path)
		}
		if len(s.Shadows) != 0 {
			t.Errorf("Shadows = %v, want empty for an unshadowed entry", s.Shadows)
		}
	})

	t.Run("shadowed entries surface on the winner rather than as their own row", func(t *testing.T) {
		high := t.TempDir()
		low := t.TempDir()
		winner := writeTmpl(t, high, "shared.json", tmplDoc("high precedence"))
		loser := writeTmpl(t, low, "shared.json", tmplDoc("low precedence"))

		p := newPulse(t, Options{TemplateDirs: []string{high, low}})

		got := p.ListTemplates()
		if len(got) != 1 {
			t.Fatalf("ListTemplates() = %v, want exactly one entry (the shadowed one gets no row of its own)", got)
		}
		s := got[0]
		if s.Description != "high precedence" || s.Path != winner {
			t.Fatalf("listed entry = %+v, want the first root's document at %q", s, winner)
		}
		if len(s.Shadows) != 1 || s.Shadows[0] != loser {
			t.Fatalf("Shadows = %v, want [%s]", s.Shadows, loser)
		}
	})

	t.Run("every listed entry is fetchable", func(t *testing.T) {
		high := t.TempDir()
		low := t.TempDir()
		writeTmpl(t, high, "shared.json", tmplDoc("high"))
		writeTmpl(t, low, "shared.json", tmplDoc("low"))
		writeTmpl(t, low, "only-low.json", tmplDoc("only low"))

		p := newPulse(t, Options{TemplateDirs: []string{high, low}})

		for _, s := range p.ListTemplates() {
			if _, err := p.GetTemplate(s.Name); err != nil {
				t.Fatalf("GetTemplate(%q) = %v; a listing whose entries cannot all be fetched is a trap", s.Name, err)
			}
		}
	})
}

// TestGetTemplate is the read side: a known name yields the full parsed
// declaration (variables included, so a caller can build a form), and every
// miss is PULSE_TEMPLATE_NOT_FOUND naming what was asked for.
func TestGetTemplate(t *testing.T) {
	dir := t.TempDir()
	writeTmpl(t, dir, "finance/revenue.json", tmplDoc("Revenue by region"))

	t.Run("known name returns the full declaration", func(t *testing.T) {
		p := newPulse(t, Options{TemplateDirs: []string{dir}})

		tmpl, err := p.GetTemplate("finance/revenue")
		if err != nil {
			t.Fatalf("GetTemplate = %v, want the loaded template", err)
		}
		if tmpl.Name != "finance/revenue" {
			t.Errorf("Name = %q, want the derived name", tmpl.Name)
		}
		if tmpl.Target != template.TargetRequest {
			t.Errorf("Target = %q, want %q", tmpl.Target, template.TargetRequest)
		}
		if len(tmpl.Variables) != 1 {
			t.Fatalf("Variables = %v, want one declaration", tmpl.Variables)
		}
		v := tmpl.Variables[0]
		if v.Name != "metric" || v.Type != template.VarField || !v.Required {
			t.Errorf("Variables[0] = %+v, want {metric field required}", v)
		}
		if len(tmpl.Body) == 0 {
			t.Error("Body is empty, want the parameterised request body")
		}
	})

	t.Run("misses are coded", func(t *testing.T) {
		tests := []struct {
			name string
			dirs []string
			ask  string
		}{
			{name: "unknown name", dirs: []string{dir}, ask: "finance/nope"},
			{name: "name carrying the file extension", dirs: []string{dir}, ask: "finance/revenue.json"},
			{name: "unconfigured engine", dirs: nil, ask: "finance/revenue"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Setenv(envTemplatesDir, "")
				p := newPulse(t, Options{TemplateDirs: tc.dirs})

				_, err := p.GetTemplate(tc.ask)
				ce := codedErr(t, err, perr.PULSE_TEMPLATE_NOT_FOUND)
				if got := ce.Details[perr.DetailTemplate]; got != tc.ask {
					t.Errorf("details[%s] = %v, want %q", perr.DetailTemplate, got, tc.ask)
				}
			})
		}
	})
}

// TestRenderTemplate covers the general render form: the right typed
// pointer for every target, exactly one populated, and the raw JSON
// retained alongside it.
func TestRenderTemplate(t *testing.T) {
	t.Run("populates exactly the pointer the target names", func(t *testing.T) {
		tests := []struct {
			name   string
			target string
			body   string
			want   template.Target
		}{
			{name: "request", target: "request", body: requestBody, want: template.TargetRequest},
			{name: "facet", target: "facet", body: facetBody, want: template.TargetFacet},
			{
				name:   "composed",
				target: "composed",
				body: `{"requests": [{"cohort": {"filename": "sales.pulse"},
				  "aggregations": [{"type": "AGG_SUM", "field": {"$var": "metric"}}]}]}`,
				want: template.TargetComposed,
			},
			{
				name:   "sample",
				target: "sample",
				body: `{"cohort": {"filename": "sales.pulse"}, "n": 5,
				  "labels": [{"field": {"$var": "metric"}, "table": "regions"}]}`,
				want: template.TargetSample,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				writeTmpl(t, dir, "t.json", facadeDoc(tc.name, tc.target, tc.body))
				p := newPulse(t, Options{TemplateDirs: []string{dir}})

				got, err := p.RenderTemplate("t", map[string]any{"metric": "amount"})
				if err != nil {
					t.Fatalf("RenderTemplate = %v, want a rendered result", err)
				}
				if got.Target != tc.want {
					t.Fatalf("Target = %q, want %q", got.Target, tc.want)
				}
				if len(got.JSON) == 0 {
					t.Error("JSON is empty; the rendered body is retained alongside the typed value on purpose")
				}
				if got.Typed() == nil {
					t.Fatal("Typed() = nil, want the populated pointer")
				}
				populated := 0
				for _, nonNil := range []bool{
					got.Request != nil, got.Composed != nil, got.Chain != nil,
					got.Facet != nil, got.Sample != nil,
				} {
					if nonNil {
						populated++
					}
				}
				if populated != 1 {
					t.Fatalf("%d typed pointers populated, want exactly 1 (%+v)", populated, got)
				}
			})
		}
	})

	t.Run("substituted values reach the typed request", func(t *testing.T) {
		dir := t.TempDir()
		writeTmpl(t, dir, "t.json", facadeDoc("request", "request", requestBody))
		p := newPulse(t, Options{TemplateDirs: []string{dir}})

		got, err := p.RenderTemplate("t", map[string]any{"metric": "amount"})
		if err != nil {
			t.Fatalf("RenderTemplate = %v", err)
		}
		if n := len(got.Request.Aggregations); n != 1 {
			t.Fatalf("Aggregations = %d, want 1", n)
		}
		if f := got.Request.Aggregations[0].Field; f != "amount" {
			t.Errorf("Aggregations[0].Field = %q, want the substituted %q", f, "amount")
		}
		if !strings.Contains(string(got.JSON), `"amount"`) {
			t.Errorf("JSON %s does not carry the substituted value", got.JSON)
		}
	})

	t.Run("unknown name is coded", func(t *testing.T) {
		t.Setenv(envTemplatesDir, "")
		p := newPulse(t, Options{})
		_, err := p.RenderTemplate("nope", nil)
		codedErr(t, err, perr.PULSE_TEMPLATE_NOT_FOUND)
	})
}

// TestRenderTemplateRequest is the 95% path plus its one new failure mode:
// a template whose target is valid but is not one this method can return.
func TestRenderTemplateRequest(t *testing.T) {
	t.Run("returns the typed request directly", func(t *testing.T) {
		dir := t.TempDir()
		writeTmpl(t, dir, "finance/revenue.json", facadeDoc("revenue", "request", requestBody))
		p := newPulse(t, Options{TemplateDirs: []string{dir}})

		req, err := p.RenderTemplateRequest("finance/revenue", map[string]any{"metric": "amount"})
		if err != nil {
			t.Fatalf("RenderTemplateRequest = %v, want a typed request", err)
		}
		if req == nil {
			t.Fatal("RenderTemplateRequest returned a nil request with a nil error")
		}
		if req.Cohort == nil || req.Cohort.Filename != "sales.pulse" {
			t.Errorf("Cohort = %+v, want the body's sales.pulse", req.Cohort)
		}
		if len(req.Aggregations) != 1 || req.Aggregations[0].Field != "amount" {
			t.Errorf("Aggregations = %+v, want one AGG_SUM over the substituted field", req.Aggregations)
		}
	})

	t.Run("non-request targets are refused by name", func(t *testing.T) {
		tests := []struct {
			name          string
			target        string
			body          string
			wantRenderPtr string
		}{
			{
				name:          "facet",
				target:        "facet",
				body:          `{"cohort": {"filename": "sales.pulse"}, "fields": ["region"]}`,
				wantRenderPtr: "Facet",
			},
			{
				name:          "composed",
				target:        "composed",
				body:          `{"requests": [{"cohort": {"filename": "sales.pulse"}}]}`,
				wantRenderPtr: "Composed",
			},
			{
				name:          "sample",
				target:        "sample",
				body:          `{"cohort": {"filename": "sales.pulse"}, "n": 5}`,
				wantRenderPtr: "Sample",
			},
			{
				name:          "chain",
				target:        "chain",
				body:          `{"cohort": {"filename": "sales.pulse"}, "stages": []}`,
				wantRenderPtr: "Chain",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				// The variable is declared but unused by these bodies;
				// supplying it keeps the required-variable rule satisfied so
				// the ONLY fault under test is the target mismatch.
				doc := `{"target": "` + tc.target + `", "body": ` + tc.body + `}`
				writeTmpl(t, dir, "t.json", doc)
				p := newPulse(t, Options{TemplateDirs: []string{dir}})

				// The general form must succeed — it is the mismatch, not
				// the template, that is at fault.
				if _, err := p.RenderTemplate("t", nil); err != nil {
					t.Fatalf("RenderTemplate = %v, want success (only RenderTemplateRequest should refuse)", err)
				}

				_, err := p.RenderTemplateRequest("t", nil)
				ce := codedErr(t, err, perr.PULSE_TEMPLATE_TARGET_UNKNOWN)

				msg := ce.Error()
				for _, want := range []string{tc.target, "RenderTemplate", tc.wantRenderPtr, "t"} {
					if !strings.Contains(msg, want) {
						t.Errorf("error %q does not name %q", msg, want)
					}
				}
				if got := ce.Details["target"]; got != tc.target {
					t.Errorf("details[target] = %v, want %q", got, tc.target)
				}
				if got := ce.Details["expected_target"]; got != "request" {
					t.Errorf("details[expected_target] = %v, want %q", got, "request")
				}
				if got := ce.Details[perr.DetailTemplate]; got != "t" {
					t.Errorf("details[%s] = %v, want %q", perr.DetailTemplate, got, "t")
				}
			})
		}
	})

	t.Run("misses are coded", func(t *testing.T) {
		dir := t.TempDir()
		writeTmpl(t, dir, "known.json", facadeDoc("known", "request", requestBody))

		tests := []struct {
			name string
			dirs []string
			ask  string
		}{
			{name: "unknown name", dirs: []string{dir}, ask: "nope"},
			{name: "unconfigured engine", dirs: nil, ask: "known"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Setenv(envTemplatesDir, "")
				p := newPulse(t, Options{TemplateDirs: tc.dirs})
				_, err := p.RenderTemplateRequest(tc.ask, map[string]any{"metric": "amount"})
				codedErr(t, err, perr.PULSE_TEMPLATE_NOT_FOUND)
			})
		}
	})
}

// TestRenderTemplate_VariableFaultsPropagateCoded is the pass-through
// contract: the facade re-codes nothing. Every variable fault the render
// walk raises reaches the caller with its own code and its own details,
// through both render entry points.
func TestRenderTemplate_VariableFaultsPropagateCoded(t *testing.T) {
	const doc = `{
  "target": "request",
  "variables": [
    {"name": "metric", "type": "field", "required": true},
    {"name": "bucket", "type": "integer", "default": 10},
    {"name": "region", "type": "enum", "values": ["north", "south"]}
  ],
  "body": {"cohort": {"filename": "sales.pulse"},
    "aggregations": [{"type": "AGG_SUM", "field": {"$var": "metric"}}],
    "groups": [{"type": "GROUP_RANGE", "field": "amount", "interval": {"$var": "bucket"}}]}
}`

	tests := []struct {
		name     string
		vars     map[string]any
		wantCode perr.Code
		wantVar  string
	}{
		{
			name:     "required variable with no value",
			vars:     map[string]any{},
			wantCode: perr.PULSE_TEMPLATE_VAR_MISSING,
			wantVar:  "metric",
		},
		{
			name:     "variable the template does not declare",
			vars:     map[string]any{"metric": "amount", "nonesuch": 1},
			wantCode: perr.PULSE_TEMPLATE_VAR_UNKNOWN,
			wantVar:  "nonesuch",
		},
		{
			name:     "supplied value of the wrong type",
			vars:     map[string]any{"metric": "amount", "bucket": "ten"},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
			wantVar:  "bucket",
		},
		{
			name:     "value outside an enum's declared set",
			vars:     map[string]any{"metric": "amount", "region": "east"},
			wantCode: perr.PULSE_TEMPLATE_VAR_ENUM,
			wantVar:  "region",
		},
	}

	dir := t.TempDir()
	writeTmpl(t, dir, "vars.json", doc)
	p := newPulse(t, Options{TemplateDirs: []string{dir}})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("RenderTemplate", func(t *testing.T) {
				_, err := p.RenderTemplate("vars", tc.vars)
				ce := codedErr(t, err, tc.wantCode)
				if got := ce.Details[perr.DetailVariable]; got != tc.wantVar {
					t.Errorf("details[%s] = %v, want %q", perr.DetailVariable, got, tc.wantVar)
				}
			})
			t.Run("RenderTemplateRequest", func(t *testing.T) {
				_, err := p.RenderTemplateRequest("vars", tc.vars)
				ce := codedErr(t, err, tc.wantCode)
				if got := ce.Details[perr.DetailVariable]; got != tc.wantVar {
					t.Errorf("details[%s] = %v, want %q", perr.DetailVariable, got, tc.wantVar)
				}
			})
		})
	}
}

// TestRenderTemplateRequest_RenderInvalidPropagates covers the render-side
// fault the strict decode owns: a rendered body carrying a key that is not
// a slot on the target type. It reaches the facade caller coded, naming the
// offending field.
func TestRenderTemplateRequest_RenderInvalidPropagates(t *testing.T) {
	dir := t.TempDir()
	writeTmpl(t, dir, "typo.json", `{
  "target": "request",
  "body": {"cohort": {"filename": "sales.pulse"}, "aggregation": []}
}`)
	p := newPulse(t, Options{TemplateDirs: []string{dir}})

	_, err := p.RenderTemplateRequest("typo", nil)
	ce := codedErr(t, err, perr.PULSE_TEMPLATE_RENDER_INVALID)
	if got := ce.Details["field"]; got != "aggregation" {
		t.Errorf("details[field] = %v, want %q", got, "aggregation")
	}
}

// buildTemplateFacadeCohort synthesizes the hermetic cohort the end-to-end
// test processes: three regions with known per-region row counts and a
// constant-per-row amount, so the aggregate the rendered request produces
// is exactly predictable without reimplementing the engine in the test.
//
// The cohort lives on an in-memory afero.Fs (cohort data always routes
// through the fs abstraction); the template it is queried by lives on a
// real temp directory (config-dir loading is the sanctioned os-package
// exception). The two coexisting in one test is the wiring under test.
func buildTemplateFacadeCohort(t *testing.T, memFs afero.Fs, path string) {
	t.Helper()

	spec := &synth.Spec{
		RowCount: 300,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"north", "south", "east"},
					"weights": []any{1.0, 1.0, 1.0},
				}},
			{Name: "amount", Type: "u32", Distribution: synth.DistConstant,
				Params: map[string]any{"value": 2.0}},
		},
	}

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Synth(context.Background(), spec, path, SynthOptions{Seed: 20260807}); err != nil {
		t.Fatalf("Synth: %v", err)
	}
}

// TestRenderTemplateRequest_EndToEnd is the epic's acceptance moment: a
// template file on disk, an engine configured to find it, a render against
// a caller-supplied variable map, and the resulting *types.Request handed
// straight to Process against a real cohort — with a real response coming
// back. Nothing here is stubbed.
//
// It is also the demonstration that no ProcessTemplate wrapper is needed:
// the two calls compose without one.
func TestRenderTemplateRequest_EndToEnd(t *testing.T) {
	const cohort = "sales.pulse"

	memFs := afero.NewMemMapFs()
	buildTemplateFacadeCohort(t, memFs, cohort)

	// The template names its cohort, groups by a caller-chosen dimension,
	// and sums a caller-chosen metric. Both markers are exercised: the
	// {"$var": …} object form and the {{name}} string form.
	dir := t.TempDir()
	writeTmpl(t, dir, "finance/revenue.json", `{
  "description": "Total metric by dimension",
  "target": "request",
  "variables": [
    {"name": "metric", "type": "field", "required": true},
    {"name": "dimension", "type": "field", "default": "region"}
  ],
  "body": {
    "cohort": {"filename": "`+cohort+`"},
    "groups": [{"type": "GROUP_CATEGORY", "field": "{{dimension}}"}],
    "aggregations": [{"type": "AGG_SUM", "field": {"$var": "metric"}, "label": "total"}]
  }
}`)

	p, err := New(Options{FS: memFs, TemplateDirs: []string{dir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The discovery path a caller actually walks: list, read the
	// declaration, then render.
	listed := p.ListTemplates()
	if len(listed) != 1 || listed[0].Name != "finance/revenue" {
		t.Fatalf("ListTemplates() = %v, want the one template", listed)
	}
	decl, err := p.GetTemplate("finance/revenue")
	if err != nil {
		t.Fatalf("GetTemplate = %v", err)
	}
	if len(decl.Variables) != 2 {
		t.Fatalf("declaration carries %d variables, want 2", len(decl.Variables))
	}

	req, err := p.RenderTemplateRequest("finance/revenue", map[string]any{"metric": "amount"})
	if err != nil {
		t.Fatalf("RenderTemplateRequest = %v", err)
	}

	// "dimension" was not supplied, so its default must have resolved.
	if len(req.Groups) != 1 || req.Groups[0].Field != "region" {
		t.Fatalf("Groups = %+v, want one GROUP_CATEGORY over the defaulted \"region\"", req.Groups)
	}

	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process(rendered request) = %v, want a real response", err)
	}
	if resp == nil {
		t.Fatal("Process returned a nil response with a nil error")
	}
	if len(resp.Data) != 3 {
		t.Fatalf("Data = %v, want one row per region", resp.Data)
	}

	// Every row is 2, evenly split three ways across 300 rows: each
	// region's sum is 100 rows x 2 = 200, and the grand total is 600.
	var grand float64
	seen := map[string]bool{}
	for _, row := range resp.Data {
		region, _ := row["region"].(string)
		if region == "" {
			t.Fatalf("row %v carries no region key", row)
		}
		seen[region] = true
		total, ok := numericCell(row["total"])
		if !ok {
			t.Fatalf("row %v carries no numeric \"total\"", row)
		}
		grand += total
	}
	for _, want := range []string{"north", "south", "east"} {
		if !seen[want] {
			t.Errorf("response is missing the %q group (rows: %v)", want, resp.Data)
		}
	}
	if grand != 600 {
		t.Fatalf("summed total across groups = %v, want 600 (300 rows x amount 2)", grand)
	}

	if resp.Metadata == nil {
		t.Error("Metadata = nil, want the run facts a real Process populates")
	}
}

// numericCell coerces a response cell to float64. Aggregate values arrive
// as whichever numeric Go type the aggregator emitted, and the end-to-end
// assertion is about the VALUE, not its concrete type.
func numericCell(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint32:
		return float64(n), true
	default:
		return 0, false
	}
}
