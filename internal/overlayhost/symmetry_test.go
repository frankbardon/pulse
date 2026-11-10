package overlayhost

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestParseSymmetryParams: correct defaults to true (R's mcnemar.test),
// decodes a boolean, and refuses a non-boolean, null or unknown key with
// PULSE_OVERLAY_PARAM_MISSING carrying the index when given.
func TestParseSymmetryParams(t *testing.T) {
	t.Parallel()

	spec := &types.OverlaySpec{Kind: types.OverlayKindSymmetry}
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || !p.Correct {
		t.Fatalf("default %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`null`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || !p.Correct {
		t.Fatalf("null params %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"correct":false}`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || p.Correct {
		t.Fatalf("correct false %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"correct":true}`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || !p.Correct {
		t.Fatalf("correct true %+v %v", p, cerr)
	}
	for _, bad := range []string{`{"correct":"yes"}`, `{"correct":null}`, `{"correct":1}`, `{"exact":"yes"}`, `{"exact":null}`, `{"mid_p":true}`, `[]`} {
		spec.Params = json.RawMessage(bad)
		_, cerr := ParseSymmetryParams(spec, 2)
		if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING || cerr.Details["index"] != 2 || cerr.Details["kind"] != "OVERLAY_SYMMETRY" {
			t.Fatalf("%s: refusal %v", bad, cerr)
		}
	}
	spec.Params = json.RawMessage(`{"exact":true,"correct":false}`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || !p.Exact || p.Correct {
		t.Fatalf("exact true %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"exact":false}`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || p.Exact || !p.Correct {
		t.Fatalf("exact false %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"exact":1}`)
	if _, cerr := ParseSymmetryParams(spec, 4); cerr == nil || cerr.Details["param"] != "exact" || cerr.Details["index"] != 4 {
		t.Fatalf("non-boolean exact refusal %v", cerr)
	}
	spec.Params = json.RawMessage(`{"correct":0}`)
	if _, cerr := ParseSymmetryParams(spec, -1); cerr == nil || cerr.Details["index"] != nil || cerr.Details["param"] != "correct" {
		t.Fatalf("no-index refusal %v", cerr)
	}
}

// TestSymmetryExactRefusals: an explicit conf_level with exact is
// refused on a table of other than 2 states (needs_2x2: the k × k exact
// Bowker test has no odds ratio) and an absent one is not; exact past
// the Bowker work cap is refused exact_too_large; exact is refused over
// a probability-weighted cell and on a non-integer discordant pair
// (binom.test's 1e-7 rounding rule); each is inert without exact.
func TestSymmetryExactRefusals(t *testing.T) {
	t.Parallel()

	kind := types.OverlayKindSymmetry
	exact, chi := SymmetryParams{Exact: true}, SymmetryParams{Correct: true}
	level := SymmetryParams{Exact: true, ConfLevel: 0.9, ConfLevelSet: true}
	if cerr := SymmetryExactConfLevelRefusal(kind, level, 2, 1); cerr != nil {
		t.Fatalf("2 states with conf_level refused: %v", cerr)
	}
	if cerr := SymmetryExactConfLevelRefusal(kind, exact, 3, 1); cerr != nil {
		t.Fatalf("exact on 3 states without conf_level refused: %v", cerr)
	}
	if cerr := SymmetryExactConfLevelRefusal(kind, chi, 3, 1); cerr != nil {
		t.Fatalf("chi-square on 3 states refused: %v", cerr)
	}
	cerr := SymmetryExactConfLevelRefusal(kind, level, 3, 1)
	if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING || cerr.Details["param"] != "conf_level" ||
		cerr.Details["reason"] != "needs_2x2" || cerr.Details["states"] != 3 || cerr.Details["index"] != 1 {
		t.Fatalf("3-state conf_level refusal %v", cerr)
	}
	cerr = SymmetryExactTooLargeRefusal(kind, 4, 1<<21, 1<<20, 2)
	if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING || cerr.Details["param"] != "exact" ||
		cerr.Details["reason"] != "exact_too_large" || cerr.Details["states"] != 4 || cerr.Details["terms"] != 1<<21 ||
		cerr.Details["limit"] != 1<<20 || cerr.Details["index"] != 2 {
		t.Fatalf("too-large refusal %v", cerr)
	}

	if cerr := SymmetryExactWeightRefusal(kind, exact, false, 0); cerr != nil {
		t.Fatalf("unweighted exact refused: %v", cerr)
	}
	if cerr := SymmetryExactWeightRefusal(kind, chi, true, 0); cerr != nil {
		t.Fatalf("probability chi-square refused: %v", cerr)
	}
	cerr = SymmetryExactWeightRefusal(kind, exact, true, 2)
	if cerr == nil || cerr.Code != errors.PULSE_WEIGHT_UNSUPPORTED || cerr.Details["slot"] != "overlays[2]" ||
		cerr.Details["param"] != "exact" || cerr.Details["kind"] != "probability" || cerr.Details["index"] != 2 {
		t.Fatalf("probability exact refusal %v", cerr)
	}

	if a, b, cerr := SymmetryExactCountsRefusal(kind, 3.00000001, 5, nil, 0); cerr != nil || a != 3 || b != 5 {
		t.Fatalf("near-integer pair: %d %d %v", a, b, cerr)
	}
	_, _, cerr = SymmetryExactCountsRefusal(kind, 3, 5.5, nil, 0)
	if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED || cerr.Details["reason"] != "non_integer" ||
		cerr.Details["index"] != 0 || cerr.Details["pair"] != nil {
		t.Fatalf("non-integer pair refusal %v", cerr)
	}
	_, _, cerr = SymmetryExactCountsRefusal(kind, 2.5, 1, []string{"b", "c"}, 0)
	if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED || cerr.Details["reason"] != "non_integer" ||
		!slices.Equal(cerr.Details["pair"].([]string), []string{"b", "c"}) {
		t.Fatalf("k x k non-integer pair refusal %v", cerr)
	}
}

// TestParseSymmetryParams_ConfLevel: conf_level defaults to 0.95 (with
// and without exact), decodes a number in (0, 1) under exact, and is
// refused PULSE_OVERLAY_PARAM_MISSING {param: conf_level, reason} —
// needs_exact without exact (or with exact false), out_of_range for a
// value outside (0, 1) or a non-number. The range is judged first, so a
// bad value without exact names its range, not exact.
func TestParseSymmetryParams_ConfLevel(t *testing.T) {
	t.Parallel()

	spec := &types.OverlaySpec{Kind: types.OverlayKindSymmetry}
	for _, params := range []string{``, `{"exact":true}`, `{"correct":false}`} {
		spec.Params = json.RawMessage(params)
		if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || p.ConfLevel != 0.95 || p.ConfLevelSet {
			t.Fatalf("%s: default conf_level %+v %v", params, p, cerr)
		}
	}
	spec.Params = json.RawMessage(`{"exact":true,"conf_level":0.9}`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || p.ConfLevel != 0.9 || !p.Exact || !p.ConfLevelSet {
		t.Fatalf("conf_level 0.9 %+v %v", p, cerr)
	}
	for _, c := range []struct{ params, reason string }{
		{`{"conf_level":0.9}`, "needs_exact"},
		{`{"exact":false,"conf_level":0.9}`, "needs_exact"},
		{`{"correct":true,"conf_level":0.95}`, "needs_exact"},
		{`{"exact":true,"conf_level":0}`, "out_of_range"},
		{`{"exact":true,"conf_level":1}`, "out_of_range"},
		{`{"exact":true,"conf_level":-0.5}`, "out_of_range"},
		{`{"exact":true,"conf_level":95}`, "out_of_range"},
		{`{"exact":true,"conf_level":"0.9"}`, "out_of_range"},
		{`{"exact":true,"conf_level":null}`, "out_of_range"},
		{`{"conf_level":1.5}`, "out_of_range"},
	} {
		spec.Params = json.RawMessage(c.params)
		_, cerr := ParseSymmetryParams(spec, 3)
		if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING || cerr.Details["param"] != "conf_level" ||
			cerr.Details["reason"] != c.reason || cerr.Details["index"] != 3 || cerr.Details["kind"] != "OVERLAY_SYMMETRY" {
			t.Fatalf("%s: refusal %v, want reason %s", c.params, cerr, c.reason)
		}
	}
}
