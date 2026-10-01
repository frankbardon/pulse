package synth

import (
	"reflect"
	"strings"
	"testing"
)

// gateCand / blockCand / depCand build the three candidate shapes the
// detectors emit, carrying just enough evidence for the ordering walk's
// labels.
func gateCand(gate, target string) RuleSpec {
	return RuleSpec{
		When:     "round(" + gate + ") == 0 || isnull(" + gate + ")",
		SetNull:  []string{target},
		Evidence: &RuleEvidence{Detector: gatingDetectorName, GateField: gate},
	}
}

func blockCand(members ...string) RuleSpec {
	return RuleSpec{
		NullTogether: members,
		Evidence:     &RuleEvidence{Detector: comissingDetectorName},
	}
}

func depCand(source, target string) RuleSpec {
	return RuleSpec{
		SetExpr:  map[string]string{target: "round(" + source + ") >= 2"},
		Evidence: &RuleEvidence{Detector: dependencyDetectorName, SourceField: source},
	}
}

// label addresses a candidate in an assertion the way a reader addresses
// it in the file.
func orderLabels(cands []RuleSpec) []string {
	out := make([]string, 0, len(cands))
	for i := range cands {
		out = append(out, ruleCandidateLabel(cands, i))
	}
	return out
}

// TestRuleCandidateOrder_WriterPrecedesReader pins the ordering rule
// itself: a candidate writing a field another READS is emitted before
// it, and the detector preference survives everywhere nothing forces a
// move.
//
// Every row states the expected order in full rather than asserting a
// property, because the property ("no violation") is satisfiable by any
// topological order and the preference — minimum perturbation — is half
// the rule. A test that only checked the constraint would pass a walk
// that shuffled the whole file.
func TestRuleCandidateOrder_WriterPrecedesReader(t *testing.T) {
	tests := []struct {
		name    string
		in      []RuleSpec
		want    []string
		moved   bool
		warning string
	}{
		{
			// The baseline the preference order was chosen for. No
			// candidate reads a field another writes, so nothing moves.
			name: "independent candidates keep the detector order",
			in: []RuleSpec{
				gateCand("screener", "answer"),
				blockCand("q1", "q2"),
				depCand("level", "derived"),
			},
			want:  []string{"gating(screener)", "co_missing(q1)", "dependency(level)"},
			moved: false,
		},
		{
			// E3-S3's finding. `screener` is a block MEMBER and a gate
			// FIELD, so the block moves the gate's own input after the
			// gate read it. The block is hoisted to just before the
			// gate, and no further.
			name: "a block holding a gate's source is hoisted before it",
			in: []RuleSpec{
				gateCand("screener", "answer"),
				blockCand("screener", "blockmate"),
			},
			want:  []string{"co_missing(screener)", "gating(screener)"},
			moved: true,
		},
		{
			// The same shape reaching the other way across the file: a
			// dependency writing the field a gate reads, emitted last.
			name: "a dependency writing a gate's source is hoisted before it",
			in: []RuleSpec{
				gateCand("derived", "opinion"),
				depCand("level", "derived"),
			},
			want:  []string{"dependency(level)", "gating(derived)"},
			moved: true,
		},
		{
			// A block holding a gate's TARGETS is NOT hoisted: that is
			// the case E3-S2's equivalence argument covers, and gate
			// first is the order where the block repairs a set_null the
			// analyst narrows by hand.
			name: "a block holding a gate's targets stays after it",
			in: []RuleSpec{
				gateCand("screener", "answer"),
				blockCand("answer", "answer2"),
			},
			want:  []string{"gating(screener)", "co_missing(answer)"},
			moved: false,
		},
		{
			// Minimum perturbation with several movers: only the
			// candidates that must move, move, and the headline gate
			// keeps its place at the top of the file.
			name: "only the forced candidates move",
			in: []RuleSpec{
				gateCand("aware", "regard"),
				gateCand("peopleAware", "people"),
				gateCand("priceAware", "price"),
				blockCand("regard", "peopleAware", "priceAware"),
				blockCand("nps", "promoter"),
				depCand("nps", "promoter"),
			},
			want: []string{
				"gating(aware)", "co_missing(regard)", "gating(peopleAware)", "gating(priceAware)",
				"co_missing(nps)", "dependency(nps)",
			},
			moved: true,
		},
		{
			// Mutual dependency: each writes a field the other reads, so
			// no linear order satisfies both. The closing edge is
			// dropped, the surviving one decides the pair — INVERTING
			// the detector order here, because the surviving edge is a
			// constraint and the detector order is a preference — and
			// the drop is SAID.
			name: "a cycle is broken at one edge and warns",
			in: []RuleSpec{
				gateCand("a", "b"),
				gateCand("b", "a"),
			},
			want:    []string{"gating(b)", "gating(a)"},
			moved:   true,
			warning: "each write a field the other reads",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var warnings []string
			got := ruleCandidateOrder(tc.in, &warnings)

			if len(got) != len(tc.in) {
				t.Fatalf("candidate count moved: got %d, want %d", len(got), len(tc.in))
			}
			if labels := orderLabels(got); !reflect.DeepEqual(labels, tc.want) {
				t.Errorf("order = %v, want %v", labels, tc.want)
			}

			// The fixture guard. A row declaring a hoist that stops
			// producing one has stopped testing the fix, and the order
			// assertion above would go on passing — E2-S1, E3-S1 and
			// E3-S2 each shipped a fixture that quietly stopped
			// exercising its own slot, in a different form each time.
			if moved := !reflect.DeepEqual(orderLabels(tc.in), orderLabels(got)); moved != tc.moved {
				t.Errorf("reorder moved a candidate = %v, want %v — the fixture no longer exercises "+
					"what this row is for", moved, tc.moved)
			}

			switch {
			case tc.warning == "" && len(warnings) > 0:
				t.Errorf("unexpected warning(s): %v", warnings)
			case tc.warning != "":
				if len(warnings) != 1 {
					t.Fatalf("warnings = %v, want exactly one", warnings)
				}
				if !strings.Contains(warnings[0], tc.warning) {
					t.Errorf("warning = %q, want it to contain %q", warnings[0], tc.warning)
				}
			}

			// Whatever the order, every candidate is emitted exactly
			// once: a reorder that dropped or duplicated a proposal
			// would be a silently different file.
			seen := map[string]int{}
			for _, l := range orderLabels(got) {
				seen[l]++
			}
			for _, l := range orderLabels(tc.in) {
				seen[l]--
			}
			for l, n := range seen {
				if n != 0 {
					t.Errorf("candidate %q emitted %+d times", l, n)
				}
			}
		})
	}
}

