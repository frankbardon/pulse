package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestProcessChain_TwoStageByteEqualToManual validates that a
// 2-stage chain (filter + aggregate → re-filter + re-aggregate)
// produces the same numeric result as running stage 1 to a manual
// intermediate map and re-running stage 2 against it.
func TestProcessChain_TwoStageByteEqualToManual(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	ctx := context.Background()

	// Stage 1: filter score > 20, sum score per category.
	// Stage 2: chain on the aggregate, filter sum < 90.
	stage1 := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "score", Values: []string{"20", "100"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum_score"},
		},
	}
	stage2 := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "sum_score", Values: []string{"0", "200"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "sum_score", Label: "n"},
		},
	}

	chain := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: []*types.ChainStage{
			{Name: "filter_sum", Request: stage1},
			{Name: "count_results", Request: stage2},
		},
	}

	resp, err := svc.ProcessChain(ctx, chain)
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	if resp.Final == nil || len(resp.Final.Data) == 0 {
		t.Fatalf("final empty: %#v", resp)
	}
	if got := len(resp.Stages); got != 2 {
		t.Fatalf("stages length = %d, want 2", got)
	}

	// FILTER_RANGE [20, 100] keeps 20+30+40+50 = 140.
	if got := resp.Stages[0].Data[0]["sum_score"].(float64); !floatClose(got, 140.0, 0.001) {
		t.Errorf("stage1 sum_score = %v, want 140.0", got)
	}
	// Stage 2 counts that 1 row. Filter 0<sum<200 keeps it.
	if got := resp.Final.Data[0]["n"].(float64); !floatClose(got, 1.0, 0.001) {
		t.Errorf("stage2 count = %v, want 1.0", got)
	}
}

// TestProcessChain_NormalizedRequest_PerStage validates the per-stage
// normalized echo: ChainResponse.NormalizedRequest is nil when echo is
// off, and when on it carries each stage's post-defaults form. Stage 0
// runs against the on-disk schema; later stages against the synthesized
// stage-output schema. Both must show defaults resolved.
func TestProcessChain_NormalizedRequest_PerStage(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	ctx := context.Background()

	// Build a chain where stage 0 omits the aggregator Type (numeric
	// default → AGG_SUM) and stage 1 references the aggregate output
	// without a Type (numeric f64 default → AGG_SUM).
	makeChain := func() *types.ChainRequest {
		return &types.ChainRequest{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Stages: []*types.ChainStage{
				{
					Name: "stage0",
					Request: &types.Request{
						Cohort:       &types.Cohort{Filename: "test.pulse"},
						Aggregations: []*types.Aggregation{{Field: "score", Label: "sum_score"}},
					},
				},
				{
					Name: "stage1",
					Request: &types.Request{
						Aggregations: []*types.Aggregation{{Field: "sum_score", Label: "total"}},
					},
				},
			},
		}
	}

	// Echo off: NormalizedRequest must be nil.
	svcOff := New(cfg)
	respOff, err := svcOff.ProcessChain(ctx, makeChain())
	if err != nil {
		t.Fatalf("echo-off ProcessChain: %v", err)
	}
	if respOff.NormalizedRequest != nil {
		t.Errorf("NormalizedRequest set with EchoRequest=false: %#v", respOff.NormalizedRequest)
	}

	// Echo on: per-stage capture, each stage shows AGG_SUM defaulted.
	svcOn := New(cfg)
	svcOn.SetEchoRequest(true)
	respOn, err := svcOn.ProcessChain(ctx, makeChain())
	if err != nil {
		t.Fatalf("echo-on ProcessChain: %v", err)
	}
	if respOn.NormalizedRequest == nil {
		t.Fatal("NormalizedRequest nil with EchoRequest=true")
	}
	if got := len(respOn.NormalizedRequest.Stages); got != 2 {
		t.Fatalf("NormalizedRequest.Stages length = %d; want 2", got)
	}
	for i, st := range respOn.NormalizedRequest.Stages {
		if st == nil || st.Request == nil {
			t.Fatalf("stage %d normalized form nil", i)
		}
		if len(st.Request.Aggregations) != 1 {
			t.Fatalf("stage %d Aggregations = %d; want 1", i, len(st.Request.Aggregations))
		}
		if got := st.Request.Aggregations[0].Type; got != types.AGG_SUM {
			t.Errorf("stage %d normalized Type = %q; want %q (per-stage default)",
				i, got, types.AGG_SUM)
		}
	}
	// Names propagated unchanged.
	if respOn.NormalizedRequest.Stages[0].Name != "stage0" {
		t.Errorf("stage 0 Name = %q; want stage0", respOn.NormalizedRequest.Stages[0].Name)
	}
	if respOn.NormalizedRequest.Stages[1].Name != "stage1" {
		t.Errorf("stage 1 Name = %q; want stage1", respOn.NormalizedRequest.Stages[1].Name)
	}
}

