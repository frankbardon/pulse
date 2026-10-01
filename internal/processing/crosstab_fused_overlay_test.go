package processing

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// runFusedCrosstabViaRunner drives the full RunCrosstabFused
// orchestrator (not just FusedCrosstabState) over a slice-backed
// iterator. The overlay fold lives at the orchestrator exit, so the
// overlay parity tests must go through this entry point rather than
// runFusedCrosstab's direct state drive.
func runFusedCrosstabViaRunner(t *testing.T, schema *encoding.Schema,
	req *types.Request, recs []*Record, disableComponents bool,
) (*types.Response, error) {
	t.Helper()
	p := NewProcessor(schema)
	p.SetDisableComponents(disableComponents)
	return p.RunCrosstabFused(context.Background(), req, NewSliceIterator(recs))
}

// runBufferedCrosstabWithComponents is runBufferedCrosstab with the
// components knob exposed so the disabled-components parity case can
// drive both paths identically.
func runBufferedCrosstabWithComponents(t *testing.T, schema *encoding.Schema,
	req *types.Request, recs []*Record, disableComponents bool,
) (*types.Response, error) {
	t.Helper()
	p := NewProcessor(schema)
	p.SetDisableComponents(disableComponents)
	return p.RunCrosstab(context.Background(), req, recs)
}

// jsonOf marshals a value to its wire form so the parity assertions
// compare exactly what an envelope consumer would see — including the
// omitempty-driven nil/empty distinctions reflect.DeepEqual is too
// strict about.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

