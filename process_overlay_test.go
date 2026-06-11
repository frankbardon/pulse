package pulse

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// crosstabOverlayPulseFile builds a tiny categorical-by-categorical
// cohort whose AGG_SUM 3×3 crosstab produces predictable margin slots,
// mirroring the processing-layer fixture in
// processing/crosstab_overlay_test.go. Row margins are 6 / 60 / 600 so
// the INDEX_VS_MARGIN axis=ROW handler emits cells that sum to 100
// (= 1/6+2/6+3/6) per row, the same canonical golden the E1-S6
// processing test pins.
func crosstabOverlayPulseFile(t *testing.T) (afero.Fs, string) {
	t.Helper()
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "overlay.pulse",
		[]string{"row", "col", "value"},
		[][]string{
			{"r0", "c0", "1"},
			{"r0", "c1", "2"},
			{"r0", "c2", "3"},
			{"r1", "c0", "10"},
			{"r1", "c1", "20"},
			{"r1", "c2", "30"},
			{"r2", "c0", "100"},
			{"r2", "c1", "200"},
			{"r2", "c2", "300"},
		})
	return memFs, "overlay.pulse"
}

// crosstabOverlayRequest returns the canonical 3×3 AGG_SUM matrix
// request used by every overlay-facade test below. Margins.Rows=true
// so the INDEX_VS_MARGIN axis=ROW handler has its denominator slot
// available.
func crosstabOverlayRequest() *Request {
	return &Request{
		Cohort: &types.Cohort{Filename: "overlay.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "row"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "col"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum_value"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
}

// TestProcess_OverlayPopulatesResponse is the E1-S9 acceptance: a
// pulse.Process call against a Crosstab request carrying Overlays
// produces Response.Overlays[0] with shape MATRIX and a non-empty
// MatrixPayload. Mirrors the processing-layer
// TestCrosstab_OverlayResponsePopulated golden but exercises the
// full facade chain (pulse.New → pulse.Process → service.Process →
// service.processCrosstab → buffered RunCrosstab → overlay fold).
//
// The fused gate (E1-S9 addition) rejects Overlays-bearing requests,
// so this test also validates that the buffered path is selected end-
// to-end — without that gate the fused exit would drop Response.
// Overlays on the floor (the fused finalize doesn't apply overlays in
// E1, see processing/crosstab.go applyOverlaysToResponse doc).
func TestProcess_OverlayPopulatesResponse(t *testing.T) {
	memFs, _ := crosstabOverlayPulseFile(t)
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := crosstabOverlayRequest()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
	}

	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp == nil {
		t.Fatal("Process returned nil response")
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatalf("expected crosstab matrix payload on response")
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("expected 1 overlay layer, got %d", len(resp.Overlays))
	}
	layer := resp.Overlays[0]
	if layer.Kind != types.OverlayKindIndexVsMargin {
		t.Fatalf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindIndexVsMargin)
	}
	if layer.Scope != types.OverlayScopeCell {
		t.Fatalf("layer.Scope = %q, want %q", layer.Scope, types.OverlayScopeCell)
	}
	if layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Fatalf("layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeMatrix)
	}
	if layer.Payload.Matrix == nil {
		t.Fatalf("layer.Payload.Matrix nil")
	}
	if len(layer.Payload.Matrix.Cells) == 0 {
		t.Fatalf("layer.Payload.Matrix.Cells empty")
	}
	if layer.Summary == nil || layer.Summary.Baseline == nil ||
		*layer.Summary.Baseline != 100.0 {
		t.Fatalf("expected baseline = 100, got %+v", layer.Summary)
	}
}

