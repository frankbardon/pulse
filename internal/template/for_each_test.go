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

// TestRenderJSON_SweepAxesPassThrough is the sweep composition rule: a
// `sweep` object's axis names scope over its `request`, `overlays` and
// `label` — the parts the sweep expansion substitutes per slot — and
// nowhere else. Template variables substitute beside the axis
// placeholders and inside `axes` itself (so a variable supplies an
// axis's values); an undeclared non-axis placeholder still refuses.
func TestRenderJSON_SweepAxesPassThrough(t *testing.T) {
	runRenderRows(t, []renderRow{
		{
			name: "axis placeholders in request, overlays and label pass through; variables substitute beside them and in axes",
			vars: decls(varDecl("metric", template.VarField), listDecl("channels", template.VarString), varDecl("base", template.VarString)),
			body: `{"requests": [{"label": "{{base}}", "cohort": {"filename": "c.pulse"}}],
				"sweep": {
					"axes": [{"name": "ch", "values": {"$var": "channels"}}, {"name": "k", "values": [1, 2]}],
					"label": "{{ ch }}_k{{k}}",
					"request": {"cohort": {"filename": "c.pulse"}, "aggregations": [{"type": "AGG_SUM", "field": {"$var": "metric"}, "label": "{{ch}}"}],
						"groups": [{"type": "GROUP_RANGE", "field": "x", "interval": {"$var": "k"}}]},
					"overlays": [{"kind": "OVERLAY_DELTA_VS_REF", "reference": "{{base}}", "targets": ["{{ch}}_k{{k}}"]}]
				}}`,
			supplied: map[string]any{"metric": "spend", "channels": []any{"tv", "radio"}, "base": "all"},
			wantJSON: `{"requests": [{"label": "all", "cohort": {"filename": "c.pulse"}}],
				"sweep": {
					"axes": [{"name": "ch", "values": ["tv", "radio"]}, {"name": "k", "values": [1, 2]}],
					"label": "{{ch}}_k{{k}}",
					"request": {"cohort": {"filename": "c.pulse"}, "aggregations": [{"type": "AGG_SUM", "field": "spend", "label": "{{ch}}"}],
						"groups": [{"type": "GROUP_RANGE", "field": "x", "interval": {"$var": "k"}}]},
					"overlays": [{"kind": "OVERLAY_DELTA_VS_REF", "reference": "all", "targets": ["{{ch}}_k{{k}}"]}]
				}}`,
		},
		{
			name:     "a sweep axis does not scope over the composed request's explicit slots",
			body:     `{"requests": [{"label": "{{ch}}"}], "sweep": {"axes": [{"name": "ch", "values": ["tv"]}], "request": {"label": "{{ch}}"}}}`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "ch",
		},
		{
			name:     "a sweep axis does not scope over the sweep's own axes or rank",
			body:     `{"sweep": {"axes": [{"name": "ch", "values": ["{{ch}}"]}], "request": {"label": "{{ch}}"}}}`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "ch",
		},
		{
			name:     "an undeclared non-axis placeholder inside the sweep request still refuses",
			body:     `{"sweep": {"axes": [{"name": "ch", "values": ["tv"]}], "request": {"label": "{{ch}}_{{nope}}"}}}`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "nope",
		},
		{
			name:     "a sweep axis name spelled as a token is not literal",
			vars:     decls(varDecl("n", template.VarString)),
			body:     `{"sweep": {"axes": [{"name": "{{n}}", "values": ["tv"]}], "request": {"label": "x"}}}`,
			supplied: map[string]any{"n": "ch"},
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "a sweep axis name spelled as a marker is not literal",
			vars:     decls(varDecl("n", template.VarString)),
			body:     `{"sweep": {"axes": [{"name": {"$var": "n"}, "values": ["tv"]}], "request": {"label": "x"}}}`,
			supplied: map[string]any{"n": "ch"},
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "a declared variable named like a sweep axis collides",
			vars:     decls(varDecl("ch", template.VarString)),
			body:     `{"sweep": {"axes": [{"name": "ch", "values": ["tv"]}], "request": {"label": "{{ch}}"}}}`,
			supplied: map[string]any{"ch": "radio"},
			wantCode: perr.PULSE_TEMPLATE_AXIS_COLLISION,
			wantVar:  "ch",
		},
	})
}

// TestValidate_SweepAxisCollisionIsDeclarationTime: a sweep axis
// collision is caught by Validate, carrying the axis name's body path.
func TestValidate_SweepAxisCollisionIsDeclarationTime(t *testing.T) {
	tm := renderTmpl(decls(varDecl("ch", template.VarString)),
		`{"sweep": {"axes": [{"name": "k", "values": [1]}, {"name": "ch", "values": ["tv"]}], "request": {"label": "{{k}}"}}}`)
	err := template.Validate(tm)
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_AXIS_COLLISION) {
		t.Fatalf("Validate = %v, want PULSE_TEMPLATE_AXIS_COLLISION", err)
	}
	if got := codedError(t, err).Details["path"]; got != "body.sweep.axes[1].name" {
		t.Errorf("details.path = %v, want the sweep axis name's body path", got)
	}
}
