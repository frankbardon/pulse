package sweep

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprslots"
)

// requireSplice asserts err is code with the field, placeholder and
// slot details, naming the placeholder in its message.
func requireSplice(t *testing.T, err error, code errors.Code, field, placeholder, slot string) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want %s, got %v", code, err)
	}
	if ce.Code != code {
		t.Fatalf("got %s (%s), want %s", ce.Code, ce.Message, code)
	}
	if ce.Details["field"] != field || ce.Details["placeholder"] != placeholder || ce.Details["slot"] != slot {
		t.Fatalf("details %v, want field %q placeholder %q slot %q", ce.Details, field, placeholder, slot)
	}
	if !strings.Contains(ce.Message, "{{"+placeholder+"}}") {
		t.Fatalf("message %q does not name the placeholder", ce.Message)
	}
}

// TestSweepExprSplice_Refused: an `{{axis}}` token inside every
// expression slot kind a sweep request or its overlays can carry is
// PULSE_SWEEP_EXPR_SPLICE, raised by the reference scan before any slot
// is substituted (the preflight never runs) — the slot list is
// internal/exprslots, the one the template renderer refuses by.
func TestSweepExprSplice_Refused(t *testing.T) {
	cases := []struct {
		name, sweep, field, slot string
	}{
		{"filter expression",
			`"request":{"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == {{w}}"}]}`,
			"sweep.request.filterers[0].expression", exprslots.SlotExpression},
		{"formula attribute",
			`"request":{"attributes":[{"type":"ATTR_FORMULA","label":"x","expression":"spend * {{w}}"}]}`,
			"sweep.request.attributes[0].expression", exprslots.SlotExpression},
		{"total() argument",
			`"request":{"attributes":[{"type":"ATTR_FORMULA","label":"x","expression":"spend / total(spend * {{ w }})"}]}`,
			"sweep.request.attributes[0].expression", exprslots.SlotExpression},
		{"attribute fit_on filterer",
			`"request":{"attributes":[{"type":"ATTR_MAHALANOBIS","fields":["a","b"],"params":{"fit_on":[{"type":"FILTER_EXPRESSION","expression":"a > {{w}}"}]}}]}`,
			"sweep.request.attributes[0].params.fit_on[0].expression", exprslots.SlotExpression},
		{"centroid filter",
			`"request":{"attributes":[{"type":"ATTR_VEC_DISTANCE","fields":["a","b"],"params":{"reference":{"centroid":{"filter":[{"type":"FILTER_EXPRESSION","expression":"a > {{w}}"}]}}}}]}`,
			"sweep.request.attributes[0].params.reference.centroid.filter[0].expression", exprslots.SlotExpression},
		{"feature fit_on",
			`"request":{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"y","fit_on":"wave < {{w}}"}}]}`,
			"sweep.request.features[0].params.fit_on", exprslots.SlotFeatureFitOn},
		{"regression holdout",
			`"request":{"regressions":[{"type":"REG_OLS","holdout":"wave >= {{w}}"}]}`,
			"sweep.request.regressions[0].holdout", exprslots.SlotHoldout},
		{"request overlay formula",
			`"request":{"overlays":[{"kind":"OVERLAY_FORMULA","params":{"formula":"cell * {{w}}"}}]}`,
			"sweep.request.overlays[0].params.formula", exprslots.SlotOverlayFormula},
		{"sweep overlay formula",
			`"request":{"cohort":{"filename":"c{{w}}.pulse"}},"overlays":[{"kind":"OVERLAY_FORMULA","params":{"formula":"slot.a.x * {{w}}"}}]`,
			"sweep.overlays[0].params.formula", exprslots.SlotOverlayFormula},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := decodeComposed(t, `{"requests":[],"sweep":{"axes":[{"name":"w","values":[1,2]}],`+tc.sweep+`}}`)
			preflight := false
			_, err := Expand(c, func(int) error { preflight = true; return nil })
			requireSplice(t, err, errors.PULSE_SWEEP_EXPR_SPLICE, tc.field, "w", tc.slot)
			if preflight {
				t.Fatal("the preflight ran: the refusal must precede any count or substitution")
			}
		})
	}
}

