package template_test

import (
	stderrors "errors"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprslots"
	"github.com/frankbardon/pulse/internal/template"
)

// extOpts flags `scale` on every extension type the cases below name;
// `tag` is never flagged. The window flags `gain`, a key no built-in
// window param shares, so the static table alone accepts it.
var extOpts = template.ValidateOptions{ExprParams: map[string][]string{
	"AGG_ACME_X":    {"scale"},
	"ATTR_ACME_X":   {"scale"},
	"FILTER_ACME_X": {"scale"},
	"GROUP_ACME_X":  {"scale"},
	"WIN_ACME_X":    {"gain"},
	"FEAT_ACME_X":   {"gain", "scale"},
	"TEST_ACME_X":   {"scale"},
	"MAT_ACME_X":    {"scale"},
	"REG_ACME_X":    {"scale"},
}}

// TestTemplateExprSplice_ExtensionParamRefused: with the instance's
// expression-capable extension params handed in (ValidateOptions), a
// `{{token}}` in one of them is PULSE_TEMPLATE_EXPR_SPLICE at
// declaration, slot extension_param, in every category and at every
// nesting the extension pass reaches — the same details shape as a
// built-in slot.
func TestTemplateExprSplice_ExtensionParamRefused(t *testing.T) {
	t.Parallel()

	p := `"params": {"scale": "params.k * {{w}}", "tag": "x"}`
	cases := []struct {
		name, target, body, path string
	}{
		{"aggregator", "request", `{"aggregations": [{"type": "AGG_ACME_X", "field": "s", ` + p + `}]}`, "body.aggregations[0].params.scale"},
		{"attribute", "request", `{"attributes": [{"type": "ATTR_ACME_X", "field": "s", ` + p + `}]}`, "body.attributes[0].params.scale"},
		{"filterer", "request", `{"filterers": [{"type": "FILTER_ACME_X", "field": "s", ` + p + `}]}`, "body.filterers[0].params.scale"},
		{"grouper", "request", `{"groups": [{"type": "GROUP_ACME_X", "field": "s", ` + p + `}]}`, "body.groups[0].params.scale"},
		{"window", "request", `{"windows": [{"type": "WIN_ACME_X", "field": "s", "params": {"gain": "params.k * {{w}}"}}]}`, "body.windows[0].params.gain"},
		{"feature", "request", `{"features": [{"type": "FEAT_ACME_X", "field": "s", ` + p + `}]}`, "body.features[0].params.scale"},
		{"test", "request", `{"tests": [{"type": "TEST_ACME_X", "field": "s", ` + p + `}]}`, "body.tests[0].params.scale"},
		{"post test", "request", `{"post_tests": [{"type": "TEST_ACME_X", ` + p + `}]}`, "body.post_tests[0].params.scale"},
		{"matrix", "request", `{"matrices": [{"type": "MAT_ACME_X", "name": "m", ` + p + `}]}`, "body.matrices[0].params.scale"},
		{"regression", "request", `{"regressions": [{"type": "REG_ACME_X", ` + p + `}]}`, "body.regressions[0].params.scale"},
		{"crosstab cell", "request", `{"crosstab": {"cell": {"type": "AGG_ACME_X", ` + p + `}}}`, "body.crosstab.cell.params.scale"},
		{"crosstab rows", "request", `{"crosstab": {"rows": [{"type": "GROUP_ACME_X", ` + p + `}]}}`, "body.crosstab.rows[0].params.scale"},
		{"MAT_DISTANCE between grouper", "request",
			`{"matrices": [{"type": "MAT_DISTANCE", "name": "d", "params": {"between": {"type": "GROUP_ACME_X", "field": "g", ` + p + `}}}]}`,
			"body.matrices[0].params.between.params.scale"},
		{"whole-string token", "request", `{"aggregations": [{"type": "AGG_ACME_X", "params": {"scale": "{{w}}"}}]}`, "body.aggregations[0].params.scale"},
		{"second flagged key", "request", `{"features": [{"type": "FEAT_ACME_X", "params": {"scale": 1, "gain": "{{w}}"}}]}`, "body.features[0].params.gain"},
		{"compose sub-request", "composed", `{"requests": [{"label": "a", "aggregations": [{"type": "AGG_ACME_X", ` + p + `}]}]}`, "body.requests[0].aggregations[0].params.scale"},
		{"compose sweep request", "composed",
			`{"sweep": {"axes": [{"name": "d", "values": [1, 2]}], "request": {"aggregations": [{"type": "AGG_ACME_X", "label": "x_{{d}}", ` + p + `}]}}}`,
			"body.sweep.request.aggregations[0].params.scale"},
		{"chain stage", "chain", `{"stages": [{"request": {"regressions": [{"type": "REG_ACME_X", ` + p + `}]}}]}`, "body.stages[0].request.regressions[0].params.scale"},
		{"facet filterer", "facet", `{"filterers": [{"type": "FILTER_ACME_X", ` + p + `}]}`, "body.filterers[0].params.scale"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := []byte(spliceDoc(c.target, c.body))
			_, err := template.ParseWith(doc, extOpts)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEMPLATE_EXPR_SPLICE {
				t.Fatalf("ParseWith = %v, want PULSE_TEMPLATE_EXPR_SPLICE", err)
			}
			if ce.Details["path"] != c.path || ce.Details["slot"] != exprslots.SlotExtensionParam || ce.Details[perr.DetailVariable] != "w" {
				t.Errorf("details %+v, want path %q slot %q variable w", ce.Details, c.path, exprslots.SlotExtensionParam)
			}
			// No instance facts: the static table alone cannot know the
			// key is an expression, so the zero options accept it.
			if _, err := template.Parse(doc); err != nil {
				t.Errorf("Parse (no instance facts) = %v, want the template to validate", err)
			}
		})
	}
}

