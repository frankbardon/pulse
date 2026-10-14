package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlay_Correspondence: the predict contract of
// OVERLAY_CORRESPONDENCE — a MATRIX-scoped, implicit-margin crosstab
// kind over a non-negative-mass cell, Level / Within pinned to 0,
// strict params, and an explicit dimensions capped at min(r, c) − 1
// when the axes' dictionaries fix the extent (the fixture's region x
// segment host is 2 x 2: one non-trivial dimension) — one row per rule.
// The runtime twins are processing.applyCorrespondence and
// validateOverlayLevelWithinRuntime, through the same overlayhost
// refusals.
func TestValidateOverlay_Correspondence(t *testing.T) {
	data := buildTestPulseFile(t, overlayPredictSchema(t))
	ok := types.OverlaySpec{Name: "ca", Kind: types.OverlayKindCorrespondence, Scope: types.OverlayScopeMatrix}
	with := func(edit func(*types.OverlaySpec)) types.OverlaySpec {
		s := ok
		edit(&s)
		return s
	}
	params := func(raw string) types.OverlaySpec {
		return with(func(s *types.OverlaySpec) { s.Params = json.RawMessage(raw) })
	}
	cases := []struct {
		name     string
		crosstab bool
		cell     types.AggregationType // "" = AGG_COUNT
		spec     types.OverlaySpec
		want     errors.Code // "" = no overlay error
	}{
		{"happy path (default dimensions capped at 1)", true, "", ok, ""},
		{"sum cell", true, types.AGG_SUM, ok, ""},
		{"explicit dimensions at cap", true, "", params(`{"dimensions":1,"normalization":"row_principal"}`), ""},
		{"column_principal", true, "", params(`{"normalization":"column_principal"}`), ""},
		{"dimensions over cap", true, "", params(`{"dimensions":2}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"dimensions zero", true, "", params(`{"dimensions":0}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"dimensions string", true, "", params(`{"dimensions":"1"}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown normalization", true, "", params(`{"normalization":"standard"}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", true, "", params(`{"axes":2}`), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"average cell", true, types.AGG_AVERAGE, ok, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"min cell", true, types.AGG_MIN, ok, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"ref margin", true, "", with(func(s *types.OverlaySpec) {
			s.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow}
		}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"no crosstab", false, "", ok, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"scope cell", true, "", with(func(s *types.OverlaySpec) { s.Scope = types.OverlayScopeCell }), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"level", true, "", with(func(s *types.OverlaySpec) { s.Level = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{"within", true, "", with(func(s *types.OverlaySpec) { s.Within = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
	}
	overlayCodes := []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
		errors.PULSE_OVERLAY_PARAM_MISSING, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED,
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Overlays: []types.OverlaySpec{c.spec}}
			if c.crosstab {
				req.Crosstab = crosstabHostSpec()
				if c.cell != "" {
					req.Crosstab.Cell.Type = c.cell
				}
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

// TestOverlayOutputResolver_Parts: the Interpretation resolver admits a
// declared part's path by the part's shape — cells.value on a MATRIX
// part, summary.<scalar key> (and deferred summary.parameters.<key>) on
// a SERIES part — and refuses an undeclared part, a path of the wrong
// shape and a non-scalar summary key.
func TestOverlayOutputResolver_Parts(t *testing.T) {
	ca := overlayOutputResolver(types.OverlayKindCorrespondence)
	sr := overlayOutputResolver(types.OverlayKindStdResidual)
	for _, c := range []struct {
		name    string
		resolve OutputResolver
		field   string
		want    FieldCheck
	}{
		{"matrix part cells", ca, "parts.row_coordinates.cells.value", FieldStatic},
		{"series part statistic", ca, "parts.row_quality.summary.statistic", FieldStatic},
		{"series part parameter", ca, "parts.column_quality.summary.parameters.mass", FieldDeferred},
		{"series part bare parameters", ca, "parts.column_quality.summary.parameters.", FieldUnknown},
		{"series part non-scalar key", ca, "parts.row_quality.summary.parameters", FieldUnknown},
		{"series part cells", ca, "parts.row_quality.cells.value", FieldUnknown},
		{"matrix part summary", ca, "parts.row_contribution.summary.statistic", FieldUnknown},
		{"distance part cells", ca, "parts.row_distance.cells.value", FieldStatic},
		{"column distance part cells", ca, "parts.column_distance.cells.value", FieldStatic},
		{"distance part summary", ca, "parts.column_distance.summary.statistic", FieldUnknown},
		{"undeclared part", ca, "parts.expected.cells.value", FieldUnknown},
		{"primary series", ca, "summary.statistic", FieldStatic},
		{"primary has no cells", ca, "cells.value", FieldUnknown},
		{"std residual matrix part", sr, "parts.p.cells.value", FieldStatic},
		{"std residual undeclared", sr, "parts.row_quality.summary.statistic", FieldUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, why := c.resolve(c.field); got != c.want {
				t.Errorf("%s: %v (%s), want %v", c.field, got, why, c.want)
			}
		})
	}
}
