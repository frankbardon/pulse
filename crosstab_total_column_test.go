package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCrosstab_TotalColumnPredictMatchesRuntime pins predict ≡ runtime
// for a crosstab with no column grouper (the implicit Total column, U47
// #326 / #328), through the facade on both dispatch arms: predict accepts
// exactly what Process runs, refuses with the same code what Process
// refuses, and reports the fused verdict the runtime acts on.
func TestCrosstab_TotalColumnPredictMatchesRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	ctx := context.Background()
	zero := 0
	xtab := func(mut func(*types.CrosstabSpec)) *types.Request {
		spec := &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n", Label: "c"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Shape:   types.CrosstabShapeMatrix,
		}
		if mut != nil {
			mut(spec)
		}
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Crosstab: spec}
	}
	cases := []struct {
		name string
		req  *types.Request
		code errors.Code // "" = accepted
	}{
		{"absent columns", xtab(nil), ""},
		{"empty columns", xtab(func(s *types.CrosstabSpec) { s.Columns = []*types.Group{} }), ""},
		{"column percent", xtab(func(s *types.CrosstabSpec) { s.Normalize = types.CrosstabNormalizeColumn }), ""},
		{"row percent", xtab(func(s *types.CrosstabSpec) { s.Normalize = types.CrosstabNormalizeRow }), ""},
		{"long shape", xtab(func(s *types.CrosstabSpec) { s.Shape = types.CrosstabShapeLong }), ""},
		{"normalize_level on the absent column axis", xtab(func(s *types.CrosstabSpec) {
			s.Normalize, s.NormalizeLevel = types.CrosstabNormalizeColumn, &zero
		}), errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE},
		{"normalize_within on the absent column axis", xtab(func(s *types.CrosstabSpec) {
			s.Normalize, s.NormalizeWithin = types.CrosstabNormalizeRow, &zero
		}), errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE},
		{"missing rows still refused", xtab(func(s *types.CrosstabSpec) { s.Rows = nil }), errors.PULSE_CROSSTAB_EMPTY_ROWS},
	}
	for _, disable := range []bool{false, true} {
		t.Run(fmt.Sprintf("disable_fusion_%v", disable), func(t *testing.T) {
			p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disable})
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					env := predictEnvelope(t, p, fs, cohort, c.req)
					resp, perr := p.Process(ctx, c.req)
					if c.code == "" {
						if len(env.Errors) != 0 {
							t.Fatalf("predict refused a no-column crosstab: %q", envCodes(env))
						}
						if perr != nil {
							t.Fatalf("runtime refused what predict accepts: %v", perr)
						}
						res := env.Data.(*descriptor.PredictResult)
						if res.CrosstabFusable == nil || *res.CrosstabFusable == disable {
							t.Errorf("CrosstabFusable = %v, want %v", res.CrosstabFusable, !disable)
						}
						if m := resp.Crosstab.Matrix; m != nil {
							if !reflect.DeepEqual(m.ColumnKeys, []types.AxisKey{{types.CrosstabTotalColumnKey}}) {
								t.Errorf("ColumnKeys = %v, want [[Total]]", m.ColumnKeys)
							}
						}
						return
					}
					found := false
					for _, e := range env.Errors {
						found = found || e.Code == string(c.code)
					}
					if !found {
						t.Errorf("predict errors = %q, want %q", envCodes(env), c.code)
					}
					if !errors.HasCode(perr, c.code) {
						t.Errorf("runtime err = %v, want %s", perr, c.code)
					}
				})
			}
		})
	}
}

