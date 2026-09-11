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
// which are actions:
//
//	{"when": "familiarity == 1",
//	 "set_null": ["perception_1", "perception_2"],
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
	env, names := rowExprEnv(s.Fields)
	probe := &compiledConstraints{fields: names}
	whenOpts := rowExprOptions(env, names, probe.isnullBuiltin, expr.AsBool())
	valueOpts := rowExprOptions(env, names, probe.isnullBuiltin)

	for i, r := range s.Rules {
		if err := validateRule(i, r, byName, whenOpts, valueOpts); err != nil {
			return err
		}
	}
	return nil
}

func validateRule(idx int, r RuleSpec, byName map[string]FieldSpec, whenOpts, valueOpts []expr.Option) error {
	if len(r.Set) == 0 && len(r.SetExpr) == 0 && len(r.SetNull) == 0 && len(r.NullTogether) == 0 {
		return ruleError(errors.PULSE_SYNTH_RULE_EMPTY, idx, "", "",
			"rule declares no action: expected at least one of set, set_expr, set_null, null_together")
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
	}
	for _, name := range sortedKeys(r.SetExpr) {
		prog, err := expr.Compile(r.SetExpr[name], valueOpts...)
		if err != nil {
			return ruleError(errors.PULSE_SYNTH_RULE_EXPR_INVALID, idx, "set_expr", name,
				fmt.Sprintf("rule set_expr[%q] = %q does not compile: %v", name, r.SetExpr[name], err))
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
	return nil
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
