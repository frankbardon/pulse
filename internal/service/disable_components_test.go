package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// boolPtr returns a pointer to b. Used to construct
// Request.DisableComponents *bool values inline.
func boolPtr(b bool) *bool { return &b }

// TestDisableComponents_OptionsLevel covers the engine-level switch:
// SetDisableComponents(true) suppresses Response.Components on every
// Process call that does not override via Request.DisableComponents.
func TestDisableComponents_OptionsLevel(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	svc.SetDisableComponents(true)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components != nil {
		t.Errorf("Response.Components = %+v; want nil (engine DisableComponents=true)", resp.Components)
	}
	if len(resp.Data) == 0 || resp.Data[0]["total"].(float64) != 150.0 {
		t.Errorf("scalar result should be unchanged by the opt-out; got %+v", resp.Data)
	}
}

// TestDisableComponents_DefaultEmits is the baseline: with no engine flag
// and no request override, Response.Components MUST populate. Guards
// against accidental "always disabled" drift.
func TestDisableComponents_DefaultEmits(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil {
		t.Fatal("Response.Components = nil; want populated under default settings")
	}
	if len(resp.Components.Aggregations) != 1 {
		t.Errorf("Components.Aggregations len = %d; want 1", len(resp.Components.Aggregations))
	}
	if resp.Components.Run == nil {
		t.Error("Components.Run = nil; want populated under default settings")
	}
}

// TestDisableComponents_RequestOverrideTrue covers the per-request opt-out:
// engine default off, request explicitly *true ⇒ Components suppressed.
func TestDisableComponents_RequestOverrideTrue(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	// Engine default: off (components emit).

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
		DisableComponents: boolPtr(true),
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components != nil {
		t.Errorf("Response.Components = %+v; want nil (request override true)", resp.Components)
	}
}

// TestDisableComponents_RequestOverrideFalse covers the per-request re-enable:
// engine default on (DisableComponents=true), request explicitly *false ⇒
// Components MUST populate for this request only.
func TestDisableComponents_RequestOverrideFalse(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	svc.SetDisableComponents(true)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
		DisableComponents: boolPtr(false),
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil {
		t.Fatal("Response.Components = nil; want populated (request override false)")
	}
	if len(resp.Components.Aggregations) != 1 {
		t.Errorf("Components.Aggregations len = %d; want 1", len(resp.Components.Aggregations))
	}
}

// TestEffectiveDisableComponents_TableDriven covers the resolver directly
// so the per-request override semantics stay regression-safe.
func TestEffectiveDisableComponents_TableDriven(t *testing.T) {
	cases := []struct {
		name        string
		engine      bool
		reqOverride *bool
		want        bool
	}{
		{name: "engine_off_no_override", engine: false, reqOverride: nil, want: false},
		{name: "engine_on_no_override", engine: true, reqOverride: nil, want: true},
		{name: "engine_off_force_off", engine: false, reqOverride: boolPtr(true), want: true},
		{name: "engine_on_force_on", engine: true, reqOverride: boolPtr(false), want: false},
		{name: "engine_off_force_on", engine: false, reqOverride: boolPtr(false), want: false},
		{name: "engine_on_force_off", engine: true, reqOverride: boolPtr(true), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{}
			svc.SetDisableComponents(tc.engine)
			req := &types.Request{DisableComponents: tc.reqOverride}
			if got := svc.effectiveDisableComponents(req); got != tc.want {
				t.Errorf("effectiveDisableComponents(engine=%v, req=%v) = %v; want %v",
					tc.engine, tc.reqOverride, got, tc.want)
			}
		})
	}
	// Nil request is a defensible call (some code paths construct nil
	// internally for facet / sample probes) — should fall back to the
	// engine default.
	t.Run("nil_request_uses_engine", func(t *testing.T) {
		svc := &Service{}
		svc.SetDisableComponents(true)
		if got := svc.effectiveDisableComponents(nil); !got {
			t.Errorf("effectiveDisableComponents(nil) = %v; want true (engine default)", got)
		}
	})
}

// TestProcessStream_DisableComponents verifies the streaming iterator
// returns nil from .Components() when components are disabled — the
// bufferedRowIter inherits the nil block from the underlying Process
// call, so streaming consumers that read .Components() on terminal
// flush get nil instead of an empty payload.
func TestProcessStream_DisableComponents(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	svc.SetDisableComponents(true)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}

	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()
	// Drain to ensure terminal flush has happened.
	for {
		_, ok, err := iter.Next(context.Background())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			break
		}
	}
	if got := iter.Components(); got != nil {
		t.Errorf("iter.Components() = %+v; want nil with components disabled", got)
	}
}

// TestDisableComponents_GroupedPath covers the buffered grouped exit —
// processGrouped + processRecords gates. The grouped path constructs
// GrouperComponents lazily and processGrouped early-returns nil when
// disabled, so Response.Components.Groupers must be empty.
func TestDisableComponents_GroupedPath(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	svc.SetDisableComponents(true)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_RANGE, Field: "score", Interval: 20},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components != nil {
		t.Errorf("grouped path: Response.Components = %+v; want nil", resp.Components)
	}
	if len(resp.Data) == 0 {
		t.Error("grouped path: expected rows in Response.Data even with components disabled")
	}
}
