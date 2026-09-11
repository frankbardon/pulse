package synth

import (
	"strings"
	"testing"
)

// TestNullTogetherWarnings_DivergenceBoundary pins the warning to the
// CONSTANT rather than to a number copied out of it, so retuning
// nullRateDivergenceThreshold moves the boundary and this test with it
// instead of leaving a stale literal that quietly stops testing the
// boundary at all.
//
// The comparison is `> threshold`, so a gap exactly AT the threshold is
// silent — but that case is NOT asserted, and deliberately: a gap of
// exactly the threshold is not generally representable (0.52 - 0.50 is
// 0.020000000000000018), so a test of it would be pinning the
// subtraction's rounding rather than the rule. What is asserted is the
// pair either side of it.
func TestNullTogetherWarnings_DivergenceBoundary(t *testing.T) {
	const gateRate = 0.5
	// Large enough to survive the float subtraction at both ends of the
	// boundary without landing on it by accident.
	const eps = 1e-6

	cases := []struct {
		name       string
		memberRate float64
		wantWarn   bool
	}{
		{"identical rates", gateRate, false},
		{"just inside the threshold", gateRate + nullRateDivergenceThreshold - eps, false},
		{"just past the threshold", gateRate + nullRateDivergenceThreshold + eps, true},
		{"below the gate, just past", gateRate - nullRateDivergenceThreshold - eps, true},
		{"far past", 0.0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			byName := map[string]FieldSpec{
				"a": {Name: "a", Type: "u4", Nullable: true, NullRate: gateRate},
				"b": {Name: "b", Type: "u4", Nullable: true, NullRate: tc.memberRate},
			}
			got := nullTogetherWarnings(3, []string{"a", "b"}, byName)
			var rateWarn string
			for _, w := range got {
				if strings.Contains(w, "null_together applies") {
					rateWarn = w
				}
			}
			if tc.wantWarn && rateWarn == "" {
				t.Fatalf("member rate %.8f vs gate %.2f: want a divergence warning, got %v",
					tc.memberRate, gateRate, got)
			}
			if !tc.wantWarn && rateWarn != "" {
				t.Fatalf("member rate %.8f vs gate %.2f: want silence, got %q",
					tc.memberRate, gateRate, rateWarn)
			}
			if rateWarn == "" {
				return
			}
			// The warning must carry the handles a reader needs: the
			// rule index, the gate it followed, and the member whose
			// rate it discarded.
			for _, want := range []string{"rule 3", `"a"`, `"b"`} {
				if !strings.Contains(rateWarn, want) {
					t.Fatalf("warning %q does not name %s", rateWarn, want)
				}
			}
		})
	}
}

// TestNullTogetherWarnings_DivergenceIsMeasuredAgainstTheGate asserts
// the comparison is each member against the FIRST field, not the
// block's max-minus-min: the first field's rate is the one that is
// actually applied, so it is the only baseline a discarded rate can be
// materially different FROM.
//
// The block below spans 0.10 to 0.90, so a max-minus-min test would
// report every member — including the one that agrees with the gate
// exactly.
func TestNullTogetherWarnings_DivergenceIsMeasuredAgainstTheGate(t *testing.T) {
	byName := map[string]FieldSpec{
		"gate":   {Name: "gate", Type: "u4", Nullable: true, NullRate: 0.90},
		"agrees": {Name: "agrees", Type: "u4", Nullable: true, NullRate: 0.90},
		"drifts": {Name: "drifts", Type: "u4", Nullable: true, NullRate: 0.10},
	}
	got := nullTogetherWarnings(0, []string{"gate", "agrees", "drifts"}, byName)
	if len(got) != 1 {
		t.Fatalf("want exactly one warning, got %v", got)
	}
	if !strings.Contains(got[0], `"drifts"`) {
		t.Fatalf("warning does not name the divergent member: %q", got[0])
	}
	if strings.Contains(got[0], `"agrees"`) {
		t.Fatalf("warning names a member that matches the gate exactly: %q", got[0])
	}
}

// TestNullTogetherWarnings_NonNullableMemberIsReportedPerField asserts
// each member the file cannot record a null for gets its own line, in
// block order, sharing set_null's message shape so the two land in one
// warning kind.
func TestNullTogetherWarnings_NonNullableMemberIsReportedPerField(t *testing.T) {
	byName := map[string]FieldSpec{
		"a": {Name: "a", Type: "u4", Nullable: true, NullRate: 0.5},
		"b": {Name: "b", Type: "u4"},
		"c": {Name: "c", Type: "packed_bool"},
	}
	got := nullTogetherWarnings(1, []string{"a", "b", "c"}, byName)
	if len(got) != 3 {
		// two non-nullable members plus the divergence line (b and c
		// declare null_rate 0 against the gate's 0.5).
		t.Fatalf("want three warnings, got %v", got)
	}
	for i, want := range []string{
		`rule 1 null_together names non-nullable field "b"`,
		`rule 1 null_together names non-nullable field "c"`,
	} {
		if !strings.HasPrefix(got[i], want) {
			t.Fatalf("warning %d = %q, want prefix %q", i, got[i], want)
		}
	}
	for _, w := range got[:2] {
		if kind, attention := classifyWarning(w); kind != "rule cannot null a non-nullable field" || !attention {
			t.Fatalf("warning %q classified as %q (attention=%v)", w, kind, attention)
		}
	}
}

// TestApplyNullTogether_CopiesTheGateBothWays is the unit-level
// statement of the resolution rule, including the direction that is
// easy to forget: a block whose gate carries a value must UN-NULL the
// members that drew one of their own. Without it the block is only
// half-shared and the all-present rate stays the product.
func TestApplyNullTogether_CopiesTheGateBothWays(t *testing.T) {
	block := []string{"a", "b", "c"}

	nullMask := map[string]bool{"a": true, "c": true}
	applyNullTogether(block, nullMask)
	for _, name := range block {
		if !nullMask[name] {
			t.Fatalf("gate is null: %q was left carrying a value", name)
		}
	}

	nullMask = map[string]bool{"b": true, "c": true}
	applyNullTogether(block, nullMask)
	for _, name := range block {
		if nullMask[name] {
			t.Fatalf("gate carries a value: %q was left null", name)
		}
	}

	// A block the filter reduced below two members states nothing, and
	// an empty one must not panic.
	applyNullTogether(nil, nullMask)
}
