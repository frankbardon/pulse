package processing

import (
	"context"
	"math"
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

// E3-S8 — broad cross-mode equivalence + slice-wide invariants.
//
// The S2/S3/S4 per-handler suites already pin streaming-vs-buffered
// byte-identity at the ApplyOverlaysSeries entry point against
// synthetic SeriesHostView fixtures. S8 lifts that contract one layer
// up — driving Processor.Process() against a hermetic record cohort
// and asserting the same byte-identity invariants hold across the
// streaming-eligible AND buffered orchestrator branches. The buffered
// variant in each pair forces the orchestrator off the streaming path
// by adding an AGG_MEDIAN aggregator (Streamable() == false), so the
// orchestrator routes through processRecords; the streaming variant
// uses only AGG_SUM (Streamable() == true) so the orchestrator routes
// through processStreamingGrouped.
//
// Math reference (fixture in overlayIntegrationRecords): three regions
// {east, west, north} with per-region AGG_SUM(score) = {30, 90, 150}.
// Grand total = 270. INDEX_VS_TOTAL = group/270*100 ∈ {11.111, 33.333,
// 55.555} (Σ = 100). SHARE_OF_TOTAL = group/270 ∈ {0.111, 0.333,
// 0.555} (Σ = 1). ZSCORE_VS_TOTAL: mean=90, M2 = (30-90)² + 0 +
// (150-90)² = 7200, sd = sqrt(7200/3) = sqrt(2400). z = (30-90)/sd, 0,
// (150-90)/sd (Σ z = 0, Σ z² = N).

// overlayE2EUlpTol mirrors the per-handler suites' tolerance — the
// Welford accumulator (single-pass Chan-Welford in the streaming
// path) matches a classical two-pass reducer within a few ULPs on
// benign inputs. The integer-valued fixture (30 / 90 / 150) is
// numerically benign so the bound stays tight; we follow the
// processing/aggregator_cohort tests' convention of using 1e-12 as
// the "within ULP via Welford" bound. The same constant gates every
// E3 cross-mode equivalence assertion below so a single regression
// reads consistently across kinds.
const overlayE2EUlpTol = 1e-12

// forceBufferedAggregation returns an AGG_MEDIAN aggregator. Adding
// it to a Request flips CanStreamRequest() to false because
// AggregationType.Streamable() rejects AGG_MEDIAN — the streaming
// fold cannot maintain a percentile-class statistic online (a sorted
// view of the input is required). The returned label is unused by the
// overlay fold (the primary aggregator for SeriesHostView is still
// req.Aggregations[0], AGG_SUM on score), so the buffered variant
// produces an identical Overlays payload to the streaming variant.
func forceBufferedAggregation() *types.Aggregation {
	return &types.Aggregation{
		Type:  types.AGG_MEDIAN,
		Field: "score",
		Label: "median_score_force_buffered",
	}
}

// runOverlayE2E drives Processor.Process against the canonical
// fixture with the supplied overlay specs and (optional) buffered-
// forcing aggregator. Returns the populated Response plus the
// observed ProcessPath so the caller can pin streaming-vs-buffered
// routing at the orchestrator level.
func runOverlayE2E(t *testing.T, specs []types.OverlaySpec, forceBuffered bool) (*types.Response, ProcessPath) {
	t.Helper()
	schema := overlayIntegrationSchema(t)
	recs := overlayIntegrationRecords(schema)
	req := overlayIntegrationBaseRequest()
	if forceBuffered {
		req.Aggregations = append(req.Aggregations, forceBufferedAggregation())
	}
	req.Overlays = specs
	proc := NewProcessor(schema)
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp, proc.LastPath()
}

// assertOverlayLayerStatistics walks the per-entry Statistic values of
// a single SeriesPayload layer and compares them to the expected
// slice element-for-element with the overlayE2EUlpTol bound. Centralises
// the per-test assertion shape so each cross-mode equivalence test
// reads as a one-liner.
func assertOverlayLayerStatistics(t *testing.T, layer *types.OverlayLayer, want []float64) {
	t.Helper()
	if layer == nil {
		t.Fatalf("assertOverlayLayerStatistics: nil layer")
	}
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Fatalf("layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeSeries)
	}
	if layer.Payload.Series == nil {
		t.Fatalf("layer.Payload.Series nil")
	}
	entries := layer.Payload.Series.Entries
	if len(entries) != len(want) {
		t.Fatalf("entry count = %d, want %d", len(entries), len(want))
	}
	for i, w := range want {
		if entries[i].Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want %v", i, w)
		}
		if math.IsNaN(w) {
			if !math.IsNaN(*entries[i].Summary.Statistic) {
				t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN",
					i, *entries[i].Summary.Statistic)
			}
			continue
		}
		if diff := *entries[i].Summary.Statistic - w; diff > overlayE2EUlpTol || diff < -overlayE2EUlpTol {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want %v ± %v",
				i, *entries[i].Summary.Statistic, w, overlayE2EUlpTol)
		}
	}
}

