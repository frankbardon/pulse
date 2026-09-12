package synth

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// ruleSpecFixture is the field set every validation case below declares
// rules against: one of each row-value class plus the narrow integer
// types whose ranges a literal can overflow.
//
// `nps` and `score` are declared NULLABLE and `id` / `aware` / the rest
// are not, which is load-bearing rather than incidental: a `set_null`
// over a field the schema cannot record a null for is refused
// (PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE), so the accepted rows below need
// a nullable target and the refusal rows need a non-nullable one. Both
// kinds have to be in one fixture.
func ruleSpecFixture(rules ...RuleSpec) *Spec {
	return &Spec{
		RowCount: 10,
		Fields: []FieldSpec{
			{Name: "id", Type: "u32", Distribution: DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
			{Name: "nps", Type: "u4", Nullable: true, Distribution: DistUniform,
				Params: map[string]any{"min": 0.0, "max": 11.0}},
			{Name: "score", Type: "f64", Nullable: true, Distribution: DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "aware", Type: "packed_bool", Distribution: DistBernoulli,
				Params: map[string]any{"p": 0.5}},
			{Name: "region", Type: "categorical_u8", Distribution: DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}}},
			{Name: "freeform", Type: "categorical_u8", Distribution: DistRegex,
				Params: map[string]any{"pattern": "[a-z]{3}"}},
			{Name: "brands", Type: "set_u8", Distribution: DistSetBernoulli,
				Params: map[string]any{
					"options":     []any{"acme", "globex"},
					"frequencies": []any{0.5, 0.5},
				}},
			{Name: "cents", Type: "decimal128", Scale: 2, Distribution: DistConstant,
				Params: map[string]any{"value": "1.25"}},
		},
		Rules: rules,
	}
}