// TestFusedCrosstab_OverlaysMatchBuffered is the E1-S1 parity check:
// for an overlay-carrying crosstab the gate now admits, the fused
// orchestrator must emit the same Response.Overlays and the same
// Response.Warnings as the buffered orchestrator. Both paths call the
// same applyOverlaysToResponse hook over an equivalent finalised
// matrix, so any divergence here means the fused exit lost the fold or
// the finalised host view differs.
func TestFusedCrosstab_OverlaysMatchBuffered(t *testing.T) {
	schema := crosstabOverlaySchema(t)
	recs := crosstabOverlayRecords(schema)

	cases := []struct {
		name     string
		overlays []types.OverlaySpec
	}{
		{
			name: "index_vs_margin_row",
			overlays: []types.OverlaySpec{{
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
			}},
		},
		{
			name: "share_of_row",
			overlays: []types.OverlaySpec{{
				Kind:  types.OverlayKindShareOfRow,
				Scope: types.OverlayScopeCell,
				Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
			}},
		},
		{
			name: "pairwise_prop_z_row",
			overlays: []types.OverlaySpec{{
				Name:  "pz",
				Kind:  types.OverlayKindPairwisePropZ,
				Scope: types.OverlayScopeRow,
			}},
		},
		{
			name: "two_layers_order_preserved",
			overlays: []types.OverlaySpec{
				{
					Name:  "row_index",
					Kind:  types.OverlayKindIndexVsMargin,
					Scope: types.OverlayScopeCell,
					Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
				},
				{
					Name:  "col_index",
					Kind:  types.OverlayKindIndexVsMargin,
					Scope: types.OverlayScopeCell,
					Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The gate must accept the request in the first place —
			// otherwise the parity below would be vacuous (both sides
			// buffered in production).
			gateReq := crosstabOverlayBaseRequest()
			gateReq.Overlays = tc.overlays
			if ok, reason := CanFuseCrosstab(gateReq, schema, nil); !ok {
				t.Fatalf("CanFuseCrosstab rejected overlay request: %s", reason)
			}

			bufReq := crosstabOverlayBaseRequest()
			bufReq.Overlays = tc.overlays
			bufResp, err := runBufferedCrosstabWithComponents(t, schema, bufReq, recs, false)
			if err != nil {
				t.Fatalf("buffered RunCrosstab: %v", err)
			}

			fusedReq := crosstabOverlayBaseRequest()
			fusedReq.Overlays = tc.overlays
			fusedResp, err := runFusedCrosstabViaRunner(t, schema, fusedReq, recs, false)
			if err != nil {
				t.Fatalf("RunCrosstabFused: %v", err)
			}

			if len(fusedResp.Overlays) != len(tc.overlays) {
				t.Fatalf("fused Overlays = %d layers, want %d",
					len(fusedResp.Overlays), len(tc.overlays))
			}
			for i, spec := range tc.overlays {
				if got := fusedResp.Overlays[i].Kind; got != spec.Kind {
					t.Errorf("layer[%d].Kind = %q, want %q (order not preserved)",
						i, got, spec.Kind)
				}
			}

			// The host matrix must match first — an overlay diff on top
			// of a matrix diff would be misleading.
			assertMatrixEqual(t, bufResp.Crosstab.Matrix, fusedResp.Crosstab.Matrix)

			if want, got := jsonOf(t, bufResp.Overlays), jsonOf(t, fusedResp.Overlays); want != got {
				t.Errorf("Overlays diverge:\nbuffered: %s\nfused:    %s", want, got)
			}
			if want, got := jsonOf(t, bufResp.Warnings), jsonOf(t, fusedResp.Warnings); want != got {
				t.Errorf("Warnings diverge:\nbuffered: %s\nfused:    %s", want, got)
			}
		})
	}
}

// TestFusedCrosstab_OverlayWarningsPromotedLikeBuffered drives a zero
// row margin so the INDEX_VS_MARGIN handler emits an OverlayWarning,
// and pins that the fused exit promotes it to Response.Warnings exactly
// as the buffered exit does. The promotion lives inside the shared
// hook, so this asserts the fused path really routes through it rather
// than reconstructing layers on its own.
func TestFusedCrosstab_OverlayWarningsPromotedLikeBuffered(t *testing.T) {
	schema := crosstabOverlaySchema(t)
	mk := func(row, col uint64, value float64) *Record {
		return NewRecord(schema, map[string]float64{
			"row":   float64(row),
			"col":   float64(col),
			"value": value,
		})
	}
	// Row r0 sums to zero on every column, so the row margin is 0 and
	// the index handler has no denominator.
	recs := []*Record{
		mk(0, 0, 0),
		mk(0, 1, 0),
		mk(1, 0, 10),
		mk(1, 1, 20),
	}

	overlays := []types.OverlaySpec{{
		Kind:  types.OverlayKindIndexVsMargin,
		Scope: types.OverlayScopeCell,
		Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
	}}

	bufReq := crosstabOverlayBaseRequest()
	bufReq.Overlays = overlays
	bufResp, err := runBufferedCrosstabWithComponents(t, schema, bufReq, recs, false)
	if err != nil {
		t.Fatalf("buffered RunCrosstab: %v", err)
	}

	fusedReq := crosstabOverlayBaseRequest()
	fusedReq.Overlays = overlays
	fusedResp, err := runFusedCrosstabViaRunner(t, schema, fusedReq, recs, false)
	if err != nil {
		t.Fatalf("RunCrosstabFused: %v", err)
	}

	if len(bufResp.Warnings) == 0 {
		t.Fatalf("fixture no longer produces a buffered overlay warning; " +
			"pick a different degenerate case")
	}
	if want, got := jsonOf(t, bufResp.Warnings), jsonOf(t, fusedResp.Warnings); want != got {
		t.Fatalf("promoted warnings diverge:\nbuffered: %s\nfused:    %s", want, got)
	}
	if want, got := jsonOf(t, bufResp.Overlays), jsonOf(t, fusedResp.Overlays); want != got {
		t.Fatalf("Overlays diverge:\nbuffered: %s\nfused:    %s", want, got)
	}
}

// TestFusedCrosstab_LongShapeOverlayNoOpsLikeBuffered verifies the
// shape=long claim in the story notes rather than re-implementing it:
// with no MatrixPayload the shared hook short-circuits, so neither path
// populates Response.Overlays and the long rows stay equal.
func TestFusedCrosstab_LongShapeOverlayNoOpsLikeBuffered(t *testing.T) {
	schema := crosstabOverlaySchema(t)
	recs := crosstabOverlayRecords(schema)

	overlays := []types.OverlaySpec{{
		Kind:  types.OverlayKindIndexVsMargin,
		Scope: types.OverlayScopeCell,
		Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
	}}

	build := func() *types.Request {
		req := crosstabOverlayBaseRequest()
		req.Crosstab.Shape = types.CrosstabShapeLong
		req.Crosstab.Margins = types.CrosstabMargins{}
		req.Overlays = overlays
		return req
	}

	bufResp, err := runBufferedCrosstabWithComponents(t, schema, build(), recs, false)
	if err != nil {
		t.Fatalf("buffered RunCrosstab: %v", err)
	}
	fusedResp, err := runFusedCrosstabViaRunner(t, schema, build(), recs, false)
	if err != nil {
		t.Fatalf("RunCrosstabFused: %v", err)
	}

	if bufResp.Overlays != nil {
		t.Fatalf("buffered long shape populated Overlays: %+v", bufResp.Overlays)
	}
	if fusedResp.Overlays != nil {
		t.Fatalf("fused long shape populated Overlays: %+v", fusedResp.Overlays)
	}
	if want, got := jsonOf(t, bufResp.Warnings), jsonOf(t, fusedResp.Warnings); want != got {
		t.Fatalf("Warnings diverge:\nbuffered: %s\nfused:    %s", want, got)
	}
	if bufResp.Crosstab.Matrix != nil || fusedResp.Crosstab.Matrix != nil {
		t.Fatalf("expected nil Matrix on long-shape result")
	}
	assertLongRowsEqual(t, bufResp.Data, fusedResp.Data)
}

// TestFusedCrosstab_ComponentsDisabledOverlayParity verifies the second
// free-parity claim: with components disabled the host view carries no
// CrosstabComponents, so a component-reading PAIRWISE handler surfaces
// PULSE_OVERLAY_COMPONENTS_REQUIRED on the fused path exactly as it
// does on the buffered path.
func TestFusedCrosstab_ComponentsDisabledOverlayParity(t *testing.T) {
	schema := crosstabOverlaySchema(t)
	recs := crosstabOverlayRecords(schema)

	overlays := []types.OverlaySpec{{
		Kind:  types.OverlayKindPairwisePropZ,
		Scope: types.OverlayScopeRow,
	}}

	bufReq := crosstabOverlayBaseRequest()
	bufReq.Overlays = overlays
	_, bufErr := runBufferedCrosstabWithComponents(t, schema, bufReq, recs, true)

	fusedReq := crosstabOverlayBaseRequest()
	fusedReq.Overlays = overlays
	_, fusedErr := runFusedCrosstabViaRunner(t, schema, fusedReq, recs, true)

	if bufErr == nil {
		t.Fatalf("expected buffered path to reject a components-reading overlay " +
			"with components disabled")
	}
	if fusedErr == nil {
		t.Fatalf("expected fused path to reject a components-reading overlay " +
			"with components disabled")
	}
	if bufErr.Error() != fusedErr.Error() {
		t.Fatalf("error text diverges:\nbuffered: %v\nfused:    %v", bufErr, fusedErr)
	}
	// E1-S2: text parity alone would still pass if both paths regressed
	// to some other coded error. Pin the code both paths must surface —
	// PULSE_OVERLAY_COMPONENTS_REQUIRED is the contract an embedder
	// switches on.
	if !pairwiseErrHasCode(bufErr, errors.PULSE_OVERLAY_COMPONENTS_REQUIRED) {
		t.Errorf("buffered error %v does not carry PULSE_OVERLAY_COMPONENTS_REQUIRED", bufErr)
	}
	if !pairwiseErrHasCode(fusedErr, errors.PULSE_OVERLAY_COMPONENTS_REQUIRED) {
		t.Errorf("fused error %v does not carry PULSE_OVERLAY_COMPONENTS_REQUIRED", fusedErr)
	}
}

// TestFusedCrosstab_NoOverlaysStillNilOverlays pins the additive
// byte-identity contract at the fused exit: wiring the hook in must not
// populate Response.Overlays for an overlay-free request.
//
// E1-S2 widens it from a fused-only nil check to a both-paths wire-form
// check. "Additive" means the overlay-free response must marshal without
// an "overlays" key at all — a non-nil empty slice would still emit one
// under a future non-omitempty tag and would break byte-identity against
// the pre-E1 baseline — and the fused and buffered wire forms must agree.
func TestFusedCrosstab_NoOverlaysStillNilOverlays(t *testing.T) {
	schema := crosstabOverlaySchema(t)
	recs := crosstabOverlayRecords(schema)

	paths := []struct {
		name string
		run  func(*testing.T, *types.Request) (*types.Response, error)
	}{
		{"fused", func(t *testing.T, req *types.Request) (*types.Response, error) {
			return runFusedCrosstabViaRunner(t, schema, req, recs, false)
		}},
		{"buffered", func(t *testing.T, req *types.Request) (*types.Response, error) {
			return runBufferedCrosstabWithComponents(t, schema, req, recs, false)
		}},
	}

	wire := make(map[string]string, len(paths))
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			resp, err := p.run(t, crosstabOverlayBaseRequest())
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if resp.Overlays != nil {
				t.Fatalf("overlay-free %s run populated Overlays: %+v", p.name, resp.Overlays)
			}
			if len(resp.Warnings) != 0 {
				t.Fatalf("overlay-free %s run emitted warnings: %+v", p.name, resp.Warnings)
			}
			if got := jsonOf(t, resp); strings.Contains(got, `"overlays"`) {
				t.Fatalf("overlay-free %s response carries an \"overlays\" key on the wire: %s", p.name, got)
			}
			wire[p.name] = jsonOf(t, resp.Crosstab)
		})
	}
	if wire["fused"] != wire["buffered"] {
		t.Errorf("overlay-free crosstab payload diverges:\nbuffered: %s\nfused:    %s",
			wire["buffered"], wire["fused"])
	}
}