// TestOverlay_IndexVsTotal_Streaming_E2E drives the streaming-eligible
// orchestrator (AGG_SUM only) end-to-end through Processor.Process
// against the hermetic three-region fixture. INDEX_VS_TOTAL is a
// streamable kind per types.OverlayStreamability so the orchestrator
// must route through processStreamingGrouped (PathStreaming) and the
// SeriesPayload values must match (group/grand_total) * 100 within ULP.
// Sum of indexed statistics is exactly 100 by construction.
func TestOverlay_IndexVsTotal_Streaming_E2E(t *testing.T) {
	resp, path := runOverlayE2E(t, []types.OverlaySpec{
		newIndexVsTotalSpec("idx_total"),
	}, false /*forceBuffered*/)
	if path != PathStreaming {
		t.Fatalf("LastPath() = %s, want %s (INDEX_VS_TOTAL is streamable)",
			path, PathStreaming)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("len(Overlays) = %d, want 1", len(resp.Overlays))
	}
	// Fixture row order: alphabetic — east(30), north(150), west(90).
	// INDEX_VS_TOTAL: 30/270*100, 150/270*100, 90/270*100.
	want := []float64{
		30.0 / 270.0 * 100.0,
		150.0 / 270.0 * 100.0,
		90.0 / 270.0 * 100.0,
	}
	assertOverlayLayerStatistics(t, &resp.Overlays[0], want)
	var sum float64
	for _, e := range resp.Overlays[0].Payload.Series.Entries {
		sum += *e.Summary.Statistic
	}
	if diff := sum - 100.0; diff > overlayE2EUlpTol || diff < -overlayE2EUlpTol {
		t.Fatalf("Σ INDEX_VS_TOTAL = %v, want 100 (PRD FR — INDEX_VS_TOTAL is a percent share)", sum)
	}
}

// TestOverlay_IndexVsTotal_Buffered_E2E forces the orchestrator off
// the streaming path by attaching an AGG_MEDIAN aggregator (which
// drops CanStreamRequest to false) and asserts the buffered-path
// SeriesPayload output is byte-identical (within ULP) to the streaming
// path's output. Cross-mode equivalence at Processor level — the
// guarantee S2's TestOverlay_IndexVsTotal_StreamingBufferedByteIdentical
// pins at handler entry, lifted to the orchestrator dispatch.
func TestOverlay_IndexVsTotal_Buffered_E2E(t *testing.T) {
	streamResp, streamPath := runOverlayE2E(t, []types.OverlaySpec{
		newIndexVsTotalSpec("idx_total"),
	}, false)
	if streamPath != PathStreaming {
		t.Fatalf("streaming variant LastPath() = %s, want %s",
			streamPath, PathStreaming)
	}
	bufResp, bufPath := runOverlayE2E(t, []types.OverlaySpec{
		newIndexVsTotalSpec("idx_total"),
	}, true /*forceBuffered*/)
	if bufPath != PathBuffered {
		t.Fatalf("buffered variant LastPath() = %s, want %s (AGG_MEDIAN should force buffered)",
			bufPath, PathBuffered)
	}
	// Per-entry equivalence within ULP. Streaming path uses a running
	// grand-total accumulator while the buffered path computes the
	// total from the materialised slice; both produce the same
	// integer-valued reduction (270) on this fixture, but we still
	// gate with overlayE2EUlpTol to keep the contract uniform across
	// kinds.
	streamEntries := streamResp.Overlays[0].Payload.Series.Entries
	bufEntries := bufResp.Overlays[0].Payload.Series.Entries
	if len(streamEntries) != len(bufEntries) {
		t.Fatalf("entry count drift: streaming=%d buffered=%d",
			len(streamEntries), len(bufEntries))
	}
	for i := range streamEntries {
		s := *streamEntries[i].Summary.Statistic
		b := *bufEntries[i].Summary.Statistic
		if diff := s - b; diff > overlayE2EUlpTol || diff < -overlayE2EUlpTol {
			t.Fatalf("entry[%d] streaming=%v buffered=%v (diff=%v)",
				i, s, b, diff)
		}
	}
}

