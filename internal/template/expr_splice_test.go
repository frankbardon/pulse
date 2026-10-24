package template_test

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprslots"
	"github.com/frankbardon/pulse/internal/template"
)

// spliceDoc is a template document declaring a number `w` and a string
// `f`, over target and body.
func spliceDoc(target, body string) string {
	return `{"target": "` + target + `", "variables": [
  {"name": "w", "type": "number", "default": 3},
  {"name": "f", "type": "string", "default": "wave > 1"}
], "body": ` + body + `}`
}

// TestTemplateExprSplice_Refused: a `{{token}}` inside every
// expression-bearing slot kind, on every target nesting it, is refused
// PULSE_TEMPLATE_EXPR_SPLICE at declaration (Parse, so a store refuses
// the file at pulse.New), with the variable, body path and slot in the
// details.
func TestTemplateExprSplice_Refused(t *testing.T) {
	cases := []struct {
		name, target, body, path, slot string
	}{
		{"filter expression", "request",
			`{"filterers": [{"type": "FILTER_EXPRESSION", "expression": "wave == {{w}}"}]}`,
			"body.filterers[0].expression", exprslots.SlotExpression},
		{"formula attribute", "request",
			`{"attributes": [{"type": "ATTR_FORMULA", "label": "x", "expression": "spend * {{w}}"}]}`,
			"body.attributes[0].expression", exprslots.SlotExpression},
		{"total() argument", "request",
			`{"attributes": [{"type": "ATTR_FORMULA", "label": "x", "expression": "spend / total(spend * {{ w }})"}]}`,
			"body.attributes[0].expression", exprslots.SlotExpression},
		{"fit_on filterer in params", "request",
			`{"attributes": [{"type": "ATTR_MAHALANOBIS", "fields": ["a", "b"], "params": {"fit_on": [{"type": "FILTER_EXPRESSION", "expression": "a > {{w}}"}]}}]}`,
			"body.attributes[0].params.fit_on[0].expression", exprslots.SlotExpression},
		{"regression holdout", "request",
			`{"regressions": [{"type": "REG_OLS", "holdout": "wave >= {{w}}"}]}`,
			"body.regressions[0].holdout", exprslots.SlotHoldout},
		{"request overlay formula", "request",
			`{"overlays": [{"kind": "OVERLAY_FORMULA", "params": {"formula": "cell * {{w}}"}}]}`,
			"body.overlays[0].params.formula", exprslots.SlotOverlayFormula},
		{"compose sub-request filter", "composed",
			`{"requests": [{"label": "a", "filterers": [{"type": "FILTER_EXPRESSION", "expression": "wave == {{w}}"}]}]}`,
			"body.requests[0].filterers[0].expression", exprslots.SlotExpression},
		{"compose overlay formula", "composed",
			`{"requests": [{"label": "a"}], "overlays": [{"kind": "OVERLAY_FORMULA", "params": {"formula": "slot.a.x * {{w}}"}}]}`,
			"body.overlays[0].params.formula", exprslots.SlotOverlayFormula},
		{"compose sweep request formula", "composed",
			`{"sweep": {"axes": [{"name": "d", "values": [1, 2]}], "request": {"attributes": [{"type": "ATTR_FORMULA", "label": "x", "expression": "spend * {{d}} * {{w}}"}]}}}`,
			"body.sweep.request.attributes[0].expression", exprslots.SlotExpression},
		{"chain stage holdout", "chain",
			`{"stages": [{"request": {"regressions": [{"type": "REG_OLS", "holdout": "wave >= {{w}}"}]}}]}`,
			"body.stages[0].request.regressions[0].holdout", exprslots.SlotHoldout},
		{"facet filter", "facet",
			`{"filterers": [{"type": "FILTER_EXPRESSION", "expression": "{{f}}"}]}`,
			"body.filterers[0].expression", exprslots.SlotExpression},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := template.Parse([]byte(spliceDoc(c.target, c.body)))
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEMPLATE_EXPR_SPLICE {
				t.Fatalf("Parse = %v, want PULSE_TEMPLATE_EXPR_SPLICE", err)
			}
			if ce.Details["path"] != c.path || ce.Details["slot"] != c.slot {
				t.Errorf("details %+v, want path %q slot %q", ce.Details, c.path, c.slot)
			}
			if v := ce.Details[perr.DetailVariable]; v != "w" && v != "f" {
				t.Errorf("details variable %v, want the spliced variable", v)
			}
		})
	}
}

