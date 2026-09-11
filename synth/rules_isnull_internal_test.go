package synth

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestIsnullUnknownField_ParsesRatherThanMatchesText is the unit-level
// gate on FU-04's detector. The question it answers — "does this
// expression call isnull with a STRING LITERAL naming a field the spec
// does not declare" — is a question about syntax, so it is asked of the
// parsed tree and never of the source text.
//
// The three negative controls are the whole reason: a field whose own
// NAME contains the builtin's, the call written inside a string literal
// (data, not code), and an argument that is only a string at RUN time.
// A substring probe answers the first two wrong, which is exactly why
// E2-S2's exprReadsIdentifier parses too.
func TestIsnullUnknownField_ParsesRatherThanMatchesText(t *testing.T) {
	declared := map[string]bool{"score": true, "region": true, "isnull_reason": true}
	cases := []struct {
		name string
		src  string
		want string // "" = nothing to refuse
	}{
		{"declared string literal", `isnull("score")`, ""},
		{"declared bare identifier", `isnull(score)`, ""},
		{"undeclared string literal", `isnull("nosuch")`, "nosuch"},
		{"undeclared single-quoted literal", `isnull('nosuch')`, "nosuch"},
		{"negated", `!isnull("nosuch") && score > 0`, "nosuch"},
		{"nested in a ternary", `score > 0 ? isnull("nosuch") : false`, "nosuch"},
		{"field name contains the builtin", `isnull_reason == 1`, ""},
		{"field name contains the builtin, and is nulled", `isnull(isnull_reason)`, ""},
		{"the call spelled inside a string literal", `region == "isnull(\"nosuch\")"`, ""},
		{"argument is only a string at run time", `isnull(region + "!")`, ""},
		{"argument is a folded concatenation", `isnull("no" + "such")`, ""},
		{"not the one-argument form", `isnull("a", "b")`, ""},
		{"unparseable defers to the compiler", `isnull("nosuch"`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := isnullUnknownField(tc.src, declared)
			if tc.want == "" {
				if found {
					t.Fatalf("isnullUnknownField(%q) reported %q; nothing should be refused here", tc.src, got)
				}
				return
			}
			if !found {
				t.Fatalf("isnullUnknownField(%q) found nothing, want %q", tc.src, tc.want)
			}
			if got != tc.want {
				t.Fatalf("isnullUnknownField(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// TestValidateRules_IsnullStringFormRefusedAtParse is FU-04 itself: the
// string-literal form of isnull names a field, and a name the spec does
// not declare is the same author fault every other slot's name is
// refused for — so it is refused at SPEC PARSE, with the rule index, the
// slot and the offending name, rather than surfacing at row 400,000 when
// the builtin is finally reached.
//
// The code is PULSE_SYNTH_RULE_FIELD_UNKNOWN and not _EXPR_INVALID: the
// expression COMPILES (expr's signature is func(string) bool and a
// string literal satisfies it), so _EXPR_INVALID's documented meaning
// would have to be widened to cover it, while _FIELD_UNKNOWN's — "a slot
// names a field the spec does not declare" — already describes it
// exactly and carries the name on details["field"].
func TestValidateRules_IsnullStringFormRefusedAtParse(t *testing.T) {
	cases := []struct {
		name      string
		rule      RuleSpec
		wantSlot  string
		wantField string
	}{
		{"in when", RuleSpec{When: `isnull("nosuch")`, SetNull: []string{"score"}}, "when", "nosuch"},
		{"in set_expr", RuleSpec{SetExpr: map[string]string{"score": `isnull("nosuch") ? 1 : 2`}}, "set_expr", "nosuch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSpec(ruleSpecFixture(tc.rule))
			var coded *errors.CodedError
			if !stderrors.As(err, &coded) {
				t.Fatalf("want a refusal at spec parse, got %v", err)
			}
			if coded.Code != errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN {
				t.Fatalf("code = %s, want %s", coded.Code, errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN)
			}
			if got := coded.Details[errors.DetailSynthRuleSlot]; got != tc.wantSlot {
				t.Errorf("details[%q] = %v, want %q", errors.DetailSynthRuleSlot, got, tc.wantSlot)
			}
			if got := coded.Details["field"]; got != tc.wantField {
				t.Errorf("details[\"field\"] = %v, want %q", got, tc.wantField)
			}
			if got := coded.Details[errors.DetailSynthRule]; got != 0 {
				t.Errorf("details[%q] = %v, want 0", errors.DetailSynthRule, got)
			}
			if !strings.Contains(coded.Message, isnullFuncName) {
				t.Errorf("message should name the builtin so the author can find it: %q", coded.Message)
			}
		})
	}
}

// TestValidateRules_IsnullDynamicArgumentIsStillDeferred pins the
// boundary of the pass above, and it is the boundary FU-06's answer
// rests on: an isnull argument that is only a string at RUN time cannot
// be resolved at parse, so it stays a run-time failure and the
// PROCESSING_RUNTIME backstop is NOT dead code.
func TestValidateRules_IsnullDynamicArgumentIsStillDeferred(t *testing.T) {
	spec := ruleSpecFixture(RuleSpec{When: `isnull(region + "!")`, SetNull: []string{"score"}})
	if err := validateSpec(spec); err != nil {
		t.Fatalf("a dynamic isnull argument is not statically knowable and must not be refused: %v", err)
	}
}

// TestConstraints_IsnullStringFormRefusedBeforeAnyRow extends FU-04 to
// the OTHER surface that shares the row environment. The two are held in
// agreement by TestValidateRules_ExprEnvIsTheConstraintEnv, so a pass
// that refused a rule's `when` and not a constraint would be a drift —
// and the constraint case is the worse of the two, since it aborted
// generation part-way through a run instead of before it.
func TestConstraints_IsnullStringFormRefusedBeforeAnyRow(t *testing.T) {
	base := ruleSpecFixture()
	_, wfs, err := buildSchema(base)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}
	_, err = compileConstraints([]ConstraintSpec{{Expr: `isnull("nosuch")`}}, wfs)
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("want a compile-time refusal, got %v", err)
	}
	if coded.Code != errors.SERVICE_VALIDATION {
		t.Fatalf("code = %s, want %s (a constraint fault is not a rule fault)", coded.Code, errors.SERVICE_VALIDATION)
	}
	if !strings.Contains(coded.Message, "nosuch") {
		t.Errorf("message should name the offending field: %q", coded.Message)
	}
	if _, err := compileConstraints([]ConstraintSpec{{Expr: `isnull("score")`}}, wfs); err != nil {
		t.Fatalf("a declared name must still compile: %v", err)
	}
}
