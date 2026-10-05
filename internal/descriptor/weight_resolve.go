package descriptor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// IsWeightAware reports whether the built-in operator op consumes a
// resolved row weight. The classification is internal/weighting's one
// table (.claude/reference/weighting.md, Aggregator classification),
// shared with the engine so predict never claims a weight the runtime
// does not apply.
func IsWeightAware(op string) bool { return weighting.IsAware(op) }

// WeightedMeanParamField returns AGG_WEIGHTED_MEAN's
// `params.weight_field` (the slot-weight sugar), or "" when the
// aggregation is another type, carries no such key, or its params do
// not decode (the factory owns that refusal).
func WeightedMeanParamField(a *types.Aggregation) string {
	if a == nil || a.Type != types.AGG_WEIGHTED_MEAN || len(a.Params) == 0 {
		return ""
	}
	var p struct {
		WeightField string `json:"weight_field"`
	}
	if json.Unmarshal(a.Params, &p) != nil {
		return ""
	}
	return p.WeightField
}

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
	// sugar is AGG_WEIGHTED_MEAN's params.weight_field: a slot-level
	// weight of kind probability.
	sugar string
	// valueFields are the columns an aggregation slot weights (its
	// Field, or AGG_RATIO's numerator / denominator); nil on every
	// other slot family.
	valueFields []string
	// overlay marks a Request.Overlays slot: its class is its kind's
	// Inferential flag (overlayWeightClass), not the operator table.
	overlay bool
	// noSlotWeight marks a slot family with no `weight` of its own
	// (windows): only the request weight is explicit there, and the
	// refusal prose says so.
	noSlotWeight bool
	// extCategory is the weight-bearing extension category
	// ("aggregator", "attribute", "test") when operator is an
	// embedder-registered operator of the slot's family, "" otherwise;
	// extAware is its registration's WeightAware declaration.
	extCategory string
	extAware    bool
	// reason is a slot-level PERMANENT refusal of a built-in operator
	// that is otherwise classed by the table: every post-test (it reads
	// aggregated rows) and a regression carrying a resample or
	// selection modifier.
	reason string
}

// Slot-level permanent refusal reasons (weightSlot.reason).
const (
	reasonPostTest  = "a post-test reads the aggregated result rows, which carry no row weights"
	reasonResample  = "row resampling under weights is replicate-weight (design-based) territory with no standard form"
	reasonSelection = "an information criterion has no standard form under weights (a probability-weighted likelihood is only a pseudo-likelihood)"
)

// overlayWeighting is one overlay kind's weight class: the kinds it
// honours a weight under (ClassAware: both; ClassFrequencyOnly:
// frequency), or a permanent refusal with its reason and, where one
// exists, the weighted twin the refusal points at.
type overlayWeighting struct {
	class  weighting.Class
	reason string
	twin   types.OverlayKind
}

// overlayWeightClasses is the per-kind overlay class table. A kind it
// does not list is classed off its manifest Inferential flag
// (overlayWeightClass): an Inferential kind is refused until its
// weighted form lands here; a descriptive kind (shares, indices,
// deltas, z-scores) reads the host payload and is untouched. A kind
// flipped to ClassAware / ClassFrequencyOnly owes its weighted contract
// in .claude/reference/weighting.md — and never before its weighted
// computation exists.
var overlayWeightClasses = map[types.OverlayKind]overlayWeighting{
	// Computes its own weighted z from the host's weighted moments,
	// under either kind (its n_basis is the caller's choice).
	types.OverlayKindPairwiseWeightedTwoMeansZ: {class: weighting.ClassAware},
	// Mean comparisons on AGG_WELFORD legs read through the weighted-host
	// rule (processing.CrosstabHostView.MeanLeg): N* off the floor keys,
	// the variance recomputed from m2 on w*, Welch df on N*.
	types.OverlayKindTCell:          {class: weighting.ClassAware},
	types.OverlayKindTVsRef:         {class: weighting.ClassAware},
	types.OverlayKindZCell:          {class: weighting.ClassAware},
	types.OverlayKindZVsRef:         {class: weighting.ClassAware},
	types.OverlayKindPairwiseWelchT: {class: weighting.ClassAware},
	types.OverlayKindPairwiseTwoMeansZ: {class: weighting.ClassRefuse,
		reason: "its unweighted Welford triple cannot carry a weighted variance; its weighted twin computes the weighted z",
		twin:   types.OverlayKindPairwiseWeightedTwoMeansZ},
	types.OverlayKindPairwiseProbitT: {class: weighting.ClassRefuse,
		reason: "the probit-transformed proportion t is a market-research convention with no reference weighted form"},
}

