package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlay_Markov: the predict contract of OVERLAY_MARKOV — a
// MATRIX-scoped, implicit-margin crosstab kind over a non-negative-mass
// cell, Level / Within pinned to 0, strict params and the same grouper
// count on both axes (the square-host rule the request shape decides) —
// one row per rule. State keys (absorbing, initial) and the key-set
// equality are runtime-only: predict reads no record, so a key the
// fixture never holds still predicts clean. The runtime twins are
// processing.applyMarkovAt and validateOverlayLevelWithinRuntime,
// through the same overlayhost refusals.
func TestValidateOverlay_Markov(t *testing.T) {
	data := buildTestPulseFile(t, overlayPredictSchema(t))
	ok := types.OverlaySpec{Name: "flow", Kind: types.OverlayKindMarkov, Scope: types.OverlayScopeMatrix}
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
		host func(*types.CrosstabSpec) // nil = the default 1 x 1-grouper crosstab
		cell types.AggregationType     // "" = AGG_COUNT
		none bool                      // no crosstab
		spec types.OverlaySpec
		want errors.Code // "" = no overlay error
	}{
		{"happy path", nil, "", false, ok, ""},
		{"sum cell", nil, types.AGG_SUM, false, ok, ""},
		{"every param", nil, "", false, params(`{"steps":[1,24],"absorbing":["churned"],"initial":{"gold":1}}`), ""},
		{"initial margin", nil, "", false, params(`{"initial":"margin"}`), ""},
		{"step over 1000", nil, "", false, params(`{"steps":[1001]}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"step zero", nil, "", false, params(`{"steps":[0]}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"absorbing empty", nil, "", false, params(`{"absorbing":[]}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"initial unknown form", nil, "", false, params(`{"initial":"uniform"}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", nil, "", false, params(`{"horizon":12}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"average cell", nil, types.AGG_AVERAGE, false, ok, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"columns carry two groupers", twoCols, "", false, ok, errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
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
		errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE,
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Overlays: []types.OverlaySpec{c.spec}}
			if c.none {
				req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
				req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}
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

// TestOverlayOutputResolver_MarkovParts: the fixed-name SERIES parts
// resolve; the per-step parts (templates named by params.steps) are
// declared for the manifest only, so an Interpretation cannot address
// them — neither a concrete step_1 nor the template itself.
func TestOverlayOutputResolver_MarkovParts(t *testing.T) {
	r := overlayOutputResolver(types.OverlayKindMarkov)
	for field, want := range map[string]FieldCheck{
		"cells.value":                              FieldStatic,
		"parts.steady_state.summary.statistic":     FieldStatic,
		"parts.absorption_time.summary.statistic":  FieldStatic,
		"parts.step_1.cells.value":                 FieldUnknown,
		"parts.step_<n>.cells.value":               FieldUnknown,
		"parts.distribution_<n>.summary.statistic": FieldUnknown,
		"parts.steady_state.cells.value":           FieldUnknown,
		"summary.parameters.classes":               FieldDeferred,
	} {
		if got, why := r(field); got != want {
			t.Errorf("%s: %v (%s), want %v", field, got, why, want)
		}
	}
}
