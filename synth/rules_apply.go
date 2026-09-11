package synth

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/frankbardon/pulse/errors"
)

// This file is the APPLICATION half of the rule layer; synth/rules.go is
// the document model and its validation. Every fault an author can write
// has already been refused by validateRules before anything here runs,
// so compileRules' own error returns cover only what validation cannot
// see (a field type buildSchema itself would refuse) and the per-row path
// carries no validation at all.
//
// # Why the pass runs LAST
//
// The semantics the rule layer exists to provide are
// `if gate then null else inferred`. A gated field is drawn NORMALLY
// first — through its own sampler and through whatever model,
// conditional pair or correlation owns it — and the rule then masks or
// replaces it only on the rows its `when` selects. Every non-gated row
// keeps the value generation inferred for it.
//
// That is why the pass is the last thing to touch a row before
// encodeRow, and the placement is the design rather than a convenience:
// running it before the model stage would hand the model the final word
// on its own target, so a `set_null` over a modelled field would be
// overwritten on every row and a `set` literal would survive only on the
// rows no model claimed. The falsification for this is in
// synth/rules_apply_test.go — moving the pass ahead of stages.models
// makes the "inferred value survives on a non-gated row" assertion fail.
//
// # The accepted consequence
//
// Because the pass runs last, a field a rule NULLS still contributed its
// DRAWN value to any model that used it as a PREDICTOR on that row. The
// alternative — evaluate the gates first, then re-run the dependent
// stages with the gated fields withheld — is a two-phase row draw, and it
// was considered and rejected in the interview: it doubles the stage
// surface, needs a dependency order between rules and models that
// neither document declares, and there is no demonstrated case wanting
// it. The single-phase reading is also defensible on its own terms: the
// respondent's propensity existed, the question simply was not asked, so
// a model that learnt from the propensity is not learning from something
// that did not happen. A later reader will reach for two phases; this
// paragraph is why not to.
//
// # Determinism
//
// The pass consumes NO RNG. It is pure assignment over the two maps
// drawRow already owns, so a spec carrying rules draws exactly the same
// per-row random sequence as the same spec with the rules removed, and a
// spec declaring no rules produces byte-identical output to a build from
// before the slot existed (TestRules_RulesFreeSpecIsByteIdenticalToPreStory).
// It also allocates nothing per row: every `set` literal is coerced to
// its row shape ONCE at compile time, and both the rule order and each
// rule's assignment order are fixed slices rather than Go map walks.

// ruleAssign is one pre-normalised `set` assignment: a target field plus
// the literal ALREADY coerced to the Go shape the row holds for that
// field's type — float64 for every scalar, string for a categorical,
// map[string]bool for a set_*.
//
// The coercion is constantRowValue, the same function the `constant`
// distribution uses, reused rather than reimplemented: a set_* literal
// arrives from JSON as []any (an array of declared option names) while
// the row wants a map[string]bool, and a second conversion written here
// would be a second place for that mapping to drift.
type ruleAssign struct {
	field string
	// value is SHARED across every row the rule fires on and must never
	// be mutated. That is safe today because the rule pass is the last
	// thing to touch a row: encodeRow only reads, and the set-pair
	// stages that DO rewrite a map[string]bool in place all run earlier.
	// A stage added AFTER the rule pass must copy before mutating.
	value any
}

// compiledRule is one RuleSpec with its predicate compiled and every
// iteration order it needs frozen into a slice.
//
// set_expr and null_together are deliberately ABSENT: they are validated
// (synth/rules.go) and not yet applied. A rule declaring only those two
// slots compiles to nothing at all and is skipped — including its
// `when`, which has no action to gate and whose evaluation could only
// fail a run that is otherwise inert.
type compiledRule struct {
	// index is the rule's position in Spec.Rules, carried because a rule
	// has no name of its own and the index is the only handle back to
	// the document — the same handle validateRules' errors use.
	index int
	// when is nil when the rule declares no predicate, which means EVERY
	// ROW. Compiled with expr.AsBool(), so the run-time type assertion
	// below is a belt-and-braces check rather than the contract.
	when    *vm.Program
	whenSrc string
	// setNull is in DECLARATION order; set is in SORTED-KEY order.
	//
	// Rules are applied in declaration order with last write wins, but
	// the order WITHIN one rule's `set` is not observable in the output
	// today and the sort is not what makes the file deterministic: a Go
	// map cannot name one field twice, so two assignments in one rule
	// always target different fields, and validateRules refuses the one
	// rule that could self-contradict (`set` and `set_null` naming the
	// same field). It is frozen into a slice anyway for two reasons that
	// are not decoration — the `sortedKeys` walk is the package's
	// standing rule for reaching a map at all (validateRules relies on
	// it for the much stronger property that a rule with two faults
	// refuses the SAME one on every run), and `set_expr` (E1-S4) READS
	// the row, at which point the order within a rule becomes observable
	// and would otherwise become map-seed dependent on the day it lands.
	setNull []string
	set     []ruleAssign
}

// ruleApplier is the compiled, per-Spec rule pass. Built once in
// generate(), run once per row at the end of drawRow.
type ruleApplier struct {
	rules []compiledRule

	// nullState exists ONLY to host the isnull builtin baked into every
	// compiled `when`. isnull answers from a null MASK rather than from
	// the row, because a nulled field still carries its drawn value in
	// the row map (see nullableSampler.next), and the mask cannot ride
	// expr's env because the env IS the row. Reusing
	// compiledConstraints as the host rather than writing a second
	// isnull is deliberate: one implementation of the builtin, the same
	// reason rowExprEnv / rowExprOptions are shared with
	// compileConstraints. One applier per generate() call, one row at a
	// time — not safe for concurrent use.
	nullState *compiledConstraints
}

