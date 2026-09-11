package synth

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/frankbardon/pulse/encoding"
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

// ruleExprAssign is one compiled `set_expr` assignment: a target field,
// the program, and everything the coercion needs resolved ONCE at
// compile time (the FieldSpec and its FieldType), so the per-row path
// does no lookup.
type ruleExprAssign struct {
	field string
	spec  FieldSpec
	ft    encoding.FieldType
	prog  *vm.Program
	src   string
}

// compiledRule is one RuleSpec with its predicate compiled and every
// iteration order it needs frozen into a slice.
//
// null_together is deliberately ABSENT: it is validated (synth/rules.go)
// and not yet applied (E1-S5). A rule declaring only that slot compiles
// to nothing at all and is skipped — including its `when`, which has no
// action to gate and whose evaluation could only fail a run that is
// otherwise inert.
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
	// setNull is in DECLARATION order; set and setExpr are in
	// SORTED-KEY order.
	//
	// E1-S3 froze these into slices expecting `set_expr` to make the
	// order WITHIN one rule observable, and reported honestly that it
	// could not falsify the sort — a Go map cannot name one field twice,
	// so two assignments in one rule always target different fields.
	// E1-S4 answered the question a different way (see apply's SNAPSHOT
	// paragraph): intra-rule order is not observable in the OUTPUT at
	// all. The sort survives for two reasons that are not decoration —
	// `sortedKeys` is the package's standing rule for reaching a map at
	// all (validateRules relies on it so a rule with two faults refuses
	// the SAME one on every run), and a rule whose set_expr results BOTH
	// fail at row time must report the same one on every run for exactly
	// the same reason.
	setNull []string
	set     []ruleAssign
	setExpr []ruleExprAssign

	// exprBuf holds this rule's evaluated set_expr results between the
	// snapshot phase and the write phase. Allocated ONCE at compile
	// time, len(setExpr), reused on every row — the pass allocates
	// nothing per row of its own. (expr.Run's own allocations are
	// inherent to evaluation and are the ones `when` has made since
	// E1-S3.)
	exprBuf []any
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
	// Two option sets over ONE environment: a `when` must return a bool
	// and says so with expr.AsBool(), while a `set_expr` value
	// expression deliberately does not constrain its return type — what
	// the result may be is the coercion matrix's question, answered
	// against the TARGET, and expr.AsBool() here would refuse
	// `{"set_expr": {"score": "nps * 2"}}` at compile time for returning
	// the very thing it is supposed to return.
	whenOpts := rowExprOptions(env, names, applier.nullState.isnullBuiltin, expr.AsBool())
	valueOpts := rowExprOptions(env, names, applier.nullState.isnullBuiltin)

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
		for _, name := range sortedKeys(r.SetExpr) {
			f, ok := byName[name]
			if !ok {
				continue
			}
			ft, known := fieldTypeFromName(f.Type)
			if !known {
				// buildSchema refuses this field on its own terms; see
				// validateRuleLiteral for why the rule is not blamed.
				continue
			}
			prog, err := expr.Compile(r.SetExpr[name], valueOpts...)
			if err != nil {
				// Unreachable via validateSpec, which compiles the same
				// source against the same options.
				return nil, nil, ruleError(errors.PULSE_SYNTH_RULE_EXPR_INVALID, i, "set_expr", name,
					fmt.Sprintf("rule set_expr[%q] = %q does not compile: %v", name, r.SetExpr[name], err))
			}
			cr.setExpr = append(cr.setExpr, ruleExprAssign{
				field: name, spec: f, ft: ft, prog: prog, src: r.SetExpr[name],
			})
		}
		cr.exprBuf = make([]any, len(cr.setExpr))

		// A rule whose only slot is null_together is INERT at this
		// story: nothing to do, so nothing to gate either.
		if len(cr.setNull) == 0 && len(cr.set) == 0 && len(cr.setExpr) == 0 {
			continue
		}
		if r.When != "" {
			prog, err := expr.Compile(r.When, whenOpts...)
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
//   - set_expr evaluates, coerces the result to the target's type
//     (synth/rules_coerce.go) and then writes exactly as set does,
//     clearing the mask for the same reason.
//
// The three therefore compose symmetrically under declaration order: a
// set_null followed by a set yields the value, a set followed by a
// set_null yields the null.
//
// # SNAPSHOT: a rule reads the row as it was BEFORE the rule ran
//
// Every expression in ONE rule — the `when` and every `set_expr` — is
// evaluated against the row as it stands when the RULE starts, and only
// then are that rule's writes applied. `UPDATE t SET a = b, b = a`
// swaps; so does `{"set_expr": {"a": "b", "b": "a"}}`. Across rules
// nothing changes: they are sequential and last write wins, so a
// `set_expr` reading a field an EARLIER rule wrote sees the new value
// and one reading a field a LATER rule will write sees the old one.
//
// This is E1-S4's answer to the question E1-S3 left open, and it is a
// DECISION. The alternative — evaluate and assign key by key, in the
// sorted order the compiled slices carry — was rejected because the
// order it would expose is not one an author can SEE: a rule's slots
// are Go maps, JSON object order is gone by the time the document is
// decoded, and the surviving order is ALPHABETICAL. Under that reading
// `{"set_expr": {"a": "b + 1", "b": "0"}}` depends on the spelling of
// the field names, and renaming a column silently changes a number.
// Snapshot semantics removes the question instead of answering it
// badly: intra-rule order is UNOBSERVABLE, so there is nothing to get
// wrong and nothing to document beyond this paragraph.
//
// Refusing intra-rule reads outright was the third option and is
// strictly worse: `{"set_expr": {"nps": "nps + 1"}}` — a field derived
// from its own drawn value — is the most natural thing an author
// writes, and any refusal broad enough to be stateable also refuses it.
//
// The three write sets within one rule are pairwise disjoint by
// validation (PULSE_SYNTH_RULE_CONFLICT refuses a field named in two of
// set / set_expr / set_null), so the order of the three write loops
// below is unobservable too.
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
		// PHASE 1 — evaluate every set_expr against the row as it
		// stands before this rule writes anything. See the SNAPSHOT
		// paragraph above for why this is a phase and not a loop body.
		for j := range r.setExpr {
			ea := &r.setExpr[j]
			out, err := expr.Run(ea.prog, row)
			if err != nil {
				return errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
					fmt.Sprintf("rule %d: evaluating set_expr[%q] = %q: %v", r.index, ea.field, ea.src, err),
					map[string]any{
						errors.DetailSynthRule:     r.index,
						errors.DetailSynthRuleSlot: "set_expr",
						"field":                    ea.field,
						"expr":                     ea.src,
					})
			}
			value, err := ruleExprRowValue(ea.field, ea.spec, ea.ft, out)
			if err != nil {
				return ruleExprResultError(r.index, ea.field, ea.src, out, err)
			}
			r.exprBuf[j] = value
		}
		// PHASE 2 — the rule's writes.
		for _, name := range r.setNull {
			nullMask[name] = true
		}
		for _, as := range r.set {
			row[as.field] = as.value
			delete(nullMask, as.field)
		}
		for j := range r.setExpr {
			row[r.setExpr[j].field] = r.exprBuf[j]
			delete(nullMask, r.setExpr[j].field)
		}
	}
	return nil
}