// TestOverlay_ShareOfTotal_Streaming_E2E drives the streaming-eligible
// orchestrator end-to-end through Processor.Process for the SHARE
// dispatch of SHARE_OF_TOTAL. Streamable per types.OverlayStreamability;
// SeriesPayload values are raw shares (group/grand_total) so the per-
// group Σ over a complete partition is 1.0.
func TestOverlay_ShareOfTotal_Streaming_E2E(t *testing.T) {
	resp, path := runOverlayE2E(t, []types.OverlaySpec{
		newShareOfTotalSeriesSpec("share_total"),
	}, false)
	if path != PathStreaming {
		t.Fatalf("LastPath() = %s, want %s", path, PathStreaming)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("len(Overlays) = %d, want 1", len(resp.Overlays))
	}
	want := []float64{
		30.0 / 270.0,
		150.0 / 270.0,
		90.0 / 270.0,
	}
	assertOverlayLayerStatistics(t, &resp.Overlays[0], want)
	var sum float64
	for _, e := range resp.Overlays[0].Payload.Series.Entries {
		sum += *e.Summary.Statistic
	}
	if diff := sum - 1.0; diff > overlayE2EUlpTol || diff < -overlayE2EUlpTol {
		t.Fatalf("Σ SHARE_OF_TOTAL = %v, want 1.0 (PRD FR — shares sum to 1 over a complete partition)", sum)
	}
}

// TestOverlay_ShareOfTotal_Buffered_E2E mirrors the streaming variant
// with AGG_MEDIAN attached. Asserts byte-identity (within ULP) of the
// SeriesPayload output across modes.
func TestOverlay_ShareOfTotal_Buffered_E2E(t *testing.T) {
	streamResp, _ := runOverlayE2E(t, []types.OverlaySpec{
		newShareOfTotalSeriesSpec("share_total"),
	}, false)
	bufResp, bufPath := runOverlayE2E(t, []types.OverlaySpec{
		newShareOfTotalSeriesSpec("share_total"),
	}, true)
	if bufPath != PathBuffered {
		t.Fatalf("buffered variant LastPath() = %s, want %s", bufPath, PathBuffered)
	}
	streamEntries := streamResp.Overlays[0].Payload.Series.Entries
	bufEntries := bufResp.Overlays[0].Payload.Series.Entries
	if len(streamEntries) != len(bufEntries) {
		t.Fatalf("entry count drift: streaming=%d buffered=%d",
			len(streamEntries), len(bufEntries))
	}
	for i := range streamEntries {
		s := *streamEntries[i].Summary.Statistic
		b := *bufEntries[i].Summary.Statistic
		if diff := s - b; diff > overlayE2EUlpTol || diff < -overlayE2EUlpTol {
			t.Fatalf("entry[%d] streaming=%v buffered=%v (diff=%v)",
				i, s, b, diff)
		}
	}
}

// TestOverlay_ZScoreVsTotal_Streaming_E2E drives the streaming-eligible
// orchestrator through Processor.Process for ZSCORE_VS_TOTAL. The
// streaming path folds Welford over the per-group accumulators inside
// the streaming Process pass; the buffered path consumes the
// materialised slice. Asserts Σ z = 0 and Σ z² = N within ULP — the
// two canonical population-z invariants per S4's acceptance.
func TestOverlay_ZScoreVsTotal_Streaming_E2E(t *testing.T) {
	resp, path := runOverlayE2E(t, []types.OverlaySpec{
		newZScoreVsTotalSpec("z_total"),
	}, false)
	if path != PathStreaming {
		t.Fatalf("LastPath() = %s, want %s", path, PathStreaming)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("len(Overlays) = %d, want 1", len(resp.Overlays))
	}
	// Fixture: values = {30, 150, 90} alphabetic; mean = 90;
	// M2 = (30-90)² + (150-90)² + 0 = 7200; sd = sqrt(7200/3) = sqrt(2400).
	mean := 90.0
	sd := math.Sqrt(2400.0)
	want := []float64{
		(30.0 - mean) / sd,
		(150.0 - mean) / sd,
		(90.0 - mean) / sd,
	}
	assertOverlayLayerStatistics(t, &resp.Overlays[0], want)
	var sumZ, sumZ2 float64
	for _, e := range resp.Overlays[0].Payload.Series.Entries {
		z := *e.Summary.Statistic
		sumZ += z
		sumZ2 += z * z
	}
	if diff := sumZ; diff > overlayE2EUlpTol || diff < -overlayE2EUlpTol {
		t.Fatalf("Σ z = %v, want 0 ± %v (PRD FR — population z-score is mean-centred)",
			sumZ, overlayE2EUlpTol)
	}
	if diff := sumZ2 - 3.0; diff > overlayE2EUlpTol || diff < -overlayE2EUlpTol {
		t.Fatalf("Σ z² = %v, want N=3 ± %v (PRD FR — population z-score variance is 1)",
			sumZ2, overlayE2EUlpTol)
	}
}