// TestValidateRules_Matrix is the eager-refusal gate. Every row is a
// malformed rule that MUST be refused at spec parse with a specific
// PULSE_SYNTH_RULE_* code, plus the well-formed rows that must survive.
//
// Timing is the whole point of the story: a rule naming a mistyped field
// or carrying an uncompilable expression is invisible at run time — the
// run succeeds and generates rows with the gate silently missing — so a
// refusal that arrives at row 400,000, or not at all, is the defect.
func TestValidateRules_Matrix(t *testing.T) {
	cases := []struct {
		name string
		rule RuleSpec
		want errors.Code // "" = must be accepted
	}{
		// --- accepted -------------------------------------------------
		{"when plus set_null", RuleSpec{When: "aware == 0", SetNull: []string{"score"}}, ""},
		{"no when applies to every row", RuleSpec{SetNull: []string{"score"}}, ""},
		{"set literal on numeric", RuleSpec{Set: map[string]any{"score": 1.5}}, ""},
		{"set bool on packed_bool", RuleSpec{Set: map[string]any{"aware": true}}, ""},
		{"set number on packed_bool", RuleSpec{Set: map[string]any{"aware": 1.0}}, ""},
		{"set declared category", RuleSpec{Set: map[string]any{"region": "west"}}, ""},
		{"set any string on unbounded categorical", RuleSpec{Set: map[string]any{"freeform": "zzz"}}, ""},
		{"set declared set options", RuleSpec{Set: map[string]any{"brands": []any{"acme"}}}, ""},
		{"set empty set selection", RuleSpec{Set: map[string]any{"brands": []any{}}}, ""},
		{"set exact decimal string", RuleSpec{Set: map[string]any{"cents": "1.25"}}, ""},
		{"set_expr compiles", RuleSpec{SetExpr: map[string]string{"score": "nps * 2"}}, ""},
		{"set_expr may return bool", RuleSpec{SetExpr: map[string]string{"aware": "nps >= 9"}}, ""},
		{"set_expr may return an int", RuleSpec{SetExpr: map[string]string{"nps": "int(nps)"}}, ""},
		{"set_expr may read its own target", RuleSpec{SetExpr: map[string]string{"nps": "nps"}}, ""},
		{"set_expr may read a field the same rule writes",
			RuleSpec{SetExpr: map[string]string{"nps": "score", "score": "nps"}}, ""},
		{"set_expr may return a number for a decimal128 target",
			RuleSpec{SetExpr: map[string]string{"cents": "score * 2"}}, ""},
		{"set_expr may return a string for a declared categorical",
			RuleSpec{SetExpr: map[string]string{"region": `nps >= 9 ? "west" : "east"`}}, ""},
		{"set_expr with an unknowable return type defers to run time",
			RuleSpec{SetExpr: map[string]string{"score": `nps >= 9 ? 1 : "x"`}}, ""},
		{"when reads isnull", RuleSpec{When: "isnull(score)", SetNull: []string{"nps"}}, ""},
		{"isnull of a declared name in string form", RuleSpec{When: `isnull("score")`, SetNull: []string{"nps"}}, ""},
		{"when reads a set member", RuleSpec{When: `brands["acme"]`, SetNull: []string{"nps"}}, ""},
		{"null_together block of two", RuleSpec{NullTogether: []string{"nps", "score"}}, ""},
		{"set and set_null on different fields",
			RuleSpec{Set: map[string]any{"score": 0.0}, SetNull: []string{"nps"}}, ""},
		{"set_null and null_together may overlap: both null",
			RuleSpec{SetNull: []string{"nps"}, NullTogether: []string{"nps", "score"}}, ""},
		{"u4 literal at the top of its range", RuleSpec{Set: map[string]any{"nps": 15.0}}, ""},

		// --- no action slot -------------------------------------------
		{"empty rule", RuleSpec{}, errors.PULSE_SYNTH_RULE_EMPTY},
		{"when with no action", RuleSpec{When: "aware == 1"}, errors.PULSE_SYNTH_RULE_EMPTY},

		// --- unknown field, every slot --------------------------------
		{"set_null unknown field", RuleSpec{SetNull: []string{"nope"}}, errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN},
		{"set unknown field", RuleSpec{Set: map[string]any{"nope": 1.0}}, errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN},
		{"set_expr unknown field", RuleSpec{SetExpr: map[string]string{"nope": "1"}}, errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN},
		{"null_together unknown field",
			RuleSpec{NullTogether: []string{"nps", "nope"}}, errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN},

		// The STRING form of isnull names a field where only the builtin
		// can see it. It COMPILES — expr's signature is
		// func(string) bool — so it used to reach isnullBuiltin's
		// run-time refusal, past the eager-validation promise. Now
		// parsed and refused here, as the undeclared name it is.
		{"isnull of an undeclared name in string form",
			RuleSpec{When: `isnull("nosuch")`, SetNull: []string{"nps"}},
			errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN},
		{"isnull of an undeclared name inside a set_expr",
			RuleSpec{SetExpr: map[string]string{"score": `isnull("nosuch") ? 1 : 2`}},
			errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN},

		// --- a null the file cannot record ----------------------------
		{"set_null over a non-nullable field",
			RuleSpec{SetNull: []string{"aware"}}, errors.PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE},
		{"null_together over a non-nullable member is NOT refused",
			RuleSpec{NullTogether: []string{"nps", "aware"}}, ""},

		// --- expressions ----------------------------------------------
		{"when does not parse", RuleSpec{When: "aware ==", SetNull: []string{"nps"}}, errors.PULSE_SYNTH_RULE_EXPR_INVALID},
		{"when names an undeclared field",
			RuleSpec{When: "nosuch == 1", SetNull: []string{"nps"}}, errors.PULSE_SYNTH_RULE_EXPR_INVALID},
		{"when is not a bool", RuleSpec{When: "nps", SetNull: []string{"score"}}, errors.PULSE_SYNTH_RULE_EXPR_INVALID},
		{"bare packed_bool in when is not a bool",
			RuleSpec{When: "aware", SetNull: []string{"nps"}}, errors.PULSE_SYNTH_RULE_EXPR_INVALID},
		{"when type mismatch against the row's shape",
			RuleSpec{When: `region == 1`, SetNull: []string{"nps"}}, errors.PULSE_SYNTH_RULE_EXPR_INVALID},
		{"set_expr does not parse",
			RuleSpec{SetExpr: map[string]string{"score": "nps +"}}, errors.PULSE_SYNTH_RULE_EXPR_INVALID},
		{"set_expr names an undeclared field",
			RuleSpec{SetExpr: map[string]string{"score": "nosuch * 2"}}, errors.PULSE_SYNTH_RULE_EXPR_INVALID},

		// --- set_expr return type, knowable at parse ------------------
		// expr type-checks against the row environment, so a return type
		// NO value could coerce to its target is refusable here, before a
		// row exists. A fault only a VALUE settles (a number out of
		// range, a category outside the declared domain) is NOT — see
		// TestRules_SetExprTimingSplit for that half, which is the one
		// this table cannot express.
		{"set_expr returns a string for a numeric target",
			RuleSpec{SetExpr: map[string]string{"score": `"1.5"`}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"set_expr returns a number for a categorical target",
			RuleSpec{SetExpr: map[string]string{"region": "nps * 2"}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"set_expr returns a bool for a categorical target",
			RuleSpec{SetExpr: map[string]string{"region": "nps >= 9"}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"set_expr returns a number for a set target",
			RuleSpec{SetExpr: map[string]string{"brands": "nps"}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"set_expr returns a set selection for a numeric target",
			RuleSpec{SetExpr: map[string]string{"score": "brands"}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		// The decision, not an oversight: a decimal128 takes an exact
		// string only from a `set` LITERAL. See ruleValueFault.
		{"set_expr returns a string for a decimal128 target",
			RuleSpec{SetExpr: map[string]string{"cents": `"1.25"`}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},

		// --- literal range / domain -----------------------------------
		{"u4 literal over range", RuleSpec{Set: map[string]any{"nps": 16.0}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"unsigned literal negative", RuleSpec{Set: map[string]any{"id": -1.0}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"packed_bool literal over range", RuleSpec{Set: map[string]any{"aware": 2.0}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"string on a numeric target", RuleSpec{Set: map[string]any{"score": "1.5"}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"number on a categorical target", RuleSpec{Set: map[string]any{"region": 1.0}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"undeclared category", RuleSpec{Set: map[string]any{"region": "north"}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"scalar on a set target", RuleSpec{Set: map[string]any{"brands": "acme"}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"undeclared set option",
			RuleSpec{Set: map[string]any{"brands": []any{"initech"}}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},
		{"non-string set option",
			RuleSpec{Set: map[string]any{"brands": []any{1.0}}}, errors.PULSE_SYNTH_RULE_VALUE_INVALID},

		// --- intra-rule conflicts -------------------------------------
		{"set and set_null name one field",
			RuleSpec{Set: map[string]any{"score": 0.0}, SetNull: []string{"score"}}, errors.PULSE_SYNTH_RULE_CONFLICT},
		{"set and set_expr name one field",
			RuleSpec{Set: map[string]any{"score": 0.0}, SetExpr: map[string]string{"score": "1"}},
			errors.PULSE_SYNTH_RULE_CONFLICT},
		{"set_expr and set_null name one field",
			RuleSpec{SetExpr: map[string]string{"score": "1"}, SetNull: []string{"score"}},
			errors.PULSE_SYNTH_RULE_CONFLICT},

		// --- null_together blocks -------------------------------------
		{"block of one", RuleSpec{NullTogether: []string{"nps"}}, errors.PULSE_SYNTH_RULE_BLOCK_INVALID},
		{"block of one repeated",
			RuleSpec{NullTogether: []string{"nps", "nps"}}, errors.PULSE_SYNTH_RULE_BLOCK_INVALID},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSpec(ruleSpecFixture(tc.rule))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want refusal %s, got nil", tc.want)
			}
			var coded *errors.CodedError
			if !stderrors.As(err, &coded) {
				t.Fatalf("want *CodedError, got %T: %v", err, err)
			}
			if coded.Code != tc.want {
				t.Fatalf("code = %s, want %s (message: %s)", coded.Code, tc.want, coded.Message)
			}
		})
	}
}

// TestValidateRules_ErrorNamesRuleIndexAndField asserts the details a
// downstream consumer addresses a fault by. A rule has no name of its
// own, so the index into Spec.Rules is the only handle back to the
// document an author wrote — and the standalone rules file (E2-S1) and
// the per-rule firing counts use the same one.
func TestValidateRules_ErrorNamesRuleIndexAndField(t *testing.T) {
	spec := ruleSpecFixture(
		RuleSpec{SetNull: []string{"score"}},
		RuleSpec{When: "aware == 1", Set: map[string]any{"nope": 1.0}},
	)
	err := validateSpec(spec)
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("want *CodedError, got %T: %v", err, err)
	}
	if coded.Code != errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN {
		t.Fatalf("code = %s", coded.Code)
	}
	if got := coded.Details[errors.DetailSynthRule]; got != 1 {
		t.Errorf("details[%q] = %v, want 1 (the SECOND rule is the bad one)", errors.DetailSynthRule, got)
	}
	if got := coded.Details[errors.DetailSynthRuleSlot]; got != "set" {
		t.Errorf("details[%q] = %v, want \"set\"", errors.DetailSynthRuleSlot, got)
	}
	if got := coded.Details["field"]; got != "nope" {
		t.Errorf("details[\"field\"] = %v, want \"nope\"", got)
	}
	if !strings.Contains(coded.Message, "nope") || !strings.Contains(coded.Message, "rule 1") {
		t.Errorf("message should name the rule index and the field, got %q", coded.Message)
	}
}

// TestValidateRules_FaultChoiceIsDeterministic pins the refusal a rule
// with SEVERAL independent faults produces. Both `set` and `set_expr`
// are Go maps, so a naive walk reports whichever key the runtime visited
// first and the error an author sees changes between runs of the same
// binary on the same document. Sorted-key iteration is the fix and this
// is its gate.
func TestValidateRules_FaultChoiceIsDeterministic(t *testing.T) {
	spec := ruleSpecFixture(RuleSpec{Set: map[string]any{
		"aaa_unknown": 1.0,
		"mmm_unknown": 1.0,
		"zzz_unknown": 1.0,
	}})
	const runs = 50
	first := ""
	for i := 0; i < runs; i++ {
		err := validateSpec(spec)
		var coded *errors.CodedError
		if !stderrors.As(err, &coded) {
			t.Fatalf("run %d: want *CodedError, got %v", i, err)
		}
		got, _ := coded.Details["field"].(string)
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d reported field %q, run 0 reported %q: map iteration order reached the error", i, got, first)
		}
	}
	if first != "aaa_unknown" {
		t.Errorf("reported field = %q, want the lowest sorted key \"aaa_unknown\"", first)
	}
}

// TestValidateRules_ExprEnvIsTheConstraintEnv asserts a rule's `when`
// and a Spec.Constraints entry accept and refuse the SAME expressions.
// They share one env builder (rowExprEnv / rowExprOptions) precisely so
// they cannot drift — a second hand-rolled environment is what produced
// issue #258 — and the observable consequence is this agreement.
//
// Both halves compile rather than generate: what is being compared is the
// COMPILE-TIME verdict, and a constraint that compiles can still reject
// every row (`isnull(score)` over a field that is never actually null
// does) for reasons that say nothing about the environment.
//
// The isnull("nosuch") row is refused on BOTH surfaces, and that is why
// FU-04's static pass was applied to compileConstraints as well as to
// validateRules: refusing it for a rule only would have made this gate
// fail, and correctly — the two surfaces share an environment and must
// share its verdicts.
func TestValidateRules_ExprEnvIsTheConstraintEnv(t *testing.T) {
	cases := []struct {
		expr     string
		accepted bool
	}{
		{"aware == 1", true},        // packed_bool is a number in the row
		{"aware", false},            // ...so bare is not a bool
		{`region == "west"`, true},  // categorical is a string
		{"region == 1", false},      // ...so a number does not compare
		{`brands["acme"]`, true},    // set_* is a map[string]bool
		{"isnull(score)", true},     // bare-identifier isnull patch
		{`isnull("nosuch")`, false}, // string form: parsed and refused on BOTH surfaces
		{"nosuch == 1", false},      // unknown identifier
		{"score > 0 && nps < 9", true},
	}
	base := ruleSpecFixture()
	_, wfs, err := buildSchema(base)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			ruleErr := validateSpec(ruleSpecFixture(RuleSpec{When: tc.expr, SetNull: []string{"score"}}))
			_, consErr := compileConstraints([]ConstraintSpec{{Expr: tc.expr}}, wfs)

			if tc.accepted {
				if ruleErr != nil {
					t.Errorf("rule when refused an expression constraints accept: %v", ruleErr)
				}
				if consErr != nil {
					t.Errorf("constraint refused it too — fixture problem, not a drift: %v", consErr)
				}
				return
			}
			if ruleErr == nil {
				t.Errorf("rule when accepted %q; the constraint compiler refuses it", tc.expr)
			}
			if consErr == nil {
				t.Errorf("constraint accepted %q — the two environments have drifted", tc.expr)
			}
		})
	}
}

// TestValidateRules_UnknownFieldTypeBlamesTheField asserts a rule over a
// field whose declared `type` is not a name the writer knows is NOT
// reported as a rule fault. buildSchema refuses that field on its own
// terms; pointing an author at their rule would point at the wrong line.
func TestValidateRules_UnknownFieldTypeBlamesTheField(t *testing.T) {
	spec := ruleSpecFixture(RuleSpec{Set: map[string]any{"weird": 1.0}})
	spec.Fields = append(spec.Fields, FieldSpec{
		Name: "weird", Type: "datetime", Distribution: DistConstant,
		Params: map[string]any{"value": 1.0},
	})
	if err := validateSpec(spec); err != nil {
		t.Fatalf("validateSpec should not blame the rule: %v", err)
	}
	_, _, err := SynthBytes(spec, Options{Seed: 1})
	if err == nil {
		t.Fatal("want buildSchema to refuse the unknown field type")
	}
	if !strings.Contains(err.Error(), "unknown field type") {
		t.Errorf("want the field's own type error, got %v", err)
	}
}
