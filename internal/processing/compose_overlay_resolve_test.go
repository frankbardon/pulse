package processing

import (
	"errors"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestResolveComposeSlots_ByLabel asserts caller-supplied labels are
// mapped verbatim into both byLabel and byIndex with no auto-default
// substitution. The two maps stay parallel (every byLabel key has a
// matching byIndex entry at the originating slot index).
func TestResolveComposeSlots_ByLabel(t *testing.T) {
	respA := composeSlotOf(types.OverlayShapeScalar)
	respB := composeSlotOf(types.OverlayShapeSeries)
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			{Label: "baseline"},
			{Label: "treatment"},
		},
	}
	responses := []*types.Response{respA, respB}

	byLabel, byIndex, err := ResolveComposeSlots(req, responses)
	if err != nil {
		t.Fatalf("ResolveComposeSlots: %v", err)
	}
	if got := byLabel["baseline"]; got != respA {
		t.Errorf("byLabel[baseline] = %p, want %p", got, respA)
	}
	if got := byLabel["treatment"]; got != respB {
		t.Errorf("byLabel[treatment] = %p, want %p", got, respB)
	}
	if got, want := byIndex["baseline"], 0; got != want {
		t.Errorf("byIndex[baseline] = %d, want %d", got, want)
	}
	if got, want := byIndex["treatment"], 1; got != want {
		t.Errorf("byIndex[treatment] = %d, want %d", got, want)
	}
	if got, want := len(byLabel), 2; got != want {
		t.Errorf("len(byLabel) = %d, want %d", got, want)
	}
}

func TestResolveComposeSlots_AutoDefaultedLabels(t *testing.T) {
	respA := composeSlotOf(types.OverlayShapeScalar)
	respB := composeSlotOf(types.OverlayShapeScalar)
	respC := composeSlotOf(types.OverlayShapeScalar)
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			{Label: ""},
			{Label: ""},
			{Label: ""},
		},
	}
	responses := []*types.Response{respA, respB, respC}

	byLabel, byIndex, err := ResolveComposeSlots(req, responses)
	if err != nil {
		t.Fatalf("ResolveComposeSlots: %v", err)
	}
	wantLabels := []string{"request_1", "request_2", "request_3"}
	wantPtrs := []*types.Response{respA, respB, respC}
	for i, lbl := range wantLabels {
		if got := byLabel[lbl]; got != wantPtrs[i] {
			t.Errorf("byLabel[%q] = %p, want %p", lbl, got, wantPtrs[i])
		}
		if got, want := byIndex[lbl], i; got != want {
			t.Errorf("byIndex[%q] = %d, want %d", lbl, got, want)
		}
	}
}

// TestResolveComposeSlots_MixedLabels asserts the resolver handles a
// mix of caller-supplied and auto-defaulted labels in a single
// ComposedRequest. Slot 0 carries a user label, slot 1 is empty
// (defaults to `request_2`), slot 2 carries another user label.
func TestResolveComposeSlots_MixedLabels(t *testing.T) {
	respA := composeSlotOf(types.OverlayShapeScalar)
	respB := composeSlotOf(types.OverlayShapeScalar)
	respC := composeSlotOf(types.OverlayShapeScalar)
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			{Label: "baseline"},
			{Label: ""},
			{Label: "treatment"},
		},
	}
	responses := []*types.Response{respA, respB, respC}

	byLabel, byIndex, err := ResolveComposeSlots(req, responses)
	if err != nil {
		t.Fatalf("ResolveComposeSlots: %v", err)
	}
	if got := byLabel["baseline"]; got != respA {
		t.Errorf("byLabel[baseline] = %p, want %p", got, respA)
	}
	if got := byLabel["request_2"]; got != respB {
		t.Errorf("byLabel[request_2] = %p, want %p", got, respB)
	}
	if got := byLabel["treatment"]; got != respC {
		t.Errorf("byLabel[treatment] = %p, want %p", got, respC)
	}
	wantIdx := map[string]int{"baseline": 0, "request_2": 1, "treatment": 2}
	for lbl, want := range wantIdx {
		if got := byIndex[lbl]; got != want {
			t.Errorf("byIndex[%q] = %d, want %d", lbl, got, want)
		}
	}
}

// TestResolveComposeSlots_NilResponseSlot asserts the resolver
// preserves a nil *Response slot in the byLabel map (the
// FailFast=false failed-slot hole) so the downstream LookupReference
// / LookupTarget surface can surface the canonical TARGET / REFERENCE
// UNKNOWN code rather than silently skipping the slot.
func TestResolveComposeSlots_NilResponseSlot(t *testing.T) {
	respA := composeSlotOf(types.OverlayShapeScalar)
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			{Label: "baseline"},
			{Label: "treatment"},
		},
	}
	responses := []*types.Response{respA, nil}

	byLabel, byIndex, err := ResolveComposeSlots(req, responses)
	if err != nil {
		t.Fatalf("ResolveComposeSlots: %v", err)
	}
	if got, ok := byLabel["treatment"]; !ok || got != nil {
		t.Errorf("byLabel[treatment] = (%p, ok=%v), want (nil, ok=true)", got, ok)
	}
	if got, want := byIndex["treatment"], 1; got != want {
		t.Errorf("byIndex[treatment] = %d, want %d", got, want)
	}
}