// TestOverlay_ZScoreVsTotal_Buffered_E2E mirrors the streaming variant
// with AGG_MEDIAN attached. Asserts byte-identity (within ULP) of the
// Welford-derived SeriesPayload across modes. Welford-Pébaÿ in the
// streaming path matches the classical two-pass reducer in the
// buffered path on this benign fixture.
func TestOverlay_ZScoreVsTotal_Buffered_E2E(t *testing.T) {
	streamResp, _ := runOverlayE2E(t, []types.OverlaySpec{
		newZScoreVsTotalSpec("z_total"),
	}, false)
	bufResp, bufPath := runOverlayE2E(t, []types.OverlaySpec{
		newZScoreVsTotalSpec("z_total"),
	}, true)
	if bufPath != PathBuffered {
		t.Fatalf("buffered variant LastPath() = %s, want %s", bufPath, PathBuffered)
	}
	streamEntries := streamResp.Overlays[0].Payload.Series.Entries
	bufEntries := bufResp.Overlays[0].Payload.Series.Entries
	if len(streamEntries) != len(bufEntries) {
		t.Fatalf("entry count drift: streaming=%d buffered=%d",
			len(streamEntries), len(bufEntries))
	}
	for i := range streamEntries {
		s := *streamEntries[i].Summary.Statistic
		b := *bufEntries[i].Summary.Statistic
		if diff := s - b; diff > overlayE2EUlpTol || diff < -overlayE2EUlpTol {
			t.Fatalf("entry[%d] streaming=%v buffered=%v (diff=%v)",
				i, s, b, diff)
		}
	}
}

// TestOverlay_DeltaVsSibling_E2E drives the buffered orchestrator
// through Processor.Process for DELTA_VS_SIBLING. The kind is buffered
// per types.OverlayStreamability (sibling resolution requires the
// fully-materialised SeriesPayload), so the orchestrator forces
// PathBuffered via canStreamOverlays even with only AGG_SUM. Asserts
// the per-group additive delta math and the unknown-sibling warning
// path (PULSE_OVERLAY_REF_UNKNOWN).
func TestOverlay_DeltaVsSibling_E2E(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		resp, path := runOverlayE2E(t, []types.OverlaySpec{
			newDeltaVsSiblingSpec("delta_east", "region", "east"),
		}, false /*forceBuffered — already buffered by overlay kind*/)
		if path != PathBuffered {
			t.Fatalf("LastPath() = %s, want %s (DELTA_VS_SIBLING is buffered)",
				path, PathBuffered)
		}
		if len(resp.Overlays) != 1 {
			t.Fatalf("len(Overlays) = %d, want 1", len(resp.Overlays))
		}
		// Fixture row order: alphabetic — east(30), north(150), west(90).
		// Sibling = east(30) → deltas 0, 120, 60.
		want := []float64{0.0, 120.0, 60.0}
		assertOverlayLayerStatistics(t, &resp.Overlays[0], want)
	})

	t.Run("unknown_sibling_emits_ref_unknown_warning", func(t *testing.T) {
		resp, _ := runOverlayE2E(t, []types.OverlaySpec{
			newDeltaVsSiblingSpec("delta_unknown", "region", "south"),
		}, false)
		// PULSE_OVERLAY_REF_UNKNOWN is a WARNING-class code emitted by
		// the sibling resolver when the (field, value) pair does not
		// resolve to a present host group.
		hasRefUnknown := false
		for _, w := range resp.Warnings {
			if w.Code == string(pulseerrors.PULSE_OVERLAY_REF_UNKNOWN) {
				hasRefUnknown = true
				break
			}
		}
		if !hasRefUnknown {
			t.Fatalf("expected PULSE_OVERLAY_REF_UNKNOWN warning for unknown sibling, got warnings = %+v",
				resp.Warnings)
		}
		// Statistics across every entry surface as NaN when the sibling
		// is unresolvable.
		for i, e := range resp.Overlays[0].Payload.Series.Entries {
			if e.Summary.Statistic == nil {
				continue
			}
			if !math.IsNaN(*e.Summary.Statistic) {
				t.Errorf("entry[%d].Summary.Statistic = %v, want NaN (unknown sibling)",
					i, *e.Summary.Statistic)
			}
		}
	})
}

