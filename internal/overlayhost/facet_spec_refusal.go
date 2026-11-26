package overlayhost

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// FacetSpecRefusal is the Ref / Scope / Level contract of a FACET-host
// spec (FacetRequest.Overlays, the four population-comparison kinds),
// shared by predict (descriptor.validateFacetOverlaySpec) and the
// runtime FACET loop (service.applyFacetOverlays), both right after the
// wrong-slot refusal and before the host-field and population work, so
// both raise the same code, message and details. Before it the runtime
// checked only that Ref.Population.Cohort was set (in other words): an
// unsupported scope, a foreign Ref arm or a non-zero Level / Within
// predict refused ran silently. nil when spec passes, or when route is
// outside the catalog (the slot's unknown-kind gate owns it) or does not
// run on the FACET slot (the wrong-slot refusal owns it). Judged in
// order:
//
//   - any Ref arm but Population set → PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {index, kind};
//   - Ref.Population unset, then Ref.Population.Cohort empty → the same;
//   - a scope other than group → PULSE_OVERLAY_SCOPE_UNSUPPORTED {index, kind, scope};
//   - Level or Within non-zero → PULSE_OVERLAY_LEVEL_OUT_OF_RANGE {index, kind, level, within};
//   - a set `weight` (a field name or a {field, kind} object) →
//     PULSE_WEIGHT_UNSUPPORTED {slot: "overlays[i]", operator, field,
//     reason}: facets are never weighted, so the weight used to be
//     ignored silently. An absent weight and `null` (the explicit
//     "run unweighted" opt-out — what a facet does anyway) pass.
func FacetSpecRefusal(spec *types.OverlaySpec, route types.OverlayKind, index int) *errors.CodedError {
	if spec == nil || len(hosts[route]) == 0 || !Accepts(Facet, route) {
		return nil
	}
	kind := string(spec.Kind)
	refusal := func(msg string) *errors.CodedError {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+kind+" "+msg, map[string]any{"index": index, "kind": kind})
	}
	other := spec.Ref
	other.Population = nil
	if other != (types.OverlayRef{}) {
		return refusal("requires ref.population only (no other ref arm)")
	}
	if spec.Ref.Population == nil {
		return refusal("requires ref.population (population-comparison reference; the comparison cohort lives on ref.population.cohort)")
	}
	if spec.Ref.Population.Cohort == "" {
		return refusal("ref.population.cohort is empty (population reference requires a comparison-cohort name)")
	}
	if spec.Scope != types.OverlayScopeGroup {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			"overlay "+kind+" does not support scope "+string(spec.Scope)+" (supports: group)",
			map[string]any{"index": index, "kind": kind, "scope": string(spec.Scope)})
	}
	if spec.Level != 0 || spec.Within != 0 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
			"overlay "+kind+" does not support level / within (population-comparison kind: the reference is a single-value lookup, not an axis prefix)",
			map[string]any{"index": index, "kind": kind, "level": spec.Level, "within": spec.Within})
	}
	if w := spec.Weight.Spec(); w != nil {
		slot := "overlays[" + strconv.Itoa(index) + "]"
		return errors.NewCodedErrorWithDetails(errors.PULSE_WEIGHT_UNSUPPORTED,
			slot+": "+kind+" cannot be weighted: "+facetWeightReason,
			map[string]any{"slot": slot, "operator": kind, "field": w.Field, "reason": facetWeightReason})
	}
	return nil
}

// facetWeightReason is why a FACET-host overlay refuses a set weight
// (PULSE_WEIGHT_UNSUPPORTED details.reason).
const facetWeightReason = "a facet is never weighted — its host distribution and the population it is compared against are unweighted counts — so set \"weight\": null or drop the key"

// FacetHostArmRefusal is the per-kind host-arm gate of a FACET-host
// spec, shared by predict (descriptor.validateFacetOverlayHostArm, from
// the schema) and the runtime FACET loop (service.applyFacetOverlays,
// from the finalised host entry), both right after the host field
// resolves and before any population work, so both raise the same code,
// message and details. discrete is the host field's tallied shape;
// coded marks an integer-coded field a label binding turned discrete
// (descriptor.FacetCodedField), named in the message and details. nil
// when the kind reads the shape:
//
//   - OVERLAY_CHISQ_VS_POP needs a discrete host (χ² goodness-of-fit
//     over buckets) — a labelled integer-coded field is one;
//   - OVERLAY_KS_VS_POP needs a numeric host (an ordered CDF) — a
//     labelled integer-coded field is not one: its numeric summary is
//     gone;
//   - OVERLAY_INDEX_VS_POP / OVERLAY_ZSCORE_VS_POP read either.
//
// Both refusals are PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
// {index, kind, field, type} (+ "label_coded": true when coded).
func FacetHostArmRefusal(kind types.OverlayKind, field, typeName string, discrete, coded bool, index int) *errors.CodedError {
	var msg string
	switch {
	case kind == types.OverlayKindChiSqVsPop && !discrete:
		msg = "overlay " + string(kind) + " requires a discrete host field; got numeric field " + field + " (" + typeName + ")"
	case kind == types.OverlayKindKSVsPop && discrete:
		msg = "overlay " + string(kind) + " requires a numeric host field; got non-numeric field " + field + " (" + typeName + ")"
		if coded {
			msg = "overlay " + string(kind) + " requires a numeric host field; field " + field + " (" + typeName +
				") is tallied as discrete codes because an augment label binding names it — drop the binding to compare its numeric distribution"
		}
	default:
		return nil
	}
	details := map[string]any{"index": index, "kind": string(kind), "field": field, "type": typeName}
	if coded {
		details["label_coded"] = true
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, msg, details)
}