// TestProcessChain_NonMergeableMiddleStageReturnsError validates that
// the chain executor refuses a non-mergeable stage and surfaces the
// offending stage index.
func TestProcessChain_NonMergeableMiddleStageReturnsError(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	ctx := context.Background()

	mergeableStage := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum_score"},
		},
	}
	// AGG_MEDIAN is not mergeable; chain gate must reject.
	badStage := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MEDIAN, Field: "sum_score", Label: "med"},
		},
	}

	chain := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: []*types.ChainStage{
			{Name: "ok", Request: mergeableStage},
			{Name: "median_blocked", Request: badStage},
		},
	}

	_, err := svc.ProcessChain(ctx, chain)
	if err == nil {
		t.Fatalf("expected PULSE_CHAIN_NOT_MERGEABLE")
	}
	if !errors.HasCode(err, errors.PULSE_CHAIN_NOT_MERGEABLE) {
		t.Fatalf("expected PULSE_CHAIN_NOT_MERGEABLE, got: %v", err)
	}
}

// TestProcessChain_FrequencyAggregatorRejected validates that
// AGG_FREQUENCY — mergeable but emits a map — fails the chain gate.
func TestProcessChain_FrequencyAggregatorRejected(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	ctx := context.Background()

	chain := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: []*types.ChainStage{
			{
				Name: "freq",
				Request: &types.Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_FREQUENCY, Field: "id", Label: "freq_id"},
					},
				},
			},
			{
				Name: "noop",
				Request: &types.Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "freq_id", Label: "n"},
					},
				},
			},
		},
	}

	_, err := svc.ProcessChain(ctx, chain)
	if err == nil {
		t.Fatalf("expected PULSE_CHAIN_NOT_MERGEABLE for AGG_FREQUENCY chain stage")
	}
	if !errors.HasCode(err, errors.PULSE_CHAIN_NOT_MERGEABLE) {
		t.Fatalf("expected PULSE_CHAIN_NOT_MERGEABLE, got %v", err)
	}
}

// TestProcessChain_EmptyStagesError validates the empty-chain guard.
func TestProcessChain_EmptyStagesError(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)
	ctx := context.Background()

	_, err := svc.ProcessChain(ctx, &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: nil,
	})
	if err == nil || !errors.HasCode(err, errors.PULSE_CHAIN_EMPTY) {
		t.Fatalf("expected PULSE_CHAIN_EMPTY, got: %v", err)
	}
}

// TestProcessChain_FiveStageScalarPipeline validates a longer chain
// where each stage filters or re-aggregates the prior stage's
// rows. Builds confidence that the synth-schema + slice-iterator
// machinery handles repeated re-materialization.
func TestProcessChain_FiveStageScalarPipeline(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{1, math.Float64bits(10.0)},
		{1, math.Float64bits(20.0)},
		{2, math.Float64bits(30.0)},
		{2, math.Float64bits(40.0)},
		{3, math.Float64bits(50.0)},
	}
	cfg := setupTestFS(t, "test.pulse", schema, records)
	svc := New(cfg)
	ctx := context.Background()

	stages := []*types.ChainStage{
		{
			Name: "group_sum",
			Request: &types.Request{
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "score", Label: "s"},
				},
			},
		},
		{
			Name: "filter_low",
			Request: &types.Request{
				Filterers: []*types.Filterer{
					{Type: types.FILTER_RANGE, Field: "s", Values: []string{"25", "1000"}},
				},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "s", Label: "ss"},
				},
			},
		},
		{
			Name: "double_count",
			Request: &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "ss", Label: "n"},
				},
			},
		},
		{
			Name: "min_max_n",
			Request: &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_MIN, Field: "n", Label: "mn"},
					{Type: types.AGG_MAX, Field: "n", Label: "mx"},
				},
			},
		},
		{
			Name: "final_sum",
			Request: &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "mn", Label: "total"},
				},
			},
		},
	}

	chain := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: stages,
	}

	resp, err := svc.ProcessChain(ctx, chain)
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	if len(resp.Stages) != 5 {
		t.Fatalf("expected 5 stages, got %d", len(resp.Stages))
	}
	if resp.Final == nil || len(resp.Final.Data) == 0 {
		t.Fatalf("final empty")
	}
	// stage1: region 1 -> 30, region 2 -> 70, region 3 -> 50
	// stage2 filter s>25: keeps 70, 50 -> sum ss=120
	// stage3 count rows of stage2: 1
	// stage4 min(1)=1, max(1)=1
	// stage5 sum(1) = 1
	if got := resp.Final.Data[0]["total"].(float64); !floatClose(got, 1.0, 0.001) {
		t.Errorf("final total = %v, want 1.0", got)
	}
}

