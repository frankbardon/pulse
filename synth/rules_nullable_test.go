package synth_test

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/synth"
)

// gatedBlockSpec is the idiom E2-S4 measured as the remedy for the gate
// rule's marginal shortfall, reduced to three block members.
//
// Rule 0's `null_together` names the NEVER-NULL `aware` first, so the
// copy CLEARS every member's own MCAR null on every row; rule 1 then
// supplies the only absence in the block, keyed on the gate. Without the
// block the members' own null_rate keeps firing underneath the gate and
// the marginal comes back nearly twice the captured one.
//
// The shape matters to FU-05 and FU-08 together: the block's gate is
// deliberately non-nullable, so a nullability refusal keyed on "any field
// a rule may null" would refuse the idiom outright, and a warning keyed
// the same way flags the design as a defect.
func gatedBlockSpec(rows int) *synth.Spec {
	fields := []synth.FieldSpec{
		{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
			Params: map[string]any{"start": 1.0}},
		{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
			Params: map[string]any{"p": 0.6}},
	}
	block := []string{"aware"}
	var targets []string
	for _, name := range []string{"p0", "p1", "p2"} {
		fields = append(fields, synth.FieldSpec{
			Name: name, Type: "u4", Nullable: true, NullRate: 0.3,
			Distribution: synth.DistUniform, Params: map[string]any{"min": 1.0, "max": 5.0}})
		block = append(block, name)
		targets = append(targets, name)
	}
	return &synth.Spec{
		RowCount: rows,
		Fields:   fields,
		Rules: []synth.RuleSpec{
			{NullTogether: block},
			{When: "aware == 0", SetNull: targets},
		},
	}
}

// TestRules_GatedBlockIdiomValidatesAndRaisesNoNullabilityWarning is the
// falsification target for FU-05 and FU-08 as ONE claim, because each is
// trivially satisfiable by abandoning the other: promoting the `set_null`
// fault to a refusal must not refuse this spec, and narrowing the
// `null_together` warning to non-gate members must not silence a real
// one.
//
// Three halves, all in one test for the same reason:
//
//  1. the spec validates and generates,
//  2. it raises NO non-nullable warning at all (before FU-08 the
//     never-null gate produced one, naming the field the copy reads
//     rather than writes), and
//  3. the structural claim actually holds in the file — the block is
//     present exactly when the gate is 1 and absent exactly when it is
//     0, never a mixture.
func TestRules_GatedBlockIdiomValidatesAndRaisesNoNullabilityWarning(t *testing.T) {
	const rows = 400
	data, res, err := synth.SynthBytes(gatedBlockSpec(rows), synth.Options{Seed: 91})
	if err != nil {
		t.Fatalf("the gated-block idiom must still validate and generate: %v", err)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "non-nullable") {
			t.Errorf("no field in this spec is nulled without being nullable; warning = %q", w)
		}
	}

	aware := readF64Field(t, data, "aware")
	var gatedRows, openRows int
	for i := range aware {
		nulls := 0
		for _, name := range []string{"p0", "p1", "p2"} {
			_, memberNulls := readFieldRows(t, data, name)
			if memberNulls[i] {
				nulls++
			}
		}
		switch {
		case aware[i] == 0:
			gatedRows++
			if nulls != 3 {
				t.Fatalf("row %d: aware=0 but %d of 3 block members carry a value — "+
					"the gate rule did not null the block", i, 3-nulls)
			}
		default:
			openRows++
			if nulls != 0 {
				t.Fatalf("row %d: aware=1 but %d of 3 block members are null — "+
					"the block did not clear the members' own null_rate", i, nulls)
			}
		}
	}
	if gatedRows == 0 || openRows == 0 {
		t.Fatalf("fixture degenerate: %d gated rows, %d open rows", gatedRows, openRows)
	}
	t.Logf("gated rows=%d open rows=%d, no partial blocks", gatedRows, openRows)
}

// TestRules_NullTogetherStillWarnsForANonGateMember is the other side of
// FU-08's narrowing: the diagnostic must survive for a member the copy
// can actually null. It is the negative control the test above cannot be.
func TestRules_NullTogetherStillWarnsForANonGateMember(t *testing.T) {
	spec := gatedBlockSpec(50)
	// p1 stops being able to record a null; the block will still try.
	for i := range spec.Fields {
		if spec.Fields[i].Name == "p1" {
			spec.Fields[i].Nullable = false
			spec.Fields[i].NullRate = 0
		}
	}
	// ...and the gate rule must stop naming it, or the refusal fires
	// first and this test would be measuring that instead.
	spec.Rules[1].SetNull = []string{"p0", "p2"}

	_, res, err := synth.SynthBytes(spec, synth.Options{Seed: 91})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, `null_together names non-nullable field "p1"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("a non-gate member the block nulls must still be reported; warnings = %v", res.Warnings)
	}
}

// TestRules_WhenRuntimeFaultIsStillReachable is FU-06's answer, pinned.
//
// The question was whether a `when` failing at RUN TIME — reported as
// PROCESSING_RUNTIME rather than a PULSE_SYNTH_RULE_* code — still has
// any reachable cause once FU-04 refuses isnull's string-literal form at
// spec parse. It does, so no code was minted: `_EXPR_INVALID` means "does
// not compile" and these expressions compile, while a new
// `_EXPR_RUNTIME` would be a third spelling of a fault the existing coded
// error already reports with the rule index, the slot and the expression
// on it — the same code, for the same class of fault, that a constraint's
// own evaluation failure uses.
//
// Two reachable routes, both asserted: an isnull argument that is only a
// string at RUN time (so no static pass can resolve it), and an ordinary
// expr evaluation fault with nothing to do with isnull at all. The second
// is why the backstop would survive even a stricter isnull pass.
func TestRules_WhenRuntimeFaultIsStillReachable(t *testing.T) {
	cases := []struct {
		name string
		when string
	}{
		{"isnull argument is dynamic", `isnull(region + "!")`},
		{"an ordinary evaluation fault", `int(region) > 0`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := gatedBlockSpec(20)
			spec.Fields = append(spec.Fields, synth.FieldSpec{
				Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{1.0, 1.0}}})
			spec.Rules = append(spec.Rules, synth.RuleSpec{When: tc.when, SetNull: []string{"p0"}})

			_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
			if err == nil {
				t.Fatalf("want a run-time failure for %q", tc.when)
			}
			var coded *errors.CodedError
			if !stderrors.As(err, &coded) {
				t.Fatalf("want a coded error, got %T: %v", err, err)
			}
			if coded.Code != errors.PROCESSING_RUNTIME {
				t.Fatalf("code = %s, want %s", coded.Code, errors.PROCESSING_RUNTIME)
			}
			// The code is generic; the DETAILS are what make it
			// actionable, and they are the reason a rule-specific code
			// adds nothing.
			if got := coded.Details[errors.DetailSynthRuleSlot]; got != "when" {
				t.Errorf("details[%q] = %v, want \"when\"", errors.DetailSynthRuleSlot, got)
			}
			if got := coded.Details[errors.DetailSynthRule]; got != 2 {
				t.Errorf("details[%q] = %v, want 2 (the third rule)", errors.DetailSynthRule, got)
			}
			if got, _ := coded.Details["expr"].(string); got != tc.when {
				t.Errorf("details[\"expr\"] = %q, want %q", got, tc.when)
			}
		})
	}
}