// TestFusedCrosstab_UnknownOverlayKindErrorsLikeBuffered pins that the
// fused exit propagates the fold's error rather than swallowing it.
func TestFusedCrosstab_UnknownOverlayKindErrorsLikeBuffered(t *testing.T) {
	schema := crosstabOverlaySchema(t)
	recs := crosstabOverlayRecords(schema)

	overlays := []types.OverlaySpec{{
		Kind:  types.OverlayKind("OVERLAY_NOT_A_THING"),
		Scope: types.OverlayScopeCell,
		Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
	}}

	bufReq := crosstabOverlayBaseRequest()
	bufReq.Overlays = overlays
	_, bufErr := runBufferedCrosstabWithComponents(t, schema, bufReq, recs, false)

	fusedReq := crosstabOverlayBaseRequest()
	fusedReq.Overlays = overlays
	fusedResp, fusedErr := runFusedCrosstabViaRunner(t, schema, fusedReq, recs, false)

	if bufErr == nil {
		t.Fatalf("expected buffered error for unknown overlay kind")
	}
	if fusedErr == nil {
		t.Fatalf("expected fused error for unknown overlay kind, got resp=%+v", fusedResp)
	}
	if bufErr.Error() != fusedErr.Error() {
		t.Fatalf("error text diverges:\nbuffered: %v\nfused:    %v", bufErr, fusedErr)
	}
}