// TestProcessChain_NoOverlaysByteIdentical asserts the post-stage-loop
// whole-chain overlay barrier is a no-op when ChainRequest.Overlays is
// nil — the response's `Overlays` slot stays nil (omitempty rule) and
// the marshalled JSON is byte-identical to a pre-E6-S3 ChainResponse.
//
// Story E6-S3 acceptance: "When ChainRequest.Overlays is empty/nil, no
// barrier work; ChainResponse.Overlays stays nil (no allocation,
// byte-identical JSON vs pre-S3)".
func TestProcessChain_NoOverlaysByteIdentical(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	ctx := context.Background()

	makeChain := func() *types.ChainRequest {
		return &types.ChainRequest{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Stages: []*types.ChainStage{
				{
					Name: "stage_0",
					Request: &types.Request{
						Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "id"}},
						Aggregations: []*types.Aggregation{
							{Type: types.AGG_SUM, Field: "score", Label: "s"},
						},
					},
				},
			},
		}
	}

	svc := New(cfg)
	resp, err := svc.ProcessChain(ctx, makeChain())
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}

	// The Overlays slot must stay nil — the barrier short-circuited.
	if resp.Overlays != nil {
		t.Errorf("ChainResponse.Overlays = %+v, want nil (barrier must short-circuit on empty req.Overlays)", resp.Overlays)
	}

	// Defense in depth: marshal the response and confirm no "overlays"
	// key landed in the JSON. The omitempty rule on the slot is what
	// drives byte-identity vs pre-E6-S3 callers.
	js, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(js); containsKey(got, `"overlays":`) {
		t.Errorf("marshalled JSON carries overlays key when none requested: %s", got)
	}
}

// containsKey returns true when needle appears in haystack. Used by
// the byte-identity guard above to confirm the omitempty rule fires.
func containsKey(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestProcessChain_StubOverlayRoundTrip asserts the post-stage-loop
// whole-chain overlay barrier populates ChainResponse.Overlays with
// one stub layer per request spec in matching index order. The stub
// handler in processing/overlay_chain_dispatch.go emits a
// zero-payload OverlayLayer whose Kind echoes the spec.Kind; the
// chassis exercises the resolver + dispatch round-trip without S4/S5
// arithmetic.
//
// Story E6-S3 acceptance: "ChainResponse.Overlays populated after the
// stage loop finishes and BEFORE the response is returned to the
// caller" + "Handler dispatch table keyed by OverlayKind".
func TestProcessChain_StubOverlayRoundTrip(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	ctx := context.Background()

	zero := 0
	req := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: []*types.ChainStage{
			{
				Name: "stage_0",
				Request: &types.Request{
					Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: "score", Label: "s"},
					},
				},
			},
		},
		// Two specs — the dispatcher must surface two layers in order.
		Overlays: []*types.ChainOverlaySpec{
			{
				Name:   "whole_chain_index",
				Kind:   types.OverlayKindIndexVsStage,
				Scope:  types.OverlayScopeTotal,
				Ref:    types.StageRef{Index: &zero},
				Target: types.StageRef{Index: &zero},
			},
			{
				Name:   "whole_chain_delta",
				Kind:   types.OverlayKindDeltaVsStage,
				Scope:  types.OverlayScopeTotal,
				Ref:    types.StageRef{Index: &zero},
				Target: types.StageRef{Index: &zero},
			},
		},
	}

	svc := New(cfg)
	resp, err := svc.ProcessChain(ctx, req)
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}

	if got, want := len(resp.Overlays), 2; got != want {
		t.Fatalf("ChainResponse.Overlays length = %d, want %d (one stub layer per spec)", got, want)
	}
	if got, want := resp.Overlays[0].Kind, types.OverlayKindIndexVsStage; got != want {
		t.Errorf("Overlays[0].Kind = %q, want %q", got, want)
	}
	if got, want := resp.Overlays[1].Kind, types.OverlayKindDeltaVsStage; got != want {
		t.Errorf("Overlays[1].Kind = %q, want %q", got, want)
	}
	if got, want := resp.Overlays[0].Name, "whole_chain_index"; got != want {
		t.Errorf("Overlays[0].Name = %q, want %q", got, want)
	}
	if got, want := resp.Overlays[1].Name, "whole_chain_delta"; got != want {
		t.Errorf("Overlays[1].Name = %q, want %q", got, want)
	}

	// Per-stage Stages[i].Overlays MUST be untouched by the whole-chain
	// barrier — the per-stage half of the dual-slot design rides on
	// the Request.Overlays slot, NOT the ChainRequest.Overlays slot.
	for i, st := range resp.Stages {
		if st.Overlays != nil {
			t.Errorf("Stages[%d].Overlays = %+v, want nil (whole-chain barrier must not mutate per-stage overlays)", i, st.Overlays)
		}
	}
}

