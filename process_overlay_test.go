package pulse

import (
	"context"
	"encoding/json"
	"math"
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

// TestExtensions_OverlayKinds_FormulaExprFn is the E8-S7 canonical-name
// acceptance gate verifying that an embedder-registered ExprFunction
// surfaced through `pulse.Options.Extensions.ExprFunctions` becomes
// reachable from an `OVERLAY_FORMULA` `Params["formula"]` expression
// when the Process facade dispatches a Crosstab + Overlays request.
// Exercises the full extension thread: pulse.New(Options{Extensions:
// {ExprFunctions: {...}}}) → ExtensionsSnapshot → buffered crosstab
// exit → ApplyOverlaysWithExtensions → FORMULA compile-time
// `ExprOptions()` slice → per-cell expr.Run.
//
// Also asserts Predict accepts the same registered-function call (no
// PULSE_OVERLAY_FORMULA_INVALID_IDENT for the registered name) so the
// predict + runtime surfaces agree on what an embedder can author. The
// PredictResult.Streamable flag must report false for the FORMULA
// catalog entry per types.OverlayStreamability — runtime fold is
// buffered.
func TestExtensions_OverlayKinds_FormulaExprFn(t *testing.T) {
	memFs, _ := crosstabOverlayPulseFile(t)

	// Embedder function: classic percent-change between two values.
	// Reachable from any FORMULA expression as a Go-side helper the
	// caller didn't have to repeat per overlay.
	pctChange := func(now, ref float64) float64 {
		if ref == 0 {
			return math.NaN()
		}
		return (now - ref) / ref * 100.0
	}

	p, err := New(Options{
		FS: memFs,
		Extensions: Extensions{
			ExprFunctions: []ExprFunction{
				{
					Name:      "pct_change",
					Signature: "pct_change(now float64, ref float64) float64",
					Fn:        pctChange,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("New(Extensions.ExprFunctions): %v", err)
	}

	// Build a Crosstab + FORMULA overlay request. The formula invokes
	// the embedder-supplied pct_change(cell, 100). Per-cell expected
	// value with cells {1..3, 10..30, 100..300} versus ref 100:
	//   row 0: -99, -98, -97
	//   row 1: -90, -80, -70
	//   row 2:   0, 100, 200
	req := crosstabOverlayRequest()
	req.Overlays = []types.OverlaySpec{
		{
			Name:   "pct_change_overlay",
			Kind:   types.OverlayKindFormula,
			Scope:  types.OverlayScopeCell,
			Params: json.RawMessage(`{"formula":"pct_change(cell, 100.0)"}`),
		},
	}

	// Predict gate: registered function must NOT surface as unknown
	// identifier; the FORMULA catalog entry must report streamable=false.
	// The public Predict facade drops envelope.Errors on the floor when
	// Valid=false (it returns the result rather than the envelope), so
	// `result.Valid==true` is the load-bearing acceptance signal — the
	// negative arm below uses descriptor.PredictFromBytes directly to
	// inspect the error code.
	predictResult, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !predictResult.Valid {
		t.Errorf("Predict.Valid = false; want true (registered ExprFunction visible to predict)")
	}
	if len(predictResult.OverlaysApplied) != 1 {
		t.Fatalf("Predict.OverlaysApplied len = %d, want 1", len(predictResult.OverlaysApplied))
	}
	if predictResult.OverlaysApplied[0].Kind != types.OverlayKindFormula {
		t.Errorf("Predict.OverlaysApplied[0].Kind = %q, want %q",
			predictResult.OverlaysApplied[0].Kind, types.OverlayKindFormula)
	}
	if predictResult.OverlaysApplied[0].Streamable {
		t.Errorf("Predict.OverlaysApplied[0].Streamable = true; want false (FORMULA is buffered)")
	}

	// Runtime arm: Process dispatches the FORMULA overlay against the
	// crosstab matrix; every present cell must carry the pct_change
	// result.
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp == nil || resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatalf("expected crosstab matrix payload on response")
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("Response.Overlays len = %d, want 1", len(resp.Overlays))
	}
	layer := resp.Overlays[0]
	if layer.Kind != types.OverlayKindFormula {
		t.Fatalf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindFormula)
	}
	if layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Fatalf("layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeMatrix)
	}
	if layer.Payload.Matrix == nil {
		t.Fatalf("layer.Payload.Matrix nil")
	}
	want := [3][3]float64{
		{-99, -98, -97},
		{-90, -80, -70},
		{0, 100, 200},
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			cell := layer.Payload.Matrix.Cells[i][j]
			if !cell.Present {
				t.Fatalf("Cells[%d][%d] absent; want present", i, j)
			}
			got, ok := cell.Value.(float64)
			if !ok {
				t.Fatalf("Cells[%d][%d].Value = %T, want float64", i, j, cell.Value)
			}
			if math.Abs(got-want[i][j]) > 1e-9 {
				t.Errorf("Cells[%d][%d].Value = %v, want %v", i, j, got, want[i][j])
			}
		}
	}

	// Negative arm: a Pulse instance without the registered function
	// must reject the same formula at Predict-time with
	// PULSE_OVERLAY_FORMULA_INVALID_IDENT. Pulse.Predict surfaces the
	// envelope.Data on Valid=false but drops envelope.Errors, so the
	// negative arm drops down to descriptor.PredictFromBytes directly
	// to inspect the on-wire error codes — mirrors what the CLI surface
	// emits when --json is set. Pins the predict-rejection contract
	// embedders rely on for catching authoring errors at validation
	// time.
	pBare, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New(no Extensions): %v", err)
	}
	predictBare, err := pBare.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict(no Extensions): %v", err)
	}
	if predictBare.Valid {
		t.Errorf("Predict.Valid = true on bare instance; want false (registered ExprFunction missing)")
	}

	// Inspect the on-wire error code by re-driving the underlying
	// predict path against the cohort bytes — same code path the CLI
	// --json envelope emits and the contract surface the predict gate
	// publishes.
	data, err := afero.ReadFile(memFs, "overlay.pulse")
	if err != nil {
		t.Fatalf("ReadFile cohort: %v", err)
	}
	envBare := descriptor.PredictFromBytes(data, req, &descriptor.PredictOptions{
		Extensions: pBare.Service().ExtensionsSnapshot(),
	})
	gotInvalidIdent := false
	for _, e := range envBare.Errors {
		if e.Code == "PULSE_OVERLAY_FORMULA_INVALID_IDENT" {
			gotInvalidIdent = true
			break
		}
	}
	if !gotInvalidIdent {
		codes := make([]string, 0, len(envBare.Errors))
		for _, e := range envBare.Errors {
			codes = append(codes, e.Code)
		}
		t.Errorf("Predict on bare instance: missing PULSE_OVERLAY_FORMULA_INVALID_IDENT; got %v", codes)
	}
}