// TestTemplateExprSplice_WholeValueAllowed: what stays legal in an
// expression slot — a whole-value marker supplying the entire
// expression, the `{{{{` escape, a for_each axis token left for the
// item's expansion — and `{{token}}` outside one (a label, a params
// entry sharing a slot's key) still interpolates.
func TestTemplateExprSplice_WholeValueAllowed(t *testing.T) {
	cases := []struct {
		name, body string
		want       map[string]any
	}{
		{"whole-value marker",
			`{"filterers": [{"type": "FILTER_EXPRESSION", "expression": {"$var": "f"}}]}`,
			map[string]any{"filterers": []any{map[string]any{"type": "FILTER_EXPRESSION", "expression": "wave > 1"}}}},
		{"escape",
			`{"attributes": [{"type": "ATTR_FORMULA", "label": "x", "expression": "name == '{{{{w}}'"}]}`,
			map[string]any{"attributes": []any{map[string]any{"type": "ATTR_FORMULA", "label": "x", "expression": "name == '{{w}}'"}}}},
		{"for_each axis token",
			`{"attributes": [{"type": "ATTR_FORMULA", "for_each": {"axes": [{"name": "d", "values": [1, 2]}]}, "label": "x_{{d}}", "expression": "spend * {{d}}"}]}`,
			map[string]any{"attributes": []any{map[string]any{"type": "ATTR_FORMULA", "for_each": map[string]any{"axes": []any{map[string]any{"name": "d", "values": []any{json.Number("1"), json.Number("2")}}}}, "label": "x_{{d}}", "expression": "spend * {{d}}"}}}},
		{"label beside an expression",
			`{"label": "wave {{w}}", "filterers": [{"type": "FILTER_EXPRESSION", "expression": "wave == params.w"}], "params": {"w": {"$var": "w"}}}`,
			map[string]any{"label": "wave 3", "filterers": []any{map[string]any{"type": "FILTER_EXPRESSION", "expression": "wave == params.w"}}, "params": map[string]any{"w": json.Number("3")}}},
		{"params entry sharing a slot key",
			`{"params": {"expression": "{{f}}", "holdout": "{{f}}"}}`,
			map[string]any{"params": map[string]any{"expression": "wave > 1", "holdout": "wave > 1"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tp, err := template.Parse([]byte(spliceDoc("request", c.body)))
			if err != nil {
				t.Fatalf("Parse = %v, want the template to validate", err)
			}
			raw, err := template.RenderJSON(tp, nil)
			if err != nil {
				t.Fatalf("RenderJSON = %v", err)
			}
			got := decodeNumbers(t, raw)
			if gj, wj := mustJSON(t, got), mustJSON(t, c.want); gj != wj {
				t.Errorf("rendered %s, want %s", gj, wj)
			}
		})
	}
}

// TestTemplateExprSplice_ParamsMarkerKeepsType: the acceptance shape —
// `"params": {"from_wave": {"$var": "from_wave"}}` beside a
// FILTER_EXPRESSION reading params.from_wave — renders to a typed
// request whose param is the NUMBER the caller supplied (a json.Number
// carrying its exact text, never a string), for both an integer beyond
// 2^53 and a fraction. The example on disk (testdata/wave-filter.json)
// is this template.
func TestTemplateExprSplice_ParamsMarkerKeepsType(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "wave-filter.json"))
	if err != nil {
		t.Fatal(err)
	}
	tp, err := template.Parse(data)
	if err != nil {
		t.Fatalf("Parse(wave-filter.json) = %v", err)
	}
	for _, supplied := range []json.Number{"7", "9007199254740993", "2.50"} {
		r, err := template.Render(tp, map[string]any{"from_wave": supplied})
		if err != nil {
			t.Fatalf("Render(%s) = %v", supplied, err)
		}
		req := r.Request
		if req == nil {
			t.Fatalf("Render(%s) populated no request", supplied)
		}
		got, ok := req.Params["from_wave"].(json.Number)
		if !ok || got != supplied {
			t.Errorf("Params[from_wave] = %#v (%T), want json.Number %s", req.Params["from_wave"], req.Params["from_wave"], supplied)
		}
		if len(req.Filterers) != 1 || req.Filterers[0].Expression != "wave == params.from_wave" {
			t.Errorf("Filterers = %+v, want the expression untouched", req.Filterers)
		}
	}
}

func decodeNumbers(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := jsonDecodeNumbers(raw, &m); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return m
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func jsonDecodeNumbers(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(v)
}