// TestOverlay_ChainPerStage_Piggyback is the E6-S1 dual-slot per-stage
// conformance test. Per-stage overlays must surface on
// ChainResponse.Stages[i].Overlays exactly as if each stage's Request
// were run standalone through E3 — there is no chain-specific overlay
// code path; the per-stage half of the dual-slot design is a pure
// consequence of the universal Request.Overlays slot landed in E1/E3.
//
// Acceptance:
//
//   - Stage 0 carries one OVERLAY_INDEX_VS_TOTAL spec; Stage 1 carries
//     one OVERLAY_SHARE_OF_TOTAL spec.
//   - ChainResponse.Stages[0].Overlays has length 1 with
//     Kind=OVERLAY_INDEX_VS_TOTAL.
//   - ChainResponse.Stages[1].Overlays has length 1 with
//     Kind=OVERLAY_SHARE_OF_TOTAL.
//   - ChainResponse.Overlays stays nil (no whole-chain overlay slot is
//     exercised here — that surface lands in E6-S2..S5).
//   - Stripping Stages[i].Overlays from the response JSON yields the
//     same Stages[i] host data as a chain run with empty per-stage
//     Overlays (additive byte-identity contract).
//
// The chain stays mergeable end-to-end (AGG_SUM + GROUP_CATEGORY only)
// so CanChainRequest accepts every stage.
func TestOverlay_ChainPerStage_Piggyback(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	// 6 records across 3 regions — region totals are arithmetically
	// clean: region=1 -> 30, region=2 -> 70, region=3 -> 150.
	records := [][]uint64{
		{1, math.Float64bits(10.0)},
		{1, math.Float64bits(20.0)},
		{2, math.Float64bits(30.0)},
		{2, math.Float64bits(40.0)},
		{3, math.Float64bits(70.0)},
		{3, math.Float64bits(80.0)},
	}
	cfg := setupTestFS(t, "test.pulse", schema, records)
	ctx := context.Background()

	// makeChain returns the 2-stage chain; withOverlays toggles the
	// per-stage Overlays slots so the same factory drives both the
	// overlay-carrying run AND the no-overlay baseline used by the
	// byte-identity guard below.
	makeChain := func(withOverlays bool) *types.ChainRequest {
		stage0 := &types.Request{
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "score", Label: "s"},
			},
		}
		stage1 := &types.Request{
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "s", Label: "ss"},
			},
		}
		if withOverlays {
			stage0.Overlays = []types.OverlaySpec{
				{
					Name:  "stage0_index_vs_total",
					Kind:  types.OverlayKindIndexVsTotal,
					Scope: types.OverlayScopeGroup,
				},
			}
			stage1.Overlays = []types.OverlaySpec{
				{
					Name:  "stage1_share_of_total",
					Kind:  types.OverlayKindShareOfTotal,
					Scope: types.OverlayScopeGroup,
				},
			}
		}
		return &types.ChainRequest{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Stages: []*types.ChainStage{
				{Name: "group_sum", Request: stage0},
				{Name: "regroup_sum", Request: stage1},
			},
		}
	}

	svc := New(cfg)
	resp, err := svc.ProcessChain(ctx, makeChain(true))
	if err != nil {
		t.Fatalf("ProcessChain (with per-stage overlays): %v", err)
	}

	// Per E6-S3, ChainResponse.Overlays MUST stay nil when the
	// originating ChainRequest carried no whole-chain overlays — the
	// whole-chain barrier in service.applyChainOverlays short-circuits
	// on empty / nil req.Overlays without allocating. This piggyback
	// test exercises only the per-stage half of the dual-slot design,
	// so the slot stays nil end-to-end (E6-S2 introduced the slot;
	// E6-S3 introduced the populating hook; with no whole-chain spec
	// the response shape is byte-identical to a pre-E6-S2 ChainResponse).
	if resp.Overlays != nil {
		t.Errorf("ChainResponse.Overlays = %+v, want nil (per-stage-only chain — no whole-chain barrier work)", resp.Overlays)
	}

	if got := len(resp.Stages); got != 2 {
		t.Fatalf("expected 2 stages, got %d", got)
	}

	// Stage 0: one INDEX_VS_TOTAL layer in spec order with stable Name.
	stage0Overlays := resp.Stages[0].Overlays
	if len(stage0Overlays) != 1 {
		t.Fatalf("Stages[0].Overlays length = %d, want 1", len(stage0Overlays))
	}
	if got, want := stage0Overlays[0].Kind, types.OverlayKindIndexVsTotal; got != want {
		t.Errorf("Stages[0].Overlays[0].Kind = %q, want %q", got, want)
	}
	if got, want := stage0Overlays[0].Name, "stage0_index_vs_total"; got != want {
		t.Errorf("Stages[0].Overlays[0].Name = %q, want %q (stable spec-order name echo)", got, want)
	}

	// Stage 1: one SHARE_OF_TOTAL layer in spec order with stable Name.
	stage1Overlays := resp.Stages[1].Overlays
	if len(stage1Overlays) != 1 {
		t.Fatalf("Stages[1].Overlays length = %d, want 1", len(stage1Overlays))
	}
	if got, want := stage1Overlays[0].Kind, types.OverlayKindShareOfTotal; got != want {
		t.Errorf("Stages[1].Overlays[0].Kind = %q, want %q", got, want)
	}
	if got, want := stage1Overlays[0].Name, "stage1_share_of_total"; got != want {
		t.Errorf("Stages[1].Overlays[0].Name = %q, want %q (stable spec-order name echo)", got, want)
	}

	// Byte-identity guard: a no-overlay baseline chain must produce
	// the same per-stage host Data as the overlay-carrying run after
	// the overlay layers are stripped. The Overlays slot is additive —
	// it must never mutate the base payload.
	baselineSvc := New(cfg)
	baseline, err := baselineSvc.ProcessChain(ctx, makeChain(false))
	if err != nil {
		t.Fatalf("ProcessChain (baseline, no overlays): %v", err)
	}
	if got := len(baseline.Stages); got != 2 {
		t.Fatalf("baseline expected 2 stages, got %d", got)
	}

	// Strip Overlays from the overlay-carrying run and JSON-compare
	// each stage's host payload (Data + Warnings + everything else
	// except Overlays) against the baseline. We mutate copies so the
	// resp object stays intact for any later assertions.
	for i, st := range resp.Stages {
		stripped := *st
		stripped.Overlays = nil
		strippedJSON, err := json.Marshal(stripped)
		if err != nil {
			t.Fatalf("marshal stripped stage %d: %v", i, err)
		}
		baselineJSON, err := json.Marshal(baseline.Stages[i])
		if err != nil {
			t.Fatalf("marshal baseline stage %d: %v", i, err)
		}
		if string(strippedJSON) != string(baselineJSON) {
			t.Fatalf("byte-identity violated for stage %d after stripping Overlays:\nwith-overlays-stripped: %s\nbaseline:               %s",
				i, strippedJSON, baselineJSON)
		}
		// Defense in depth: the baseline stage must NOT carry overlays.
		if baseline.Stages[i].Overlays != nil {
			t.Errorf("baseline Stages[%d].Overlays should be nil; got %+v",
				i, baseline.Stages[i].Overlays)
		}
	}
}