// TestRuleCandidateOrder_NoViolationSurvives is the property the table
// above pins case by case, asserted once over every row: after the walk,
// no candidate writes a field an EARLIER candidate reads.
func TestRuleCandidateOrder_NoViolationSurvives(t *testing.T) {
	in := []RuleSpec{
		gateCand("aware", "regard"),
		gateCand("peopleAware", "people"),
		gateCand("derived", "opinion"),
		blockCand("regard", "peopleAware"),
		blockCand("nps", "promoter"),
		depCand("level", "derived"),
		depCand("nps", "promoter"),
	}
	var warnings []string
	got := ruleCandidateOrder(in, &warnings)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	written := map[string]bool{}
	for i := range got {
		for f := range ruleWrittenFields(got[i]) {
			written[f] = true
		}
	}
	violations := 0
	for i := range got {
		w := ruleWrittenFields(got[i])
		for j := 0; j < i; j++ {
			for f := range ruleReadFields(got[j], written) {
				if w[f] {
					violations++
					t.Errorf("candidate %d (%s) writes %q that candidate %d (%s) already read",
						i, ruleCandidateLabel(got, i), f, j, ruleCandidateLabel(got, j))
				}
			}
		}
	}
	// The guard: an input with no edges would report zero violations
	// whatever the walk did.
	if violations == 0 && reflect.DeepEqual(orderLabels(in), orderLabels(got)) {
		t.Fatal("nothing moved — this fixture no longer carries an ordering constraint")
	}
}

// TestExprIdentifiers_ReadsOnlyFieldsAnotherCandidateWrites pins the
// tokeniser boundary: the scan is not an expr parse, and what keeps it
// exact is the intersection against the written set, which is what drops
// the builtins without enumerating them.
func TestExprIdentifiers_ReadsOnlyFieldsAnotherCandidateWrites(t *testing.T) {
	r := RuleSpec{
		When:    "round(aware) == 0 || isnull(peopleAware)",
		SetExpr: map[string]string{"flag": "round(nps) >= 9 && !isnull(nps)"},
	}
	written := map[string]bool{"aware": true, "peopleAware": true, "nps": true}
	got := ruleReadFields(r, written)
	want := map[string]bool{"aware": true, "peopleAware": true, "nps": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reads = %v, want %v", got, want)
	}
	// `round` and `isnull` ARE identifiers. They produce no edge because
	// no candidate writes a field by those names — and if one did, the
	// edge would be correct.
	if ids := exprIdentifiers(r.When); len(ids) == 0 {
		t.Fatal("tokeniser returned nothing")
	} else {
		found := false
		for _, id := range ids {
			if id == "round" || id == "isnull" {
				found = true
			}
		}
		if !found {
			t.Fatal("tokeniser no longer sees the builtins, so the intersection is no longer what excludes them")
		}
	}
}