// TestSweepExprSplice_Allowed: what stays legal — an axis token in a
// label or any other non-expression string, a `{"$var": …}` axis marker
// on a request param read as params.<name> or on an operator param, an
// axis supplying the WHOLE expression through a marker, the `{{{{`
// escape inside an expression, and a request params entry sharing an
// expression slot's key.
func TestSweepExprSplice_Allowed(t *testing.T) {
	cases := map[string]string{
		"label and cohort": `"label":"w{{w}}","request":{"cohort":{"filename":"c{{w}}.pulse"}}`,
		"request params marker": `"request":{"params":{"w":{"$var":"w"}},` +
			`"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.w"}]}`,
		"operator params marker":          `"request":{"windows":[{"type":"WIN_EWMA","field":"x","label":"e","order_by":[{"field":"t"}],"params":{"decay":{"$var":"w"}}}]}`,
		"whole-expression marker":         `"request":{"filterers":[{"type":"FILTER_EXPRESSION","expression":{"$var":"w"}}]}`,
		"escape in an expression":         `"label":"l{{w}}","request":{"filterers":[{"type":"FILTER_EXPRESSION","expression":"name == '{{{{w}}'"}]}`,
		"params entry sharing a slot key": `"request":{"params":{"expression":"{{w}}"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			vals := `[1,2]`
			if name == "whole-expression marker" {
				vals = `["wave == 1","wave == 2"]`
			}
			c := decodeComposed(t, `{"requests":[],"sweep":{"axes":[{"name":"w","values":`+vals+`}],`+body+`}}`)
			exp, err := Expand(c, nil)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if len(exp.Composed.Requests) != 2 {
				t.Fatalf("expanded %d slots, want 2", len(exp.Composed.Requests))
			}
		})
	}
	c := decodeComposed(t, `{"requests":[],"sweep":{"axes":[{"name":"w","values":["wave == 1","wave == 2"]}],`+
		`"request":{"filterers":[{"type":"FILTER_EXPRESSION","expression":{"$var":"w"}}]}}}`)
	if got := mustExpand(t, c).Composed.Requests[1].Filterers[0].Expression; got != "wave == 2" {
		t.Fatalf("whole-expression slot 1 = %q", got)
	}
	c = decodeComposed(t, `{"requests":[],"sweep":{"axes":[{"name":"w","values":[1]}],"label":"l{{w}}",`+
		`"request":{"filterers":[{"type":"FILTER_EXPRESSION","expression":"name == '{{{{w}}'"}]}}}`)
	if got := mustExpand(t, c).Composed.Requests[0].Filterers[0].Expression; got != "name == '{{w}}'" {
		t.Fatalf("escaped expression = %q", got)
	}
}

// TestForEachExprSplice_Refused: an `{{axis}}` token inside every
// expression slot kind a window, feature or attribute item can carry is
// PULSE_FOR_EACH_EXPR_SPLICE, request-rooted, before the preflight.
func TestForEachExprSplice_Refused(t *testing.T) {
	const fe = `"for_each":{"axes":[{"name":"k","values":[2,3]}]}`
	cases := []struct {
		name, req, field, slot string
	}{
		{"formula attribute",
			`{"attributes":[{` + fe + `,"type":"ATTR_FORMULA","label":"x{{k}}","expression":"week * {{k}}"}]}`,
			"attributes[0].expression", exprslots.SlotExpression},
		{"total() argument",
			`{"attributes":[{"type":"ATTR_ZSCORE","field":"a"},{` + fe + `,"type":"ATTR_FORMULA","label":"x{{k}}","expression":"week / total(week * {{k}})"}]}`,
			"attributes[1].expression", exprslots.SlotExpression},
		{"attribute fit_on filterer",
			`{"attributes":[{` + fe + `,"type":"ATTR_MAHALANOBIS","label":"m{{k}}","fields":["a","b"],"params":{"fit_on":[{"type":"FILTER_EXPRESSION","expression":"a > {{k}}"}]}}]}`,
			"attributes[0].params.fit_on[0].expression", exprslots.SlotExpression},
		{"centroid filter",
			`{"attributes":[{` + fe + `,"type":"ATTR_VEC_DISTANCE","label":"d{{k}}","fields":["a","b"],"params":{"reference":{"centroid":{"filter":[{"type":"FILTER_EXPRESSION","expression":"a > {{k}}"}]}}}}]}`,
			"attributes[0].params.reference.centroid.filter[0].expression", exprslots.SlotExpression},
		{"feature fit_on",
			`{"features":[{` + fe + `,"type":"FEAT_TARGET_ENCODE","field":"g","label":"te{{k}}","params":{"target":"y","fit_on":"wave < {{k}}"}}]}`,
			"features[0].params.fit_on", exprslots.SlotFeatureFitOn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preflight := false
			_, _, err := ExpandItems(decodeRequest(t, tc.req), func(int) error { preflight = true; return nil })
			requireSplice(t, err, errors.PULSE_FOR_EACH_EXPR_SPLICE, tc.field, "k", tc.slot)
			if preflight {
				t.Fatal("the preflight ran: the refusal must precede any count or substitution")
			}
		})
	}
}

// TestForEachExprSplice_Allowed: axis tokens in labels and fields, a
// `$var` axis marker on an operator param, an axis supplying a whole
// params-borne expression (a feature's fit_on) through a marker, and an
// attribute's params.fit_on keyword (not an expression slot) all
// expand. (An item's own `expression` is a string slot, so a marker
// cannot supply it: there the alternative is a Compose sweep reading
// params.<name>.)
func TestForEachExprSplice_Allowed(t *testing.T) {
	r := decodeRequest(t, `{
		"windows": [{"for_each": {"axes": [{"name": "d", "values": [0.3, 0.5]}]}, "type": "WIN_EWMA", "field": "x",
			"label": "x_{{d}}", "order_by": [{"field": "t"}], "params": {"decay": {"$var": "d"}}}],
		"attributes": [
			{"for_each": {"axes": [{"name": "k", "values": [2, 3]}]},
			 "type": "ATTR_FORMULA", "label": "x_by{{k}}", "expression": "week * 2"},
			{"for_each": {"axes": [{"name": "m", "values": ["filtered"]}]},
			 "type": "ATTR_MAHALANOBIS", "label": "m_{{m}}", "fields": ["a", "b"], "params": {"fit_on": "{{m}}"}}
		],
		"features": [{"for_each": {"axes": [{"name": "s", "values": [1, 5]}, {"name": "f", "values": ["wave < 2", "wave < 3"]}], "mode": "zip"},
			"type": "FEAT_TARGET_ENCODE", "field": "g", "label": "te{{s}}",
			"params": {"target": "y", "smoothing": {"$var": "s"}, "fit_on": {"$var": "f"}}}]
	}`)
	out, _ := mustExpandItems(t, r)
	if len(out.Windows) != 2 || len(out.Attributes) != 3 || len(out.Features) != 2 {
		t.Fatalf("windows %d attributes %d features %d", len(out.Windows), len(out.Attributes), len(out.Features))
	}
	if out.Attributes[1].Label != "x_by3" || string(out.Attributes[2].Params) != `{"fit_on":"filtered"}` {
		t.Fatalf("attributes = %+v %+v", out.Attributes[1], out.Attributes[2])
	}
	if got := string(out.Features[1].Params); got != `{"fit_on":"wave < 3","smoothing":5,"target":"y"}` {
		t.Fatalf("feature 1 params = %s", got)
	}
}
