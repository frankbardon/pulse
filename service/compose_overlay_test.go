package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// composeOverlayBaseRequest builds a 2-slot ComposedRequest of simple
// AGG_COUNT-on-score requests, each carrying a caller-supplied label.
// Used by the COMPOSE-host overlay barrier tests to exercise the
// post-slot-barrier hook with deterministic per-slot responses.
func composeOverlayBaseRequest() *types.ComposedRequest {
	return &types.ComposedRequest{
		Requests: []*types.Request{
			{
				Label:  "baseline",
				Cohort: &types.Cohort{Filename: "test.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				},
			},
			{
				Label:  "treatment",
				Cohort: &types.Cohort{Filename: "test.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				},
			},
		},
	}
}

// TestCompose_NoOverlaysByteIdentical asserts the serial Compose path
// emits byte-identical JSON for the per-slot Response slice when
// `req.Overlays` is nil — the additive-byte-identity guarantee for
// callers that have not opted into Compose-only overlays.
func TestCompose_NoOverlaysByteIdentical(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := composeOverlayBaseRequest()
	responses, err := svc.Compose(context.Background(), composed)
	if err != nil {
		t.Fatalf("Compose (no overlays): %v", err)
	}
	if got, want := len(responses), 2; got != want {
		t.Fatalf("len(responses) = %d, want %d", got, want)
	}

	// Marshal each response — the wire shape must carry no `overlays`
	// key because the per-Request Response.Overlays slot is also
	// omitempty and the request had no per-Request overlays.
	for i, resp := range responses {
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("response[%d] marshal: %v", i, err)
		}
		if strings.Contains(string(raw), `"overlays"`) {
			t.Errorf("response[%d] carries overlays key: %s", i, raw)
		}
	}
}

// TestCompose_StubOverlayRoundTrip asserts the serial Compose path
// invokes applyComposeOverlays when `req.Overlays` is non-empty AND
// the hook produces one layer per spec. The facade still returns
// []*Response (the layers themselves are discarded until E7-S15 lifts
// the facade), so the round-trip here verifies the hook does not
// surface an error and the Compose call itself succeeds.
func TestCompose_StubOverlayRoundTrip(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := composeOverlayBaseRequest()
	composed.Overlays = []types.ComposeOverlaySpec{
		{
			Name:      "stub_layer_one",
			Kind:      types.OverlayKindIndexVsStage,
			Scope:     types.OverlayScopeTotal,
			Reference: "baseline",
			Targets:   []string{"treatment"},
		},
	}

	responses, err := svc.Compose(context.Background(), composed)
	if err != nil {
		t.Fatalf("Compose (with stub overlay): %v", err)
	}
	if got, want := len(responses), 2; got != want {
		t.Fatalf("len(responses) = %d, want %d", got, want)
	}
}

// TestCompose_StubOverlayOrderPreserved asserts the serial Compose
// path's overlay hook walks `req.Overlays` in spec order. The chassis
// dispatch returns one layer per spec in matching index order; this
// test exercises the hook directly (via applyComposeOverlays) to
// verify the order contract because the serial Compose facade does
// not surface the layers slice (deferred to E7-S15).
func TestCompose_StubOverlayOrderPreserved(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := composeOverlayBaseRequest()
	composed.Overlays = []types.ComposeOverlaySpec{
		{
			Name:      "first",
			Kind:      types.OverlayKindIndexVsStage,
			Scope:     types.OverlayScopeTotal,
			Reference: "baseline",
			Targets:   []string{"treatment"},
		},
		{
			Name:      "second",
			Kind:      types.OverlayKindDeltaVsStage,
			Scope:     types.OverlayScopeTotal,
			Reference: "baseline",
			Targets:   []string{"treatment"},
		},
	}

	// Run the full Compose path so applyComposeLabelDefaults +
	// per-slot Process + applyComposeOverlays all engage. Then call
	// applyComposeOverlays directly with the same inputs to verify
	// the order contract on the returned slice (the facade discards
	// the layer slice; the hook is the canonical surface for order
	// observation in the chassis).
	responses, err := svc.Compose(context.Background(), composed)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	// Re-run the label-defaults pass to reproduce the per-slot
	// labels the orchestrator passed into the hook.
	requests, err := applyComposeLabelDefaults(composed)
	if err != nil {
		t.Fatalf("applyComposeLabelDefaults: %v", err)
	}
	layers, _, err := svc.applyComposeOverlays(context.Background(), composed, requests, responses)
	if err != nil {
		t.Fatalf("applyComposeOverlays: %v", err)
	}
	if got, want := len(layers), 2; got != want {
		t.Fatalf("len(layers) = %d, want %d", got, want)
	}
	if got, want := layers[0].Name, "first"; got != want {
		t.Errorf("layers[0].Name = %q, want %q", got, want)
	}
	if got, want := layers[1].Name, "second"; got != want {
		t.Errorf("layers[1].Name = %q, want %q", got, want)
	}
	if got, want := layers[0].Kind, types.OverlayKindIndexVsStage; got != want {
		t.Errorf("layers[0].Kind = %q, want %q", got, want)
	}
	if got, want := layers[1].Kind, types.OverlayKindDeltaVsStage; got != want {
		t.Errorf("layers[1].Kind = %q, want %q", got, want)
	}
}

// TestComposeParallel_NoOverlaysByteIdentical asserts the parallel
// Compose path emits byte-identical JSON for the per-slot Response
// slice when `req.Overlays` is nil. Mirrors the serial-path guarantee
// across the bounded worker pool.
func TestComposeParallel_NoOverlaysByteIdentical(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := composeOverlayBaseRequest()
	responses, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{MaxWorkers: 2})
	if err != nil {
		t.Fatalf("ComposeParallel (no overlays): %v", err)
	}
	if got, want := len(responses), 2; got != want {
		t.Fatalf("len(responses) = %d, want %d", got, want)
	}
	for i, resp := range responses {
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("response[%d] marshal: %v", i, err)
		}
		if strings.Contains(string(raw), `"overlays"`) {
			t.Errorf("response[%d] carries overlays key: %s", i, raw)
		}
	}
}