// compileRules compiles Spec.Rules into the per-row pass. Returns a nil
// applier when nothing is left to apply, so drawRow's call is free for
// the overwhelmingly common rules-free spec.
//
// Warnings, not errors, for the one fault validation cannot express
// today: see ruleNonNullableWarning.
func compileRules(rules []RuleSpec, wfs []*writerField) (*ruleApplier, []string, error) {
	if len(rules) == 0 {
		return nil, nil, nil
	}
	specs := make([]FieldSpec, 0, len(wfs))
	byName := make(map[string]FieldSpec, len(wfs))
	for _, wf := range wfs {
		specs = append(specs, wf.spec)
		byName[wf.spec.Name] = wf.spec
	}
	env, names := rowExprEnv(specs)

	applier := &ruleApplier{nullState: &compiledConstraints{fields: names}}
	opts := rowExprOptions(env, names, applier.nullState.isnullBuiltin, expr.AsBool())

	var warnings []string
	for i, r := range rules {
		cr := compiledRule{index: i, whenSrc: r.When}

		// set_null first so the declaration order of the two arrays a
		// rule can carry is preserved exactly as written.
		for _, name := range r.SetNull {
			cr.setNull = append(cr.setNull, name)
			if f, ok := byName[name]; ok && !f.Nullable {
				warnings = append(warnings, ruleNonNullableWarning(i, name))
			}
		}
		for _, name := range sortedKeys(r.Set) {
			f, ok := byName[name]
			if !ok {
				// validateRules already refused this; a spec reaching
				// here without it went around validateSpec.
				continue
			}
			value, err := constantRowValue(f, r.Set[name])
			if err != nil {
				return nil, nil, err
			}
			cr.set = append(cr.set, ruleAssign{field: name, value: value})
		}

		// A rule whose only slots are set_expr / null_together is INERT
		// at this story: nothing to do, so nothing to gate either.
		if len(cr.setNull) == 0 && len(cr.set) == 0 {
			continue
		}
		if r.When != "" {
			prog, err := expr.Compile(r.When, opts...)
			if err != nil {
				// Unreachable via validateSpec, which compiles the same
				// source against the same options. Returned rather than
				// panicked so a caller that built a Spec by hand and
				// bypassed validation still gets a coded refusal.
				return nil, nil, ruleError(errors.PULSE_SYNTH_RULE_EXPR_INVALID, i, "when", "",
					fmt.Sprintf("rule when %q does not compile: %v", r.When, err))
			}
			cr.when = prog
		}
		applier.rules = append(applier.rules, cr)
	}
	if len(applier.rules) == 0 {
		return nil, warnings, nil
	}
	return applier, warnings, nil
}

// ruleNonNullableWarning names a set_null target the schema cannot
// actually record a null for.
//
// encodeRow writes the per-record null bitmap for NULLABLE fields only,
// so a set_null over a field declared without `"nullable": true` writes
// the type's zero as an ordinary VALUE and no null bit — which for a u4
// or a packed_bool is indistinguishable from a real 0. That is the
// silent-outcome class this whole layer exists to remove, so it is
// reported. It is a warning rather than a refusal because the rule is
// otherwise well-formed and the fix is on the FIELD, not the rule; an
// eager refusal wants its own coded fault and belongs with the rest of
// validateRules.
func ruleNonNullableWarning(idx int, field string) string {
	return fmt.Sprintf("rule %d set_null names non-nullable field %q: "+
		"the row will carry 0 rather than a null; declare the field nullable", idx, field)
}

// apply runs every compiled rule over one drawn row, in declaration
// order, LAST WRITE WINS.
//
// Writes both maps drawRow owns and nothing else:
//
//   - set_null sets nullMask[field] and leaves row[field] alone. The
//     drawn value stays in the row on purpose — it is the same shape
//     nullableSampler produces (value present, mask set), so isnull()
//     and a later rule reading the field behave identically whether the
//     null came from the field's own null_rate or from a rule. The value
//     does not reach the file: writeFieldValueForField writes the type's
//     zero for every isNull field.
//   - set writes row[field] and CLEARS nullMask[field]. A rule that
//     states a field's value is stating the field HAS one; leaving a
//     drawn null in the mask would write 0 to the wire and the literal
//     would silently vanish.
//
// The two therefore compose symmetrically under declaration order: a
// set_null followed by a set yields the value, a set followed by a
// set_null yields the null.
func (a *ruleApplier) apply(row map[string]any, nullMask map[string]bool) error {
	if a == nil {
		return nil
	}
	// The mask the isnull builtin reads, rebound each row. Cheap, and it
	// must happen before the first Run: a stale mask would answer about
	// the previous row.
	a.nullState.nullMask = nullMask

	for i := range a.rules {
		r := &a.rules[i]
		if r.when != nil {
			out, err := expr.Run(r.when, row)
			if err != nil {
				return errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
					fmt.Sprintf("rule %d: evaluating when %q: %v", r.index, r.whenSrc, err),
					map[string]any{
						errors.DetailSynthRule:     r.index,
						errors.DetailSynthRuleSlot: "when",
						"expr":                     r.whenSrc,
					})
			}
			gate, isBool := out.(bool)
			if !isBool {
				return errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
					fmt.Sprintf("rule %d: when %q did not return bool", r.index, r.whenSrc),
					map[string]any{
						errors.DetailSynthRule:     r.index,
						errors.DetailSynthRuleSlot: "when",
						"expr":                     r.whenSrc,
					})
			}
			if !gate {
				continue
			}
		}
		for _, name := range r.setNull {
			nullMask[name] = true
		}
		for _, as := range r.set {
			row[as.field] = as.value
			delete(nullMask, as.field)
		}
	}
	return nil
}
