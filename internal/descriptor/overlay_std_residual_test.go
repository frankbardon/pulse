package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlay_StdResidual: the predict contract of
// OVERLAY_STD_RESIDUAL — a CELL-scoped, implicit-margin crosstab kind
// with Level / Within pinned to 0 (the runtime twin is
// processing.validateOverlayLevelWithinRuntime) — one row per rule.
func TestValidateOverlay_StdResidual(t *testing.T) {
	data := buildTestPulseFile(t, overlayPredictSchema(t))
	ok := types.OverlaySpec{Name: "sr", Kind: types.OverlayKindStdResidual, Scope: types.OverlayScopeCell}
	with := func(edit func(*types.OverlaySpec)) types.OverlaySpec {
		s := ok
		edit(&s)
		return s
	}
	cases := []struct {
		name     string
		crosstab bool
		spec     types.OverlaySpec
		want     errors.Code // "" = no overlay error
	}{
		{"happy path", true, ok, ""},
		{"ref margin", true, with(func(s *types.OverlaySpec) {
			s.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow}
		}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"ref prior", true, with(func(s *types.OverlaySpec) { s.Ref.Prior = &types.OverlayPriorRef{} }), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"no crosstab", false, ok, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"scope matrix", true, with(func(s *types.OverlaySpec) { s.Scope = types.OverlayScopeMatrix }), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"scope row", true, with(func(s *types.OverlaySpec) { s.Scope = types.OverlayScopeRow }), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"level", true, with(func(s *types.OverlaySpec) { s.Level = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{"within", true, with(func(s *types.OverlaySpec) { s.Within = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
	}
	overlayCodes := []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Overlays: []types.OverlaySpec{c.spec}}
			if c.crosstab {
				req.Crosstab = crosstabHostSpec()
			} else {
				req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
				req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}
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