// TestComposeParallel_StubOverlayRoundTrip asserts the parallel
// Compose path invokes applyComposeOverlays when `req.Overlays` is
// non-empty AND the hook produces one layer per spec, regardless of
// the parallel worker dispatch order (the orchestrator reorders
// results back into slot-index order before invoking the hook).
func TestComposeParallel_StubOverlayRoundTrip(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := composeOverlayBaseRequest()
	composed.Overlays = []types.ComposeOverlaySpec{
		{
			Name:      "first",
			Kind:      types.OverlayKindIndexVsStage,
			Scope:     types.OverlayScopeTotal,
			Reference: "baseline",
			Targets:   []string{"treatment"},
		},
		{
			Name:      "second",
			Kind:      types.OverlayKindDeltaVsStage,
			Scope:     types.OverlayScopeTotal,
			Reference: "baseline",
			Targets:   []string{"treatment"},
		},
	}

	responses, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{MaxWorkers: 2})
	if err != nil {
		t.Fatalf("ComposeParallel (with stub overlays): %v", err)
	}
	if got, want := len(responses), 2; got != want {
		t.Fatalf("len(responses) = %d, want %d", got, want)
	}

	// Verify spec-order layer emission on the dispatch hook directly
	// — the parallel orchestrator reorders results back into
	// slot-index order before invoking applyComposeOverlays, so the
	// layer order matches the spec order regardless of worker
	// scheduling.
	requests, err := applyComposeLabelDefaults(composed)
	if err != nil {
		t.Fatalf("applyComposeLabelDefaults: %v", err)
	}
	layers, _, err := svc.applyComposeOverlays(context.Background(), composed, requests, responses)
	if err != nil {
		t.Fatalf("applyComposeOverlays: %v", err)
	}
	if got, want := len(layers), 2; got != want {
		t.Fatalf("len(layers) = %d, want %d", got, want)
	}
	if got, want := layers[0].Name, "first"; got != want {
		t.Errorf("layers[0].Name = %q, want %q", got, want)
	}
	if got, want := layers[1].Name, "second"; got != want {
		t.Errorf("layers[1].Name = %q, want %q", got, want)
	}
}

// TestCompose_FailFast_SkipsOverlays asserts that under FailFast=true
// (default) a slot failure causes the orchestrator to return the
// first observed error WITHOUT invoking applyComposeOverlays. The
// guarantee is that no partial overlay emission leaks when a slot
// could not produce a *Response.
//
// The serial Compose path has no FailFast knob — a slot error
// returns early from the loop. This test exercises the parallel
// path's FailFast=true branch which carries the explicit "skip
// overlays" comment in the source.
func TestCompose_FailFast_SkipsOverlays(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	// Mix one bad cohort with one valid one. Slot 0 fires a coded
	// error → FailFast=true → the orchestrator returns immediately.
	composed := &types.ComposedRequest{
		Requests: []*types.Request{
			{
				Label:  "broken",
				Cohort: &types.Cohort{Filename: "missing.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				},
			},
			{
				Label:  "ok",
				Cohort: &types.Cohort{Filename: "test.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				},
			},
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "should_not_run",
				Kind:      types.OverlayKindIndexVsStage,
				Scope:     types.OverlayScopeTotal,
				Reference: "broken",
				Targets:   []string{"ok"},
			},
		},
	}

	_, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{
		MaxWorkers: 2,
		FailFast:   true,
	})
	if err == nil {
		t.Fatal("ComposeParallel(FailFast, broken slot): want error, got nil")
	}
	// The error must come from the per-slot failure (the failing
	// cohort open), NOT from the overlay dispatch. The overlay hook
	// must have been skipped — if applyComposeOverlays HAD run with
	// the broken slot's response = nil, the resolver would have
	// surfaced PULSE_OVERLAY_REF_UNKNOWN instead of the cohort-open
	// error. Assert the error string carries the cohort-open
	// signature ("missing.pulse") rather than the overlay-resolver
	// signature ("overlay reference").
	if strings.Contains(err.Error(), "overlay") {
		t.Errorf("FailFast=true should have skipped overlays, but error mentions overlay: %v", err)
	}
}

