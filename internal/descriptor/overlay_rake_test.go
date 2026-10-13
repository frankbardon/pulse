package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlay_Rake: the predict contract of OVERLAY_RAKE — a
// MATRIX-scoped, implicit-margin crosstab kind over a non-negative-mass
// cell, Level / Within pinned to 0, strict params (counts totals
// included) and, on a categorical axis, every target key a dictionary
// entry — one row per rule. A host key the targets leave out is the
// runtime's call (predict cannot know which keys the records hold), so
// it predicts clean, as does an unknown key on an axis no dictionary
// bounds. The runtime twins are processing.applyRakeAt and
// validateOverlayLevelWithinRuntime, through the same overlayhost
// refusals.
func TestValidateOverlay_Rake(t *testing.T) {
	data := buildTestPulseFile(t, overlayPredictSchema(t))
	const targets = `"row_targets":{"north":0.5,"south":0.5},"col_targets":{"retail":0.6,"wholesale":0.4}`
	ok := types.OverlaySpec{Name: "fit", Kind: types.OverlayKindRake, Scope: types.OverlayScopeMatrix,
		Params: json.RawMessage(`{` + targets + `}`)}
	with := func(edit func(*types.OverlaySpec)) types.OverlaySpec {
		s := ok
		edit(&s)
		return s
	}
	params := func(raw string) types.OverlaySpec {
		return with(func(s *types.OverlaySpec) { s.Params = json.RawMessage(raw) })
	}
	twoCols := func(c *types.CrosstabSpec) {
		c.Columns = append(c.Columns, &types.Group{Type: types.GROUP_CATEGORY, Field: "region"})
	}
	cases := []struct {
		name string
		host func(*types.CrosstabSpec) // nil = the default region x segment crosstab
		cell types.AggregationType     // "" = AGG_COUNT
		none bool                      // no crosstab
		spec types.OverlaySpec
		want errors.Code // "" = no overlay error
	}{
		{"happy path", nil, "", false, ok, ""},
		{"sum cell", nil, types.AGG_SUM, false, ok, ""},
		{"every param", nil, "", false, params(`{"row_targets":{"north":40,"south":60},"col_targets":{"retail":30,"wholesale":70},"targets_as":"counts","max_iter":500,"tolerance":1e-6}`), ""},
		{"host key left out (runtime's call)", nil, "", false, params(`{"row_targets":{"north":1},"col_targets":{"retail":1,"wholesale":1}}`), ""},
		{"unbounded axis takes any key", twoCols, "", false, params(`{"row_targets":{"north":1,"south":1},"col_targets":{"[\"retail\",\"north\"]":1,"anything":1}}`), ""},
		{"row target outside the dictionary", nil, "", false, params(`{"row_targets":{"north":1,"south":1,"mars":1},"col_targets":{"retail":1,"wholesale":1}}`), errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH},
		{"column target outside the dictionary", nil, "", false, params(`{"row_targets":{"north":1,"south":1},"col_targets":{"retail":1,"online":1}}`), errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH},
		{"counts totals differ", nil, "", false, params(`{"row_targets":{"north":40,"south":60},"col_targets":{"retail":30,"wholesale":60},"targets_as":"counts"}`), errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH},
		{"no params", nil, "", false, params(``), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"col targets missing", nil, "", false, params(`{"row_targets":{"north":1}}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"negative target", nil, "", false, params(`{"row_targets":{"north":-1,"south":2},"col_targets":{"retail":1}}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"targets_as unknown", nil, "", false, params(`{` + targets + `,"targets_as":"weights"}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"max_iter zero", nil, "", false, params(`{` + targets + `,"max_iter":0}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"tolerance zero", nil, "", false, params(`{` + targets + `,"tolerance":0}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", nil, "", false, params(`{` + targets + `,"write_weights":true}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"average cell", nil, types.AGG_AVERAGE, false, ok, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"ref margin", nil, "", false, with(func(s *types.OverlaySpec) {
			s.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow}
		}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"no crosstab", nil, "", true, ok, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"scope cell", nil, "", false, with(func(s *types.OverlaySpec) { s.Scope = types.OverlayScopeCell }), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"level", nil, "", false, with(func(s *types.OverlaySpec) { s.Level = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{"within", nil, "", false, with(func(s *types.OverlaySpec) { s.Within = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
	}
	overlayCodes := []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
		errors.PULSE_OVERLAY_PARAM_MISSING, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED,
		errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH,
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Overlays: []types.OverlaySpec{c.spec}}
			if c.none {
				req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
				req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "value", Label: "n"}}
			} else {
				req.Crosstab = crosstabHostSpec()
				if c.cell != "" {
					req.Crosstab.Cell.Type = c.cell
				}
				if c.host != nil {
					c.host(req.Crosstab)
				}
			}
			env := predictFromBytes(data, req, nil)
			for _, code := range overlayCodes {
				if got := hasErrorCode(env, code); got != (code == c.want) {
					t.Errorf("%s present = %v, want %v (errors %+v)", code, got, code == c.want, env.Errors)
				}
			}
		})
	}
}

// TestValidateOverlay_RakeKeyDetails: the dictionary refusal names the
// axis and the offending keys, with an empty host_only side.
func TestValidateOverlay_RakeKeyDetails(t *testing.T) {
	data := buildTestPulseFile(t, overlayPredictSchema(t))
	req := &types.Request{Crosstab: crosstabHostSpec(), Overlays: []types.OverlaySpec{{
		Name: "fit", Kind: types.OverlayKindRake, Scope: types.OverlayScopeMatrix,
		Params: json.RawMessage(`{"row_targets":{"north":1,"south":1},"col_targets":{"retail":1,"wholesale":1,"online":1,"kiosk":1}}`),
	}}}
	env := predictFromBytes(data, req, nil)
	if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH) {
		t.Fatalf("errors %+v", env.Errors)
	}
	d := env.Errors[0].Details
	got, _ := json.Marshal(map[string]any{"axis": d["axis"], "target_only": d["target_only"], "host_only": d["host_only"], "index": d["index"]})
	if string(got) != `{"axis":"column","host_only":[],"index":0,"target_only":["kiosk","online"]}` {
		t.Fatalf("details %s", got)
	}
}

// TestOverlayOutputResolver_RakeParts: the factors MATRIX part
// resolves on cells.value only.
func TestOverlayOutputResolver_RakeParts(t *testing.T) {
	r := overlayOutputResolver(types.OverlayKindRake)
	for field, want := range map[string]FieldCheck{
		"cells.value":                     FieldStatic,
		"parts.factors.cells.value":       FieldStatic,
		"parts.factors.summary.statistic": FieldUnknown,
		"parts.fitted.cells.value":        FieldUnknown,
		"summary.parameters.converged":    FieldDeferred,
		"summary.parameters.iterations":   FieldDeferred,
		"summary.parameters.residual":     FieldDeferred,
	} {
		if got, why := r(field); got != want {
			t.Errorf("%s: %v (%s), want %v", field, got, why, want)
		}
	}
}