// ---------------------------------------------------------------------
// E1-S2: AGG_WEIGHTED_MEAN + OVERLAY_PAIRWISE_PROP_Z parity, and the
// AGG_WELFORD scope-boundary guard.
//
// The E1-S1 layer above covers the payload-only overlay kinds
// (INDEX_VS_MARGIN / SHARE_OF_ROW) plus a PROP_Z over an AGG_SUM cell.
// The two fixtures below close the remaining gaps:
//
//   - AGG_WEIGHTED_MEAN is the cell aggregator the pairwise family is
//     actually deployed against (its sum_weights component is the
//     n_source=cell_weight_sum denominator), so it needs its own parity
//     coverage rather than riding on AGG_SUM's.
//   - AGG_WELFORD is the documented scope boundary: it is NOT mergeable,
//     so the gate rejects it and OVERLAY_PAIRWISE_WELCH_T /
//     OVERLAY_PAIRWISE_TWO_MEANS_Z keep executing on the buffered path.
//     That is by design, not a regression.
// ---------------------------------------------------------------------

// crosstabWeightedOverlaySchema is crosstabOverlaySchema plus a weight
// field, so AGG_WEIGHTED_MEAN has a Params.weight_field to point at.
// 3 rows × 2 columns keeps the pairwise fan-out small (3 row pairs).
func crosstabWeightedOverlaySchema(t *testing.T) *encoding.Schema {
	t.Helper()
	rowDict := encoding.NewDictionary()
	for _, r := range []string{"r0", "r1", "r2"} {
		if _, err := rowDict.Add(r); err != nil {
			t.Fatalf("row dict.Add: %v", err)
		}
	}
	colDict := encoding.NewDictionary()
	for _, c := range []string{"c0", "c1"} {
		if _, err := colDict.Add(c); err != nil {
			t.Fatalf("col dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "row", Type: encoding.FieldTypeCategoricalU8, Dictionary: rowDict},
			{Name: "col", Type: encoding.FieldTypeCategoricalU8, Dictionary: colDict},
			{Name: "value", Type: encoding.FieldTypeF64},
			{Name: "weight", Type: encoding.FieldTypeF64},
		},
	}
}

// crosstabWeightedOverlayRecords lays three (value, weight) observations
// into each of the six cells. Deliberately:
//
//   - values sit in 0..100 so the default PSource=cell_value_pct reading
//     (p = cell/100) stays a legal proportion;
//   - weights differ per row so sum_weights (the cell_weight_sum n leg)
//     is NOT a constant and a fused/buffered divergence in the weighted
//     recurrence would move the p-values;
//   - n per cell is 3 (> 1) so no kernel short-circuits on n_too_small.
//
// Author order is fixed (a slice, never a map) because the fused path
// interns axis keys in first-seen order and the parity assertion is
// order-sensitive.
func crosstabWeightedOverlayRecords(schema *encoding.Schema) []*Record {
	mk := func(row, col int, value, weight float64) *Record {
		return NewRecord(schema, map[string]float64{
			"row":    float64(row),
			"col":    float64(col),
			"value":  value,
			"weight": weight,
		})
	}
	type obs struct {
		row, col      int
		value, weight float64
	}
	fixture := []obs{
		{0, 0, 40, 1}, {0, 0, 50, 3}, {0, 0, 60, 2},
		{0, 1, 20, 2}, {0, 1, 30, 1}, {0, 1, 25, 5},
		{1, 0, 55, 4}, {1, 0, 65, 1}, {1, 0, 60, 3},
		{1, 1, 35, 1}, {1, 1, 45, 2}, {1, 1, 40, 4},
		{2, 0, 70, 2}, {2, 0, 80, 2}, {2, 0, 75, 1},
		{2, 1, 10, 3}, {2, 1, 15, 1}, {2, 1, 12, 2},
	}
	out := make([]*Record, 0, len(fixture))
	for _, o := range fixture {
		out = append(out, mk(o.row, o.col, o.value, o.weight))
	}
	return out
}

// crosstabWeightedOverlayBaseRequest is the AGG_WEIGHTED_MEAN sibling of
// crosstabOverlayBaseRequest. MarginMeanReducible keeps it on the fused
// side of the gate (see TestCanFuseCrosstab_MeanReducibleCellAccepted).
func crosstabWeightedOverlayBaseRequest() *types.Request {
	return &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "row"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "col"}},
			Cell: &types.Aggregation{
				Type:   types.AGG_WEIGHTED_MEAN,
				Field:  "value",
				Label:  "wmean_value",
				Params: json.RawMessage(`{"weight_field":"weight"}`),
			},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
}