// TestOverlay_IndexVsSibling_E2E drives the buffered orchestrator
// through Processor.Process for INDEX_VS_SIBLING. Buffered per
// types.OverlayStreamability (sibling resolution requires the fully-
// materialised SeriesPayload). Asserts the (group / sibling) * 100
// math AND the zero-sibling warning path (PULSE_OVERLAY_REF_ZERO + NaN
// statistics). The fixture for the zero path overrides the per-region
// totals to drive the sibling group to a post-filter sum of zero.
func TestOverlay_IndexVsSibling_E2E(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		resp, path := runOverlayE2E(t, []types.OverlaySpec{
			newIndexVsSiblingSpec("idx_east", "region", "east"),
		}, false)
		if path != PathBuffered {
			t.Fatalf("LastPath() = %s, want %s (INDEX_VS_SIBLING is buffered)",
				path, PathBuffered)
		}
		if len(resp.Overlays) != 1 {
			t.Fatalf("len(Overlays) = %d, want 1", len(resp.Overlays))
		}
		// Fixture row order: alphabetic — east(30), north(150), west(90).
		// Sibling = east(30) → index = group/30 * 100 ⇒ 100, 500, 300.
		want := []float64{100.0, 500.0, 300.0}
		assertOverlayLayerStatistics(t, &resp.Overlays[0], want)
	})

	t.Run("zero_sibling_emits_ref_zero_warning", func(t *testing.T) {
		// Custom fixture: override the east records to sum to zero
		// (50 + -50) so the sibling resolves but its value is 0.
		schema := overlayIntegrationSchema(t)
		mk := func(region uint64, score float64) *Record {
			return NewRecord(schema, map[string]float64{
				"region": float64(region),
				"score":  score,
			})
		}
		recs := []*Record{
			mk(0, 50), mk(0, -50), // east sums to 0
			mk(1, 40), mk(1, 50), // west = 90
			mk(2, 70), mk(2, 80), // north = 150
		}
		req := overlayIntegrationBaseRequest()
		req.Overlays = []types.OverlaySpec{
			newIndexVsSiblingSpec("idx_zero", "region", "east"),
		}
		proc := NewProcessor(schema)
		resp, err := proc.Process(context.Background(), req, NewSliceIterator(recs))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		hasRefZero := false
		for _, w := range resp.Warnings {
			if w.Code == string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
				hasRefZero = true
				break
			}
		}
		if !hasRefZero {
			t.Fatalf("expected PULSE_OVERLAY_REF_ZERO warning for zero-valued sibling, got warnings = %+v",
				resp.Warnings)
		}
		for i, e := range resp.Overlays[0].Payload.Series.Entries {
			if e.Summary.Statistic == nil {
				continue
			}
			if !math.IsNaN(*e.Summary.Statistic) {
				t.Errorf("entry[%d].Summary.Statistic = %v, want NaN (zero sibling)",
					i, *e.Summary.Statistic)
			}
		}
	})
}