// overlayWeightClass classes an overlay kind: its overlayWeightClasses
// entry, else ClassRefuse for an Inferential kind (manifest flag,
// capabilities_overlay.go) and ClassNone for a descriptive one.
func overlayWeightClass(kind string) weighting.Class {
	k := types.OverlayKind(kind)
	if w, ok := overlayWeightClasses[k]; ok {
		return w.class
	}
	if overlayCapabilityFor(k).Inferential {
		return weighting.ClassRefuse
	}
	return weighting.ClassNone
}

// OverlayWeightKinds returns the weight kinds an overlay kind honours
// a weight under (manifest weight_kinds), nil for a refused or
// descriptive kind.
func OverlayWeightKinds(kind types.OverlayKind) []types.WeightKind {
	return weighting.KindsOfClass(overlayWeightClass(string(kind)))
}

// permanentReason is why the slot's operator is refused permanently
// under a weight, "" when it is not: a slot-level reason (a post-test,
// a regression modifier) first, then the overlay table, then the
// operator table.
func permanentReason(s weightSlot) string {
	switch {
	case s.reason != "":
		return s.reason
	case s.overlay:
		return overlayWeightClasses[types.OverlayKind(s.operator)].reason
	}
	return weighting.RefusalReason(s.operator)
}

// refusalHead is the shared head of a refused slot's
// PULSE_WEIGHT_UNSUPPORTED: "<slot>: <op> ..." naming the permanent
// reason or the pending gap, its details ({slot, operator, field}, plus
// `reason` when permanent and `alternative` for an overlay with a
// weighted twin the instance offers), and the alternative's prose
// suffix ("" when none).
func refusalHead(s weightSlot, field string, inst *InstanceSnapshot) (head string, details map[string]any, alt string) {
	details = map[string]any{"slot": s.slot, "operator": s.operator, "field": field}
	reason := permanentReason(s)
	if reason == "" {
		return fmt.Sprintf("%s: %s has no weighted form yet", s.slot, s.operator), details, ""
	}
	details["reason"] = reason
	if twin := overlayWeightClasses[types.OverlayKind(s.operator)].twin; s.overlay && twin != "" && !inst.Hidden(string(twin)) {
		details["alternative"] = string(twin)
		alt = fmt.Sprintf(", or use %s for the weighted comparison", twin)
	}
	return fmt.Sprintf("%s: %s cannot be weighted: %s", s.slot, s.operator, reason), details, alt
}

// weightUnsupported is the PULSE_WEIGHT_UNSUPPORTED refusal of a
// refused slot: permanent (details.reason) or pending (no weighted form
// yet).
func weightUnsupported(s weightSlot, field string, inst *InstanceSnapshot) error {
	head, details, alt := refusalHead(s, field, inst)
	return errors.NewCodedErrorWithDetails(errors.PULSE_WEIGHT_UNSUPPORTED,
		head+"; opt the slot out with \"weight\": null to run it unweighted"+alt, details)
}

// kindOnlyHead is the head and details of a frequency-only slot
// refused under a weight of another kind.
func kindOnlyHead(s weightSlot, field string, kind types.WeightKind) (string, map[string]any) {
	return fmt.Sprintf("%s: %s has a weighted form only under weight kind %q, and the weight in force is kind %q", s.slot, s.operator, types.WeightKindFrequency, kind),
		map[string]any{"slot": s.slot, "operator": s.operator, "field": field, "kind": string(kind),
			"supported_kinds": []string{string(types.WeightKindFrequency)}}
}

// weightKindUnsupported is the PULSE_WEIGHT_UNSUPPORTED refusal of a
// frequency-only slot under a probability weight.
func weightKindUnsupported(s weightSlot, spec *types.WeightSpec) error {
	head, details := kindOnlyHead(s, spec.Field, spec.EffectiveKind())
	return errors.NewCodedErrorWithDetails(errors.PULSE_WEIGHT_UNSUPPORTED,
		head+"; use a frequency weight (integer replication counts) or opt the slot out with \"weight\": null to run it unweighted", details)
}