// pairwisePropZLayerPresentCells counts the Present p-value cells across
// every PAIRWISE_PROP_Z layer on a response. Used as the non-vacuity
// guard: a parity assertion over two all-absent matrices would pass
// while proving nothing.
func pairwisePropZLayerPresentCells(resp *types.Response) int {
	present := 0
	for _, layer := range resp.Overlays {
		if layer.Kind != types.OverlayKindPairwisePropZ {
			continue
		}
		if layer.Payload.Matrix == nil {
			continue
		}
		for _, row := range layer.Payload.Matrix.Cells {
			for _, cell := range row {
				if cell.Present {
					present++
				}
			}
		}
	}
	return present
}

// TestFusedCrosstab_PairwisePropZOverWeightedMeanMatchesBuffered is the
// E1-S2 headline case. OVERLAY_PAIRWISE_PROP_Z is a component-READING
// overlay — unlike INDEX_VS_MARGIN it reaches past the MatrixPayload into
// Response.Components.Crosstab (per-cell n, sum_weights, margin counts) —
// so this is the case that proves the fused Finalize builds a components
// block the overlay fold can consume identically to the buffered one.
// AGG_WEIGHTED_MEAN is the cell aggregator because its sum_weights
// component is the n_source=cell_weight_sum denominator; a fused
// divergence in the Chan-Welford weighted recurrence would surface here
// as a p-value diff, not just a cell-value diff.
//
// Each row asserts the gate FIRST: without that, a parity assertion over
// two buffered runs would pass vacuously.
func TestFusedCrosstab_PairwisePropZOverWeightedMeanMatchesBuffered(t *testing.T) {
	schema := crosstabWeightedOverlaySchema(t)
	recs := crosstabWeightedOverlayRecords(schema)

	propZ := func(scope types.OverlayScope, params types.PairwiseOverlayParams) types.OverlaySpec {
		return types.OverlaySpec{
			Name:   "pz",
			Kind:   types.OverlayKindPairwisePropZ,
			Scope:  scope,
			Params: mustParams(t, params),
		}
	}

	cases := []struct {
		name     string
		overlays []types.OverlaySpec
		// expectSkips flips the non-vacuity guard: instead of demanding
		// present p-values, the row demands that BOTH paths degrade
		// identically (zero present cells, a non-empty promoted warning
		// set). Degradation parity is as load-bearing as value parity.
		expectSkips bool
	}{
		{
			// Default n leg: the universal-floor per-cell record count.
			name: "row_scope_default_n",
			overlays: []types.OverlaySpec{
				propZ(types.OverlayScopeRow, types.PairwiseOverlayParams{}),
			},
		},
		{
			// The weighted n leg — reads sum_weights straight off the
			// AGG_WEIGHTED_MEAN component map. This is the mode the
			// weighted cell aggregator exists to serve.
			name: "row_scope_cell_weight_sum",
			overlays: []types.OverlaySpec{
				propZ(types.OverlayScopeRow, types.PairwiseOverlayParams{
					NSource: types.PairwiseNSourceCellWeightSum,
				}),
			},
		},
		{
			// Margin-count n leg exercises the fused margin-components
			// arithmetic, which is derived rather than accumulated.
			name: "row_scope_row_margin_n",
			overlays: []types.OverlaySpec{
				propZ(types.OverlayScopeRow, types.PairwiseOverlayParams{
					NSource: types.PairwiseNSourceRowMarginN,
				}),
			},
		},
		{
			// Column scope transposes the pair axis; the fused axis
			// interning order must survive the transpose.
			name: "column_scope_default_n",
			overlays: []types.OverlaySpec{
				propZ(types.OverlayScopeColumn, types.PairwiseOverlayParams{}),
			},
		},
		{
			// p_source=cell_value reads the weighted mean as a RAW
			// proportion. These weighted means live in 0..100, so every
			// pair is out of the unit interval and the pooled-SE kernel
			// declines — a deliberate degenerate row that pins the
			// skip-warning tally parity rather than value parity.
			name: "row_scope_p_source_cell_value_degenerate",
			overlays: []types.OverlaySpec{
				propZ(types.OverlayScopeRow, types.PairwiseOverlayParams{
					PSource: types.PairwisePSourceCellValue,
				}),
			},
			expectSkips: true,
		},
		{
			// A component-reading layer stacked with a payload-only one:
			// order preservation across mixed layer families.
			name: "prop_z_then_index_vs_margin",
			overlays: []types.OverlaySpec{
				propZ(types.OverlayScopeRow, types.PairwiseOverlayParams{}),
				{
					Name:  "row_index",
					Kind:  types.OverlayKindIndexVsMargin,
					Scope: types.OverlayScopeCell,
					Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gateReq := crosstabWeightedOverlayBaseRequest()
			gateReq.Overlays = tc.overlays
			ok, reason := CanFuseCrosstab(gateReq, schema, nil)
			if !ok {
				t.Fatalf("CanFuseCrosstab rejected AGG_WEIGHTED_MEAN + PAIRWISE_PROP_Z: %s", reason)
			}
			if reason != "" {
				t.Fatalf("expected empty reason on success, got %q", reason)
			}

			bufReq := crosstabWeightedOverlayBaseRequest()
			bufReq.Overlays = tc.overlays
			bufResp, err := runBufferedCrosstabWithComponents(t, schema, bufReq, recs, false)
			if err != nil {
				t.Fatalf("buffered RunCrosstab: %v", err)
			}

			fusedReq := crosstabWeightedOverlayBaseRequest()
			fusedReq.Overlays = tc.overlays
			fusedResp, err := runFusedCrosstabViaRunner(t, schema, fusedReq, recs, false)
			if err != nil {
				t.Fatalf("RunCrosstabFused: %v", err)
			}

			// Non-vacuity: either the prop-Z layer produced real
			// p-values on both paths, or (for the degenerate row) both
			// paths skipped every pair and said so. An all-absent matrix
			// with no warnings would make the parity comparison below
			// meaningless.
			bufPresent := pairwisePropZLayerPresentCells(bufResp)
			fusedPresent := pairwisePropZLayerPresentCells(fusedResp)
			if tc.expectSkips {
				if bufPresent != 0 || fusedPresent != 0 {
					t.Fatalf("expected every pair to skip, got buffered=%d fused=%d present cells",
						bufPresent, fusedPresent)
				}
				if len(bufResp.Warnings) == 0 {
					t.Fatalf("degenerate row produced no buffered skip warnings; " +
						"the fixture no longer degrades and the row proves nothing")
				}
			} else {
				if bufPresent == 0 {
					t.Fatalf("buffered PROP_Z layer produced zero present p-values; " +
						"fixture no longer exercises the kernel")
				}
				if fusedPresent == 0 {
					t.Fatalf("fused PROP_Z layer produced zero present p-values")
				}
			}

			if len(fusedResp.Overlays) != len(tc.overlays) {
				t.Fatalf("fused Overlays = %d layers, want %d",
					len(fusedResp.Overlays), len(tc.overlays))
			}
			for i, spec := range tc.overlays {
				if got := fusedResp.Overlays[i].Kind; got != spec.Kind {
					t.Errorf("layer[%d].Kind = %q, want %q (order not preserved)",
						i, got, spec.Kind)
				}
			}

			// Host matrix first — an overlay diff sitting on top of a
			// matrix diff would point at the wrong culprit.
			assertMatrixEqual(t, bufResp.Crosstab.Matrix, fusedResp.Crosstab.Matrix)

			// The components block is the pairwise family's input, so
			// pin it explicitly rather than inferring it from the
			// overlay output.
			if want, got := jsonOf(t, bufResp.Components), jsonOf(t, fusedResp.Components); want != got {
				t.Errorf("Components diverge:\nbuffered: %s\nfused:    %s", want, got)
			}
			if want, got := jsonOf(t, bufResp.Overlays), jsonOf(t, fusedResp.Overlays); want != got {
				t.Errorf("Overlays diverge:\nbuffered: %s\nfused:    %s", want, got)
			}
			if want, got := jsonOf(t, bufResp.Warnings), jsonOf(t, fusedResp.Warnings); want != got {
				t.Errorf("Warnings diverge:\nbuffered: %s\nfused:    %s", want, got)
			}
		})
	}
}

// TestFusedCrosstab_WeightedMeanComponentsCarrySumWeights guards the
// input the n_source=cell_weight_sum case above depends on. If the fused
// Finalize ever stopped emitting sum_weights, every pair would skip on
// extract-failure and the parity assertion would go green over two
// empty matrices. Pinning the component key keeps that failure loud.
func TestFusedCrosstab_WeightedMeanComponentsCarrySumWeights(t *testing.T) {
	schema := crosstabWeightedOverlaySchema(t)
	recs := crosstabWeightedOverlayRecords(schema)

	paths := []struct {
		name string
		run  func(*testing.T, *types.Request) (*types.Response, error)
	}{
		{"buffered", func(t *testing.T, req *types.Request) (*types.Response, error) {
			return runBufferedCrosstabWithComponents(t, schema, req, recs, false)
		}},
		{"fused", func(t *testing.T, req *types.Request) (*types.Response, error) {
			return runFusedCrosstabViaRunner(t, schema, req, recs, false)
		}},
	}

	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			resp, err := p.run(t, crosstabWeightedOverlayBaseRequest())
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if resp.Components == nil || resp.Components.Crosstab == nil {
				t.Fatalf("no crosstab components block")
			}
			cells := resp.Components.Crosstab.CellComponents
			if len(cells) == 0 {
				t.Fatalf("empty CellComponents")
			}
			for r, row := range cells {
				for c, cell := range row {
					if cell == nil {
						continue
					}
					if _, ok := cell["sum_weights"]; !ok {
						t.Fatalf("CellComponents[%d][%d] missing sum_weights key: %v", r, c, cell)
					}
					if _, ok := cell["n"]; !ok {
						t.Fatalf("CellComponents[%d][%d] missing universal-floor n: %v", r, c, cell)
					}
				}
			}
		})
	}
}