// TestResolveComposeSlots_NilRequestSlot asserts a nil *Request slot
// is skipped entirely — neither byLabel nor byIndex carries an entry
// for the corresponding default label. Downstream lookups against a
// label that was never registered fall through the same "unknown
// label" coded-error branch as a typo.
func TestResolveComposeSlots_NilRequestSlot(t *testing.T) {
	respA := composeSlotOf(types.OverlayShapeScalar)
	respB := composeSlotOf(types.OverlayShapeScalar)
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			{Label: "baseline"},
			nil,
			{Label: "tail"},
		},
	}
	responses := []*types.Response{respA, nil, respB}

	byLabel, byIndex, err := ResolveComposeSlots(req, responses)
	if err != nil {
		t.Fatalf("ResolveComposeSlots: %v", err)
	}
	if _, present := byLabel["request_2"]; present {
		t.Error("byLabel[request_2] present, want absent (nil *Request slot skipped)")
	}
	if got, want := len(byLabel), 2; got != want {
		t.Errorf("len(byLabel) = %d, want %d", got, want)
	}
	if got, want := byIndex["tail"], 2; got != want {
		t.Errorf("byIndex[tail] = %d, want %d", got, want)
	}
}

// TestResolveComposeSlots_EmptyRequest asserts the (nil, empty)
// short-circuit returns (nil, nil, nil) — the resolver mirrors the
// chassis short-circuit shape so call sites can invoke the resolver
// unconditionally without zero-allocation cost on the no-overlay
// path.
func TestResolveComposeSlots_EmptyRequest(t *testing.T) {
	byLabel, byIndex, err := ResolveComposeSlots(nil, nil)
	if err != nil {
		t.Fatalf("ResolveComposeSlots(nil, nil): %v", err)
	}
	if byLabel != nil || byIndex != nil {
		t.Errorf("ResolveComposeSlots(nil, nil) = (%v, %v), want (nil, nil)", byLabel, byIndex)
	}
	byLabel, byIndex, err = ResolveComposeSlots(&types.ComposedRequest{}, nil)
	if err != nil {
		t.Fatalf("ResolveComposeSlots(empty, nil): %v", err)
	}
	if byLabel != nil || byIndex != nil {
		t.Errorf("ResolveComposeSlots(empty, nil) = (%v, %v), want (nil, nil)", byLabel, byIndex)
	}
}

// TestResolveComposeSlots_SlotCountMismatch asserts a defensive guard
// against orchestrator-side invariant violation: when the responses
// slice does not parallel req.Requests the resolver returns a coded
// PROCESSING_INTERNAL error rather than indexing past the end of
// either slice.
func TestResolveComposeSlots_SlotCountMismatch(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			{Label: "baseline"},
			{Label: "treatment"},
		},
	}
	responses := []*types.Response{composeSlotOf(types.OverlayShapeScalar)}

	_, _, err := ResolveComposeSlots(req, responses)
	if err == nil {
		t.Fatal("ResolveComposeSlots(mismatched slots): want error, got nil")
	}
	var coded *pulseerrors.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if coded.Code != pulseerrors.PROCESSING_INTERNAL {
		t.Errorf("coded.Code = %q, want %q", coded.Code, pulseerrors.PROCESSING_INTERNAL)
	}
}

// TestResolveComposeSlots_DuplicateLabelCollision asserts the
// defense-in-depth duplicate-label rejection. The service-side
// applyComposeLabelDefaults normaliser rejects duplicates with
// PULSE_COMPOSE_LABEL_COLLISION at the pre-barrier step; the resolver
// catches the same condition when callers reach it directly
// (descriptor predict, MCP tools, tests).
func TestResolveComposeSlots_DuplicateLabelCollision(t *testing.T) {
	respA := composeSlotOf(types.OverlayShapeScalar)
	respB := composeSlotOf(types.OverlayShapeScalar)
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			{Label: "baseline"},
			{Label: "baseline"},
		},
	}
	responses := []*types.Response{respA, respB}

	_, _, err := ResolveComposeSlots(req, responses)
	if err == nil {
		t.Fatal("ResolveComposeSlots(duplicate label): want error, got nil")
	}
	var coded *pulseerrors.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if coded.Code != pulseerrors.PULSE_COMPOSE_LABEL_COLLISION {
		t.Errorf("coded.Code = %q, want %q", coded.Code, pulseerrors.PULSE_COMPOSE_LABEL_COLLISION)
	}
}

