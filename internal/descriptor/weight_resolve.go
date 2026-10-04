package descriptor

import (
	"fmt"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// weightAwareOperators is the set of built-in operators that consume a
// resolved row weight. It is the hook ResolveWeights reads to report
// "applied" vs "skipped_not_weight_aware"; the weighting-descriptive
// effort fills it operator by operator as each one learns to weight
// (.claude/reference/weighting.md, Aggregator classification). Until an
// operator is listed it is reported as skipped, never as applied — a
// predict answer must not claim a weight the runtime does not use.
var weightAwareOperators = map[string]bool{}

// IsWeightAware reports whether the built-in operator (or overlay kind)
// op consumes a resolved row weight.
func IsWeightAware(op string) bool { return weightAwareOperators[op] }

// weightFieldTypes is the set of field types a weight may live in:
// unsigned integers and floats. decimal128, categorical, date-family,
// packed_bool and set fields are refused.
var weightFieldTypes = map[encoding.FieldType]bool{
	encoding.FieldTypeU4:  true,
	encoding.FieldTypeU8:  true,
	encoding.FieldTypeU16: true,
	encoding.FieldTypeU32: true,
	encoding.FieldTypeU64: true,
	encoding.FieldTypeF32: true,
	encoding.FieldTypeF64: true,
}

// IsWeightFieldType reports whether a weight may live in a field of
// type t: the unsigned integers and the floats.
func IsWeightFieldType(t encoding.FieldType) bool { return weightFieldTypes[t] }

// weightSlot is one slot that can carry a per-slot `weight`, flattened
// out of a request so the resolution rules are written once over every
// slot family.
type weightSlot struct {
	slot     string
	operator string
	weight   types.SlotWeight
	// hidden: the instance hides operator, so it is judged as a
	// never-registered (not weight-aware) name.
	hidden bool
}

// ValidateWeightSpec is the shape rule every weight passes before any
// schema is consulted: a non-empty field and a known kind (empty means
// probability). where names the weight in the refusal ("weight",
// "aggregations[0].weight", "Options.DefaultWeight"). A nil spec is
// valid (no weight). pulse.New applies it to Options.DefaultWeight; the
// resolver applies it to every request and slot weight.
func ValidateWeightSpec(spec *types.WeightSpec, where string) error {
	if spec == nil {
		return nil
	}
	if spec.Field == "" {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			where+": a weight must name a field",
			map[string]any{"slot": where})
	}
	switch spec.Kind {
	case "", types.WeightKindProbability, types.WeightKindFrequency:
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		fmt.Sprintf("%s: weight kind %q is not one of %q, %q", where, spec.Kind, types.WeightKindProbability, types.WeightKindFrequency),
		map[string]any{"slot": where, "field": spec.Field, "kind": string(spec.Kind)})
}

// weightFieldTypeRefusal refuses a weight field the schema carries
// under a type a weight cannot live in. A field the schema does not
// carry is not judged here (the field-reference rule owns it); a nil
// schema judges nothing.
func weightFieldTypeRefusal(schema *encoding.Schema, where, field string) error {
	if schema == nil {
		return nil
	}
	f := schema.Field(field)
	if f == nil || IsWeightFieldType(f.Type) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		fmt.Sprintf("%s: weight field %q is of type %s; a weight must be an unsigned integer (u4, u8, u16, u32, u64) or float (f32, f64) field", where, field, f.Type.String()),
		map[string]any{"slot": where, "field": field, "type": f.Type.String()})
}

// weightSlots flattens req's weight-bearing slots in reporting order:
// aggregations, crosstab cell, crosstab margin aggregations, tests,
// post-tests, regressions, attributes, overlays.
func weightSlots(req *types.Request, inst *InstanceSnapshot) []weightSlot {
	var slots []weightSlot
	add := func(slot, op string, w types.SlotWeight) {
		slots = append(slots, weightSlot{slot: slot, operator: op, weight: w, hidden: inst.Hidden(op)})
	}
	for i, a := range req.Aggregations {
		if a != nil {
			add(fmt.Sprintf("aggregations[%d]", i), string(a.Type), a.Weight)
		}
	}
	if ct := req.Crosstab; ct != nil {
		if ct.Cell != nil {
			add("crosstab.cell", string(ct.Cell.Type), ct.Cell.Weight)
		}
		for i, a := range ct.MarginAggregations {
			if a != nil {
				add(fmt.Sprintf("crosstab.margin_aggregations[%d]", i), string(a.Type), a.Weight)
			}
		}
	}
	for i, t := range req.Tests {
		if t != nil {
			add(fmt.Sprintf("tests[%d]", i), string(t.Type), t.Weight)
		}
	}
	for i, t := range req.PostTests {
		if t != nil {
			add(fmt.Sprintf("post_tests[%d]", i), string(t.Type), t.Weight)
		}
	}
	for i, r := range req.Regressions {
		if r != nil {
			add(fmt.Sprintf("regressions[%d]", i), string(r.Type), r.Weight)
		}
	}
	for i, a := range req.Attributes {
		if a != nil {
			add(fmt.Sprintf("attributes[%d]", i), string(a.Type), a.Weight)
		}
	}
	for i, o := range req.Overlays {
		add(fmt.Sprintf("overlays[%d]", i), string(o.Kind), o.Weight)
	}
	return slots
}