// extensionNotWeightAware is the PULSE_EXTENSION_NOT_WEIGHT_AWARE
// refusal of a weight in force on an extension operator whose
// registration does not declare WeightAware.
func extensionNotWeightAware(s weightSlot, field string, explicit bool) error {
	var why string
	switch {
	case s.extCategory == extCategoryAggregator:
		why = "drop the weight or set \"weight\": null on the slot (an instance default is skipped on it)"
	case explicit:
		why = "set \"weight\": null on the slot to run it unweighted"
	default:
		why = "an extension " + s.extCategory + " is refused under any weight in force, the instance default included; set \"weight\": null on the slot to run it unweighted"
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_NOT_WEIGHT_AWARE,
		fmt.Sprintf("%s: extension operator %s does not declare WeightAware; %s", s.slot, s.operator, why),
		map[string]any{"slot": s.slot, "operator": s.operator, "field": field})
}

// aggValueFields lists the columns a weighted aggregation multiplies by
// the weight: AGG_RATIO ignores its Field and weights its two params;
// every other aggregator weights its Field.
func aggValueFields(a *types.Aggregation) []string {
	if a.Type != types.AGG_RATIO {
		return []string{a.Field}
	}
	var p struct {
		Num string `json:"numerator_field"`
		Den string `json:"denominator_field"`
	}
	if len(a.Params) > 0 && json.Unmarshal(a.Params, &p) != nil {
		return nil
	}
	return []string{p.Num, p.Den}
}

