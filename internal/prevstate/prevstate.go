// Package prevstate is the one ATTR_PREVIOUS_STATE slot rule: which
// slot keys it reads (field, subject_field, order_field), which it
// refuses, and the field types each accepts. The runtime factory
// (internal/processing) and predict (internal/descriptor) both call
// Check, so the two raise the same code, message and details by
// construction; the field-reference walk (internal/descriptor, run on
// both sides before any operator is built) calls FieldRefs and
// ForeignSlotRefusal.
//
// It is a leaf: it imports only the public errors / encoding / types
// packages and internal/fieldaccept, never internal/processing or
// internal/service, so internal/descriptor may import it without
// breaking TestPredictNoExecutionImports.
package prevstate

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// op is the operator name every refusal carries.
const op = string(types.ATTR_PREVIOUS_STATE)

// Ref is one field the slot reads: the slot key and the field it names.
type Ref struct {
	Slot fieldaccept.Slot
	Name string
}

// FieldRefs returns the fields attr reads, in judgement order (field,
// subject_field, order_field), skipping an empty name (Check's
// presence refusal).
func FieldRefs(attr *types.Attribute) []Ref {
	if attr == nil {
		return nil
	}
	var out []Ref
	for _, r := range []Ref{
		{fieldaccept.SlotField, attr.Field},
		{fieldaccept.SlotSubject, attr.SubjectField},
		{fieldaccept.SlotOrder, attr.OrderField},
	} {
		if r.Name != "" {
			out = append(out, r)
		}
	}
	return out
}

// Check is the slot's configuration rule, first refusal first:
//
//  1. field, subject_field and order_field are each required
//     (PROCESSING_CONFIG);
//  2. target, predictors and expression belong to other attribute
//     types and would be silently ignored here (PROCESSING_CONFIG);
//  3. each named schema field's type is judged by the accepted-types
//     table (PULSE_FIELD_TYPE_UNSUPPORTED): field a categorical_* or
//     packed_bool (a coded unsigned integer is refused with a pointer
//     at re-importing it as a categorical), subject_field any scalar
//     but a set_*, order_field a date or datetime.
//
// A name the schema does not carry (unknown, or a derived column) is
// the field-reference walk's refusal, and a vector field its
// PULSE_VECTOR_FIELD_NOT_SCALAR, so neither is judged here. A nil
// schema judges only rules 1-2.
func Check(attr *types.Attribute, schema *encoding.Schema) *errors.CodedError {
	if attr == nil {
		return errors.NewCodedError(errors.PROCESSING_CONFIG, op+" requires an attribute spec")
	}
	for _, r := range []Ref{
		{fieldaccept.SlotField, attr.Field},
		{fieldaccept.SlotSubject, attr.SubjectField},
		{fieldaccept.SlotOrder, attr.OrderField},
	} {
		if r.Name == "" {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				op+" requires "+string(r.Slot)+" ("+slotRole(r.Slot)+")",
				map[string]any{"attribute": op, "slot": string(r.Slot)})
		}
	}
	if attr.Target != "" || len(attr.Predictors) > 0 || attr.Expression != "" {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			op+" does not take target, predictors or expression; it reads field (the state), subject_field and order_field, and label names the output column",
			map[string]any{
				"attribute":  op,
				"target":     attr.Target,
				"predictors": attr.Predictors,
				"expression": attr.Expression,
			})
	}
	if schema == nil {
		return nil
	}
	for _, r := range FieldRefs(attr) {
		f := schema.Field(r.Name)
		if f == nil {
			continue
		}
		cerr := fieldaccept.Refusal(op, r.Slot, r.Name, f.Type)
		if cerr == nil {
			continue
		}
		if r.Slot == fieldaccept.SlotField && unsignedInteger(f.Type) {
			cerr.Message += "; a coded integer state carries no labels — re-import the column as a categorical (an explicit import schema) and name that field"
		}
		return cerr
	}
	return nil
}

// ForeignSlotRefusal refuses subject_field / order_field on any
// attribute route other than ATTR_PREVIOUS_STATE: every other attribute
// would ignore them silently. Nil when neither is set, or route is
// ATTR_PREVIOUS_STATE.
func ForeignSlotRefusal(attr *types.Attribute, route types.AttributeType) *errors.CodedError {
	if attr == nil || route == types.ATTR_PREVIOUS_STATE || (attr.SubjectField == "" && attr.OrderField == "") {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		string(attr.Type)+" does not take subject_field or order_field (only "+op+" reads them)",
		map[string]any{
			"attribute":     string(attr.Type),
			"subject_field": attr.SubjectField,
			"order_field":   attr.OrderField,
		})
}

// slotRole is the one-phrase role of each required slot.
func slotRole(s fieldaccept.Slot) string {
	switch s {
	case fieldaccept.SlotSubject:
		return "the field identifying each record's subject"
	case fieldaccept.SlotOrder:
		return "the date or datetime field ordering each subject's records"
	}
	return "the categorical or packed_bool state to read"
}

// unsignedInteger reports whether ft is one of the unsigned integer
// types a coded state is typically stored as.
func unsignedInteger(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64:
		return true
	}
	return false
}