// TestProcess_NoOverlaysPreservesBaseline is the PRD §9 canonical
// additive byte-identity check at the facade level: a Crosstab request
// WITHOUT Overlays produces a Response whose Crosstab payload is
// reflect.DeepEqual to the same request shape executed against the
// same cohort with Response.Overlays explicitly nil. The hook short-
// circuits on len(req.Overlays) == 0, so the pre-overlay and post-
// overlay baselines must be byte-identical.
//
// Complements the processing-layer
// TestCrosstab_NoOverlaysPreservesByteIdentity by exercising the full
// facade chain, so a regression in the facade plumbing (e.g. a shallow
// copy that loses the Overlays slot, or accidental population of the
// slot when the request had none) surfaces here.
func TestProcess_NoOverlaysPreservesBaseline(t *testing.T) {
	memFs, _ := crosstabOverlayPulseFile(t)
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	respBase, err := p.Process(context.Background(), crosstabOverlayRequest())
	if err != nil {
		t.Fatalf("baseline Process: %v", err)
	}
	if respBase == nil {
		t.Fatal("baseline Process returned nil")
	}
	if respBase.Overlays != nil {
		t.Fatalf("baseline must not populate Response.Overlays: %+v", respBase.Overlays)
	}

	// Identical shape with explicit nil Overlays must produce the same
	// Crosstab payload — the hook fires but short-circuits.
	reqEmpty := crosstabOverlayRequest()
	reqEmpty.Overlays = nil
	respEmpty, err := p.Process(context.Background(), reqEmpty)
	if err != nil {
		t.Fatalf("empty-overlays Process: %v", err)
	}
	if respEmpty.Overlays != nil {
		t.Fatalf("nil Overlays must leave Response.Overlays nil: %+v", respEmpty.Overlays)
	}
	if !reflect.DeepEqual(respBase.Crosstab.Matrix, respEmpty.Crosstab.Matrix) {
		t.Fatalf("matrix byte-identity violated:\n base: %+v\nempty: %+v",
			respBase.Crosstab.Matrix, respEmpty.Crosstab.Matrix)
	}
	if !reflect.DeepEqual(respBase.Warnings, respEmpty.Warnings) {
		t.Fatalf("warnings drift: base=%v empty=%v",
			respBase.Warnings, respEmpty.Warnings)
	}
}

// TestProcess_OverlayEchoedRequest verifies the EchoRequest path
// surfaces the Overlays slot verbatim in the descriptor.Envelope's
// Request field. The CLI --echo-request flag and the library-level
// pulse.Options.EchoRequest both rely on this — embedders logging or
// replaying executed requests must see the overlay specs the engine
// ran against.
//
// The Envelope is constructed exactly the way the CLI does it
// (internal/cli/api.go apiProcessCmd): NewEnvelope wraps the
// response, env.WithRequest(req) attaches the post-defaults request.
// Smart defaults already mutated req in place inside Process, so what
// the test asserts here is precisely what the wire emits.
func TestProcess_OverlayEchoedRequest(t *testing.T) {
	memFs, _ := crosstabOverlayPulseFile(t)
	p, err := New(Options{FS: memFs, EchoRequest: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := crosstabOverlayRequest()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
	}

	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// Mirror the CLI envelope construction so an envelope-level
	// regression (omitempty, nil slice, type assertion) surfaces here.
	env := descriptor.NewEnvelope(resp).WithRequest(req)
	if env.Request == nil {
		t.Fatal("envelope.Request unexpectedly nil after WithRequest")
	}
	echoed, ok := env.Request.(*Request)
	if !ok {
		t.Fatalf("envelope.Request type = %T, want *types.Request", env.Request)
	}
	if len(echoed.Overlays) != 1 {
		t.Fatalf("echoed.Overlays len = %d, want 1", len(echoed.Overlays))
	}
	if echoed.Overlays[0].Kind != types.OverlayKindIndexVsMargin {
		t.Fatalf("echoed.Overlays[0].Kind = %q, want %q",
			echoed.Overlays[0].Kind, types.OverlayKindIndexVsMargin)
	}
	if echoed.Overlays[0].Ref.Margin == nil ||
		echoed.Overlays[0].Ref.Margin.Axis != types.MarginAxisRow {
		t.Fatalf("echoed.Overlays[0].Ref.Margin = %+v, want Axis=row",
			echoed.Overlays[0].Ref.Margin)
	}

	// JSON-roundtrip the envelope and assert the overlays slot survives
	// at wire shape. Catches any future omitempty drift or json-tag
	// regression on the Request alias.
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal envelope: %v", err)
	}
	var parsed struct {
		Request struct {
			Overlays []types.OverlaySpec `json:"overlays"`
		} `json:"request"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("json.Unmarshal envelope: %v", err)
	}
	if len(parsed.Request.Overlays) != 1 {
		t.Fatalf("wire-shape echoed overlays len = %d, want 1\nraw: %s",
			len(parsed.Request.Overlays), string(raw))
	}
	if parsed.Request.Overlays[0].Kind != types.OverlayKindIndexVsMargin {
		t.Fatalf("wire-shape echoed kind = %q, want %q",
			parsed.Request.Overlays[0].Kind, types.OverlayKindIndexVsMargin)
	}
}
