package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E4-S2 predict arm: a panel prefix slab that cannot partition the key
// set is refused at descriptor.ValidateCompose too, because
// pulse.Compose never runs predict and a predict-only refusal stops
// nothing.

// panelPartitionSlot builds one authored Compose slot whose crosstab
// row axis is the given grouper tuple.
func panelPartitionSlot(label string, rowTypes ...types.GroupType) *types.Request {
	rows := make([]*types.Group, 0, len(rowTypes))
	for i, gt := range rowTypes {
		rows = append(rows, &types.Group{Type: gt, Field: []string{"region", "brands", "wave"}[i%3]})
	}
	return &types.Request{
		Label: label,
		Crosstab: &types.CrosstabSpec{
			Rows:    rows,
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "col"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// panelPartitionComposed wires a two-slot panel over the given row axes.
func panelPartitionComposed(refRows, targetRows []types.GroupType, params map[string]any) *types.ComposedRequest {
	return &types.ComposedRequest{
		Requests: []*types.Request{
			panelPartitionSlot("ref", refRows...),
			panelPartitionSlot("t1", targetRows...),
		},
		Overlays: []types.ComposeOverlaySpec{{
			Name:      "panel",
			Kind:      types.OverlayKindPropZPanel,
			Scope:     types.OverlayScopeCell,
			Reference: "ref",
			Targets:   []string{"t1"},
			Params:    params,
		}},
	}
}

// slabError finds the partition refusal in an envelope, or nil.
func slabError(env *descriptor.Envelope) *descriptor.EnvelopeEntry {
	for i := range env.Errors {
		if env.Errors[i].Code == string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED) {
			return env.Errors[i]
		}
	}
	return nil
}

func TestValidateCompose_PanelWithinFanOutBeyondPrefixRefused(t *testing.T) {
	fanOut := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT}
	env := ValidateCompose(panelPartitionComposed(fanOut, fanOut, map[string]any{
		"n_source":       types.PanelNSourceRowMarginValueWithin,
		"n_within_depth": 0,
	}))
	e := slabError(env)
	if e == nil {
		t.Fatalf("expected %s, got %+v", errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED, env.Errors)
	}
	for k, want := range map[string]any{
		"dim_index": 1, "panel_index": 0, "slot_index": 0, "axis": "row",
	} {
		if e.Details[k] != want {
			t.Errorf("Details[%q] = %v, want %v", k, e.Details[k], want)
		}
	}
	if !strings.Contains(e.Message, string(types.GROUP_SET_PER_ELEMENT)) {
		t.Errorf("message does not name the offending grouper: %s", e.Message)
	}
	if result := env.Data.(*ComposeValidationResult); result.Valid {
		t.Error("Valid stayed true on a refused spec")
	}
}

// The omitted-depth exact-margin form sums NOTHING, so a fan-out row
// axis is correct there. Gating it would refuse a request exactly as
// sound as the legacy default it reproduces.
func TestValidateCompose_PanelWithinOmittedDepthNotGated(t *testing.T) {
	fanOut := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT}
	env := ValidateCompose(panelPartitionComposed(fanOut, fanOut, map[string]any{
		"n_source": types.PanelNSourceRowMarginValueWithin,
	}))
	if e := slabError(env); e != nil {
		t.Fatalf("omitted depth was gated; it sums nothing: %s", e.Message)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", env.Errors)
	}
}

// A fan-out grouper INSIDE the fixed prefix multiplies slabs rather
// than cells, so each slab still partitions its own keys. Same rule
// the axis-pairing arm applies.
func TestValidateCompose_PanelWithinFanOutInsidePrefixAccepted(t *testing.T) {
	inside := []types.GroupType{types.GROUP_SET_PER_ELEMENT, types.GROUP_CATEGORY}
	env := ValidateCompose(panelPartitionComposed(inside, inside, map[string]any{
		"n_source":       types.PanelNSourceRowMarginValueWithin,
		"n_within_depth": 0,
	}))
	if e := slabError(env); e != nil {
		t.Fatalf("a fan-out inside the fixed prefix was refused: %s", e.Message)
	}
}

