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
		return refusal("requires Ref.Population only (no other Ref arm)")
	}
	if spec.Ref.Population == nil {
		return refusal("requires Ref.Population (population-comparison reference; the comparison cohort lives on Ref.Population.Cohort)")
	}
	if spec.Ref.Population.Cohort == "" {
		return refusal("Ref.Population.Cohort is empty (population reference requires a comparison-cohort name)")
	}
	if spec.Scope != types.OverlayScopeGroup {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			"overlay "+kind+" does not support scope "+string(spec.Scope)+" (supports: group)",
			map[string]any{"index": index, "kind": kind, "scope": string(spec.Scope)})
	}
	if spec.Level != 0 || spec.Within != 0 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
			"overlay "+kind+" does not support Level / Within (population-comparison kind: the reference is a single-value lookup, not an axis prefix)",
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
