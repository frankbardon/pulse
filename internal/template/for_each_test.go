package template_test

import (
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/template"
)

// TestRenderJSON_ForEachItemAxesPassThrough is the for_each composition
// rule: inside an item carrying a `for_each`, a `{{axis}}` token or a
// {"$var": "axis"} marker naming one of THAT item's axes is left for the
// item's own expansion, while template variables substitute everywhere —
// beside the axis placeholders, and inside the for_each value itself.
func TestRenderJSON_ForEachItemAxesPassThrough(t *testing.T) {
	runRenderRows(t, []renderRow{
		{
			name: "axis placeholders pass through; template variables substitute beside them and in the axis list",
			vars: decls(varDecl("prefix", template.VarString), listDecl("decays", template.VarNumber), varDecl("metric", template.VarField)),
			body: `{"windows": [{
				"type": "WIN_EWMA", "field": {"$var": "metric"}, "label": "{{prefix}}_ad{{d}}",
				"order_by": [{"field": "week"}],
				"params": {"decay": {"$var": "decay"}},
				"for_each": {"axes": [{"name": "d", "values": [30, 50, 70]}, {"name": "decay", "values": {"$var": "decays"}}], "mode": "zip"}
			}]}`,
			supplied: map[string]any{"prefix": "x", "decays": []any{0.3, 0.5, 0.7}, "metric": "spend"},
			wantJSON: `{"windows": [{
				"type": "WIN_EWMA", "field": "spend", "label": "x_ad{{d}}",
				"order_by": [{"field": "week"}],
				"params": {"decay": {"$var": "decay"}},
				"for_each": {"axes": [{"name": "d", "values": [30, 50, 70]}, {"name": "decay", "values": [0.3, 0.5, 0.7]}], "mode": "zip"}
			}]}`,
		},
		{
			name:     "a spaced axis token passes through in its canonical spelling",
			vars:     decls(),
			body:     `{"features": [{"type": "FEAT_LOG", "field": "{{ ch }}_spend", "for_each": {"axes": [{"name": "ch", "values": ["tv"]}]}}]}`,
			wantJSON: `{"features": [{"type": "FEAT_LOG", "field": "{{ch}}_spend", "for_each": {"axes": [{"name": "ch", "values": ["tv"]}]}}]}`,
		},
		{
			name:     "the doubled-brace escape still renders a literal opener inside an item",
			vars:     decls(),
			body:     `{"features": [{"type": "FEAT_LOG", "field": "{{{{ch}}_spend", "for_each": {"axes": [{"name": "ch", "values": ["tv"]}]}}]}`,
			wantJSON: `{"features": [{"type": "FEAT_LOG", "field": "{{ch}}_spend", "for_each": {"axes": [{"name": "ch", "values": ["tv"]}]}}]}`,
		},
		{
			name: "an axis is scoped to its own item: a sibling item naming it is an undeclared reference",
			vars: decls(),
			body: `{"features": [
				{"type": "FEAT_LOG", "field": "{{ch}}_spend", "for_each": {"axes": [{"name": "ch", "values": ["tv"]}]}},
				{"type": "FEAT_LOG", "field": "{{ch}}_reach"}
			]}`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "ch",
		},
		{
			name:     "the for_each value is outside the axis scope: an axis token inside it is undeclared",
			vars:     decls(),
			body:     `{"features": [{"type": "FEAT_LOG", "field": "{{ch}}", "for_each": {"axes": [{"name": "ch", "values": ["{{ch}}"]}]}}]}`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "ch",
		},
		{
			name:     "an axis name written as an interpolation is refused",
			vars:     decls(varDecl("n", template.VarString)),
			body:     `{"features": [{"type": "FEAT_LOG", "field": "x", "for_each": {"axes": [{"name": "{{n}}", "values": ["tv"]}]}}]}`,
			supplied: map[string]any{"n": "ch"},
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "an axis name written as a marker is refused",
			vars:     decls(varDecl("n", template.VarString)),
			body:     `{"features": [{"type": "FEAT_LOG", "field": "x", "for_each": {"axes": [{"name": {"$var": "n"}, "values": ["tv"]}]}}]}`,
			supplied: map[string]any{"n": "ch"},
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "a template variable sharing an axis name is an ambiguity, refused",
			vars:     decls(varDecl("ch", template.VarString)),
			body:     `{"features": [{"type": "FEAT_LOG", "field": "{{ch}}_spend", "for_each": {"axes": [{"name": "ch", "values": ["tv"]}]}}]}`,
			supplied: map[string]any{"ch": "radio"},
			wantCode: perr.PULSE_TEMPLATE_AXIS_COLLISION,
			wantVar:  "ch",
		},
	})
}

// TestValidate_ForEachAxisCollisionIsDeclarationTime: the collision is an
// author error, so it fails Validate — the fail-fast-at-load promise —
// and carries the axis name's body path. It fires even when the
// colliding variable is never referenced.
func TestValidate_ForEachAxisCollisionIsDeclarationTime(t *testing.T) {
	tm := renderTmpl(decls(varDecl("decay", template.VarNumber)),
		`{"windows": [{"type": "WIN_EWMA", "field": "x", "params": {"decay": {"$var": "decay"}},
			"for_each": {"axes": [{"name": "decay", "values": [0.3]}]}}]}`)
	err := template.Validate(tm)
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_AXIS_COLLISION) {
		t.Fatalf("Validate = %v, want PULSE_TEMPLATE_AXIS_COLLISION", err)
	}
	ce := codedError(t, err)
	if got := ce.Details["path"]; got != "body.windows[0].for_each.axes[0].name" {
		t.Errorf("details.path = %v, want the axis name's body path", got)
	}

	unused := renderTmpl(decls(varDecl("ch", template.VarString)),
		`{"features": [{"type": "FEAT_LOG", "field": "{{ch}}_spend", "for_each": {"axes": [{"name": "ch", "values": ["tv"]}]}}]}`)
	if err := template.Validate(unused); !perr.HasCode(err, perr.PULSE_TEMPLATE_AXIS_COLLISION) {
		t.Fatalf("Validate (colliding variable referenced only as the axis) = %v, want PULSE_TEMPLATE_AXIS_COLLISION", err)
	}
}