// decimalWeightRefusal refuses an applied weight on a slot whose value
// field is decimal128: the decimal aggregation path has no weighted
// form. A nil schema judges nothing.
func decimalWeightRefusal(schema *encoding.Schema, s weightSlot, spec *types.WeightSpec) error {
	if schema == nil {
		return nil
	}
	for _, name := range s.valueFields {
		f := schema.Field(name)
		if f == nil || !f.Type.IsDecimal() {
			continue
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_WEIGHT_UNSUPPORTED,
			fmt.Sprintf("%s: %s over decimal128 field %q has no weighted form; opt the slot out with \"weight\": null to run it unweighted", s.slot, s.operator, name),
			map[string]any{"slot": s.slot, "operator": s.operator, "field": spec.Field, "value_field": name, "type": f.Type.String()})
	}
	return nil
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
// post-tests, regressions, attributes, overlays, groups, crosstab rows,
// crosstab columns, windows (the last four were added after the first
// eight, so they trail rather than reorder an existing report).
func weightSlots(req *types.Request, inst *InstanceSnapshot) []weightSlot {
	var slots []weightSlot
	add := func(slot, op string, w types.SlotWeight) {
		slots = append(slots, weightSlot{slot: slot, operator: op, weight: w, hidden: inst.Hidden(op)})
	}
	// ext marks the slot just added as an extension operator of
	// category when the instance registers one under its name.
	ext := func(category string) {
		s := &slots[len(slots)-1]
		if registered, aware := inst.Extensions().WeightAwareness(category, s.operator); registered {
			s.extCategory, s.extAware = category, aware
		}
	}
	addAgg := func(slot string, a *types.Aggregation) {
		add(slot, string(a.Type), a.Weight)
		slots[len(slots)-1].sugar = WeightedMeanParamField(a)
		slots[len(slots)-1].valueFields = aggValueFields(a)
		ext(extCategoryAggregator)
	}
	for i, a := range req.Aggregations {
		if a != nil {
			addAgg(fmt.Sprintf("aggregations[%d]", i), a)
		}
	}
	if ct := req.Crosstab; ct != nil {
		if ct.Cell != nil {
			addAgg("crosstab.cell", ct.Cell)
		}
		for i, a := range ct.MarginAggregations {
			if a != nil {
				addAgg(fmt.Sprintf("crosstab.margin_aggregations[%d]", i), a)
			}
		}
	}
	for i, t := range req.Tests {
		if t != nil {
			add(fmt.Sprintf("tests[%d]", i), string(t.Type), t.Weight)
			ext(extCategoryTest)
		}
	}
	for i, t := range req.PostTests {
		if t != nil {
			add(fmt.Sprintf("post_tests[%d]", i), string(t.Type), t.Weight)
			ext(extCategoryTest)
			if slots[len(slots)-1].extCategory == "" {
				slots[len(slots)-1].reason = reasonPostTest
			}
		}
	}
	for i, r := range req.Regressions {
		if r != nil {
			add(fmt.Sprintf("regressions[%d]", i), string(r.Type), r.Weight)
			switch {
			case r.Resample != "":
				slots[len(slots)-1].reason = reasonResample
			case r.Selection != "":
				slots[len(slots)-1].reason = reasonSelection
			}
		}
	}
	for i, a := range req.Attributes {
		if a != nil {
			add(fmt.Sprintf("attributes[%d]", i), string(a.Type), a.Weight)
			ext(extCategoryAttribute)
		}
	}
	for i, o := range req.Overlays {
		add(fmt.Sprintf("overlays[%d]", i), string(o.Kind), o.Weight)
		slots[len(slots)-1].overlay = true
	}
	addGroups := func(prefix string, groups []*types.Group) {
		for i, g := range groups {
			if g != nil {
				add(fmt.Sprintf("%s[%d]", prefix, i), string(g.Type), g.Weight)
			}
		}
	}
	addGroups("groups", req.Groups)
	if ct := req.Crosstab; ct != nil {
		addGroups("crosstab.rows", ct.Rows)
		addGroups("crosstab.columns", ct.Columns)
	}
	for i, w := range req.Windows {
		if w != nil {
			add(fmt.Sprintf("windows[%d]", i), string(w.Type), types.SlotWeight{})
			slots[len(slots)-1].noSlotWeight = true
		}
	}
	return slots
}

// requestNamesWeight reports whether req, any of its slots, or the
// instance default names a weight (a slot `null` included), or the
// request carries an AGG_WEIGHTED_MEAN slot.
func requestNamesWeight(req *types.Request, slots []weightSlot, defaultWeight *types.WeightSpec) bool {
	if req.Weight != nil || defaultWeight != nil {
		return true
	}
	for _, s := range slots {
		// An AGG_WEIGHTED_MEAN slot is a weighted figure by definition:
		// it resolves (and is refused when nothing resolves) even when
		// no weight is named anywhere.
		if !s.weight.IsZero() || s.sugar != "" || (s.operator == string(types.AGG_WEIGHTED_MEAN) && !s.hidden) {
			return true
		}
	}
	return false
}

// slotOwnWeight resolves a slot's OWN weight: its `weight`, folded with
// AGG_WEIGHTED_MEAN's params.weight_field sugar (kind probability).
// set=false means the slot inherits. A sugar field that disagrees with
// the slot's explicit weight — a different field, or `null` — is
// PROCESSING_CONFIG.
func slotOwnWeight(s weightSlot) (spec *types.WeightSpec, null, set bool, err error) {
	if s.sugar == "" {
		switch {
		case s.weight.IsNull():
			return nil, true, true, nil
		case !s.weight.IsZero():
			return s.weight.Spec(), false, true, nil
		}
		return nil, false, false, nil
	}
	conflict := func(with string) error {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s: AGG_WEIGHTED_MEAN params.weight_field %q conflicts with the slot weight %s; name the weight once", s.slot, s.sugar, with),
			map[string]any{"slot": s.slot, "operator": s.operator, "field": s.sugar})
	}
	switch {
	case s.weight.IsNull():
		return nil, false, false, conflict("null")
	case !s.weight.IsZero():
		own := s.weight.Spec()
		if own.Field != s.sugar {
			return nil, false, false, conflict(fmt.Sprintf("%q", own.Field))
		}
		return own, false, true, nil
	}
	return &types.WeightSpec{Field: s.sugar, Kind: types.WeightKindProbability}, false, true, nil
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
//   - a slot's own weight passes the same two checks. On
//     AGG_WEIGHTED_MEAN, params.weight_field is the slot's own weight
//     (kind probability); one that disagrees with an explicit slot
//     `weight` (another field, or `null`) is PROCESSING_CONFIG, and an
//     AGG_WEIGHTED_MEAN slot nothing resolves a weight for is
//     PROCESSING_CONFIG;
//   - the slot's operator class (internal/weighting) decides what a
//     resolved weight does: weight-aware ⇒ applied; not weightable ⇒
//     skipped under the instance default but PROCESSING_CONFIG under
//     an explicit (slot or request) weight — a WIN_* slot has no slot
//     weight, so only the request weight is explicit there; refused
//     (ClassRefuse — a pending inferential operator, or a permanent
//     refusal carrying details.reason: weighting.RefusalReason, every
//     built-in post-test, a regression with a resample or selection
//     modifier) ⇒ PULSE_WEIGHT_UNSUPPORTED under any weight, the
//     instance default included; frequency-only ⇒ applied under kind
//     frequency, PULSE_WEIGHT_UNSUPPORTED naming the kind under kind
//     probability (the default included); every operator the table
//     does not govern ⇒ skipped;
//   - an extension aggregator, attribute or test (the instance's
//     ExtensionsSnapshot) is classed by its registration's WeightAware
//     declaration: aware ⇒ applied (no decimal128 refusal — the
//     extension owns its decimal reads); a non-aware aggregator ⇒
//     skipped under the instance default, PULSE_EXTENSION_NOT_WEIGHT_AWARE
//     under an explicit weight; a non-aware attribute or test ⇒
//     PULSE_EXTENSION_NOT_WEIGHT_AWARE under any weight, the instance
//     default included (it may read the whole population, like the
//     built-in tests and reference attributes). A hidden extension is
//     never-registered (skipped);
//   - an overlay is classed by overlayWeightClass (the per-kind
//     overlayWeightClasses table, else its manifest Inferential flag):
//     a refused kind is PULSE_WEIGHT_UNSUPPORTED (a frequency-only one
//     only under kind probability) under a weight resolved on its OWN slot
//     (slot → request → default, so a request or instance weight that
//     weights the host weights the overlay too); its `weight: null`
//     opts out. A host weighted only by its own slot weight (or
//     AGG_WEIGHTED_MEAN's params.weight_field) does not refuse an
//     overlay nothing else weights — the shipped pairwise n_source
//     modes read such a host on purpose;
//   - a weight-aware slot whose value field is decimal128 (its Field,
//     or AGG_RATIO's numerator_field / denominator_field) ⇒
//     PULSE_WEIGHT_UNSUPPORTED under ANY weight, the instance default
//     included: the decimal path has no weighted form, and skipping it
//     would mix an unweighted figure into a weighted table;
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
		own, null, set, err := slotOwnWeight(s)
		if err != nil {
			return nil, err
		}
		var spec *types.WeightSpec
		switch {
		case null:
			rw.Status, rw.Source = descriptor.WeightStatusOptedOut, descriptor.WeightSourceSlot
		case set:
			spec, rw.Source = own, descriptor.WeightSourceSlot
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
		}
		class := weighting.ClassNone
		switch {
		case s.hidden:
		case s.overlay:
			class = overlayWeightClass(s.operator)
		case s.extCategory != "" && s.extAware:
			class = weighting.ClassAware
		case s.extCategory != "":
			// Classed below: a non-aware extension aggregator is
			// skipped under the default, every other refusal is
			// PULSE_EXTENSION_NOT_WEIGHT_AWARE.
		default:
			class = weighting.ClassOf(s.operator)
		}
		if s.reason != "" && !s.hidden {
			// A slot-level permanent refusal (post-test, regression
			// modifier) refuses whatever the operator's own class.
			class = weighting.ClassRefuse
		}
		if spec == nil {
			// AGG_WEIGHTED_MEAN IS a weighted figure: with nothing
			// resolving (no weight anywhere, or the slot opted out)
			// there is no answer to give.
			if s.operator == string(types.AGG_WEIGHTED_MEAN) && !s.hidden {
				remedy := "set params.weight_field, a slot weight or a request weight"
				if !inst.Enabled(featWeighting) {
					// The slot and request weight are hidden with
					// capability:weighting; only the sugar remains.
					remedy = "set params.weight_field"
				}
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					s.slot+": AGG_WEIGHTED_MEAN needs a weight: "+remedy,
					map[string]any{"slot": s.slot, "operator": s.operator})
			}
			out = append(out, rw)
			continue
		}
		rw.Field, rw.Kind = spec.Field, string(spec.EffectiveKind())
		explicit := rw.Source != descriptor.WeightSourceOptions
		if s.extCategory != "" && !s.extAware && !s.hidden {
			if explicit || s.extCategory != extCategoryAggregator {
				return nil, extensionNotWeightAware(s, spec.Field, explicit)
			}
			rw.Status = descriptor.WeightStatusSkippedNotWeightAware
			out = append(out, rw)
			continue
		}
		switch class {
		case weighting.ClassRefuse:
			return nil, weightUnsupported(s, spec.Field, inst)
		case weighting.ClassFrequencyOnly:
			if spec.EffectiveKind() != types.WeightKindFrequency {
				return nil, weightKindUnsupported(s, spec)
			}
		case weighting.ClassNotWeightable:
			if explicit && s.noSlotWeight {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("%s: %s has no weighted form and carries no slot weight; drop the request weight (an instance default is skipped on it)", s.slot, s.operator),
					map[string]any{"slot": s.slot, "operator": s.operator, "field": spec.Field})
			}
			if explicit {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("%s: %s is not weight-aware; drop the weight or set \"weight\": null on the slot", s.slot, s.operator),
					map[string]any{"slot": s.slot, "operator": s.operator, "field": spec.Field})
			}
			rw.Status = descriptor.WeightStatusSkippedNotWeightAware
			out = append(out, rw)
			continue
		case weighting.ClassNone:
			rw.Status = descriptor.WeightStatusSkippedNotWeightAware
			out = append(out, rw)
			continue
		}
		// An overlay reads its host's weighted figures, never the weight
		// column itself, so an inherited default is judged on the host.
		if rw.Source == descriptor.WeightSourceOptions && schema != nil && !s.overlay {
			if schema.Field(spec.Field) == nil {
				return nil, refusal(fmt.Sprintf("%s: Options.DefaultWeight references unknown field: %s", s.slot, spec.Field),
					map[string]any{"field": spec.Field, "slot": s.slot, "operator": s.operator})
			}
			if err := weightFieldTypeRefusal(schema, s.slot+".weight", spec.Field); err != nil {
				return nil, err
			}
		}
		// The decimal refusal is the built-in decimal path's: an
		// extension reads decimal fields through
		// extend.Record.DecimalValue and owns its weighted form.
		if s.extCategory == "" {
			if err := decimalWeightRefusal(schema, s, spec); err != nil {
				return nil, err
			}
		}
		rw.Status = descriptor.WeightStatusApplied
		out = append(out, rw)
	}
	return out, nil
}

