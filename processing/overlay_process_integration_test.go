package processing

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// overlayIntegrationSchema returns the canonical (region, score) schema
// the grouped-Process overlay-integration tests reuse. region is a
// 3-entry categorical so the grouped path emits one bucket per category;
// score is f64 so AGG_SUM produces a well-defined primary metric for the
// SeriesHostView resolver to fold over.
func overlayIntegrationSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, r := range []string{"east", "west", "north"} {
		if _, err := dict.Add(r); err != nil {
			t.Fatalf("dict.Add(%q): %v", r, err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
}

// overlayIntegrationRecords builds a 6-record fixture (two records per
// region) so AGG_SUM grouped by region produces three known per-group
// totals: east=10+20=30, west=40+50=90, north=70+80=150. Grand total =
// 270; OVERLAY_INDEX_VS_TOTAL emits east=30/270*100, west=90/270*100,
// north=150/270*100. The numbers are arithmetically clean for the
// post-condition assertions in the smoke tests below.
func overlayIntegrationRecords(schema *encoding.Schema) []*Record {
	mk := func(region uint64, score float64) *Record {
		return NewRecord(schema, map[string]float64{
			"region": float64(region),
			"score":  score,
		})
	}
	return []*Record{
		mk(0, 10), mk(0, 20),
		mk(1, 40), mk(1, 50),
		mk(2, 70), mk(2, 80),
	}
}

// overlayIntegrationBaseRequest returns the canonical grouped Process
// request the integration tests share — GROUP_CATEGORY on region with
// AGG_SUM over score. Overlays slot is left empty so each test can
// install its own spec list without rewriting the base.
func overlayIntegrationBaseRequest() *types.Request {
	return &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum_score"},
		},
	}
}

// TestProcess_OverlaysSeriesPopulatesResponse verifies the E3-S6 grouped
// Process wiring lands: a Request{Groups, Aggregations, Overlays} call
// against Processor.Process populates Response.Overlays with one layer
// per spec in matching order. The layer payload matches the E3-S2
// INDEX_VS_TOTAL handler output for the three known per-region totals
// (30, 90, 150) over a grand total of 270.
func TestProcess_OverlaysSeriesPopulatesResponse(t *testing.T) {
	schema := overlayIntegrationSchema(t)
	recs := overlayIntegrationRecords(schema)
	req := overlayIntegrationBaseRequest()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsTotal,
			Scope: types.OverlayScopeRow,
		},
	}
	proc := NewProcessor(schema)
	iter := NewSliceIterator(recs)
	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("expected 1 overlay layer, got %d", len(resp.Overlays))
	}
	layer := resp.Overlays[0]
	if layer.Kind != types.OverlayKindIndexVsTotal {
		t.Fatalf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindIndexVsTotal)
	}
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Fatalf("layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeSeries)
	}
	if layer.Payload.Series == nil {
		t.Fatalf("layer.Payload.Series nil")
	}
	entries := layer.Payload.Series.Entries
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (one per group), got %d", len(entries))
	}
	// INDEX_VS_TOTAL per-entry: each entry's statistic must be
	// (group_total / grand_total) * 100. Indexed values sum to 100 by
	// construction (sum of shares × 100).
	const eps = 1e-9
	var sum float64
	for i, e := range entries {
		if e.Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil", i)
		}
		sum += *e.Summary.Statistic
	}
	if diff := sum - 100.0; diff > eps || diff < -eps {
		t.Fatalf("Σ indexed statistics = %v, want 100 (FR INDEX_VS_TOTAL)", sum)
	}
}

// TestProcess_NoOverlaysSeriesPreservesBaseline is the PRD §9 additive
// byte-identity check for the grouped Process path: a Request with no
// Overlays slot must produce a Response whose Overlays field is nil and
// whose Data is byte-identical to a no-overlay baseline. The hook
// must never mutate the host result when req.Overlays is empty.
func TestProcess_NoOverlaysSeriesPreservesBaseline(t *testing.T) {
	schema := overlayIntegrationSchema(t)
	recs := overlayIntegrationRecords(schema)

	baseReq := overlayIntegrationBaseRequest()
	baseResp, err := NewProcessor(schema).Process(context.Background(), baseReq, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("baseline Process: %v", err)
	}
	if baseResp.Overlays != nil {
		t.Fatalf("baseline must not populate Overlays: %+v", baseResp.Overlays)
	}

	emptyReq := overlayIntegrationBaseRequest()
	emptyReq.Overlays = nil
	emptyResp, err := NewProcessor(schema).Process(context.Background(), emptyReq, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("empty-overlays Process: %v", err)
	}
	if emptyResp.Overlays != nil {
		t.Fatalf("empty overlays must leave Response.Overlays nil: %+v",
			emptyResp.Overlays)
	}
	if !reflect.DeepEqual(baseResp.Data, emptyResp.Data) {
		t.Fatalf("data byte-identity violated when overlays empty\nbase=%+v\nempty=%+v",
			baseResp.Data, emptyResp.Data)
	}
	if !reflect.DeepEqual(baseResp.Warnings, emptyResp.Warnings) {
		t.Fatalf("warnings drift when overlays empty: base=%v empty=%v",
			baseResp.Warnings, emptyResp.Warnings)
	}
}