// TestCrosstabWelfordCell_StaysBufferedWithCorrectOverlays is the scope
// boundary, written down in code so a future reader does not file it as
// a regression.
//
// AGG_WELFORD is Streamable but NOT Mergeable (sample variance does not
// pool by addition across partitions) and its MarginReducibility is
// MarginRecompute. Both arms of the gate reject it, and the mergeable
// arm fires first. That means OVERLAY_PAIRWISE_WELCH_T and
// OVERLAY_PAIRWISE_TWO_MEANS_Z — the two kinds that read a Welford
// {mean, variance, n} triple off CellComponents — necessarily keep
// executing on the buffered path even after E1-S1 admitted overlays to
// the fused gate. Widening the gate to cover them is a separate effort,
// not a bug in this one.
//
// The second half asserts those overlays are still CORRECT on the
// buffered path: p-values are cross-checked against the closed form
// recomputed from the response's own CellComponents, so a coordinate or
// extraction bug cannot hide behind a self-consistent kernel call.
func TestCrosstabWelfordCell_StaysBufferedWithCorrectOverlays(t *testing.T) {
	schema := crosstabWeightedOverlaySchema(t)
	recs := crosstabWeightedOverlayRecords(schema)

	// AGG_WELFORD is map-valued, so no margins are requested (the
	// pairwise Welford kinds read n from the triple, never from a
	// margin count).
	baseReq := func() *types.Request {
		return &types.Request{
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "row"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "col"}},
				Cell:    &types.Aggregation{Type: types.AGG_WELFORD, Field: "value", Label: "moments"},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	}

	cases := []struct {
		name string
		kind types.OverlayKind
		// closed recomputes the expected two-sided p-value from the two
		// legs' Welford triples, independently of the kernel wiring.
		closed func(m1, v1 float64, n1 int, m2, v2 float64, n2 int) float64
	}{
		{
			name: "welch_t",
			kind: types.OverlayKindPairwiseWelchT,
			closed: func(m1, v1 float64, n1 int, m2, v2 float64, n2 int) float64 {
				a, b := v1/float64(n1), v2/float64(n2)
				se := math.Sqrt(a + b)
				tStat := (m1 - m2) / se
				df := (a + b) * (a + b) /
					((a*a)/float64(n1-1) + (b*b)/float64(n2-1))
				return studentTTwoSidedP(tStat, df)
			},
		},
		{
			name: "two_means_z",
			kind: types.OverlayKindPairwiseTwoMeansZ,
			closed: func(m1, v1 float64, n1 int, m2, v2 float64, n2 int) float64 {
				a, b := v1/float64(n1), v2/float64(n2)
				z := (m1 - m2) / math.Sqrt(a+b)
				return 2 * standardNormalCDF(-math.Abs(z))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseReq()
			req.Overlays = []types.OverlaySpec{{
				Name:  "pw",
				Kind:  tc.kind,
				Scope: types.OverlayScopeRow,
			}}

			// --- Gate arm: still buffered, on the aggregator branch. ---
			ok, reason := CanFuseCrosstab(req, schema, nil)
			if ok {
				t.Fatalf("CanFuseCrosstab admitted an AGG_WELFORD cell; the "+
					"Welford overlay family is expected to stay buffered "+
					"(reason was %q)", reason)
			}
			if !strings.Contains(reason, "non-mergeable cell aggregator") ||
				!strings.Contains(reason, string(types.AGG_WELFORD)) {
				t.Fatalf("expected rejection on the non-mergeable aggregator arm, got %q", reason)
			}

			// --- Correctness arm: the buffered path still delivers. ---
			resp, err := runBufferedCrosstabWithComponents(t, schema, req, recs, false)
			if err != nil {
				t.Fatalf("buffered RunCrosstab: %v", err)
			}
			if len(resp.Overlays) != 1 {
				t.Fatalf("expected 1 overlay layer, got %d", len(resp.Overlays))
			}
			layer := resp.Overlays[0]
			if layer.Kind != tc.kind {
				t.Fatalf("layer.Kind = %q, want %q", layer.Kind, tc.kind)
			}
			mx := layer.Payload.Matrix
			if mx == nil {
				t.Fatalf("overlay layer carries no matrix payload")
			}
			// Row scope: the overlay payload's ROW axis carries the
			// pairs (3 host rows -> 3 pairs) and its COLUMN axis echoes
			// the host's 2 columns.
			if len(mx.RowKeys) != 3 {
				t.Fatalf("expected 3 pair rows, got %d: %v", len(mx.RowKeys), mx.RowKeys)
			}
			if len(mx.ColumnKeys) != 2 {
				t.Fatalf("expected 2 echoed host columns, got %d: %v", len(mx.ColumnKeys), mx.ColumnKeys)
			}
			if got := mx.RowKeys[0]; got[0] != "r0" || got[1] != "r1" {
				t.Fatalf("pair[0] key = %v, want [r0 r1]", got)
			}

			if resp.Components == nil || resp.Components.Crosstab == nil {
				t.Fatalf("AGG_WELFORD crosstab produced no components block")
			}
			cellComps := resp.Components.Crosstab.CellComponents

			// Cross-check pair (row0, row1) at host column 0 against the
			// closed form recomputed from the triples the response itself
			// published.
			m1, v1, n1 := welfordTripleFromComponents(t, cellComps, 0, 0)
			m2, v2, n2 := welfordTripleFromComponents(t, cellComps, 1, 0)
			want := tc.closed(m1, v1, n1, m2, v2, n2)

			// Cells is [pair][host column]; [0][0] is pair (r0, r1)
			// evaluated at host column c0 — the exact two legs the
			// triples above were read from.
			cell := mx.Cells[0][0]
			if !cell.Present {
				t.Fatalf("pair (r0,r1) at host column 0 is absent; " +
					"the Welford triple did not reach the kernel")
			}
			got, okFloat := cell.Value.(float64)
			if !okFloat {
				t.Fatalf("pair cell value %v is not a float64", cell.Value)
			}
			if math.Abs(got-want) > 1e-12 {
				t.Fatalf("p-value = %v, want %v (closed form over the response's own components)", got, want)
			}
		})
	}
}

// welfordTripleFromComponents pulls {mean, variance, n} out of a
// CellComponents slot, failing loudly rather than defaulting to zero —
// a silent zero would make the closed-form cross-check above pass on
// garbage.
func welfordTripleFromComponents(t *testing.T, cells [][]map[string]any, r, c int) (mean, variance float64, n int) {
	t.Helper()
	if r >= len(cells) || c >= len(cells[r]) || cells[r][c] == nil {
		t.Fatalf("CellComponents[%d][%d] absent", r, c)
	}
	cell := cells[r][c]
	num := func(key string) float64 {
		v, ok := cell[key]
		if !ok {
			t.Fatalf("CellComponents[%d][%d] missing %q: %v", r, c, key, cell)
		}
		f, ok := componentToFloat(v)
		if !ok {
			t.Fatalf("CellComponents[%d][%d][%q] = %v is not numeric", r, c, key, v)
		}
		return f
	}
	return num("mean"), num("variance"), int(num("n"))
}