// requestNamesWeight reports whether req, any of its slots, or the
// instance default names a weight (a slot `null` included).
func requestNamesWeight(req *types.Request, slots []weightSlot, defaultWeight *types.WeightSpec) bool {
	if req.Weight != nil || defaultWeight != nil {
		return true
	}
	for _, s := range slots {
		if !s.weight.IsZero() {
			return true
		}
	}
	return false
}

// ResolveWeights is the single weight-resolution pass for a Request.
// The runtime (internal/service, every execution mode — Compose slots
// and chain stages each resolve their own Request) and predict both
// call it, so a request is refused or accepted identically by both.
//
// Precedence per weight-bearing slot: slot `weight` (`null` opts the
// slot out) → req.Weight → defaultWeight (pulse.Options.DefaultWeight)
// → none. Rules, in evaluation order:
//
//   - req.Weight and defaultWeight must pass ValidateWeightSpec
//     (PROCESSING_CONFIG), whether or not a slot inherits them;
//   - req.Weight's field, when the schema carries it, must be an
//     unsigned-integer or float field (PROCESSING_CONFIG with details
//     {slot, field, type});
//   - a slot's own weight passes the same two checks;
//   - an inherited Options.DefaultWeight is judged only where it
//     APPLIES (the slot's operator is weight-aware): there its field
//     must exist in the schema (the field-reference refusal,
//     SERVICE_VALIDATION) and be of a weight type. A default that
//     applies to no slot of this request is never refused, so an
//     instance default does not break a cohort that lacks the column
//     for a request that would not use it.
//
// A field the schema does not carry on a request or slot weight is the
// field-reference rule's refusal (FieldRefRefusals judges every
// explicit weight field against the schema), not this pass's. A slot
// with an empty operator Type is skipped (a type error, reported
// identically with or without a weight). A nil schema skips every
// field-dependent refusal. inst routes hidden operators as
// never-registered (not weight-aware); nil hides nothing.
//
// The result is nil when neither the request, any slot, nor
// defaultWeight names a weight. It never mutates req.
func ResolveWeights(req *types.Request, schema *encoding.Schema, defaultWeight *types.WeightSpec, inst *InstanceSnapshot) ([]descriptor.ResolvedWeight, error) {
	if req == nil {
		return nil, nil
	}
	slots := weightSlots(req, inst)
	if !requestNamesWeight(req, slots, defaultWeight) {
		return nil, nil
	}
	if err := ValidateWeightSpec(defaultWeight, "Options.DefaultWeight"); err != nil {
		return nil, err
	}
	if err := ValidateWeightSpec(req.Weight, "weight"); err != nil {
		return nil, err
	}
	if req.Weight != nil {
		if err := weightFieldTypeRefusal(schema, "weight", req.Weight.Field); err != nil {
			return nil, err
		}
	}

	out := []descriptor.ResolvedWeight{}
	for _, s := range slots {
		if s.operator == "" {
			continue
		}
		rw := descriptor.ResolvedWeight{Slot: s.slot, Operator: s.operator}
		var spec *types.WeightSpec
		switch {
		case s.weight.IsNull():
			rw.Status, rw.Source = descriptor.WeightStatusOptedOut, descriptor.WeightSourceSlot
			out = append(out, rw)
			continue
		case !s.weight.IsZero():
			spec, rw.Source = s.weight.Spec(), descriptor.WeightSourceSlot
			where := s.slot + ".weight"
			if err := ValidateWeightSpec(spec, where); err != nil {
				return nil, err
			}
			if err := weightFieldTypeRefusal(schema, where, spec.Field); err != nil {
				return nil, err
			}
		case req.Weight != nil:
			spec, rw.Source = req.Weight, descriptor.WeightSourceRequest
		case defaultWeight != nil:
			spec, rw.Source = defaultWeight, descriptor.WeightSourceOptions
		default:
			rw.Status, rw.Source = descriptor.WeightStatusNone, descriptor.WeightSourceNone
			out = append(out, rw)
			continue
		}
		rw.Field, rw.Kind = spec.Field, string(spec.EffectiveKind())
		aware := !s.hidden && IsWeightAware(s.operator)
		if !aware {
			rw.Status = descriptor.WeightStatusSkippedNotWeightAware
			out = append(out, rw)
			continue
		}
		if rw.Source == descriptor.WeightSourceOptions && schema != nil {
			if schema.Field(spec.Field) == nil {
				return nil, refusal(fmt.Sprintf("%s: Options.DefaultWeight references unknown field: %s", s.slot, spec.Field),
					map[string]any{"field": spec.Field, "slot": s.slot, "operator": s.operator})
			}
			if err := weightFieldTypeRefusal(schema, s.slot+".weight", spec.Field); err != nil {
				return nil, err
			}
		}
		rw.Status = descriptor.WeightStatusApplied
		out = append(out, rw)
	}
	return out, nil
}

// resolveRequestWeights is the validators' mirror of one runtime
// resolution: smart defaults on a clone (unless opts.DisableDefaults)
// against schema, then ResolveWeights with the options' default weight.
func resolveRequestWeights(req *types.Request, schema *encoding.Schema, opts *PredictOptions) ([]descriptor.ResolvedWeight, error) {
	if opts == nil {
		opts = &PredictOptions{}
	}
	return ResolveWeights(defaultedForValidation(req, schema, opts), schema, opts.DefaultWeight, opts.instance())
}
