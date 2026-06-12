package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// labelComposeRequests builds a ComposedRequest of n slots, each carrying
// the caller-supplied label at the matching index (empty string allowed).
func labelComposeRequests(labels []string) *types.ComposedRequest {
	reqs := make([]*types.Request, len(labels))
	for i, l := range labels {
		reqs[i] = &types.Request{
			Label:  l,
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
		}
	}
	return &types.ComposedRequest{Requests: reqs}
}

// TestRequestLabel_AutoDefault_AllEmpty exercises the all-empty path: every
// slot must land with `request_<index+1>` on the cloned slot list while the
// caller's pointers stay zero. The Compose call itself succeeds because no
// collisions exist after synthesis.
func TestRequestLabel_AutoDefault_AllEmpty(t *testing.T) {
	composed := labelComposeRequests([]string{"", "", ""})

	out, err := applyComposeLabelDefaults(composed)
	if err != nil {
		t.Fatalf("applyComposeLabelDefaults: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d slots, want 3", len(out))
	}
	want := []string{"request_1", "request_2", "request_3"}
	for i, r := range out {
		if r.Label != want[i] {
			t.Errorf("slot %d label = %q, want %q", i, r.Label, want[i])
		}
	}
}

// TestRequestLabel_AutoDefault_MixedFillsHoles exercises the mixed path:
// the auto-default fills the empty slot while caller-supplied values pass
// through verbatim.
func TestRequestLabel_AutoDefault_MixedFillsHoles(t *testing.T) {
	composed := labelComposeRequests([]string{"audience_a", "", ""})

	out, err := applyComposeLabelDefaults(composed)
	if err != nil {
		t.Fatalf("applyComposeLabelDefaults: %v", err)
	}
	want := []string{"audience_a", "request_2", "request_3"}
	for i, r := range out {
		if r.Label != want[i] {
			t.Errorf("slot %d label = %q, want %q", i, r.Label, want[i])
		}
	}
}

// TestRequestLabel_AutoDefault_DoesNotMutateCaller verifies the clone-then-
// fill contract: the caller's *Request pointer's Label stays empty after
// Compose runs the auto-default pass.
func TestRequestLabel_AutoDefault_DoesNotMutateCaller(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	original := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	composed := &types.ComposedRequest{Requests: []*types.Request{original}}

	if _, err := svc.Compose(context.Background(), composed); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if original.Label != "" {
		t.Errorf("caller's Request.Label mutated to %q, want empty", original.Label)
	}
}

// TestRequestLabel_CollisionRejected verifies that two caller-supplied
// identical labels are rejected with PULSE_COMPOSE_LABEL_COLLISION before
// any slot dispatches.
func TestRequestLabel_CollisionRejected(t *testing.T) {
	composed := labelComposeRequests([]string{"dup", "dup", "other"})

	_, err := applyComposeLabelDefaults(composed)
	if err == nil {
		t.Fatalf("expected collision error, got nil")
	}
	var coded *pulseerrors.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %T", err)
	}
	if coded.Code != pulseerrors.PULSE_COMPOSE_LABEL_COLLISION {
		t.Errorf("got code %q, want PULSE_COMPOSE_LABEL_COLLISION", coded.Code)
	}
	if !strings.Contains(coded.Message, `"dup"`) {
		t.Errorf("expected message to name the colliding label, got %q", coded.Message)
	}
}

// TestRequestLabel_CollisionWithAutoDefault verifies the cross-mode
// collision arm: a caller-supplied label that happens to match the
// auto-default name an adjacent empty slot would synthesize.
func TestRequestLabel_CollisionWithAutoDefault(t *testing.T) {
	// Slot 0 carries "request_2" (caller-supplied), slot 1 is empty and
	// auto-fills to "request_2" — collision.
	composed := labelComposeRequests([]string{"request_2", ""})

	_, err := applyComposeLabelDefaults(composed)
	if err == nil {
		t.Fatalf("expected collision error, got nil")
	}
	var coded *pulseerrors.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %T", err)
	}
	if coded.Code != pulseerrors.PULSE_COMPOSE_LABEL_COLLISION {
		t.Errorf("got code %q, want PULSE_COMPOSE_LABEL_COLLISION", coded.Code)
	}
}

// TestRequestLabel_ComposeAcceptsAutoDefault is the end-to-end sanity check:
// a ComposedRequest with all-empty labels runs Compose to completion and
// every per-slot response is populated. This is the "existing Compose tests
// pass byte-identical" acceptance — no caller observes any behaviour
// difference today.
func TestRequestLabel_ComposeAcceptsAutoDefault(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := labelComposeRequests([]string{"", "", ""})
	resps, err := svc.Compose(context.Background(), composed)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(resps) != 3 {
		t.Fatalf("got %d responses, want 3", len(resps))
	}
	for i, r := range resps {
		if r == nil || len(r.Data) == 0 {
			t.Fatalf("slot %d empty response", i)
		}
	}
}

// TestRequestLabel_ComposeParallelHonoursDefault mirrors the Compose path
// for the parallel orchestrator.
func TestRequestLabel_ComposeParallelHonoursDefault(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := labelComposeRequests([]string{"", "audience_a", ""})
	resps, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{MaxWorkers: 2})
	if err != nil {
		t.Fatalf("ComposeParallel: %v", err)
	}
	if len(resps) != 3 {
		t.Fatalf("got %d responses, want 3", len(resps))
	}
}

// TestRequestLabel_ComposeParallelCollisionRejected verifies the parallel
// path rejects collisions before launching any worker.
func TestRequestLabel_ComposeParallelCollisionRejected(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := labelComposeRequests([]string{"dup", "dup"})
	_, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{MaxWorkers: 2})
	if err == nil {
		t.Fatalf("expected collision error, got nil")
	}
	var coded *pulseerrors.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %T", err)
	}
	if coded.Code != pulseerrors.PULSE_COMPOSE_LABEL_COLLISION {
		t.Errorf("got code %q, want PULSE_COMPOSE_LABEL_COLLISION", coded.Code)
	}
}