// TestLookupReference_HappyPath asserts a label that resolves to a
// non-nil *Response slot returns the slot pointer verbatim with no
// error.
func TestLookupReference_HappyPath(t *testing.T) {
	resp := composeSlotOf(types.OverlayShapeScalar)
	byLabel := map[string]*types.Response{"baseline": resp}

	got, err := LookupReference(byLabel, "baseline", 0)
	if err != nil {
		t.Fatalf("LookupReference: %v", err)
	}
	if got != resp {
		t.Errorf("LookupReference returned %p, want %p", got, resp)
	}
}

func TestLookupReference_UnknownReturnsCoded(t *testing.T) {
	byLabel := map[string]*types.Response{"baseline": composeSlotOf(types.OverlayShapeScalar)}

	_, err := LookupReference(byLabel, "missing", 7)
	if err == nil {
		t.Fatal("LookupReference(missing): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN)
	var coded *pulseerrors.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got := coded.Details["index"]; got != 7 {
		t.Errorf("Details[index] = %v, want 7", got)
	}
	if got := coded.Details["which"]; got != "reference" {
		t.Errorf("Details[which] = %v, want %q", got, "reference")
	}
}

// TestLookupReference_EmptyLabel asserts an empty Reference label
// surfaces the canonical PULSE_OVERLAY_REFERENCE_UNKNOWN code (every
// Compose-only spec must declare a Reference; predict catches this
// upstream but the runtime resolver fails closed).
func TestLookupReference_EmptyLabel(t *testing.T) {
	byLabel := map[string]*types.Response{"baseline": composeSlotOf(types.OverlayShapeScalar)}

	_, err := LookupReference(byLabel, "", 0)
	if err == nil {
		t.Fatal("LookupReference(empty): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN)
}

// TestLookupReference_NilSlotReturnsCoded asserts a label that
// resolves to a nil *Response (the FailFast=false hole) surfaces
// PULSE_OVERLAY_REFERENCE_UNKNOWN rather than returning the nil
// pointer to the handler.
func TestLookupReference_NilSlotReturnsCoded(t *testing.T) {
	byLabel := map[string]*types.Response{"baseline": nil}

	_, err := LookupReference(byLabel, "baseline", 0)
	if err == nil {
		t.Fatal("LookupReference(nil slot): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN)
}

// TestLookupTarget_HappyPath asserts a label that resolves to a
// non-nil *Response slot returns the slot pointer verbatim with no
// error.
func TestLookupTarget_HappyPath(t *testing.T) {
	resp := composeSlotOf(types.OverlayShapeScalar)
	byLabel := map[string]*types.Response{"treatment": resp}

	got, err := LookupTarget(byLabel, "treatment", 0, 0)
	if err != nil {
		t.Fatalf("LookupTarget: %v", err)
	}
	if got != resp {
		t.Errorf("LookupTarget returned %p, want %p", got, resp)
	}
}

// TestLookupTarget_UnknownReturnsCoded asserts an unknown label
// surfaces a coded PROCESSING_INTERNAL error whose Details carry the
// canonical PULSE_OVERLAY_TARGET_UNKNOWN code plus the originating
// (spec index, target index) coordinate pair.
func TestLookupTarget_UnknownReturnsCoded(t *testing.T) {
	byLabel := map[string]*types.Response{"baseline": composeSlotOf(types.OverlayShapeScalar)}

	_, err := LookupTarget(byLabel, "missing", 7, 3)
	if err == nil {
		t.Fatal("LookupTarget(missing): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_TARGET_UNKNOWN)
	var coded *pulseerrors.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got := coded.Details["index"]; got != 7 {
		t.Errorf("Details[index] = %v, want 7", got)
	}
	if got := coded.Details["target_index"]; got != 3 {
		t.Errorf("Details[target_index] = %v, want 3", got)
	}
	if got := coded.Details["which"]; got != "target" {
		t.Errorf("Details[which] = %v, want %q", got, "target")
	}
}

// TestLookupTarget_EmptyLabel asserts an empty target label surfaces
// the canonical PULSE_OVERLAY_TARGET_UNKNOWN code.
func TestLookupTarget_EmptyLabel(t *testing.T) {
	byLabel := map[string]*types.Response{"baseline": composeSlotOf(types.OverlayShapeScalar)}

	_, err := LookupTarget(byLabel, "", 0, 0)
	if err == nil {
		t.Fatal("LookupTarget(empty): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_TARGET_UNKNOWN)
}

// TestLookupTarget_NilSlotReturnsCoded asserts a label that resolves
// to a nil *Response (the FailFast=false hole) surfaces
// PULSE_OVERLAY_TARGET_UNKNOWN rather than returning the nil pointer
// to the handler.
func TestLookupTarget_NilSlotReturnsCoded(t *testing.T) {
	byLabel := map[string]*types.Response{"treatment": nil}

	_, err := LookupTarget(byLabel, "treatment", 0, 0)
	if err == nil {
		t.Fatal("LookupTarget(nil slot): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_TARGET_UNKNOWN)
}