// TestCrosstab_TotalColumnOverlaysPredictMatchesRuntime pins predict ≡
// runtime for overlays over the implicit Total column: predict reads the
// spec (no column grouper) while runtime reads the materialised host (one
// "Total" column under a TOTAL header), so every axis-shape rule must
// agree on what that column is — a one-position axis for Level / Within
// and bucket counts, a zero-grouper axis for the square-host arity rule,
// and the one known key "Total" for the rake target check.
func TestCrosstab_TotalColumnOverlaysPredictMatchesRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	ctx := context.Background()
	xtab := func(norm types.CrosstabNormalize, ov types.OverlaySpec) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Crosstab: &types.CrosstabSpec{
				Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "n", Label: "c"},
				Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				Shape:     types.CrosstabShapeMatrix,
				Normalize: norm,
			},
			Overlays: []types.OverlaySpec{ov},
		}
	}
	none, col := types.CrosstabNormalizeNone, types.CrosstabNormalizeColumn
	cases := []struct {
		name string
		req  *types.Request
		code errors.Code // "" = accepted
	}{
		{"share of column", xtab(none, types.OverlaySpec{Kind: types.OverlayKindShareOfCol, Scope: types.OverlayScopeCell, Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}}}), ""},
		{"share of column level past the one position", xtab(none, types.OverlaySpec{Kind: types.OverlayKindShareOfCol, Scope: types.OverlayScopeCell, Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}}, Level: 1}),
			errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{"share of row within past the one position", xtab(none, types.OverlaySpec{Kind: types.OverlayKindShareOfRow, Scope: types.OverlayScopeCell, Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}, Within: 1}),
			errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{"markov needs a square host", xtab(none, types.OverlaySpec{Kind: types.OverlayKindMarkov, Scope: types.OverlayScopeMatrix}), errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"retention needs a square host", xtab(none, types.OverlaySpec{Kind: types.OverlayKindRetention, Scope: types.OverlayScopeRow}), errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"symmetry needs a square host", xtab(none, types.OverlaySpec{Kind: types.OverlayKindSymmetry, Scope: types.OverlayScopeMatrix}), errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"rake onto the Total key", xtab(none, types.OverlaySpec{Kind: types.OverlayKindRake, Scope: types.OverlayScopeMatrix,
			Params: json.RawMessage(`{"row_targets":{"a":14,"b":13,"c":13},"col_targets":{"Total":40}}`)}), ""},
		{"rake onto a key the Total column lacks", xtab(none, types.OverlaySpec{Kind: types.OverlayKindRake, Scope: types.OverlayScopeMatrix,
			Params: json.RawMessage(`{"row_targets":{"a":14,"b":13,"c":13},"col_targets":{"x":40}}`)}), errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH},
		{"correspondence over one column", xtab(none, types.OverlaySpec{Kind: types.OverlayKindCorrespondence, Scope: types.OverlayScopeMatrix}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"chi-square over one column", xtab(none, types.OverlaySpec{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"per-row chi-square over one column", xtab(none, types.OverlaySpec{Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"adjusted residuals over one column", xtab(none, types.OverlaySpec{Kind: types.OverlayKindStdResidual, Scope: types.OverlayScopeCell}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"pairwise rows", xtab(col, types.OverlaySpec{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow}), ""},
	}
	for _, disable := range []bool{false, true} {
		t.Run(fmt.Sprintf("disable_fusion_%v", disable), func(t *testing.T) {
			p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disable})
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					env := predictEnvelope(t, p, fs, cohort, c.req)
					_, perr := p.Process(ctx, c.req)
					var rtCode string
					if perr != nil {
						var ce *errors.CodedError
						if !stderrors.As(perr, &ce) {
							t.Fatalf("runtime error is uncoded: %v", perr)
						}
						rtCode = string(ce.Code)
					}
					predicted := map[string]bool{}
					var rtMessage string
					if perr != nil {
						var ce *errors.CodedError
						if stderrors.As(perr, &ce) {
							rtMessage = ce.Message
						}
					}
					messageMatched := false
					for _, e := range env.Errors {
						predicted[e.Code] = true
						messageMatched = messageMatched || (e.Code == rtCode && e.Message == rtMessage)
					}
					// The two-column refusal is the one shared helper on both
					// sides (overlayhost.TotalColumnRefusal), raised ahead of
					// the handler's own count guard: same words, not merely
					// the same code. (The Level / Within twins word the axis
					// differently by design.)
					if rtCode == string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) && !messageMatched {
						t.Errorf("runtime message %q differs from predict's %q", rtMessage, envCodes(env))
					}
					switch {
					case rtCode == "" && len(env.Errors) != 0:
						t.Fatalf("predict refused %q what runtime runs", envCodes(env))
					case rtCode != "" && !predicted[rtCode]:
						t.Fatalf("runtime refused %s (%v); predict said %q", rtCode, perr, envCodes(env))
					}
					// Pin the expected verdict where the case names a code.
					if c.code == "" && rtCode != "" {
						t.Errorf("refused %s, want accepted", rtCode)
					}
					if c.code != "" && rtCode != string(c.code) {
						t.Errorf("code = %q, want %s", rtCode, c.code)
					}
				})
			}
		})
	}
}

// TestCrosstab_TotalColumnPValueCount pins predict's p-value count over
// the implicit Total column: a row-scope pairwise layer carries one p per
// row pair per column, and the one Total column makes that the pair count
// exactly as the runtime layer emits it.
func TestCrosstab_TotalColumnPValueCount(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	req := &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Crosstab: &types.CrosstabSpec{
			Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "n", Label: "c"},
			Shape:     types.CrosstabShapeMatrix,
			Normalize: types.CrosstabNormalizeColumn,
		},
		Overlays: []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow}},
	}
	res := predictEnvelope(t, p, fs, cohort, req).Data.(*descriptor.PredictResult)
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	pm := resp.Overlays[0].Payload.Matrix
	emitted := len(pm.RowKeys) * len(pm.ColumnKeys)
	if res.PValues == nil || res.PValues.Total != emitted || emitted != 3 {
		t.Errorf("predict p_values = %+v, runtime emits %d pair p-values (want 3)", res.PValues, emitted)
	}
}
