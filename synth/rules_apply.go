package synth

import (
	"fmt"
	"math"
	"strings"

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
// Every action slot is live as of E1-S5. A rule compiles to nothing only
// when no slot names a field the schema declares, which validateRules
// already refuses; the skip below survives for a caller that built a
// Spec by hand and went around validation.
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

	// nullTogether is the block in DECLARATION order, and the order is
	// the SEMANTICS here rather than a determinism detail: the FIRST
	// entry is the block's gate and the only member whose own null
	// decision survives. See applyNullTogether.
	nullTogether []string

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

	// rowFired and firings are the firing counter (synth/rules_firing.go).
	// Both are len(rules) and allocated ONCE here, so the per-row pass
	// still allocates nothing: rowFired is scratch for the row being
	// drawn, cleared at the top of every apply, and firings accumulates
	// only the rows generate() ACCEPTED (commitRow). A rule that fired
	// solely on constraint-rejected attempts left nothing in the file
	// and reports zero, which is the honest answer.
	rowFired []bool
	firings  []int

	// owned / rowOwnedNull / ownedNull are the null-OWNERSHIP accounting
	// (synth/rules_ownership.go): the fields whose own null draw a
	// `{"owns_nulls": true}` rule discarded, a per-row scratch of how
	// each of them ended up, and the running totals over ACCEPTED rows.
	// All three are sized once here and are nil for every spec that
	// declares no ownership, so the pass still allocates nothing per row.
	owned        []ownedNullField
	rowOwnedNull []bool
	ownedNull    []int

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
	fieldOrder := make([]string, 0, len(wfs))
	byName := make(map[string]FieldSpec, len(wfs))
	for _, wf := range wfs {
		specs = append(specs, wf.spec)
		fieldOrder = append(fieldOrder, wf.spec.Name)
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
		// No nullability check here: a set_null naming a non-nullable
		// field is REFUSED at spec parse
		// (PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE,
		// ruleSetNullNullableFault), so a spec reaching compileRules has
		// none. The warning that used to live here was the whole signal,
		// and three lines in a capped terminal summary was not enough
		// signal for a gate silently going missing.
		cr.setNull = append(cr.setNull, r.SetNull...)
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

		// null_together keeps DECLARATION order — the first surviving
		// member is the block's gate — and drops any name the schema does
		// not declare, which validateRules has already refused. A block
		// left with fewer than two members states nothing.
		for _, name := range r.NullTogether {
			if _, ok := byName[name]; ok {
				cr.nullTogether = append(cr.nullTogether, name)
			}
		}
		if len(cr.nullTogether) < 2 {
			cr.nullTogether = nil
		} else {
			warnings = append(warnings, nullTogetherWarnings(i, cr.nullTogether, byName)...)
		}

		// A rule that names no declarable field in any slot has nothing
		// to do, so there is nothing to gate either and its `when` is not
		// even compiled. Unreachable through validateSpec, which refuses
		// an actionless rule (PULSE_SYNTH_RULE_EMPTY) and an unknown field
		// name (PULSE_SYNTH_RULE_FIELD_UNKNOWN) before either can get
		// here.
		if len(cr.setNull) == 0 && len(cr.set) == 0 && len(cr.setExpr) == 0 && len(cr.nullTogether) == 0 {
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
		// No compiled rule, so nothing to apply and nothing to count —
		// including no ownership report. Unreachable through
		// validateSpec, which refuses every rule shape that compiles to
		// nothing before buildSchema can wrap a sampler for it.
		return nil, warnings, nil
	}
	applier.rowFired = make([]bool, len(applier.rules))
	applier.firings = make([]int, len(applier.rules))
	// The ownership accounting is derived from the SAME predicate
	// buildSchema used to suppress the draws (ruleOwnedNullFields), over
	// the raw rules rather than the compiled ones: a claim whose rule was
	// dropped here has still had its sampler wrapped, so the report has
	// to cover it or the suppression would be the one thing in the pass
	// with no end-of-run statement about it.
	applier.owned = buildOwnedNullFields(rules, fieldOrder, byName)
	applier.rowOwnedNull = make([]bool, len(applier.owned))
	applier.ownedNull = make([]int, len(applier.owned))
	return applier, warnings, nil
}

// ruleNonNullableWarning names a target the schema cannot actually
// record a null for. `slot` is the rule slot that asked, and since the
// set_null arm became a refusal there is exactly one caller —
// "null_together" — but the parameter stays: the message shape is what
// the warning taxonomy keys on (classifyWarning matches
// " names non-nullable field "), and a second slot acquiring the same
// diagnostic must land in the same kind rather than inventing a second
// wording.
//
// encodeRow writes the per-record null bitmap for NULLABLE fields only,
// so nulling a field declared without `"nullable": true` writes the
// type's zero as an ordinary VALUE and no null bit — which for a u4 or a
// packed_bool is indistinguishable from a real 0.
//
// It stays a WARNING here where `set_null` became a refusal because the
// two slots claim different things: `set_null` states the field IS null
// on a matching row, which a non-nullable field can never honour, while
// `null_together` states the members carry the GATE's decision — a
// decision that may be "present" on every row, and is exactly that in
// the gated-block idiom, whose gate is deliberately a never-null field.
// See ruleSetNullNullableFault for the full reasoning; refusing here
// would refuse that idiom.
func ruleNonNullableWarning(idx int, slot, field string) string {
	return fmt.Sprintf("rule %d %s names non-nullable field %q: "+
		"the row will carry 0 rather than a null; declare the field nullable", idx, slot, field)
}

// nullRateDivergenceThreshold is how far a null_together member's OWN
// declared null_rate may sit from the block gate's before the copy is
// reported.
//
// The block's resolution rule discards every member's null_rate but the
// first's (see applyNullTogether), and discarding a declared number
// without saying so is precisely the silent divergence the rule layer
// exists to remove — so past this distance it is said out loud. It stays
// a WARNING rather than a refusal because a real block whose fields
// drifted slightly (a coding difference, a partial re-ask, a rate read
// back off a rounded published table) would otherwise become unusable,
// and the copy is still the right answer for it.
//
// The comparison is ABSOLUTE, in probability, and deliberately not
// relative: what the copy throws away is a NUMBER OF ROWS, so a relative
// test would scream about 0.001 against 0.002 — four rows in ten
// thousand — while staying quiet about 0.80 against 0.84, which is four
// hundred. 0.02 is two rows in a hundred. Below it the gap is within
// what coding drift or a rounded figure explains and is invisible in any
// rendered table; above it the fields were not asked as one question,
// and the rate the copy applies is materially not the rate the member
// declared.
//
// It is a package constant, not an Options field, matching
// minVarianceExplained / minLevelObservations: it is a tuning of a
// diagnostic, not a parameter of the request, and a knob here would
// mostly be used to turn off the one warning that exists to stop a
// declared rate vanishing unannounced. No value of it changes a
// generated byte or refuses a spec.
const nullRateDivergenceThreshold = 0.02

// nullTogetherWarnings reports, once per spec at compile time, the two
// things a null_together block can be doing that the generated file
// cannot show:
//
//   - a NON-GATE member the schema cannot record a null for at all, and
//   - a member whose own declared null_rate the block's gate overrides
//     by more than nullRateDivergenceThreshold.
//
// block must already be the filtered, declaration-ordered member list
// with at least two entries; block[0] is the gate.
//
// # The gate is skipped, deliberately, in BOTH loops
//
// applyNullTogether never writes nullMask[block[0]] — the gate's own
// state is what gets COPIED FROM — so a non-nullable gate is not a field
// the block fails to null, and reporting it was accurate about the
// declaration while being wrong about the consequence. It was also noisy
// for exactly the shape the diagnostic should stay quiet on: the gated
// block idiom names a NEVER-NULL field first on purpose, so that the copy
// clears every member's own MCAR null and a later rule's `set_null`
// becomes the only source of absence in the block. Warning there flags
// the design as a defect.
//
// The divergence loop already skipped the gate for a different reason —
// it is measured AGAINST the gate — so after this narrowing a
// non-nullable gate produces no line at all, and a block is only
// reported for members whose null decision the copy can actually reach.
func nullTogetherWarnings(idx int, block []string, byName map[string]FieldSpec) []string {
	var out []string
	for _, name := range block[1:] {
		if f, ok := byName[name]; ok && !f.Nullable {
			out = append(out, ruleNonNullableWarning(idx, "null_together", name))
		}
	}
	gate := byName[block[0]]
	var divergent []string
	for _, name := range block[1:] {
		f := byName[name]
		if math.Abs(f.NullRate-gate.NullRate) <= nullRateDivergenceThreshold {
			continue
		}
		divergent = append(divergent, fmt.Sprintf("%q (%.4g)", name, f.NullRate))
	}
	if len(divergent) > 0 {
		out = append(out, fmt.Sprintf("rule %d null_together applies %q null_rate %.4g to the whole block; "+
			"%s declare a materially different rate (more than %.4g apart) and theirs is ignored",
			idx, block[0], gate.NullRate, strings.Join(divergent, ", "), nullRateDivergenceThreshold))
	}
	return out
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
//   - null_together copies nullMask[block[0]] onto every other member,
//     so the block is all null or all present. It is the rule's LAST
//     write and it moves no value; see applyNullTogether.
//
// The first three therefore compose symmetrically under declaration
// order: a set_null followed by a set yields the value, a set followed
// by a set_null yields the null.
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
	// Same reasoning for the firing scratch: it describes the row being
	// drawn, and the row it described may have been rejected.
	clear(a.rowFired)

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
		// The rule applies to this row. Recorded BEFORE the writes
		// because the gate is what "fired" means — a rule whose every
		// write is a no-op on this row still selected it.
		a.noteFired(i)
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
		// PHASE 3 — the block decision, LAST within the rule. See
		// applyNullTogether for why it follows the per-field writes
		// rather than preceding them.
		applyNullTogether(r.nullTogether, nullMask)
	}
	// PHASE 4, once per row rather than once per rule: snapshot how each
	// OWNED field ended up. It is taken after every rule has run because
	// the file records the mask as the last rule left it, and a claim is
	// answerable only against that. Pure reads into a slice sized at
	// compile time — no RNG, no allocation.
	a.noteOwnedNulls(nullMask)
	return nil
}