// TestStreamability_OverlaysKnown_ProcessSubset is the E3-narrow
// specialisation of the slice-wide TestStreamability_OverlaysKnown
// gate in types/overlay_streamability_test.go. It asserts every kind
// in the E3 grouped-Process subset (INDEX_VS_TOTAL / SHARE_OF_TOTAL /
// ZSCORE_VS_TOTAL streamable; DELTA_VS_SIBLING / INDEX_VS_SIBLING
// buffered) has a row in types.OverlayStreamability matching the
// runtime declaration.
//
// Distinct from the full-table gate: this test reads only the five
// E3 kinds — the canStreamOverlays runtime helper consults the same
// table, and a regression on any one of these five would break
// streaming-vs-buffered routing for grouped Process. The full table
// gate enforces completeness across every kind in
// AllOverlayKinds(); this subset gate documents that E3-S2/S3/S4 are
// streamable and E3-S5 is buffered, per the kind-catalog-v1
// "Streaming-capable subset" carve-out.
func TestStreamability_OverlaysKnown_ProcessSubset(t *testing.T) {
	cases := []struct {
		kind            types.OverlayKind
		wantStreamable  bool
		runtimeBranchTo string
	}{
		{types.OverlayKindIndexVsTotal, true, "streaming-grouped pass"},
		{types.OverlayKindShareOfTotal, true, "streaming-grouped pass"},
		{types.OverlayKindZScoreVsTotal, true, "streaming-grouped pass"},
		{types.OverlayKindDeltaVsSibling, false, "buffered processRecords"},
		{types.OverlayKindIndexVsSibling, false, "buffered processRecords"},
	}
	for _, c := range cases {
		streamable, known := types.OverlayStreamable(c.kind)
		if !known {
			t.Errorf("OverlayStreamable(%s) known=false; expected row in OverlayStreamability for the E3 subset",
				c.kind)
			continue
		}
		if streamable != c.wantStreamable {
			t.Errorf("OverlayStreamable(%s) = %v, want %v (E3 routing: %s)",
				c.kind, streamable, c.wantStreamable, c.runtimeBranchTo)
		}
	}
}

// TestOverlay_SeriesSpecOrderPreserved_E2E asserts the response-layer
// order matches the request-spec order element-for-element. Per FR-A2
// (PRD §3) the orchestrator emits one OverlayLayer per OverlaySpec in
// matching slot order. A regression here would surface as cross-mode
// drift the renderer would silently misinterpret — laying a Z-score
// payload onto a SHARE renderer slot.
//
// Spec order: [ZSCORE_VS_TOTAL, INDEX_VS_TOTAL, SHARE_OF_TOTAL]. All
// three streamable so the test exercises the streaming-grouped exit.
func TestOverlay_SeriesSpecOrderPreserved_E2E(t *testing.T) {
	specs := []types.OverlaySpec{
		newZScoreVsTotalSpec("z_total"),
		newIndexVsTotalSpec("idx_total"),
		newShareOfTotalSeriesSpec("share_total"),
	}
	resp, path := runOverlayE2E(t, specs, false)
	if path != PathStreaming {
		t.Fatalf("LastPath() = %s, want %s (all three kinds streamable)", path, PathStreaming)
	}
	if len(resp.Overlays) != len(specs) {
		t.Fatalf("len(Overlays) = %d, want %d", len(resp.Overlays), len(specs))
	}
	wantKinds := []types.OverlayKind{
		types.OverlayKindZScoreVsTotal,
		types.OverlayKindIndexVsTotal,
		types.OverlayKindShareOfTotal,
	}
	for i, want := range wantKinds {
		if got := resp.Overlays[i].Kind; got != want {
			t.Errorf("Overlays[%d].Kind = %q, want %q (spec-order guarantee)",
				i, got, want)
		}
	}
	// Renderer-facing name pinning: spec.Name → layer.Name verbatim.
	wantNames := []string{"z_total", "idx_total", "share_total"}
	for i, want := range wantNames {
		if got := resp.Overlays[i].Name; got != want {
			t.Errorf("Overlays[%d].Name = %q, want %q",
				i, got, want)
		}
	}
}

