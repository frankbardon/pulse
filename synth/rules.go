package synth

import (
	"fmt"
	"math"
	"sort"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// RuleSpec is one STRUCTURAL rule: a statement about which fields a row
// may carry a value for, and what that value is, imposed on top of
// whatever the distributions, conditional pairs, correlations and models
// produced. It is the mechanism for the class of survey fact no
// statistical summary can express — "the perception block is not asked
// of a respondent who has never heard of the brand", "promoter is nps >=
// 9" — which generation otherwise reproduces as soft variation with a
// plausible-looking marginal and no gate at all.
//
// Five slots, exactly one of which (`when`) is a predicate and four of
// which are actions, plus one MODIFIER (`owns_nulls`) that changes what
// a `set_null` means for the fields it names rather than adding an
// action of its own:
//
//	{"when": "familiarity == 1",
//	 "set_null": ["perception_1", "perception_2"],
//	 "owns_nulls": true,
//	 "set": {"segment": "unaware"},
//	 "set_expr": {"promoter": "nps >= 9"},
//	 "null_together": ["nps", "nps_reason"]}
//
// `set` and `set_expr` are SIBLING KEYS rather than one map carrying a
// {"$expr": ...} marker, and that is a settled decision rather than an
// accident of implementation: one map leaves {"set": {"region": "west"}}
// undecidable between a categorical literal and a bare identifier, while
// two keys make the question impossible to ask. Do not merge them.
//
// # Application semantics
//
// Rules apply in DECLARATION ORDER, sequentially, LAST WRITE WINS. An
// expression reads whatever the row holds at the moment its rule runs,
// which a later rule may still change — so a `set_expr` reading a field
// an EARLIER rule wrote sees the new value, and one reading a field a
// LATER rule will write sees the old one. That is correct and
// surprising, and it is why the order is the author's to see: the rules
// are deliberately NOT topologically sorted, because a sort would make
// the applied order implicit and declaration order is the one ordering
// an author can read off the document.
//
// The pass runs ONCE per row, LAST — after the model stage, the final
// thing to touch a row before it is encoded. The semantics that buys are
// `if gate then null else inferred`: a gated field is still drawn
// normally, through its own sampler and whatever model or conditional
// pair owns it, and the rule then masks or replaces it on the rows the
// `when` selects, leaving the inferred value everywhere else.
//
// The pass consumes no RNG, so a spec declaring no rules generates
// byte-identical output to the same spec with the slot absent.
//
// Validation is EAGER — every fault below is refused at spec parse, not
// at row 400,000 (see validateRules).
//
// The standalone rules-file format is this array itself, so an inline
// `rules` declaration and a `--rules` file are the same JSON.
type RuleSpec struct {
	// When is an optional expr-lang predicate over the row. The rule
	// applies to rows satisfying it; ABSENT MEANS EVERY ROW, which is
	// not the same as "never" and is the form a derived-field rule
	// (`set_expr` computing a value from other columns) normally takes.
	// Compiled against the same environment Spec.Constraints uses
	// (rowExprEnv / rowExprOptions), so the two cannot diverge: every
	// scalar including packed_bool is a number, a categorical is a
	// string, a set_* is a map[string]bool, and absence is tested with
	// isnull(field). Must return a bool.
	When string `json:"when,omitempty"`

	// SetNull names fields to null on a matching row. It removes a value
	// rather than supplying one, so the field's own generation still
	// matters on every row the rule does not gate — which is why a
	// set_null target is never pre-claimed away from its model.
	SetNull []string `json:"set_null,omitempty"`

	// OwnsNulls declares that THIS RULE is the only source of absence
	// for the fields it names in SetNull: their own `null_rate` draw is
	// DISCARDED and the rule's gate becomes the single thing that can
	// null them. Scoped to SetNull and to nothing else — a rule
	// declaring it without a SetNull field is refused
	// (PULSE_SYNTH_RULE_OWNERSHIP_INVALID).
	//
	// # The arithmetic it fixes
	//
	// A captured `null_rate` is a MARGINAL: it already includes whatever
	// the gate removed. Left in place beneath a `set_null` the two
	// compose — a field is null when the gate fires OR when its own
	// draw says so — and the generated rate comes out at
	// g + (1-g)*r rather than r. Measured on the motivating 122-field
	// survey profile, `regard` declares 0.2526 and generated 0.4418
	// behind a `{"when": "aware == 0"}` gate that is exactly right about
	// WHICH rows are absent. Declaring ownership lands it on the gate's
	// own firing rate, which is what the captured number was measuring.
	//
	// # Zero, not a residual, and why
	//
	// The exactly-correct suppression is the RESIDUAL rate conditional
	// on the rule not firing, (r - g) / (1 - g). It needs g, the rule's
	// firing PROBABILITY, and a `when` is an arbitrary predicate over a
	// row: nothing in a Spec knows how often it will be true, and
	// estimating it would need a pre-pass whose own rows are drawn from
	// the rates it is trying to correct. So the declaration zeroes the
	// draw and the gap is MEASURED at generation instead of guessed —
	// ownershipWarnings reports any owned field whose realised null rate
	// misses its declared one (synth/rules_ownership.go).
	//
	// Where g IS knowable the residual is bounded rather than unknown:
	// `profile create --suggest-rules` admits a gate only when the
	// target is null on at least gateHighNullRate of gated rows and at
	// most gateLowNullRate (0.02) of open rows, and that second
	// threshold IS the residual. Every candidate detection can emit is
	// therefore within 0.02 of exact under a zeroed draw — the same
	// distance nullRateDivergenceThreshold already calls immaterial —
	// which is why detection emits this flag on every gating candidate.
	//
	// # Two rules may own one field
	//
	// Ownership is a UNION, not an exclusive claim, and deliberately not
	// routed through resolveConflicts' claim(): `set_null` REMOVES a
	// value rather than supplying one, so it never claims a field away
	// from its own generation (E2-S2), and "two gates can each account
	// for this field's absence" is a coherent statement needing no
	// arbitration. The draw is suppressed once.
	//
	// # Its relationship to NullTogether
	//
	// A block already does this for its non-gate members, by COPY rather
	// than by suppression: applyNullTogether overwrites every member's
	// decision with the gate's, so their own null_rate is discarded
	// exactly as this flag discards it. The gated-block idiom — name a
	// never-null field first so the copy clears the block's own MCAR
	// nulls, then supply the real gate with a `set_null` — is ownership
	// obtained as a SIDE EFFECT of that copy. OwnsNulls is the same
	// statement said directly, and on the rule that makes it true. The
	// block is still required for the shape it alone expresses: a
	// co-missing block with NO gating field, where one member's own draw
	// is the block's only source of absence.
	OwnsNulls bool `json:"owns_nulls,omitempty"`

	// Set assigns LITERAL values: a number or bool for a scalar target,
	// a declared category string for a categorical_*, an array of
	// declared options for a set_*. Never an expression — see SetExpr,
	// and see the type doc for why the two are separate keys.
	Set map[string]any `json:"set,omitempty"`

	// SetExpr assigns the RESULT of an expression evaluated over the row
	// at the moment this rule runs. The expressions compile here; the
	// per-target coercion of the result (bool to 1/0 for a numeric
	// target, string to a dictionary entry for a categorical) is applied
	// by the rule pass.
	SetExpr map[string]string `json:"set_expr,omitempty"`

	// NullTogether nulls a block of fields as ONE decision rather than
	// per field, the co-missingness shape a survey question block has:
	// the FIRST named field's null state is copied to the rest, so the
	// block is all null or all present and never a mixture. Every other
	// member's own null_rate is consequently IGNORED — the cost of the
	// only resolution that adds no randomness, reported by
	// nullTogetherWarnings when the discarded rate is materially
	// different. Requires at least two distinct fields — a one-field
	// block is set_null spelled less clearly. Applied LAST within its
	// rule; see applyNullTogether.
	NullTogether []string `json:"null_together,omitempty"`

	// Evidence is the ONE INERT SLOT: the measurement that proposed this
	// rule, when `profile create --suggest-rules` wrote it. Nothing in
	// generation reads it — not compileRules, not the rule pass, not
	// validateRules — and it cannot change a generated byte. It rides
	// the rule rather than a sibling document so a candidate an analyst
	// deletes takes its evidence with it, and so the file stays the bare
	// array `--rules` consumes unmodified. Read RuleEvidence's own doc
	// before touching this; the inertness is the contract, and
	// TestRuleEvidence_IsInert is the gate on it.
	Evidence *RuleEvidence `json:"_evidence,omitempty"`
}

// ruleFieldSlots is the fixed order in which validateRules walks a
// rule's slots. Iteration order matters even for validation: a rule with
// two independent faults must report the SAME one on every run, or the
// refusal an author sees depends on Go's map seed. Every map walk below
// sorts its keys for the same reason.
var ruleFieldSlots = []string{"set", "set_expr", "set_null", "null_together"}

// validateRules refuses every malformed rule at SPEC PARSE. That timing
// is the whole point: a rule naming a mistyped field, or carrying an
// expression that cannot compile, is a defect an author can only find
// from the refusal — the alternative is a run that generates 400,000
// plausible rows with the gate silently missing, which is precisely the
// failure the rule layer exists to remove.
//
// Called from validateSpec, so it is reached by Synth, SynthBytes,
// AugmentFromProfile and ParseSpec alike. SpecFromProfile bypasses
// validateSpec by design and derives no rules of its own, so there is
// nothing for it to skip today; a rules file merged onto a derived spec
// is validated by the merge's own Synth call.
//
// Faults, one coded error each, first one wins:
//
//   - PULSE_SYNTH_RULE_EMPTY — no action slot at all.
//   - PULSE_SYNTH_RULE_FIELD_UNKNOWN — a slot names an undeclared field.
//   - PULSE_SYNTH_RULE_EXPR_INVALID — `when` or a `set_expr` value does
//     not compile (`when` must additionally return bool).
//   - PULSE_SYNTH_RULE_VALUE_INVALID — a `set` literal cannot be written
//     to its target.
//   - PULSE_SYNTH_RULE_CONFLICT — one rule names a field in two slots
//     that disagree.
//   - PULSE_SYNTH_RULE_BLOCK_INVALID — `null_together` names fewer than
//     two distinct fields.
//   - PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE — `set_null` names a field the
//     schema cannot record a null for.
//   - PULSE_SYNTH_RULE_OWNERSHIP_INVALID — `owns_nulls` is declared with
//     an empty `set_null`, so the claim has no referent.
//
// Every error carries the rule index (errors.DetailSynthRule) and, where
// one exists, the offending slot (errors.DetailSynthRuleSlot) and field,
// because a rule has no name of its own and the index is the only handle
// back to the document.
func validateRules(s *Spec) error {
	if len(s.Rules) == 0 {
		return nil
	}
	byName := make(map[string]FieldSpec, len(s.Fields))
	for _, f := range s.Fields {
		byName[f.Name] = f
	}
	// One environment for every rule in the spec, built by the same
	// helper compileConstraints uses. Compiling here is the only way to
	// keep the refusal eager, and sharing the builder is the only way to
	// keep "a rule's `when` sees what a constraint sees" true.
	//
	// # The programs compiled here are DISCARDED, deliberately
	//
	// compileRules compiles the same sources against the same options a
	// moment later, so every `when` and every `set_expr` is compiled
	// TWICE per Spec. Handing the applier these programs was considered
	// and REJECTED, for three reasons and one measurement:
	//
	//  1. SpecFromProfile bypasses validateSpec by design, so
	//     compileRules must be able to compile on its own whatever
	//     happens here. A cache would be an optional fast path, not a
	//     removal of the second compile site.
	//  2. ApplyRulesFile validates a COPY and then replaces Spec.Rules.
	//     A cache produced by validation is therefore a cache of a
	//     DIFFERENT rules slice, and the staleness would be silent —
	//     the applier would run programs compiled from rules the spec no
	//     longer carries.
	//  3. Threading it from validateSpec through Synth / SynthBytes /
	//     AugmentFromProfile into generate() widens the signature of the
	//     one function whose determinism contract is load-bearing.
	//
	// Measured: ~11.4us per expression compile, so a 14-rule candidate
	// file pays ~160us ONCE per run, against ~1.6s to generate 20,000
	// rows of the motivating cohort. Two independent compiles are also
	// what keeps the eager refusal and the applier's own refusal PROVEN
	// to agree rather than assumed to
	// (TestCompileRules_RefusesIndependentlyOfValidateRules).
	env, names := rowExprEnv(s.Fields)
	probe := &compiledConstraints{fields: names}
	whenOpts := rowExprOptions(env, names, probe.isnullBuiltin, expr.AsBool())
	valueOpts := rowExprOptions(env, names, probe.isnullBuiltin)

	for i, r := range s.Rules {
		if err := validateRule(i, r, byName, names, whenOpts, valueOpts); err != nil {
			return err
		}
	}
	return nil
}

func validateRule(idx int, r RuleSpec, byName map[string]FieldSpec, declared map[string]bool, whenOpts, valueOpts []expr.Option) error {
	if len(r.Set) == 0 && len(r.SetExpr) == 0 && len(r.SetNull) == 0 && len(r.NullTogether) == 0 {
		return ruleError(errors.PULSE_SYNTH_RULE_EMPTY, idx, "", "",
			"rule declares no action: expected at least one of set, set_expr, set_null, null_together")
	}

	// Reported SECOND, immediately after the actionless rule and before
	// any slot's contents are read, because it is the same CLASS of
	// fault: the rule's own declaration is incoherent before any field
	// name, expression or literal in it has been looked at. An ownership
	// claim with no referent is silent if it is merely ignored — the
	// owned fields keep the double-counted null rate the flag exists to
	// remove, and nothing in the run says the flag did nothing — so it
	// is refused rather than dropped. Scoped to `set_null` alone:
	// `null_together` discards its non-gate members' own null_rate by
	// copying the gate's decision and needs no flag to do it.
	if r.OwnsNulls && len(r.SetNull) == 0 {
		return ruleError(errors.PULSE_SYNTH_RULE_OWNERSHIP_INVALID, idx, "owns_nulls", "",
			"rule declares owns_nulls but names no set_null field: "+
				"the claim is scoped to set_null and has nothing to apply to")
	}

	// Every field a slot names must exist, checked before anything else
	// reads the field's type. Walked in a fixed slot order with sorted
	// keys inside each map so two faults in one rule always report the
	// same one.
	for _, slot := range ruleFieldSlots {
		for _, name := range ruleSlotFields(r, slot) {
			if _, ok := byName[name]; !ok {
				return ruleError(errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN, idx, slot, name,
					fmt.Sprintf("rule %s names field %q, which the spec does not declare", slot, name))
			}
		}
	}

	if r.When != "" {
		if _, err := expr.Compile(r.When, whenOpts...); err != nil {
			return ruleError(errors.PULSE_SYNTH_RULE_EXPR_INVALID, idx, "when", "",
				fmt.Sprintf("rule when %q does not compile: %v", r.When, err))
		}
		if err := validateRuleIsnull(idx, "when", "", r.When, declared); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(r.SetExpr) {
		prog, err := expr.Compile(r.SetExpr[name], valueOpts...)
		if err != nil {
			return ruleError(errors.PULSE_SYNTH_RULE_EXPR_INVALID, idx, "set_expr", name,
				fmt.Sprintf("rule set_expr[%q] = %q does not compile: %v", name, r.SetExpr[name], err))
		}
		if err := validateRuleIsnull(idx, "set_expr", name, r.SetExpr[name], declared); err != nil {
			return err
		}
		if err := validateRuleExpr(idx, name, prog, byName[name]); err != nil {
			return err
		}
	}

	// Two slots naming one field inside ONE rule have no order to appeal
	// to — rules are ordered, the slots of a rule are not — so a
	// self-contradiction is refused rather than arbitrated. set_null and
	// null_together are NOT a conflict with each other: they are
	// arbitrated by a FIXED, documented within-rule order instead
	// (applyNullTogether runs last), under which the useful composition
	// — set_null the block's gate, the block follows — does what it
	// reads like.
	for _, pair := range [][2]string{{"set", "set_null"}, {"set", "set_expr"}, {"set_expr", "set_null"}} {
		if name, ok := firstOverlap(ruleSlotFields(r, pair[0]), ruleSlotFields(r, pair[1])); ok {
			return ruleError(errors.PULSE_SYNTH_RULE_CONFLICT, idx, pair[0], name,
				fmt.Sprintf("rule names field %q in both %s and %s", name, pair[0], pair[1]))
		}
	}

	if len(r.NullTogether) > 0 {
		distinct := make(map[string]bool, len(r.NullTogether))
		for _, name := range r.NullTogether {
			distinct[name] = true
		}
		if len(distinct) < 2 {
			return ruleError(errors.PULSE_SYNTH_RULE_BLOCK_INVALID, idx, "null_together", "",
				fmt.Sprintf("rule null_together names %d distinct field(s); a block needs at least 2", len(distinct)))
		}
	}

	for _, name := range sortedKeys(r.Set) {
		if err := validateRuleLiteral(idx, name, r.Set[name], byName[name]); err != nil {
			return err
		}
	}

	// LAST, deliberately. A set_null target the schema cannot record a
	// null for is refused rather than warned (ruleSetNullNullableFault),
	// but it is a fault of the FIELD DECLARATION rather than of the rule's
	// structure, so it is reported only once nothing structural is wrong:
	// a rule whose `when` does not compile AND whose set_null target is
	// non-nullable has a broken predicate, and saying so first points the
	// author at the line they have to fix anyway. Same reasoning puts it
	// after the `set` literal matrix, which is the other
	// can-this-value-land check.
	for _, name := range r.SetNull {
		if err := ruleSetNullNullableFault(idx, byName[name]); err != nil {
			return err
		}
	}
	return nil
}

// validateRuleIsnull refuses, at SPEC PARSE, an expression calling
// isnull with a STRING LITERAL naming a field the spec does not declare.
//
// It is PULSE_SYNTH_RULE_FIELD_UNKNOWN rather than _EXPR_INVALID, and the
// choice is the point rather than a detail. The expression COMPILES —
// expr's signature for the builtin is func(string) bool and a string
// literal satisfies it whatever the string says — so _EXPR_INVALID,
// whose documented meaning is "does not compile against the row
// environment", would have to be widened to cover a fault that is not a
// compile failure at all. _FIELD_UNKNOWN's meaning already describes it
// exactly ("a rule names a field the spec does not declare"), and it is
// the same fault every other slot's name is refused for: the argument to
// isnull IS a field name, just one written where only the builtin can
// see it. details["field"] carries the offending name, so the two spell
// the fault the same way.
//
// Why it is caught here at all, rather than left to isnullBuiltin's
// loud run-time refusal: validateRules' whole promise is that a
// malformed rule is refused at spec parse, and this was the one
// author-visible fault that slipped past it. expr.Patch cannot return an
// error, so the patcher that handles the bare-identifier form could not
// have carried the check.
func validateRuleIsnull(idx int, slot, target, src string, declared map[string]bool) error {
	unknown, bad := isnullUnknownField(src, declared)
	if !bad {
		return nil
	}
	where := "rule " + slot
	if target != "" {
		where = fmt.Sprintf("rule %s[%q]", slot, target)
	}
	return ruleError(errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN, idx, slot, unknown,
		fmt.Sprintf("%s calls %s with unknown field %q in %q", where, isnullFuncName, unknown, src))
}

// ruleSetNullNullableFault refuses a `set_null` naming a field the file
// cannot record a null for. It is the one fault that moved from a
// WARNING to a refusal after E1, and the reasoning is worth keeping
// because the slot next door kept the warning.
//
// encodeRow writes a null bit for NULLABLE fields only, so nulling a
// field declared without `"nullable": true` writes the type's zero as an
// ordinary value — indistinguishable from a real 0 on a u4 or a
// packed_bool. The rule FIRES and the file cannot show it, which is
// precisely the silent-outcome class the rule layer exists to remove,
// and it is eagerly detectable from FieldSpec.Nullable. A warning was
// not enough in practice: the terminal summary caps each kind at three
// examples, so on a survey-shaped spec three lines out of thousands of
// warning lines were the entire signal that a gate had gone missing.
//
// # Why `null_together` does NOT get the same refusal
//
// The refusal is keyed on what the SLOT CLAIMS, not on whether a field
// could conceivably end up nulled. `set_null` claims "this field is null
// on a matching row" — an unconditional statement about the field, which
// a non-nullable field can never honour. `null_together` claims
// something else: "these members carry the GATE's decision", which
// includes UN-nulling, and the direction it takes is the gate's run-time
// state rather than the rule's claim. A non-nullable member of a block
// whose gate is itself never null is doing nothing wrong, and that shape
// is not hypothetical — it is the measured gated-block idiom, where the
// block's gate is deliberately a never-null field (`aware`) so the copy
// CLEARS the members' own MCAR nulls and a later rule's `set_null`
// supplies the real gate. A refusal keyed on "any field a rule may null"
// refuses that idiom, which is the most useful shape this layer has. The
// non-gate members keep the warning instead (nullTogetherWarnings).
func ruleSetNullNullableFault(idx int, f FieldSpec) error {
	if f.Name == "" || f.Nullable {
		return nil
	}
	return ruleError(errors.PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE, idx, "set_null", f.Name,
		fmt.Sprintf("rule set_null names field %q, which is not declared nullable: "+
			"the row would carry 0 rather than a null and the file could not show the rule fired", f.Name))
}

// validateRuleLiteral refuses a `set` literal the target field cannot
// hold. It is a THIN WRAPPER over ruleValueFault (synth/rules_coerce.go),
// the one coercion matrix a `set` literal and a `set_expr` result share
// — a literal is refused at parse for BOTH fault kinds, because the
// value is right there in the document.
//
// The matrix used to live here, inlined. It moved when set_expr went
// live and needed the same answers: two copies of a type/range/domain
// table is exactly the shape E1-S6 exists because of, and here a
// disagreement would be silent (a u4 taking 16 is masked to its low
// nibble and stored as 0, which is a real value).
//
// A field whose declared `type` is not a name fieldTypeFromName knows is
// SKIPPED here rather than reported as a rule fault — buildSchema
// refuses that field on its own terms ("unknown field type %q for field
// %q"), and blaming the rule for it would point an author at the wrong
// line.
func validateRuleLiteral(idx int, name string, value any, f FieldSpec) error {
	ft, ok := fieldTypeFromName(f.Type)
	if !ok {
		return nil
	}
	kind, reason, err := ruleValueFault("set", name, f, ft, value, false)
	if err != nil {
		return err
	}
	if kind == ruleFaultNone {
		return nil
	}
	return ruleError(errors.PULSE_SYNTH_RULE_VALUE_INVALID, idx, "set", name, reason)
}

// validateRuleExpr refuses, AT SPEC PARSE, a `set_expr` whose inferred
// return type no value could coerce to its target.
//
// The timing split is the story's own rule: expr type-checks against the
// row environment, so `{"set_expr": {"region": "nps * 2"}}` is knowably
// impossible before a row exists and must not generate 400,000 rows
// first. A fault only a VALUE settles — a number out of the type's
// range, a computed category outside the declared domain — cannot be
// seen here and is refused at row time by the same matrix.
//
// prog is the already-compiled program; its Node carries the type expr
// inferred. An interface{} type is expr saying it could not infer, and
// defers whole.
func validateRuleExpr(idx int, name string, prog *vm.Program, f FieldSpec) error {
	ft, ok := fieldTypeFromName(f.Type)
	if !ok {
		return nil
	}
	node := prog.Node()
	if node == nil {
		return nil
	}
	if err := ruleExprStaticFault(name, f, ft, node.Type()); err != nil {
		return ruleError(errors.PULSE_SYNTH_RULE_VALUE_INVALID, idx, "set_expr", name, err.Error())
	}
	return nil
}

// scalarRange returns the inclusive value range a scalar field type can
// represent, and whether it is bounded at all. The bounds mirror what
// writeFieldValueForField would actually write: it CLAMPS an out-of-range
// unsigned value and masks a u4 to its low nibble, so a literal outside
// the range is silently something other than what was asked for — which
// is exactly the class of fault an eager refusal exists to catch. f32 /
// f64 / decimal128 are unbounded here; a float target takes any finite
// number and decimal overflow is the decimal codec's own error.
func scalarRange(ft encoding.FieldType) (lo, hi float64, bounded bool) {
	switch ft {
	case encoding.FieldTypePackedBool:
		return 0, 1, true
	case encoding.FieldTypeU4:
		return 0, 15, true
	case encoding.FieldTypeU8:
		return 0, math.MaxUint8, true
	case encoding.FieldTypeU16:
		return 0, math.MaxUint16, true
	case encoding.FieldTypeU32, encoding.FieldTypeDate:
		return 0, math.MaxUint32, true
	case encoding.FieldTypeU64:
		return 0, math.MaxUint64, true
	}
	return 0, 0, false
}

// declaredCategoricalDomain returns the value set a categorical field's
// own distribution declares, and whether the domain is knowable at all.
//
// Only weighted_categorical declares one (`params.values`). A `regex`
// field's domain is a language, and a `constant` field has a single value
// that a rule overriding it is not contradicting, so both are reported
// UNBOUNDED and a literal against them is accepted: the encoder grows the
// dictionary for an unseen value, which is legal on the wire. The
// bounded case is worth refusing because a typo'd category would
// otherwise add a silent extra dictionary entry that looks like data.
func declaredCategoricalDomain(f FieldSpec) ([]string, bool) {
	if f.Distribution != DistWeightedCategorical {
		return nil, false
	}
	values, ok, err := paramStringSlice(f.Name, f.Params, "values")
	if err != nil || !ok || len(values) == 0 {
		return nil, false
	}
	return values, true
}

// ruleSlotFields returns the field names one slot of a rule names, in a
// deterministic order: declaration order for the two arrays, sorted key
// order for the two maps. Never Go map order — a rule with two faults
// must refuse the same one on every run.
func ruleSlotFields(r RuleSpec, slot string) []string {
	switch slot {
	case "set":
		return sortedKeys(r.Set)
	case "set_expr":
		return sortedKeys(r.SetExpr)
	case "set_null":
		return r.SetNull
	case "null_together":
		return r.NullTogether
	}
	return nil
}

// sortedKeys returns a map's keys in sorted order. Generic over the
// value type so the `set` (any) and `set_expr` (string) maps share it.
func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// firstOverlap returns the first member of a that also appears in b.
func firstOverlap(a, b []string) (string, bool) {
	if len(a) == 0 || len(b) == 0 {
		return "", false
	}
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	for _, s := range a {
		if in[s] {
			return s, true
		}
	}
	return "", false
}

func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// ruleError builds one rule-fault error with the standard details.
func ruleError(code errors.Code, idx int, slot, field, msg string) error {
	details := map[string]any{errors.DetailSynthRule: idx}
	if slot != "" {
		details[errors.DetailSynthRuleSlot] = slot
	}
	if field != "" {
		details["field"] = field
	}
	return errors.NewCodedErrorWithDetails(code, fmt.Sprintf("rule %d: %s", idx, msg), details)
}
