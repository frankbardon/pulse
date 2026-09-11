package synth

import (
	"fmt"
	"math"
	"reflect"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// THE COERCION MATRIX.
//
// A rule writes a value to a field two ways — a `set` LITERAL out of the
// document and a `set_expr` RESULT out of an expression — and both have
// to answer the same question: can this Go value be written to this
// field's type, and what Go shape does the row have to hold for it?
//
// There is exactly ONE answer, in ruleValueFault below, and both paths
// go through it. That is not tidiness: E1-S6 exists because two tables
// that had to agree did not, and a disagreement here is invisible — a
// bool landing on a u8 as a Go-formatted string encodes as 0, a computed
// category outside the declared domain adds a dictionary entry that
// looks like data, a u4 taking 16 is masked to its low nibble and stored
// as 0. Every one of those generates a plausible file.
//
// # The matrix, stated for an author
//
// Three TARGET classes, the same three sentinelFor derives:
//
//	scalar        u4 u8 u16 u32 u64 f32 f64 date packed_bool decimal128
//	categorical   categorical_u8 categorical_u16 categorical_u32
//	set           set_u8 set_u16 set_u32 set_u64
//
//	result \ target | scalar                  | categorical      | set
//	----------------+-------------------------+------------------+-----------------
//	bool            | 1 / 0                   | refused          | refused
//	number          | the number, range-checked| refused         | refused
//	string          | refused (see decimal)   | must be declared | refused
//	set selection   | refused                  | refused          | the selection
//
// "number" is any of expr's numeric results (float64 from arithmetic,
// int from a literal or len()); "set selection" is a map[string]bool (a
// set_* field read straight off the row) or an array of option names.
//
// # The two timings
//
// A fault the RETURN TYPE alone settles is refused at SPEC PARSE
// (ruleExprStaticFault, driven by expr's own inferred type), because a
// rule that can never work should not generate 400,000 rows first. A
// fault only a VALUE can settle — a number out of range, a category
// outside the declared domain — is refused at row time, because that is
// the first moment it exists. Both refusals are the same coded error and
// come out of the same matrix; the static side does not own a table.
//
// # The one asymmetry: decimal128 from a string
//
// A `set` literal string on a decimal128 target is kept VERBATIM
// (constantRowValue's decimal arm) and parsed by encoding.ParseDecimal128,
// which is exact where a float64 would already have lost digits. A
// `set_expr` result string on the same target is REFUSED, and that is a
// decision:
//
//   - Exactness is not actually reachable through an expression. expr
//     types every scalar field as the float64 the row holds, so anything
//     computed has already been through a float64 before the rule sees
//     it; keeping the string form would promise an exactness that was
//     lost upstream.
//   - Accepting it would be actively harmful. rowExprEnv types a
//     decimal128 field as float64, so a row carrying a string for it
//     makes the NEXT rule's `when` or `set_expr` over that field fail at
//     run time with "invalid operation: float64(string)" — issue #258's
//     failure, now intermittent, firing only on the rows the earlier
//     rule gated.
//
// So exactness lives where it is genuinely achievable — `set`, with a
// literal — and the refusal says so.

// ruleFaultKind separates a fault about the value's TYPE from one about
// the value itself. The split is what makes two timings possible off one
// matrix: only a type fault is knowable before a row exists.
type ruleFaultKind int

const (
	ruleFaultNone ruleFaultKind = iota
	// ruleFaultType marks a fault NO value of the offending Go type
	// could avoid, which is what makes it refusable from the return type
	// alone at spec parse.
	ruleFaultType
	// ruleFaultValue marks a fault this PARTICULAR value has — out of
	// the type's range, outside the field's declared domain, not finite.
	// Another value of the same Go type would be fine, so it can only be
	// refused when the value exists.
	ruleFaultValue
)

// ruleValueFault is the single matrix. It returns the kind of fault (if
// any) plus the author-facing reason, formatted against slot and name so
// one implementation can speak for `set` and for `set_expr`.
//
// fromExpr selects the decimal128 asymmetry documented above and nothing
// else; every other cell is identical for a literal and for a computed
// value, deliberately, because the reason for each refusal is a property
// of the TARGET rather than of where the value came from.
//
// The error return is for a malformed spec param (a set_* whose
// `params.options` is not an array of strings), which is the field's
// fault rather than the rule's and is surfaced unchanged.
func ruleValueFault(slot, name string, f FieldSpec, ft encoding.FieldType, value any, fromExpr bool) (ruleFaultKind, string, error) {
	typeFault := func(format string, args ...any) (ruleFaultKind, string, error) {
		return ruleFaultType, fmt.Sprintf(format, args...), nil
	}
	valueFault := func(format string, args ...any) (ruleFaultKind, string, error) {
		return ruleFaultValue, fmt.Sprintf(format, args...), nil
	}
	switch {
	case ft.IsCategorical():
		s, isStr := value.(string)
		if !isStr {
			return typeFault("rule %s[%q]: categorical field needs a string literal, got %T", slot, name, value)
		}
		if domain, bounded := declaredCategoricalDomain(f); bounded && !containsString(domain, s) {
			return valueFault("rule %s[%q] = %q is not one of the field's declared values %v", slot, name, s, domain)
		}
		return ruleFaultNone, "", nil

	case ft.IsSet():
		arr, isArr := value.([]any)
		if !isArr {
			sel, isMap := value.(map[string]bool)
			if !isMap {
				return typeFault("rule %s[%q]: set field needs an array of option strings, got %T", slot, name, value)
			}
			// A set_* field read straight off the row. Its members are
			// its OWN field's option names, and the target may be a
			// different set field, so the selection is still checked
			// below — walked in sorted order so a rule with two bad
			// options refuses the same one on every run.
			for _, opt := range sortedKeys(sel) {
				if sel[opt] {
					arr = append(arr, opt)
				}
			}
		}
		options, _, err := paramStringSlice(f.Name, f.Params, "options")
		if err != nil {
			return ruleFaultNone, "", err
		}
		for j, e := range arr {
			s, isStr := e.(string)
			if !isStr {
				return typeFault("rule %s[%q][%d]: set option must be a string, got %T", slot, name, j, e)
			}
			if len(options) > 0 && !containsString(options, s) {
				return valueFault("rule %s[%q][%d] = %q is not one of the field's declared options %v",
					slot, name, j, s, options)
			}
		}
		return ruleFaultNone, "", nil

	default:
		// Scalar class. A bool is legal on every scalar target and means
		// 1/0 — the reduction every other boolean path in the package
		// applies — so `{"set": {"flag": true}}` and
		// `{"set_expr": {"promoter": "nps >= 9"}}` read the way an
		// author writes them.
		if _, isBool := value.(bool); isBool {
			return ruleFaultNone, "", nil
		}
		num, isNum := literalFloat(value)
		if !isNum {
			if _, isStr := value.(string); isStr && ft.IsDecimal() {
				if fromExpr {
					return typeFault("rule %s[%q]: a decimal128 target takes an exact string only as a `set` "+
						"literal, never from an expression — expr has already reduced the value to a float64, "+
						"and a string in the row would break the next rule's reference to %q", slot, name, name)
				}
				return ruleFaultNone, "", nil
			}
			return typeFault("rule %s[%q]: %s field needs a number or a bool, got %T", slot, name, f.Type, value)
		}
		if math.IsNaN(num) || math.IsInf(num, 0) {
			return valueFault("rule %s[%q]: %s field cannot hold %v", slot, name, f.Type, value)
		}
		lo, hi, bounded := scalarRange(ft)
		if bounded && (num < lo || num > hi) {
			return valueFault("rule %s[%q] = %v is outside the representable range [%v, %v] of %s",
				slot, name, num, lo, hi, f.Type)
		}
		return ruleFaultNone, "", nil
	}
}

// ruleExprRowValue is the RUN-TIME half: it takes one `set_expr` result
// as expr returned it and produces the value the row must hold for the
// target field, or a coded refusal.
//
// Three steps, none of which is a second conversion:
//
//  1. ruleExprShape adapts the Go shapes only an EXPRESSION can produce
//     into the shapes the literal path already speaks.
//  2. ruleValueFault — the one matrix — decides whether it is writable.
//  3. constantRowValue — the `constant` distribution's own coercion,
//     which is also what a `set` literal goes through — produces the row
//     shape. A set_* target's array becomes the row's map[string]bool
//     here, exactly once, in the place E1-S3 already put it.
func ruleExprRowValue(name string, f FieldSpec, ft encoding.FieldType, out any) (any, error) {
	v := ruleExprShape(out)
	kind, reason, err := ruleValueFault("set_expr", name, f, ft, v, true)
	if err != nil {
		return nil, err
	}
	if kind != ruleFaultNone {
		return nil, fmt.Errorf("%s", reason)
	}
	return constantRowValue(f, v)
}

// ruleExprStaticFault is the SPEC-PARSE half. Given the return type expr
// inferred for the expression, it answers whether NO value of that type
// could be written to the target — the only fault knowable before a row
// exists.
//
// It does not own a table: it builds the type's zero value and asks the
// same ruleValueFault the run-time path asks, then refuses only on a
// ruleFaultType. A ruleFaultValue on the zero (an empty string is not a
// declared category; a 0 is in range for every bounded type, but a
// narrower one might not be) is deliberately ignored, because the static
// side has no value to judge and a false refusal here would reject a
// rule that is correct on every row it fires on.
//
// A nil type, or an interface type, is expr saying it could not infer —
// a conditional whose branches disagree, a nil literal. Those defer to
// run time whole.
func ruleExprStaticFault(name string, f FieldSpec, ft encoding.FieldType, rt reflect.Type) error {
	if rt == nil || rt.Kind() == reflect.Interface {
		return nil
	}
	probe := ruleExprShape(reflect.Zero(rt).Interface())
	kind, reason, err := ruleValueFault("set_expr", name, f, ft, probe, true)
	if err != nil {
		return err
	}
	if kind != ruleFaultType {
		return nil
	}
	return fmt.Errorf("%s", reason)
}

// ruleExprShape adapts an expression result's Go shape into one of the
// shapes constantRowValue and ruleValueFault already understand. It is
// NOT a second coercion: it renames shapes, it never decides whether a
// value is writable and it never produces the row's value.
//
// It exists because expr can return integer and slice shapes a JSON
// document cannot. literalFloat covers the widths a decoded document
// produces (float64, int, int64, uint64, float32); the narrower integer
// kinds reach it only from an expression, and split() is the obvious way
// to produce a set selection, so both are mapped rather than refused.
func ruleExprShape(v any) any {
	switch x := v.(type) {
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	case int8:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return float64(x)
	case uint:
		return float64(x)
	case uint8:
		return float64(x)
	case uint16:
		return float64(x)
	case uint32:
		return float64(x)
	}
	return v
}

// ruleExprResultError wraps a coercion refusal in the rule family's
// coded error. PULSE_SYNTH_RULE_VALUE_INVALID is REUSED rather than
// given a sibling: the fault is "the value this rule wants to write
// cannot be written to its target", which is one concept whether the
// value was typed into the document or computed from the row, and the
// shared ruleValueFault means the two really are the same check. The
// details map carries which (slot "set" vs "set_expr") and, for a
// computed value, the expression and the result.
func ruleExprResultError(idx int, name, src string, out any, cause error) error {
	details := map[string]any{
		errors.DetailSynthRule:     idx,
		errors.DetailSynthRuleSlot: "set_expr",
		"field":                    name,
		"expr":                     src,
	}
	if out != nil {
		details["value"] = fmt.Sprintf("%v", out)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_RULE_VALUE_INVALID,
		fmt.Sprintf("rule %d: %s", idx, cause.Error()), details)
}