// Every slot is walked, not just the reference, and the diagnostic
// names the offender by BOTH panel index and authored slot index —
// panel 0 is the reference, not Compose slot 0, so the panel index
// alone cannot be resolved against the caller's own request.
func TestValidateCompose_PanelWithinOffendingTargetSlotRefuses(t *testing.T) {
	env := ValidateCompose(panelPartitionComposed(
		[]types.GroupType{types.GROUP_CATEGORY, types.GROUP_CATEGORY},
		[]types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT},
		map[string]any{
			"n_source":       types.PanelNSourceRowMarginValueWithin,
			"n_within_depth": 0,
		}))
	e := slabError(env)
	if e == nil {
		t.Fatalf("a clean reference masked a dirty target: %+v", env.Errors)
	}
	if e.Details["panel_index"] != 1 || e.Details["slot_index"] != 1 {
		t.Errorf("Details panel_index/slot_index = %v/%v, want 1/1",
			e.Details["panel_index"], e.Details["slot_index"])
	}
	if e.Details["slot_label"] != "t1" {
		t.Errorf("Details[slot_label] = %v, want t1", e.Details["slot_label"])
	}
}

// An inert depth (a mode that does not read it) is its own refusal and
// must keep firing instead of the partition one — answering a
// configuration typo with a statistical-validity diagnostic names the
// wrong fix.
func TestValidateCompose_PanelWithinInertDepthKeepsItsOwnCode(t *testing.T) {
	fanOut := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT}
	env := ValidateCompose(panelPartitionComposed(fanOut, fanOut, map[string]any{
		"n_source":       types.PanelNSourceCellNUnweighted,
		"n_within_depth": 0,
	}))
	if slabError(env) != nil {
		t.Fatal("the partition gate fired on a mode that sums nothing")
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Fatalf("expected the inert-param refusal, got %+v", env.Errors)
	}
}

// Extension groupers reach the predict arm through
// PredictOptions.Extensions — descriptor/ may not import processing/,
// so the snapshot is the only route. A registration declaring
// FansOut=true is gated exactly like GROUP_SET_PER_ELEMENT; an
// unregistered name passes, because it cannot execute.
func TestValidateComposeWithOptions_PanelWithinExtensionGrouperGated(t *testing.T) {
	const custom types.GroupType = "GROUP_PANEL_FANOUT_X"
	axis := []types.GroupType{types.GROUP_CATEGORY, custom}
	params := map[string]any{
		"n_source":       types.PanelNSourceRowMarginValueWithin,
		"n_within_depth": 0,
	}

	if e := slabError(ValidateCompose(panelPartitionComposed(axis, axis, params))); e != nil {
		t.Fatalf("an unregistered grouper was refused; it cannot execute: %s", e.Message)
	}

	flat := &PredictOptions{Extensions: &ExtensionsSnapshot{
		Groupers: []descriptor.OperatorMeta{{Name: string(custom), FansOut: false}},
	}}
	if e := slabError(ValidateComposeWithOptions(panelPartitionComposed(axis, axis, params), flat)); e != nil {
		t.Fatalf("a registration declaring FansOut=false was refused: %s", e.Message)
	}

	fans := &PredictOptions{Extensions: &ExtensionsSnapshot{
		Groupers: []descriptor.OperatorMeta{{Name: string(custom), FansOut: true}},
	}}
	e := slabError(ValidateComposeWithOptions(panelPartitionComposed(axis, axis, params), fans))
	if e == nil {
		t.Fatal("an extension grouper declaring FansOut=true was not refused")
	}
	if e.Details["group_type"] != string(custom) {
		t.Errorf("Details[group_type] = %v, want %q", e.Details["group_type"], custom)
	}
}