// inheritedHostWeight returns the first overlay-host slot of req (an
// aggregation or the crosstab cell) that a request-level or instance
// default weight is APPLIED to and whose kind refuses accepts, plus
// that weight's field and kind; "" when none. A host weighted only by
// its own slot weight (or AGG_WEIGHTED_MEAN's params.weight_field) does
// not count — the same line the per-request overlay rule draws. A
// request the resolver refuses reports none: its own slot fails first.
func inheritedHostWeight(req *types.Request, defaultWeight *types.WeightSpec, inst *InstanceSnapshot, refuses func(kind string) bool) (slot, field, kind string) {
	rws, err := ResolveWeights(req, nil, defaultWeight, inst)
	if err != nil {
		return "", "", ""
	}
	for _, rw := range rws {
		if rw.Status != descriptor.WeightStatusApplied || rw.Source == descriptor.WeightSourceSlot || !refuses(rw.Kind) {
			continue
		}
		if rw.Slot == "crosstab.cell" || strings.HasPrefix(rw.Slot, "aggregations[") {
			return rw.Slot, rw.Field, rw.Kind
		}
	}
	return "", "", ""
}

// crosstabCellWeightBasis is the weight basis of req's crosstab cell —
// the weighted-host signal a per-Request overlay reads: the cell's
// resolved weight (slot → request → Options.DefaultWeight; `weight:
// null` opts out) when APPLIED, so the cell carries the weighted floor
// keys at runtime, whatever the weight's source. Unweighted when the
// cell resolves none, is not weight-aware, or the request is refused
// on another slot (that refusal is reported on its own).
func crosstabCellWeightBasis(req *types.Request, opts *PredictOptions) weighting.Basis {
	if req == nil || req.Crosstab == nil {
		return weighting.Unweighted
	}
	var def *types.WeightSpec
	if opts != nil {
		def = opts.DefaultWeight
	}
	rws, err := ResolveWeights(req, nil, def, opts.instance())
	if err != nil {
		return weighting.Unweighted
	}
	for _, rw := range rws {
		if rw.Slot == "crosstab.cell" && rw.Status == descriptor.WeightStatusApplied {
			return weighting.BasisOf(&types.WeightSpec{Field: rw.Field, Kind: types.WeightKind(rw.Kind)})
		}
	}
	return weighting.Unweighted
}