// TestProcess_OverlayKindUnknown verifies the runtime mirror of the
// E3-S1 validation defense: a Request.Overlays entry naming an
// unregistered kind produces a CodedError whose details carry the
// canonical PULSE_OVERLAY_KIND_UNKNOWN code. Predict catches this at
// validation time; the runtime gate is defense in depth. Mirrors the
// MATRIX path's TestCrosstab_UnknownOverlayKindReturnsCodedError.
func TestProcess_OverlayKindUnknown(t *testing.T) {
	schema := overlayIntegrationSchema(t)
	recs := overlayIntegrationRecords(schema)
	req := overlayIntegrationBaseRequest()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKind("OVERLAY_NOT_A_REAL_SERIES_KIND"),
			Scope: types.OverlayScopeRow,
		},
	}
	proc := NewProcessor(schema)
	iter := NewSliceIterator(recs)
	resp, err := proc.Process(context.Background(), req, iter)
	if err == nil {
		t.Fatalf("expected error for unknown overlay kind, got resp=%+v", resp)
	}
	coded, ok := err.(*pulseerrors.CodedError)
	if !ok {
		t.Fatalf("err type = %T, want *pulseerrors.CodedError", err)
	}
	if got, want := coded.Details["code"], string(pulseerrors.PULSE_OVERLAY_KIND_UNKNOWN); got != want {
		t.Fatalf("err.Details[\"code\"] = %v, want %v", got, want)
	}
	if got := coded.Details["host"]; got != "series" {
		t.Fatalf("err.Details[\"host\"] = %v, want %q", got, "series")
	}
}

// TestProcess_OverlayMixedModeDowngrades verifies the mixed-mode
// downgrade rule (PRD §6): a Request mixing a streamable overlay
// (E3-S2/S3/S4 — OVERLAY_INDEX_VS_TOTAL is streamable per
// types.OverlayStreamability) with a non-streamable overlay
// (E3-S5 — OVERLAY_INDEX_VS_SIBLING is buffered) forces the entire
// Request through the buffered path so the post-finalize hook sees a
// complete SeriesHostView. The CanStreamRequest gate is the single
// decision point — once it returns false the buffered processRecords
// path runs and Response.Overlays still populates correctly.
func TestProcess_OverlayMixedModeDowngrades(t *testing.T) {
	schema := overlayIntegrationSchema(t)
	req := overlayIntegrationBaseRequest()
	// Pure streamable: INDEX_VS_TOTAL alone. CanStreamRequest should
	// admit (this is the baseline so we know the path-agnostic assertion
	// below is exercising the downgrade rather than another gate).
	req.Overlays = []types.OverlaySpec{
		{Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeRow},
	}
	if !CanStreamRequest(req, schema) {
		t.Fatalf("CanStreamRequest=false for pure-streamable overlay slate; expected true so the downgrade rule is the only knob this test moves")
	}
	// Mixed: streamable INDEX_VS_TOTAL + non-streamable INDEX_VS_SIBLING.
	// CanStreamRequest MUST return false (mixed-mode downgrade rule
	// per PRD §6).
	req.Overlays = []types.OverlaySpec{
		{Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeRow},
		{
			Kind:  types.OverlayKindIndexVsSibling,
			Scope: types.OverlayScopeRow,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: "east"},
			},
		},
	}
	if CanStreamRequest(req, schema) {
		t.Fatalf("CanStreamRequest=true for mixed-mode overlay slate; expected false (mixed streamable + non-streamable forces buffered per PRD §6)")
	}
	// A pure non-streamable slate also forces buffered.
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsSibling,
			Scope: types.OverlayScopeRow,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: "east"},
			},
		},
	}
	if CanStreamRequest(req, schema) {
		t.Fatalf("CanStreamRequest=true for pure non-streamable overlay slate; expected false")
	}
	// Unknown overlay kind also forces buffered (so ApplyOverlaysSeries
	// surfaces PULSE_OVERLAY_KIND_UNKNOWN from the buffered exit).
	req.Overlays = []types.OverlaySpec{
		{Kind: types.OverlayKind("OVERLAY_UNKNOWN_KIND_FOR_DOWNGRADE"), Scope: types.OverlayScopeRow},
	}
	if CanStreamRequest(req, schema) {
		t.Fatalf("CanStreamRequest=true for unknown overlay kind; expected false (unknown kinds force buffered)")
	}
}
