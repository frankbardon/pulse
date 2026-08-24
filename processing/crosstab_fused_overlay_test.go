package processing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
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
}

// TestFusedCrosstab_NoOverlaysStillNilOverlays pins the additive
// byte-identity contract at the fused exit: wiring the hook in must not
// populate Response.Overlays for an overlay-free request.
func TestFusedCrosstab_NoOverlaysStillNilOverlays(t *testing.T) {
	schema := crosstabOverlaySchema(t)
	recs := crosstabOverlayRecords(schema)

	req := crosstabOverlayBaseRequest()
	resp, err := runFusedCrosstabViaRunner(t, schema, req, recs, false)
	if err != nil {
		t.Fatalf("RunCrosstabFused: %v", err)
	}
	if resp.Overlays != nil {
		t.Fatalf("overlay-free fused run populated Overlays: %+v", resp.Overlays)
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("overlay-free fused run emitted warnings: %+v", resp.Warnings)
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
