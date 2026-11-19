package descriptor

import (
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// formulaNullableOperandAdvisories: one
// PULSE_ADVISORY_FORMULA_NULLABLE_OPERAND per ATTR_FORMULA (the
// instance offers) that feeds a nullable input, unguarded, to an
// operand that rejects nil (exprident.NullableOperands: arithmetic, an
// ordering compare, unary minus, an index, a nil-rejecting built-in's
// argument), in declared order. ATTR_FORMULA is the one row-level
// expression site such a nil ABORTS (formulaAttribute.Row raises the
// evaluation error): FILTER_EXPRESSION and a total() argument drop the
// row, a regression holdout predicate skips it (n_null) and WIN_FORMULA
// reads a null input as a null output, so none of them advises.
//
// The nullable inputs: a schema field whose Nullable flag is set
// (authoritative, so a nullable column with no null cell over-warns, as
// FORMULA_NULLABLE_RESULT does), and the label of an EARLIER attribute
// whose output can be null — a built-in by its declaration
// (attributeNullOutputs), an earlier ATTR_FORMULA when its result can
// be nil (exprident.NullableResult over the same view), an extension
// attribute by the instance its factory builds for the slot
// (extend.NullableAttribute, ExtensionsSnapshot.AttributeEmitsNull).
// Any other name (a feature output, a window label, a vector) is not
// judged. details {slot, operator, label, fields, operand_kind}:
// operand_kind[i] is the kind of fields[i]'s first unguarded use.
// factories false (a refused request) judges no extension attribute.
func formulaNullableOperandAdvisories(req *types.Request, schema *encoding.Schema, prefix string, inst *InstanceSnapshot, factories bool) []descriptor.Advisory {
	if schema == nil || !slices.ContainsFunc(req.Attributes, func(a *types.Attribute) bool {
		return a != nil && opRoute(inst, a.Type) == types.ATTR_FORMULA
	}) {
		return nil
	}
	// derived holds each attribute label's nullability, judged lazily —
	// only a label a formula reads is judged, so an extension factory
	// runs only for one — and over the labels declared before it.
	type derivedNull struct {
		at         int
		judge      func(nullable func(string) bool) bool
		done, null bool
	}
	derived := map[string]*derivedNull{}
	var view func(limit int) func(string) bool
	view = func(limit int) func(string) bool {
		return func(name string) bool {
			if d, ok := derived[name]; ok && d.at < limit {
				if !d.done {
					d.null, d.done = d.judge(view(d.at)), true
				}
				return d.null
			}
			f := schema.Field(name)
			return f != nil && f.Nullable
		}
	}
	var out []descriptor.Advisory
	for i, attr := range req.Attributes {
		if attr == nil {
			continue
		}
		label := attr.Label
		if label == "" {
			label = vectors.DefaultAttributeLabel(attr)
		}
		op := opRoute(inst, attr.Type)
		if op != types.ATTR_FORMULA {
			derived[label] = &derivedNull{at: i, judge: func(nullable func(string) bool) bool {
				return attributeOutputNullable(attr, op, req, schema, nullable, inst, factories)
			}}
			continue
		}
		if reads, parsed := exprident.NullableOperands(attr.Expression, view(i)); parsed && len(reads) > 0 {
			out = append(out, nullableOperandAdvisory(prefix+"attributes["+strconv.Itoa(i)+"]", label, reads))
		}
		derived[label] = &derivedNull{at: i, judge: func(nullable func(string) bool) bool {
			fields, literal, _ := exprident.NullableResult(attr.Expression, nullable)
			return len(fields) > 0 || literal
		}}
	}
	return out
}

// operandKindNouns is how the advisory message names each operand kind.
var operandKindNouns = map[string]string{
	exprident.OperandArithmetic: "arithmetic",
	exprident.OperandCompare:    "an ordering comparison",
	exprident.OperandUnaryMinus: "a unary minus",
	exprident.OperandIndex:      "an index",
	exprident.OperandFunction:   "a function argument that rejects nil",
}

func nullableOperandAdvisory(slot, label string, reads []exprident.OperandRead) descriptor.Advisory {
	fields := make([]string, len(reads))
	kinds := make([]string, len(reads))
	uses := make([]string, len(reads))
	fixes := make([]string, len(reads))
	for i, r := range reads {
		fields[i], kinds[i] = r.Field, r.Kind
		uses[i] = r.Field + " (" + operandKindNouns[r.Kind] + ")"
		fixes[i] = r.Field + " ?? 0"
	}
	noun := "the nullable input "
	if len(reads) > 1 {
		noun = "the nullable inputs "
	}
	return descriptor.Advisory{
		Code: string(errors.PULSE_ADVISORY_FORMULA_NULLABLE_OPERAND),
		Message: "ATTR_FORMULA at " + slot + " (" + label + ") feeds " + noun + strings.Join(uses, ", ") +
			" to an operand that cannot take nil, with no guard, so the run fails with an evaluation error on the first row where one is null (a null operand never yields a null); " +
			"guard each with " + strings.Join(fixes, ", ") + ", a field != nil ? ... : ... ternary or a field != nil && ... short-circuit, or filter those rows out first.",
		Details: map[string]any{
			"slot":         slot,
			"operator":     string(types.ATTR_FORMULA),
			"label":        label,
			"fields":       fields,
			"operand_kind": kinds,
		},
	}
}

// attributeNullOutput is when a built-in attribute's output can be null.
type attributeNullOutput uint8

const (
	// nullNever: no row is ever null (a null input reads 0).
	nullNever attributeNullOutput = iota
	// nullOnNullInput: null exactly on a row whose Field is null (a
	// zero-spread score nulls every row too, but that is the data's,
	// warned at run time as PULSE_ATTR_ZERO_SPREAD, never the schema's).
	nullOnNullInput
	// nullStructural: the operator writes nulls the schema cannot rule
	// out (a row dropped from a fit, a partition's first row, a missing
	// vector member), so its output is always judged nullable.
	nullStructural
	// nullFormulaResult: ATTR_FORMULA — nullable when its result can be
	// nil (exprident.NullableResult).
	nullFormulaResult
)

// attributeNullOutputs declares, per built-in attribute, when its output
// can be null — the runtime's AttributeNull returns and NullEmitter
// declarations (internal/processing). TestAttributeNullOutputs_Complete
// holds it to types.AllAttributeTypes.
var attributeNullOutputs = map[types.AttributeType]attributeNullOutput{
	types.ATTR_CODE_IN:            nullNever,
	types.ATTR_SET_HAS:            nullNever,
	types.ATTR_ZSCORE:             nullOnNullInput,
	types.ATTR_TSCORE:             nullOnNullInput,
	types.ATTR_NORMALIZED:         nullOnNullInput,
	types.ATTR_PERCENTILE:         nullOnNullInput,
	types.ATTR_SET_POPCOUNT:       nullOnNullInput,
	types.ATTR_DATE_PART:          nullOnNullInput, // or a null per-row zone cell
	types.ATTR_FORMULA:            nullFormulaResult,
	types.ATTR_PREVIOUS_STATE:     nullStructural,
	types.ATTR_REG_FITTED:         nullStructural,
	types.ATTR_REG_RESIDUAL:       nullStructural,
	types.ATTR_REG_LEVERAGE:       nullStructural,
	types.ATTR_REG_INFLUENCE:      nullStructural,
	types.ATTR_REG_PREDICT:        nullStructural,
	types.ATTR_MAHALANOBIS:        nullStructural,
	types.ATTR_PC_SCORE:           nullStructural,
	types.ATTR_FACTOR_SCORE:       nullStructural,
	types.ATTR_SEM_SCORE:          nullStructural,
	types.ATTR_SCALE_SCORE:        nullStructural,
	types.ATTR_PROFILE_SIMILARITY: nullStructural,
	types.ATTR_VEC_DISTANCE:       nullStructural,
}

// attributeOutputNullable reports whether attr's output (op its routed
// type; "" when the instance hides it) can be null on some row. An
// extension attribute is asked through its factory only when factories
// (the request is not refused: no factory runs for a refused request).
func attributeOutputNullable(attr *types.Attribute, op types.AttributeType, req *types.Request, schema *encoding.Schema, nullable func(string) bool, inst *InstanceSnapshot, factories bool) bool {
	class, builtin := attributeNullOutputs[op]
	if !builtin {
		ext := inst.Extensions()
		if !factories || op == "" || ext == nil || ext.AttributeEmitsNull[op] == nil {
			return false
		}
		return ext.AttributeEmitsNull[op](attr, schema)
	}
	switch class {
	case nullOnNullInput:
		if nullable(attr.Field) {
			return true
		}
		if op != types.ATTR_DATE_PART {
			return false
		}
		// A per-row zone applies only to a datetime; a null zone cell
		// nulls the part.
		if f := schema.Field(attr.Field); f == nil || f.Type != encoding.FieldTypeDateTime {
			return false
		}
		zf, _ := zoneFieldFor(zoneSlot{tz: attr.TimeZone, tzField: attr.TimeZoneField}, req.TimeZone, req.TimeZoneField, schema.ZoneField)
		return zf != "" && nullable(zf)
	case nullStructural:
		return true
	}
	return false
}
