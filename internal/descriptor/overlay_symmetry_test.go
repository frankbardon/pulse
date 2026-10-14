package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlay_Symmetry: the predict contract of OVERLAY_SYMMETRY
// — a MATRIX-scoped, implicit-margin crosstab kind over a non-negative-
// mass cell, Level / Within pinned to 0, a strict boolean correct param
// and the same grouper count on both axes — one row per rule. Which keys
// each axis holds is the runtime's (a one-axis state is a zero row
// there, never a refusal). The runtime twins are
// processing.applySymmetryAt and validateOverlayLevelWithinRuntime,
// through the same overlayhost refusals.
func TestValidateOverlay_Symmetry(t *testing.T) {
	data := buildTestPulseFile(t, overlayPredictSchema(t))
	ok := types.OverlaySpec{Name: "sym", Kind: types.OverlayKindSymmetry, Scope: types.OverlayScopeMatrix}
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
		host func(*types.CrosstabSpec)
		cell types.AggregationType
		none bool
		spec types.OverlaySpec
		want errors.Code
	}{
		{"happy path", nil, "", false, ok, ""},
		{"sum cell", nil, types.AGG_SUM, false, ok, ""},
		{"correct false", nil, "", false, params(`{"correct":false}`), ""},
		{"correct not a boolean", nil, "", false, params(`{"correct":"no"}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", nil, "", false, params(`{"exact":true}`), errors.PULSE_OVERLAY_PARAM_MISSING},
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
