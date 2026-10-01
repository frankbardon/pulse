package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E4-S3 predict arm: the panel's DISTINCT-KEY within-prefix mode is
// accepted as a mode, and inherits the E4-S2 slab gate at
// descriptor.ValidateCompose too — because pulse.Compose never runs
// predict and a predict-only refusal stops nothing.
//
// Admission is deliberately NOT here. It classifies the cell
// aggregator from the COMPONENTS a materialised slot emitted, which a
// no-execute validator cannot see; the MATRIX arm draws exactly the
// same line (descriptor checks the params shape, processing checks the
// host).

// The mode must be a KNOWN n_source. If it were not, the unknown-mode
// refusal would mask every assertion below with a params error.
func TestValidateCompose_PanelDistinctWithinIsAKnownMode(t *testing.T) {
	clean := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_CATEGORY}
	env := ValidateCompose(panelPartitionComposed(clean, clean, map[string]any{
		"n_source": types.PanelNSourceRowMarginDistinctWithin,
	}))
	for i := range env.Errors {
		if env.Errors[i].Code == string(errors.PULSE_OVERLAY_PARAM_MISSING) {
			t.Fatalf("known mode refused as a params error: %s", env.Errors[i].Message)
		}
	}

	// The CROSSTAB family's spelling must stay unknown here. The two
	// hosts sum different things and differ by roughly the column
	// count; accepting both spellings would restore the ambiguity the
	// separate name exists to destroy.
	env = ValidateCompose(panelPartitionComposed(clean, clean, map[string]any{
		"n_source": types.PairwiseNSourceNWithinDistinct,
	}))
	var found bool
	for i := range env.Errors {
		if env.Errors[i].Code == string(errors.PULSE_OVERLAY_PARAM_MISSING) &&
			strings.Contains(env.Errors[i].Message, "unknown n_source") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the crosstab spelling %q was accepted on the panel; got %+v",
			types.PairwiseNSourceNWithinDistinct, env.Errors)
	}
}

// The inherited refusal on the predict arm. Same axis, same code, same
// Details the runtime twin emits.
func TestValidateCompose_PanelDistinctWithinFanOutBeyondPrefixRefused(t *testing.T) {
	fanOut := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT}
	env := ValidateCompose(panelPartitionComposed(fanOut, fanOut, map[string]any{
		"n_source":       types.PanelNSourceRowMarginDistinctWithin,
		"n_within_depth": 0,
	}))
	e := slabError(env)
	if e == nil {
		t.Fatalf("expected %s, got %+v", errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED, env.Errors)
	}
	for k, want := range map[string]any{
		"dim_index": 1, "panel_index": 0, "slot_index": 0, "axis": "row",
		"n_source": types.PanelNSourceRowMarginDistinctWithin,
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

// Omitted depth reads the exact per-slot distinct margin and sums
// nothing, so the same fan-out axis is correct there and must not be
// gated on this arm either.
func TestValidateCompose_PanelDistinctWithinOmittedDepthNotGated(t *testing.T) {
	fanOut := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT}
	env := ValidateCompose(panelPartitionComposed(fanOut, fanOut, map[string]any{
		"n_source": types.PanelNSourceRowMarginDistinctWithin,
	}))
	if e := slabError(env); e != nil {
		t.Fatalf("omitted depth was gated; it sums nothing: %s", e.Message)
	}
}

// n_within_depth alongside a mode that does not read it stays an
// inert-param refusal, and the diagnostic must now name BOTH
// within-prefix modes — a caller told the depth applies to
// row_margin_value_within "only" would not discover the distinct leg.
func TestValidateCompose_PanelDepthDiagnosticNamesBothWithinModes(t *testing.T) {
	clean := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_CATEGORY}
	env := ValidateCompose(panelPartitionComposed(clean, clean, map[string]any{
		"n_source":       types.PanelNSourceCellNUnweighted,
		"n_within_depth": 0,
	}))
	var msg string
	for i := range env.Errors {
		if env.Errors[i].Code == string(errors.PULSE_OVERLAY_PARAM_MISSING) {
			msg = env.Errors[i].Message
		}
	}
	if msg == "" {
		t.Fatalf("expected an inert-param refusal, got %+v", env.Errors)
	}
	for _, mode := range types.PanelNSourcesUsingWithinDepth() {
		if !strings.Contains(msg, mode) {
			t.Errorf("diagnostic omits the within-prefix mode %q: %s", mode, msg)
		}
	}
}