// applyNullTogether collapses a block of fields onto ONE null decision:
// the state of block[0] is copied to every other member, so the members
// are all null or all present and never a mixture.
//
// # Why the FIRST field wins
//
// Every field has already consumed its own null draw by the time the
// rule pass runs, so copying an existing decision is the only resolution
// that adds no randomness — and the pass consuming no RNG is a hard
// contract, not a preference (synth/writer.go's determinism rule). A
// majority vote or a re-draw would both need a number the row does not
// have. The consequence is real and is documented rather than hidden:
// every member but the first has its own null_rate IGNORED, and
// nullTogetherWarnings says so out loud whenever the discarded rate is
// materially different. It also matches the survey fact the slot exists
// for — a question block is gated by the QUESTION, and the first named
// field is the author's statement of which question that is.
//
// The arithmetic this fixes: four fields each declaring null_rate 0.826
// draw independently, so all four are present on 0.174^4 of rows — 45 in
// 40,000 on the motivating cohort, against the ~6,960 the block actually
// has. After the copy the block is present on 0.174 of rows, the first
// field's own complement.
//
// # Why it is the rule's LAST write
//
// Within one rule the slots have no order an author can read — they are
// Go maps and JSON arrays in one object — so the order is fixed here and
// documented instead. The block goes last because that is the composition
// that is USEFUL: `{"set_null": ["nps"], "null_together": ["nps", …]}`
// nulls the gate and the whole block follows it, which is the natural
// reading. Reversed, the block would be decided from the gate's DRAWN
// state and the set_null would then break it apart again on the very
// rows it was meant to gate.
//
// The same order means a set_null naming a NON-gate member of the block
// in the SAME rule is overridden by the block — the block is a statement
// about all of them and it is applied after. Across rules there is a real
// order to appeal to and the ordinary last-write-wins applies instead:
// a later rule's set_null over any member does break the block, and a
// later null_together re-decides it from whatever the gate holds by then.
//
// Copying moves the null DECISION only, never a value: each member keeps
// the value its own sampler drew. A member the block nulls keeps that
// value in the row exactly as set_null leaves it (isnull() and any later
// rule then read the two alike), and writeFieldValueForField writes the
// type's zero for it. A member the block UN-nulls — the gate carried a
// value, the member had drawn a null — publishes the value it drew,
// which is the whole point: a block is present together or absent
// together.
func applyNullTogether(block []string, nullMask map[string]bool) {
	if len(block) == 0 {
		return
	}
	gateIsNull := nullMask[block[0]]
	for _, name := range block[1:] {
		if gateIsNull {
			nullMask[name] = true
			continue
		}
		delete(nullMask, name)
	}
}
