package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlay_Retention: the predict contract of
// OVERLAY_RETENTION — a ROW-scoped, implicit-margin crosstab kind over a
// non-negative-mass cell, Level / Within pinned to 0, a strict absorbing
// param and the same grouper count on both axes — one row per rule.
// Whether an absorbing name is a host state is the runtime's (predict
// reads no record), so an absent name passes here. The runtime twins
// are processing.applyRetentionAt and validateOverlayLevelWithinRuntime,
// through the same overlayhost refusals.
func TestValidateOverlay_Retention(t *testing.T) {
	t.Parallel()

	data := buildTestPulseFile(t, overlayPredictSchema(t))
	ok := types.OverlaySpec{Name: "keep", Kind: types.OverlayKindRetention, Scope: types.OverlayScopeRow}
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
		{"absorbing", nil, "", false, params(`{"absorbing":["churned"]}`), ""},
		{"absorbing empty", nil, "", false, params(`{"absorbing":[]}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"absorbing repeated", nil, "", false, params(`{"absorbing":["a","a"]}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", nil, "", false, params(`{"steps":[1]}`), errors.PULSE_REQUEST_UNKNOWN_FIELD},
		{"average cell", nil, types.AGG_AVERAGE, false, ok, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"columns carry two groupers", twoCols, "", false, ok, errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"ref margin", nil, "", false, with(func(s *types.OverlaySpec) {
			s.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow}
		}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"no crosstab", nil, "", true, ok, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"scope matrix", nil, "", false, with(func(s *types.OverlaySpec) { s.Scope = types.OverlayScopeMatrix }), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"level", nil, "", false, with(func(s *types.OverlaySpec) { s.Level = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{"within", nil, "", false, with(func(s *types.OverlaySpec) { s.Within = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
	}
	overlayCodes := []errors.Code{
		errors.PULSE_REQUEST_UNKNOWN_FIELD,
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