// TestOverlay_NoOverlaysAdditiveByteIdentity_E2E lifts S6's
// TestProcess_NoOverlaysSeriesPreservesBaseline byte-identity assertion
// from Response.Data + Response.Warnings to the full Response (including
// Overlays). Confirms that a Request with `Overlays: nil` produces a
// Response that is structurally indistinguishable from a Request that
// omits the Overlays slot entirely.
//
// PRD §9 (additive viz contract): adding overlays MUST NOT mutate the
// baseline response. The S6 test covers Data + Warnings; this test
// extends coverage to the full Response surface, including the Overlays
// field itself (must be nil for both shapes — leaving the JSON
// `overlays` slot omitted via omitempty rather than emitting an empty
// array). DeepEqual asserts every exported field matches.
func TestOverlay_NoOverlaysAdditiveByteIdentity_E2E(t *testing.T) {
	schema := overlayIntegrationSchema(t)
	recs := overlayIntegrationRecords(schema)

	// Variant A: Request with no Overlays slot at all (the legacy
	// shape — Overlays field stays nil).
	baseReq := overlayIntegrationBaseRequest()
	baseResp, err := NewProcessor(schema).Process(context.Background(), baseReq, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("baseline Process: %v", err)
	}

	// Variant B: Request with explicit empty Overlays slice. Wire
	// shape preserves additivity by omitempty — both variants must
	// produce IDENTICAL responses.
	emptyReq := overlayIntegrationBaseRequest()
	emptyReq.Overlays = []types.OverlaySpec{}
	emptyResp, err := NewProcessor(schema).Process(context.Background(), emptyReq, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("empty-overlays Process: %v", err)
	}

	if baseResp.Overlays != nil {
		t.Fatalf("baseline response Overlays must be nil; got %+v", baseResp.Overlays)
	}
	if emptyResp.Overlays != nil {
		t.Fatalf("empty-overlays response Overlays must be nil; got %+v", emptyResp.Overlays)
	}
	if !reflect.DeepEqual(baseResp.Data, emptyResp.Data) {
		t.Fatalf("Data byte-identity violated:\nbase=%+v\nempty=%+v",
			baseResp.Data, emptyResp.Data)
	}
	if !reflect.DeepEqual(baseResp.Warnings, emptyResp.Warnings) {
		t.Fatalf("Warnings drift:\nbase=%v\nempty=%v",
			baseResp.Warnings, emptyResp.Warnings)
	}
	if !reflect.DeepEqual(baseResp.Metadata, emptyResp.Metadata) {
		t.Fatalf("Metadata drift:\nbase=%+v\nempty=%+v",
			baseResp.Metadata, emptyResp.Metadata)
	}
	if !reflect.DeepEqual(baseResp.PostTests, emptyResp.PostTests) {
		t.Fatalf("PostTests drift:\nbase=%+v\nempty=%+v",
			baseResp.PostTests, emptyResp.PostTests)
	}
}

// TestOverlay_MixedModeForceBuffered_E2E asserts a mixed-mode overlay
// slate (one streamable + one buffered kind) forces the entire
// Request through the buffered processRecords path. The
// canStreamOverlays gate (processing/overlay_series_response.go) is
// the single decision point; once it returns false the orchestrator
// dispatches PathBuffered regardless of the streaming-eligibility of
// the underlying aggregations. Confirms BOTH overlays still surface
// correctly at the buffered exit.
//
// Companion to TestProcess_OverlayMixedModeDowngrades (which pins
// canStream returning false) — this test drives the full Process
// pipeline so the assertions cover the dispatched path AND the
// post-finalize Overlays output.
func TestOverlay_MixedModeForceBuffered_E2E(t *testing.T) {
	resp, path := runOverlayE2E(t, []types.OverlaySpec{
		newIndexVsTotalSpec("idx_total_streamable"),
		newDeltaVsSiblingSpec("delta_buffered", "region", "east"),
	}, false /*forceBuffered — mixed slate already downgrades*/)
	if path != PathBuffered {
		t.Fatalf("LastPath() = %s, want %s (mixed streamable+buffered slate forces buffered)",
			path, PathBuffered)
	}
	if len(resp.Overlays) != 2 {
		t.Fatalf("len(Overlays) = %d, want 2 (both kinds still fold at buffered exit)",
			len(resp.Overlays))
	}
	// Kind order matches spec order.
	if got := resp.Overlays[0].Kind; got != types.OverlayKindIndexVsTotal {
		t.Errorf("Overlays[0].Kind = %q, want %q",
			got, types.OverlayKindIndexVsTotal)
	}
	if got := resp.Overlays[1].Kind; got != types.OverlayKindDeltaVsSibling {
		t.Errorf("Overlays[1].Kind = %q, want %q",
			got, types.OverlayKindDeltaVsSibling)
	}
	// INDEX_VS_TOTAL math still holds at buffered exit. Fixture row
	// order: alphabetic — east(30), north(150), west(90).
	wantIdx := []float64{
		30.0 / 270.0 * 100.0,
		150.0 / 270.0 * 100.0,
		90.0 / 270.0 * 100.0,
	}
	assertOverlayLayerStatistics(t, &resp.Overlays[0], wantIdx)
	// DELTA_VS_SIBLING math: sibling=east(30) → deltas 0, 120, 60.
	wantDelta := []float64{0.0, 120.0, 60.0}
	assertOverlayLayerStatistics(t, &resp.Overlays[1], wantDelta)
}
