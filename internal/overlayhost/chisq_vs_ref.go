package overlayhost

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_CHISQ_VS_REF params.reference_kind values (#791).
const (
	// ChiSqVsRefFixed (the default) reads the reference slot as a FIXED
	// distribution: goodness of fit, R chisq.test(target[C], p = ref[C],
	// rescale.p = TRUE).
	ChiSqVsRefFixed = "fixed"
	// ChiSqVsRefSample reads the reference slot as a second SAMPLE: the
	// 2 x |C| homogeneity test of the two tables, R chisq.test(rbind(
	// target[C], ref[C])).
	ChiSqVsRefSample = "sample"
)

// ChiSqVsRefReferenceKindKey is the params key naming the mode.
const ChiSqVsRefReferenceKindKey = "reference_kind"

// ChiSqVsRefReferenceKinds lists the accepted params.reference_kind
// values, sorted (the manifest's EnumValues).
func ChiSqVsRefReferenceKinds() []string {
	return []string{ChiSqVsRefFixed, ChiSqVsRefSample}
}

// ChiSqVsRefReferenceKind reads an OVERLAY_CHISQ_VS_REF spec's
// params.reference_kind, shared by predict (internal/descriptor) and
// runtime (internal/processing, internal/service) so both raise the
// same code, message and details. Absent is ChiSqVsRefFixed; anything
// but the two strings is PULSE_OVERLAY_PARAM_MISSING (the overlay
// bad-param code, OVERLAY_CHISQ_MATRIX's rule) with details {kind,
// param, reference_kind} plus index when index >= 0. A spec of another
// kind answers ChiSqVsRefFixed, nil.
func ChiSqVsRefReferenceKind(spec *types.ComposeOverlaySpec, index int) (string, *errors.CodedError) {
	if spec == nil || spec.Kind != types.OverlayKindChiSqVsRef {
		return ChiSqVsRefFixed, nil
	}
	v, ok := paramread.Get(paramread.Overlay, string(spec.Kind), spec.Params, ChiSqVsRefReferenceKindKey)
	if !ok || v == nil {
		return ChiSqVsRefFixed, nil
	}
	if s, isStr := v.(string); isStr && (s == ChiSqVsRefFixed || s == ChiSqVsRefSample) {
		return s, nil
	}
	return "", markovParamRefusal(spec.Kind, index, ChiSqVsRefReferenceKindKey,
		`reference_kind must be "fixed" or "sample"`,
		map[string]any{ChiSqVsRefReferenceKindKey: v})
}

// ChiSqVsRefReadsReference reports whether spec reads its reference
// slot as a sample (params.reference_kind "sample"): the reference's
// own weight floor then scales its table, so the components-disabled
// refusal and the Compose components veto cover the reference too. A
// spec of any other kind, or a refused value, answers false (the param
// refusal owns the bad value).
func ChiSqVsRefReadsReference(spec *types.ComposeOverlaySpec) bool {
	k, err := ChiSqVsRefReferenceKind(spec, -1)
	return err == nil && k == ChiSqVsRefSample
}