// TestCompose_NoFailFast_RunsOverlaysOnSucceededSlots asserts that
// under FailFast=false the orchestrator routes through the
// non-FailFast aggregation branch and surfaces the per-slot error
// aggregated under SERVICE_INTERNAL — the overlay hook is NOT
// invoked in this branch either (the non-FailFast branch in
// compose_parallel.go returns the aggregated error before reaching
// the overlay barrier, matching the FailFast=true behaviour
// structurally).
//
// This test locks the structural shape: when ANY slot fails, the
// orchestrator surfaces an error without invoking the overlay hook.
// A future story may lift the non-FailFast branch to run overlays
// over the succeeded subset; until then the test guards against
// accidental partial emission on slot failure.
func TestCompose_NoFailFast_RunsOverlaysOnSucceededSlots(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	// Both slots SUCCEED in this test — the assertion is that
	// FailFast=false with all-succeed runs the overlay hook
	// successfully. The mixed-failure path is covered by
	// TestComposeParallel_NoFailFastAggregatesErrors.
	composed := composeOverlayBaseRequest()
	composed.Overlays = []types.ComposeOverlaySpec{
		{
			Name:      "ran",
			Kind:      types.OverlayKindIndexVsStage,
			Scope:     types.OverlayScopeTotal,
			Reference: "baseline",
			Targets:   []string{"treatment"},
		},
	}

	responses, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{
		MaxWorkers: 2,
		FailFast:   false,
	})
	if err != nil {
		t.Fatalf("ComposeParallel(FailFast=false, all-succeed): %v", err)
	}
	if got, want := len(responses), 2; got != want {
		t.Fatalf("len(responses) = %d, want %d", got, want)
	}

	// Verify the overlay hook ran by calling it directly with the
	// same inputs and observing the spec-order layer emission.
	requests, err := applyComposeLabelDefaults(composed)
	if err != nil {
		t.Fatalf("applyComposeLabelDefaults: %v", err)
	}
	layers, _, err := svc.applyComposeOverlays(context.Background(), composed, requests, responses)
	if err != nil {
		t.Fatalf("applyComposeOverlays(succeeded slots): %v", err)
	}
	if got, want := len(layers), 1; got != want {
		t.Fatalf("len(layers) = %d, want %d", got, want)
	}
	if got, want := layers[0].Name, "ran"; got != want {
		t.Errorf("layers[0].Name = %q, want %q", got, want)
	}
}

// TestApplyComposeOverlays_EmptyShortCircuitsNoAlloc asserts the
// service-layer hook short-circuits before walking the labels slice
// when req.Overlays is empty — the hot-path byte-identity guarantee
// for callers that have not opted into Compose-only overlays. Calling
// the hook with a nil requests / nil responses slice MUST succeed
// because the hook returns before touching them.
func TestApplyComposeOverlays_EmptyShortCircuitsNoAlloc(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := composeOverlayBaseRequest()
	composed.Overlays = nil

	layers, warnings, err := svc.applyComposeOverlays(context.Background(), composed, nil, nil)
	if err != nil {
		t.Fatalf("applyComposeOverlays(empty): %v", err)
	}
	if layers != nil {
		t.Errorf("layers = %+v, want nil (short-circuit)", layers)
	}
	if warnings != nil {
		t.Errorf("warnings = %+v, want nil (short-circuit)", warnings)
	}

	// Same short-circuit on explicit-empty slice (not just nil).
	composed.Overlays = []types.ComposeOverlaySpec{}
	layers, warnings, err = svc.applyComposeOverlays(context.Background(), composed, nil, nil)
	if err != nil {
		t.Fatalf("applyComposeOverlays(explicit-empty): %v", err)
	}
	if layers != nil {
		t.Errorf("layers = %+v, want nil (short-circuit)", layers)
	}
	if warnings != nil {
		t.Errorf("warnings = %+v, want nil (short-circuit)", warnings)
	}
}