// ComposeOverlayWeightRefusal is the Compose-host half of the overlay
// weight refusal, keyed off the same per-kind class as the per-request
// rule (overlayWeightClass). A ComposeOverlaySpec carries no weight of
// its own, so a refused kind is PULSE_WEIGHT_UNSUPPORTED when a slot it
// reads — its reference or a target, every slot when Targets is empty —
// has a request-level or instance default weight applied to an
// aggregation or crosstab cell; a frequency-only kind is refused only
// when that weight is of kind probability; a kind weighted under both
// kinds, and a descriptive one, never. The opt-out is on the host:
// `weight: null` on the slot's weighted aggregations (the overlay then
// compares unweighted figures). requests and labels are parallel
// (labels already defaulted); the runtime (Service.applyComposeOverlays)
// and ValidateComposeWithOptions both call it with the RAW slots, so
// they refuse identically. Details: {slot: overlays[i], operator,
// field, host: requests[j].<slot>}, plus `reason` (and `alternative`)
// for a permanent refusal and `kind` / `supported_kinds` for a
// frequency-only one.
func ComposeOverlayWeightRefusal(overlays []types.ComposeOverlaySpec, requests []*types.Request, labels []string, defaultWeight *types.WeightSpec, inst *InstanceSnapshot) error {
	for i := range overlays {
		if err := composeOverlayWeightRefusal(i, &overlays[i], requests, labels, defaultWeight, inst); err != nil {
			return err
		}
	}
	return nil
}