// TestTemplateExprSplice_ExtensionAxisTokenRefused: a for_each axis
// token in a flagged extension param is refused too, naming the axis.
func TestTemplateExprSplice_ExtensionAxisTokenRefused(t *testing.T) {
	t.Parallel()

	body := `{"windows": [{"type": "WIN_ACME_X", "field": "s", "for_each": {"axes": [{"name": "d", "values": [1, 2]}]}, "label": "w_{{d}}", "params": {"gain": "{{d}} * 2"}}]}`
	_, err := template.ParseWith([]byte(spliceDoc("request", body)), extOpts)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEMPLATE_EXPR_SPLICE {
		t.Fatalf("ParseWith = %v, want PULSE_TEMPLATE_EXPR_SPLICE", err)
	}
	if ce.Details[perr.DetailVariable] != "d" || ce.Details["path"] != "body.windows[0].params.gain" {
		t.Errorf("details %+v, want the axis d at body.windows[0].params.gain", ce.Details)
	}
}

// TestTemplateExprSplice_ExtensionParamAllowed: what the hook leaves
// alone — an unflagged key of a flagged operator, a flagged key's name
// on an operator that does not flag it (built-in or extension), a
// whole-value marker and the escape in a flagged key, a `type` that is
// itself template syntax, and a request-level params entry.
func TestTemplateExprSplice_ExtensionParamAllowed(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body string }{
		{"unflagged key", `{"aggregations": [{"type": "AGG_ACME_X", "params": {"scale": 2, "tag": "t_{{w}}"}}]}`},
		{"built-in operator", `{"aggregations": [{"type": "AGG_SUM", "field": "s", "params": {"scale": "{{w}}"}}]}`},
		{"unflagged extension", `{"aggregations": [{"type": "AGG_ACME_Y", "params": {"scale": "{{w}}"}}]}`},
		{"whole-value marker", `{"aggregations": [{"type": "AGG_ACME_X", "params": {"scale": {"$var": "w"}}}]}`},
		{"escape", `{"aggregations": [{"type": "AGG_ACME_X", "params": {"scale": "'{{{{w}}'"}}]}`},
		{"templated type", `{"aggregations": [{"type": "{{f}}", "params": {"scale": "{{w}}"}}]}`},
		{"request params", `{"aggregations": [{"type": "AGG_ACME_X", "params": {"scale": 2}}], "params": {"scale": "{{w}}"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := template.ParseWith([]byte(spliceDoc("request", c.body)), extOpts); err != nil {
				t.Fatalf("ParseWith = %v, want the template to validate", err)
			}
		})
	}
}

// TestTemplateExprSplice_ExtensionStoreRescan: a store built with the
// instance facts refuses the splice at construction, and a rescan
// judges a file written later with the same facts — the broken file is
// listed broken and fetching it surfaces the splice code.
func TestTemplateExprSplice_ExtensionStoreRescan(t *testing.T) {
	t.Parallel()

	bad := spliceDoc("request", `{"aggregations": [{"type": "AGG_ACME_X", "params": {"scale": "{{w}}"}}]}`)

	dir := t.TempDir()
	writeFile(t, dir, "bad.json", bad)
	if _, err := template.NewStoreWith([]string{dir}, extOpts); !perr.HasCode(err, perr.PULSE_TEMPLATE_EXPR_SPLICE) {
		t.Fatalf("NewStoreWith = %v, want PULSE_TEMPLATE_EXPR_SPLICE", err)
	}

	dir = t.TempDir()
	writeFile(t, dir, "good.json", spliceDoc("request", `{"aggregations": [{"type": "AGG_ACME_X", "params": {"scale": 2}}]}`))
	s, err := template.NewStoreWith([]string{dir}, extOpts)
	if err != nil {
		t.Fatalf("NewStoreWith = %v", err)
	}
	writeFile(t, dir, "bad.json", bad)
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload = %v", err)
	}
	if _, err := s.Get("bad"); !perr.HasCode(err, perr.PULSE_TEMPLATE_EXPR_SPLICE) {
		t.Fatalf("Get(bad) = %v, want PULSE_TEMPLATE_EXPR_SPLICE", err)
	}
	broken := false
	for _, sum := range s.List() {
		if sum.Name == "bad" && sum.Broken {
			broken = true
		}
	}
	if !broken {
		t.Fatalf("List = %+v, want bad listed broken", s.List())
	}
}