func composeOverlayWeightRefusal(i int, spec *types.ComposeOverlaySpec, requests []*types.Request, labels []string, defaultWeight *types.WeightSpec, inst *InstanceSnapshot) error {
	kind := string(spec.Kind)
	if inst.Hidden(kind) {
		return nil
	}
	var refuses func(string) bool
	switch overlayWeightClass(kind) {
	case weighting.ClassRefuse:
		refuses = func(string) bool { return true }
	case weighting.ClassFrequencyOnly:
		refuses = func(k string) bool { return k != string(types.WeightKindFrequency) }
	default:
		return nil
	}
	reads := func(label string) bool {
		if len(spec.Targets) == 0 || label == spec.Reference {
			return true
		}
		for _, t := range spec.Targets {
			if t == label {
				return true
			}
		}
		return false
	}
	for j, r := range requests {
		if r == nil || j >= len(labels) || !reads(labels[j]) {
			continue
		}
		slot, field, wkind := inheritedHostWeight(r, defaultWeight, inst, refuses)
		if slot == "" {
			continue
		}
		host := fmt.Sprintf("requests[%d].%s", j, slot)
		s := weightSlot{slot: fmt.Sprintf("overlays[%d]", i), operator: kind, overlay: true}
		hostProse := fmt.Sprintf("its host %s (slot %q) is weighted by %q", host, labels[j], field)
		var (
			msg     string
			details map[string]any
		)
		if overlayWeightClass(kind) == weighting.ClassFrequencyOnly {
			head, d := kindOnlyHead(s, field, types.WeightKind(wkind))
			msg, details = head+"; "+hostProse+"; use a frequency weight on that slot or set \"weight\": null on it to compare unweighted figures", d
		} else {
			head, d, alt := refusalHead(s, field, inst)
			msg, details = head+"; "+hostProse+"; set \"weight\": null on that slot to compare unweighted figures"+alt, d
		}
		details["host"] = host
		return errors.NewCodedErrorWithDetails(errors.PULSE_WEIGHT_UNSUPPORTED, msg, details)
	}
	return nil
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
